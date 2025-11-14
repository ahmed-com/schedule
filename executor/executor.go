package executor

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/id"
	"github.com/ahmed-com/schedule/metrics"
	"github.com/ahmed-com/schedule/storage"
)

// Executor handles the execution of job occurrences and their tasks
type Executor struct {
	store   storage.Storage
	metrics metrics.MetricsCollector
}

// NewExecutor creates a new executor instance
func NewExecutor(store storage.Storage) *Executor {
	return &Executor{
		store:   store,
		metrics: metrics.NewNoOpMetrics(), // Default to no-op
	}
}

// SetMetrics sets the metrics collector for this executor
func (e *Executor) SetMetrics(m metrics.MetricsCollector) {
	e.metrics = m
}

// ExecuteJobOccurrence executes a job occurrence with all its tasks
func (e *Executor) ExecuteJobOccurrence(ctx context.Context, job *jobistemer.Job, occurrence *jobistemer.JobOccurrence) (*jobistemer.ExecutionReport, error) {
	startTime := time.Now()

	// Create job-level context with timeout
	jobCtx, cancel := context.WithTimeout(ctx, job.Config.ExecutionTimeout)
	defer cancel()

	// Update occurrence status to Running
	occurrence.Status = jobistemer.JobStatusRunning
	occurrence.StartTime = timePtr(startTime)
	if err := e.updateOccurrenceStatus(jobCtx, occurrence); err != nil {
		return nil, err
	}

	// Execute OnStart hook
	if job.OnStart != nil {
		job.OnStart(jobCtx, occurrence)
	}

	var report *jobistemer.ExecutionReport
	var err error

	// Execute based on TaskExecutionMode
	switch job.Config.TaskExecutionMode {
	case jobistemer.TaskExecutionModeSequential:
		report, err = e.executeSequential(jobCtx, job, occurrence)
	case jobistemer.TaskExecutionModeParallel:
		report, err = e.executeParallel(jobCtx, job, occurrence)
	default:
		report, err = e.executeSequential(jobCtx, job, occurrence)
	}

	// Update final status
	endTime := time.Now()
	occurrence.EndTime = &endTime
	if err != nil {
		occurrence.Status = jobistemer.JobStatusFailed
	} else {
		occurrence.Status = jobistemer.JobStatusCompleted
	}
	e.updateOccurrenceStatus(jobCtx, occurrence)

	// Track metrics
	duration := endTime.Sub(startTime)
	e.metrics.ObserveJobDuration(job.Name, duration)
	e.metrics.IncJobOccurrences(job.Name, string(occurrence.Status))

	// Execute OnComplete hook
	if job.OnComplete != nil {
		job.OnComplete(jobCtx, report)
	}

	return report, err
}

// executeSequential executes tasks sequentially
func (e *Executor) executeSequential(ctx context.Context, job *jobistemer.Job, occurrence *jobistemer.JobOccurrence) (*jobistemer.ExecutionReport, error) {
	report := jobistemer.NewExecutionReport(occurrence.ID, job.Config.FailureStrategy)

	for _, task := range job.Tasks {
		taskReport := e.executeTask(ctx, job, task, occurrence)
		report.AddTaskReport(taskReport)

		// Check failure strategy
		if taskReport.FinalStatus == jobistemer.TaskStatusFailed {
			if job.Config.FailureStrategy == jobistemer.FailureStrategyFailFast {
				report.GroupOutcome = "failed"
				return report, fmt.Errorf("task %s failed, aborting due to fail-fast", task.Name)
			}
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			report.GroupOutcome = "canceled"
			return report, ctx.Err()
		default:
		}
	}

	report.GroupOutcome = "completed"
	return report, nil
}

// executeParallel executes tasks in parallel with fail-fast support
func (e *Executor) executeParallel(ctx context.Context, job *jobistemer.Job, occurrence *jobistemer.JobOccurrence) (*jobistemer.ExecutionReport, error) {
	report := jobistemer.NewExecutionReport(occurrence.ID, job.Config.FailureStrategy)

	// Create cancellable context for fail-fast
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, task := range job.Tasks {
		wg.Add(1)

		go func(t *jobistemer.Task) {
			defer wg.Done()

			taskReport := e.executeTask(groupCtx, job, t, occurrence)

			mu.Lock()
			report.AddTaskReport(taskReport)
			mu.Unlock()

			// Check failure strategy
			if taskReport.FinalStatus == jobistemer.TaskStatusFailed {
				taskConfig := job.GetEffectiveConfig(t)
				strategy := job.Config.FailureStrategy
				if taskConfig.FailureStrategy != nil {
					strategy = *taskConfig.FailureStrategy
				}

				if strategy == jobistemer.FailureStrategyFailFast {
					// Signal all other tasks to abort
					cancel()
				}
			}
		}(task)
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Determine group outcome
	if ctx.Err() != nil {
		report.GroupOutcome = "canceled"
	} else {
		hasFailure := false
		for _, tr := range report.TaskReports {
			if tr.FinalStatus == jobistemer.TaskStatusFailed {
				hasFailure = true
				break
			}
		}
		if hasFailure {
			report.GroupOutcome = "failed"
		} else {
			report.GroupOutcome = "completed"
		}
	}

	return report, nil
}

// executeTask executes a single task with retry logic
func (e *Executor) executeTask(ctx context.Context, job *jobistemer.Job, task *jobistemer.Task, occurrence *jobistemer.JobOccurrence) *jobistemer.TaskReport {
	taskConfig := job.GetEffectiveConfig(task)

	// Create task run record
	runID := id.GenerateTaskRunID(occurrence.ID, task.ID)
	taskRun := &storage.TaskRun{
		ID:           runID,
		OccurrenceID: occurrence.ID,
		TaskID:       task.ID,
		Status:       jobistemer.TaskStatusPending,
		RetryCount:   0,
		CreatedAt:    time.Now(),
	}
	e.store.CreateTaskRun(ctx, taskRun)

	report := &jobistemer.TaskReport{
		TaskID:   task.ID,
		TaskName: task.Name,
		Attempts: make([]*jobistemer.AttemptReport, 0),
	}

	retryPolicy := taskConfig.RetryPolicy
	if retryPolicy == nil {
		retryPolicy = &jobistemer.RetryPolicy{MaxRetries: 0}
	}

	var lastErr error
	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		attemptReport := e.executeAttempt(ctx, job, task, taskRun, attempt, taskConfig)
		report.Attempts = append(report.Attempts, attemptReport)

		if attemptReport.Status == jobistemer.TaskStatusSuccess {
			report.FinalStatus = jobistemer.TaskStatusSuccess
			taskRun.Status = jobistemer.TaskStatusSuccess
			taskRun.EndTime = timePtr(time.Now())
			e.store.UpdateTaskRun(ctx, taskRun)
			return report
		}

		lastErr = fmt.Errorf("%s", attemptReport.ErrorMessage)

		// Check if we should retry
		if attempt < retryPolicy.MaxRetries {
			// Calculate backoff
			backoff := retryPolicy.RetryInterval
			if retryPolicy.BackoffFactor > 1.0 {
				backoff = time.Duration(float64(backoff) * math.Pow(retryPolicy.BackoffFactor, float64(attempt)))
			}

			// Wait before retry
			select {
			case <-ctx.Done():
				report.FinalStatus = jobistemer.TaskStatusCanceled
				taskRun.Status = jobistemer.TaskStatusCanceled
				taskRun.EndTime = timePtr(time.Now())
				e.store.UpdateTaskRun(ctx, taskRun)
				return report
			case <-time.After(backoff):
				// Continue to next attempt
			}
		}
	}

	// All attempts failed
	report.FinalStatus = jobistemer.TaskStatusFailed
	taskRun.Status = jobistemer.TaskStatusFailed
	taskRun.RetryCount = len(report.Attempts)
	taskRun.ErrorMessage = lastErr.Error()
	taskRun.EndTime = timePtr(time.Now())
	e.store.UpdateTaskRun(ctx, taskRun)

	return report
}

// executeAttempt executes a single attempt of a task
func (e *Executor) executeAttempt(ctx context.Context, job *jobistemer.Job, task *jobistemer.Task, taskRun *storage.TaskRun, attemptNum int, config jobistemer.TaskConfig) *jobistemer.AttemptReport {
	attemptID := id.GenerateExecutionAttemptID(taskRun.ID, attemptNum)

	attempt := &storage.ExecutionAttempt{
		ID:            attemptID,
		TaskRunID:     taskRun.ID,
		AttemptNumber: attemptNum,
		Status:        jobistemer.TaskStatusRunning,
		StartTime:     time.Now(),
		CreatedAt:     time.Now(),
	}
	e.store.CreateExecutionAttempt(ctx, attempt)

	report := &jobistemer.AttemptReport{
		AttemptNumber: attemptNum,
		StartTime:     attempt.StartTime,
	}

	// Create task-specific timeout context
	timeout := config.TaskTimeout
	if timeout == nil {
		defaultTimeout := 5 * time.Minute
		timeout = &defaultTimeout
	}

	taskCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	// Execute task function
	execCtx := &jobistemer.ExecutionContext{
		OccurrenceID: taskRun.OccurrenceID,
		TaskRunID:    taskRun.ID,
		AttemptID:    attemptID,
	}

	err := task.Fn(taskCtx, execCtx)

	endTime := time.Now()
	report.EndTime = &endTime
	report.Duration = endTime.Sub(attempt.StartTime)

	// Update attempt record
	attempt.EndTime = &endTime

	if err != nil {
		if taskCtx.Err() == context.DeadlineExceeded {
			report.Status = jobistemer.TaskStatusTimeout
			attempt.Status = jobistemer.TaskStatusTimeout
			report.ErrorMessage = "task execution timeout"
		} else {
			report.Status = jobistemer.TaskStatusFailed
			attempt.Status = jobistemer.TaskStatusFailed
			report.ErrorMessage = err.Error()
		}
		attempt.ErrorMessage = report.ErrorMessage
	} else {
		report.Status = jobistemer.TaskStatusSuccess
		attempt.Status = jobistemer.TaskStatusSuccess
	}

	e.store.UpdateExecutionAttempt(ctx, attempt)

	// Track metrics
	e.metrics.IncTaskAttempts(job.Name, task.Name, string(attempt.Status))
	e.metrics.ObserveTaskDuration(job.Name, task.Name, report.Duration)

	return report
}

// updateOccurrenceStatus updates a job occurrence in storage
func (e *Executor) updateOccurrenceStatus(ctx context.Context, occurrence *jobistemer.JobOccurrence) error {
	// Convert to storage format
	storageOcc := &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		OwningNodeID:  occurrence.OwningNodeID,
		StartTime:     occurrence.StartTime,
		EndTime:       occurrence.EndTime,
		IsRecoveryRun: occurrence.IsRecoveryRun,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     time.Now(),
	}
	return e.store.UpdateJobOccurrence(ctx, storageOcc)
}

func timePtr(t time.Time) *time.Time {
	return &t
}
