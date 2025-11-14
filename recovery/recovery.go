package recovery

import (
	"context"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/concurrency"
	"github.com/ahmed-com/schedule/executor"
	"github.com/ahmed-com/schedule/id"
	"github.com/ahmed-com/schedule/metrics"
	"github.com/ahmed-com/schedule/storage"
)

// RecoveryHandler handles recovery of missed job occurrences
type RecoveryHandler struct {
	store    storage.Storage
	executor *executor.Executor
	pool     *concurrency.WorkerPool
	metrics  metrics.MetricsCollector
}

// NewRecoveryHandler creates a new recovery handler
func NewRecoveryHandler(store storage.Storage, exec *executor.Executor, pool *concurrency.WorkerPool) *RecoveryHandler {
	return &RecoveryHandler{
		store:    store,
		executor: exec,
		pool:     pool,
		metrics:  metrics.NewNoOpMetrics(), // Default to no-op
	}
}

// SetMetrics sets the metrics collector for this recovery handler
func (r *RecoveryHandler) SetMetrics(m metrics.MetricsCollector) {
	r.metrics = m
}

// ApplyStrategy applies the configured recovery strategy to missed occurrences
func (r *RecoveryHandler) ApplyStrategy(ctx context.Context, job *jobistemer.Job, missedTimes []time.Time) error {
	if len(missedTimes) == 0 {
		return nil
	}

	switch job.Config.RecoveryStrategy {
	case jobistemer.RecoveryStrategyExecuteAll:
		return r.executeAll(ctx, job, missedTimes)
	case jobistemer.RecoveryStrategyExecuteLast:
		return r.executeLast(ctx, job, missedTimes)
	case jobistemer.RecoveryStrategyMarkAsMissed:
		return r.markAsMissed(ctx, job, missedTimes)
	case jobistemer.RecoveryStrategyBoundedWindow:
		return r.boundedWindow(ctx, job, missedTimes)
	default:
		return r.executeAll(ctx, job, missedTimes)
	}
}

// executeAll executes all missed occurrences
func (r *RecoveryHandler) executeAll(ctx context.Context, job *jobistemer.Job, missedTimes []time.Time) error {
	for _, t := range missedTimes {
		if err := r.scheduleRecoveryRun(ctx, job, t, "full_backfill"); err != nil {
			return err
		}
	}
	return nil
}

// executeLast executes only the most recent missed occurrence
func (r *RecoveryHandler) executeLast(ctx context.Context, job *jobistemer.Job, missedTimes []time.Time) error {
	if len(missedTimes) == 0 {
		return nil
	}
	lastTime := missedTimes[len(missedTimes)-1]
	return r.scheduleRecoveryRun(ctx, job, lastTime, "latest_only")
}

// markAsMissed marks occurrences as missed without executing them
func (r *RecoveryHandler) markAsMissed(ctx context.Context, job *jobistemer.Job, missedTimes []time.Time) error {
	// Just log, don't execute
	// In a production system, you might want to create occurrence records with a "Missed" status
	return nil
}

// boundedWindow applies a bounded window to missed occurrences
func (r *RecoveryHandler) boundedWindow(ctx context.Context, job *jobistemer.Job, missedTimes []time.Time) error {
	if job.Config.BoundedWindow == nil {
		return r.executeAll(ctx, job, missedTimes)
	}

	window := job.Config.BoundedWindow
	var filteredTimes []time.Time

	// Apply max duration filter
	if window.MaxDuration > 0 {
		cutoff := time.Now().Add(-window.MaxDuration)
		for _, t := range missedTimes {
			if t.After(cutoff) {
				filteredTimes = append(filteredTimes, t)
			}
		}
	} else {
		filteredTimes = missedTimes
	}

	// Apply max occurrences filter
	if window.MaxOccurrences > 0 && len(filteredTimes) > window.MaxOccurrences {
		filteredTimes = filteredTimes[len(filteredTimes)-window.MaxOccurrences:]
	}

	return r.executeAll(ctx, job, filteredTimes)
}

// scheduleRecoveryRun schedules a recovery run for a specific time
func (r *RecoveryHandler) scheduleRecoveryRun(ctx context.Context, job *jobistemer.Job, scheduledTime time.Time, strategy string) error {
	occurrenceID := id.GenerateJobOccurrenceID(job.ID, scheduledTime)

	occurrence := &jobistemer.JobOccurrence{
		ID:            occurrenceID,
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: scheduledTime,
		Status:        jobistemer.JobStatusPending,
		Config:        job.Config,
		IsRecoveryRun: true,
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
		IsRecoveryRun: occurrence.IsRecoveryRun,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	}

	// Atomic create (idempotent)
	err := r.store.CreateJobOccurrence(ctx, storageOcc)
	if err != nil {
		// Already exists, skip
		return nil
	}

	// Track recovery metric
	r.metrics.IncRecoveryRuns(job.Name, strategy)

	// Submit to worker pool
	r.pool.Submit(func() {
		r.executor.ExecuteJobOccurrence(ctx, job, occurrence)
	})

	return nil
}
