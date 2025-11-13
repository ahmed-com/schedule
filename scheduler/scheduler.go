package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/concurrency"
	"github.com/ahmed-com/schedule/executor"
	"github.com/ahmed-com/schedule/id"
	"github.com/ahmed-com/schedule/metrics"
	"github.com/ahmed-com/schedule/recovery"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/ticker"
)

// Scheduler is the main orchestrator for job scheduling and execution
type Scheduler struct {
	config  jobistemer.SchedulerConfig
	store   storage.Storage
	exec    *executor.Executor
	pool    *concurrency.WorkerPool
	reaper  *recovery.Reaper
	metrics metrics.MetricsCollector

	jobs   map[string]*jobistemer.Job
	jobsMu sync.RWMutex

	running   bool
	runningMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a new scheduler instance
func NewScheduler(config jobistemer.SchedulerConfig, store storage.Storage) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	exec := executor.NewExecutor(store)
	pool := concurrency.NewWorkerPool(config.MaxConcurrentJobs)
	reaper := recovery.NewReaper(store, config.ReaperInterval)

	// Use provided metrics collector or default to NoOp
	metricsCollector := config.Metrics
	if metricsCollector == nil {
		metricsCollector = metrics.NewNoOpMetrics()
	}
	// Type assert to ensure it implements MetricsCollector
	mc, ok := metricsCollector.(metrics.MetricsCollector)
	if !ok {
		mc = metrics.NewNoOpMetrics()
	}

	// Pass metrics to executor
	exec.SetMetrics(mc)

	return &Scheduler{
		config:  config,
		store:   store,
		exec:    exec,
		pool:    pool,
		reaper:  reaper,
		metrics: mc,
		jobs:    make(map[string]*jobistemer.Job),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// RegisterJob registers a job with the scheduler
func (s *Scheduler) RegisterJob(job *jobistemer.Job) error {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job already registered: %s", job.ID)
	}

	// Serialize job config
	configBytes, err := json.Marshal(job.Config)
	if err != nil {
		return fmt.Errorf("failed to serialize job config: %w", err)
	}

	// Persist job to storage
	storageJob := &storage.Job{
		ID:             job.ID,
		Name:           job.Name,
		ScheduleType:   "cron", // TODO: detect from ticker type
		ScheduleConfig: nil,    // TODO: serialize ticker config
		Config:         configBytes,
		Version:        job.Version,
		Active:         job.Active,
		Paused:         job.Paused,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.store.CreateJob(s.ctx, storageJob); err != nil {
		return err
	}

	// Persist tasks
	for _, task := range job.Tasks {
		taskConfigBytes, err := json.Marshal(task.Config)
		if err != nil {
			return fmt.Errorf("failed to serialize task config: %w", err)
		}

		storageTask := &storage.Task{
			ID:        task.ID,
			JobID:     job.ID,
			Name:      task.Name,
			Config:    taskConfigBytes,
			Order:     task.Order,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.store.CreateTask(s.ctx, storageTask); err != nil {
			return err
		}
	}

	s.jobs[job.ID] = job
	return nil
}

// Start starts the scheduler
func (s *Scheduler) Start() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler already running")
	}

	// Start recovery process
	if err := s.performRecovery(); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	// Start reaper
	s.reaper.Start(s.ctx)

	// Start worker pool
	s.pool.Start()

	// Start all job tickers
	s.jobsMu.RLock()
	for _, job := range s.jobs {
		if job.Active && !job.Paused {
			job.Ticker.Start()
			s.wg.Add(1)
			go s.watchJob(job)
		}
	}
	s.jobsMu.RUnlock()

	s.running = true
	return nil
}

// watchJob monitors a job's ticker and triggers executions
func (s *Scheduler) watchJob(job *jobistemer.Job) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case execCtx := <-job.Ticker.Channel():
			s.handleJobTrigger(job, execCtx)
		}
	}
}

// handleJobTrigger handles a job trigger event
func (s *Scheduler) handleJobTrigger(job *jobistemer.Job, execCtx ticker.ExecutionContext) {
	// Check overlap policy
	isRunning, err := job.IsRunning(s.ctx, func(ctx context.Context) ([]*jobistemer.JobOccurrence, error) {
		storageOccs, err := s.store.ListRunningOccurrences(ctx)
		if err != nil {
			return nil, err
		}

		// Convert storage occurrences to JobOccurrence
		occs := make([]*jobistemer.JobOccurrence, len(storageOccs))
		for i, storageOcc := range storageOccs {
			occs[i] = &jobistemer.JobOccurrence{
				ID:            storageOcc.ID,
				JobID:         storageOcc.JobID,
				JobVersion:    storageOcc.JobVersion,
				ScheduledTime: storageOcc.ScheduledTime,
				Status:        storageOcc.Status,
				OwningNodeID:  storageOcc.OwningNodeID,
				StartTime:     storageOcc.StartTime,
				EndTime:       storageOcc.EndTime,
				IsRecoveryRun: storageOcc.IsRecoveryRun,
				CreatedAt:     storageOcc.CreatedAt,
				UpdatedAt:     storageOcc.UpdatedAt,
			}
		}
		return occs, nil
	})

	if err != nil {
		// Log error
		return
	}

	if isRunning {
		switch job.Config.OverlapPolicy {
		case jobistemer.OverlapPolicySkip:
			// Skip this occurrence
			return
		case jobistemer.OverlapPolicyQueue:
			// Create occurrence with Queued status
			// Will be picked up by OnComplete hook (not implemented in this phase)
		case jobistemer.OverlapPolicyAllow:
			// Continue with creation
		}
	}

	// Create deterministic job occurrence ID
	occurrenceID := id.GenerateJobOccurrenceID(job.ID, execCtx.ScheduledTime)

	// Serialize config
	configBytes, _ := json.Marshal(job.Config)

	occurrence := &jobistemer.JobOccurrence{
		ID:            occurrenceID,
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: execCtx.ScheduledTime,
		Status:        jobistemer.JobStatusPending,
		Config:        job.Config,
		IsRecoveryRun: false,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Convert to storage format
	storageOcc := &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		OwningNodeID:  s.config.NodeID,
		Config:        configBytes,
		IsRecoveryRun: occurrence.IsRecoveryRun,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	}

	// Atomic create (race to win)
	err = s.store.CreateJobOccurrence(s.ctx, storageOcc)
	if err != nil {
		// Another node won the race or already exists
		return
	}

	// Submit to worker pool
	s.pool.Submit(func() {
		_, err := s.exec.ExecuteJobOccurrence(s.ctx, job, occurrence)
		if err != nil {
			// Log error (in production, use proper logging)
		}
	})
}

// performRecovery performs OnBoot recovery
func (s *Scheduler) performRecovery() error {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()

	recoveryHandler := recovery.NewRecoveryHandler(s.store, s.exec, s.pool)
	recoveryHandler.SetMetrics(s.metrics)

	for _, job := range s.jobs {
		if !job.Active {
			continue
		}

		// Get last run time
		var lastRunTime time.Time
		storageJob, err := s.store.GetJob(s.ctx, job.ID)
		if err != nil {
			return err
		}

		if storageJob != nil && storageJob.LastRunTime != nil {
			lastRunTime = *storageJob.LastRunTime
		} else {
			lastRunTime = time.Now().Add(-24 * time.Hour) // Default to 24h ago
		}

		// Get missed occurrences
		missedTimes, err := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
		if err != nil {
			return err
		}

		// Apply recovery strategy
		err = recoveryHandler.ApplyStrategy(s.ctx, job, missedTimes)
		if err != nil {
			return err
		}
	}

	return nil
}

// Shutdown gracefully shuts down the scheduler
func (s *Scheduler) Shutdown(timeout time.Duration) error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return nil
	}

	// Stop all tickers
	s.jobsMu.RLock()
	for _, job := range s.jobs {
		job.Ticker.Stop()
	}
	s.jobsMu.RUnlock()

	// Signal cancellation
	s.cancel()

	// Wait for grace period or all jobs to complete
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All jobs completed gracefully
	case <-time.After(timeout):
		// Timeout - force shutdown
	}

	// Stop worker pool
	s.pool.Stop()

	// Stop reaper
	s.reaper.Stop()

	s.running = false
	return nil
}

// GetJob returns a registered job by ID
func (s *Scheduler) GetJob(jobID string) (*jobistemer.Job, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()

	job, exists := s.jobs[jobID]
	return job, exists
}

// ListJobs returns all registered jobs
func (s *Scheduler) ListJobs() []*jobistemer.Job {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()

	jobs := make([]*jobistemer.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// updateMetrics updates metrics gauges based on current system state
func (s *Scheduler) updateMetrics() {
	// Count running occurrences
	runningOccs, err := s.store.ListRunningOccurrences(s.ctx)
	if err == nil {
		s.metrics.SetJobsRunning(len(runningOccs))
	}

	// Count queued jobs (jobs in worker pool queue)
	queueLen := s.pool.QueueLength()
	s.metrics.SetJobsInQueue(queueLen)

	// Count paused jobs
	s.jobsMu.RLock()
	pausedCount := 0
	for _, job := range s.jobs {
		if job.Paused {
			pausedCount++
		}
	}
	s.jobsMu.RUnlock()
	s.metrics.SetJobsPaused(pausedCount)
}
