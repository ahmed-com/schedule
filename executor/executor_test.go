package executor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/ticker"
)

// mockStorage implements a minimal storage interface for testing
type mockStorage struct {
	occurrences      []*storage.JobOccurrence
	taskRuns         []*storage.TaskRun
	executionAttempts []*storage.ExecutionAttempt
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		occurrences:      make([]*storage.JobOccurrence, 0),
		taskRuns:         make([]*storage.TaskRun, 0),
		executionAttempts: make([]*storage.ExecutionAttempt, 0),
	}
}

func (m *mockStorage) CreateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	m.occurrences = append(m.occurrences, occurrence)
	return nil
}

func (m *mockStorage) UpdateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	for i, occ := range m.occurrences {
		if occ.ID == occurrence.ID {
			m.occurrences[i] = occurrence
			return nil
		}
	}
	return errors.New("occurrence not found")
}

func (m *mockStorage) CreateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	m.taskRuns = append(m.taskRuns, run)
	return nil
}

func (m *mockStorage) UpdateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	for i, tr := range m.taskRuns {
		if tr.ID == run.ID {
			m.taskRuns[i] = run
			return nil
		}
	}
	return errors.New("task run not found")
}

func (m *mockStorage) CreateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	m.executionAttempts = append(m.executionAttempts, attempt)
	return nil
}

func (m *mockStorage) UpdateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	for i, att := range m.executionAttempts {
		if att.ID == attempt.ID {
			m.executionAttempts[i] = attempt
			return nil
		}
	}
	return errors.New("execution attempt not found")
}

// Implement other required methods with no-ops
func (m *mockStorage) CreateJob(ctx context.Context, job *storage.Job) error { return nil }
func (m *mockStorage) GetJob(ctx context.Context, jobID string) (*storage.Job, error) {
	return nil, nil
}
func (m *mockStorage) UpdateJob(ctx context.Context, job *storage.Job) error { return nil }
func (m *mockStorage) DeleteJob(ctx context.Context, jobID string) error     { return nil }
func (m *mockStorage) ListJobs(ctx context.Context) ([]*storage.Job, error)  { return nil, nil }
func (m *mockStorage) CreateTask(ctx context.Context, task *storage.Task) error { return nil }
func (m *mockStorage) GetTask(ctx context.Context, taskID string) (*storage.Task, error) {
	return nil, nil
}
func (m *mockStorage) UpdateTask(ctx context.Context, task *storage.Task) error { return nil }
func (m *mockStorage) DeleteTask(ctx context.Context, taskID string) error      { return nil }
func (m *mockStorage) ListTasksByJobID(ctx context.Context, jobID string) ([]*storage.Task, error) {
	return nil, nil
}
func (m *mockStorage) GetJobOccurrence(ctx context.Context, occurrenceID string) (*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) DeleteJobOccurrence(ctx context.Context, occurrenceID string) error {
	return nil
}
func (m *mockStorage) ListRunningOccurrences(ctx context.Context) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) ListPendingOccurrences(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) GetTaskRun(ctx context.Context, runID string) (*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockStorage) DeleteTaskRun(ctx context.Context, runID string) error { return nil }
func (m *mockStorage) ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockStorage) GetExecutionAttempt(ctx context.Context, attemptID string) (*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockStorage) ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockStorage) Close() error { return nil }

func TestExecutorSequentialSuccess(t *testing.T) {
	store := newMockStorage()
	executor := NewExecutor(store)

	config := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	var task1Called, task2Called bool
	job.AddTask("task-1", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		task1Called = true
		return nil
	}, jobistemer.TaskConfig{})

	job.AddTask("task-2", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		task2Called = true
		return nil
	}, jobistemer.TaskConfig{})

	occurrence := &jobistemer.JobOccurrence{
		ID:            "occ-1",
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: time.Now(),
		Status:        jobistemer.JobStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Pre-create the occurrence in storage
	store.CreateJobOccurrence(context.Background(), &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	})

	report, err := executor.ExecuteJobOccurrence(context.Background(), job, occurrence)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if !task1Called {
		t.Error("Task 1 was not called")
	}
	if !task2Called {
		t.Error("Task 2 was not called")
	}
	if report.GroupOutcome != "completed" {
		t.Errorf("Expected group outcome 'completed', got '%s'", report.GroupOutcome)
	}
	if len(report.TaskReports) != 2 {
		t.Errorf("Expected 2 task reports, got %d", len(report.TaskReports))
	}
}

func TestExecutorSequentialFailFast(t *testing.T) {
	store := newMockStorage()
	executor := NewExecutor(store)

	config := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	var task2Called bool
	job.AddTask("task-1", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		return errors.New("task 1 failed")
	}, jobistemer.TaskConfig{})

	job.AddTask("task-2", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		task2Called = true
		return nil
	}, jobistemer.TaskConfig{})

	occurrence := &jobistemer.JobOccurrence{
		ID:            "occ-1",
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: time.Now(),
		Status:        jobistemer.JobStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Pre-create the occurrence in storage
	store.CreateJobOccurrence(context.Background(), &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	})

	report, err := executor.ExecuteJobOccurrence(context.Background(), job, occurrence)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if task2Called {
		t.Error("Task 2 should not have been called due to fail-fast")
	}
	if report == nil {
		t.Fatal("Expected report to be non-nil")
	}
	if report.GroupOutcome != "failed" {
		t.Errorf("Expected group outcome 'failed', got '%s'", report.GroupOutcome)
	}
	if len(report.TaskReports) != 1 {
		t.Errorf("Expected 1 task report, got %d", len(report.TaskReports))
	}
}

func TestExecutorParallelSuccess(t *testing.T) {
	store := newMockStorage()
	executor := NewExecutor(store)

	config := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeParallel,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	var task1Called, task2Called int32
	job.AddTask("task-1", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		atomic.StoreInt32(&task1Called, 1)
		time.Sleep(10 * time.Millisecond)
		return nil
	}, jobistemer.TaskConfig{})

	job.AddTask("task-2", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		atomic.StoreInt32(&task2Called, 1)
		time.Sleep(10 * time.Millisecond)
		return nil
	}, jobistemer.TaskConfig{})

	occurrence := &jobistemer.JobOccurrence{
		ID:            "occ-1",
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: time.Now(),
		Status:        jobistemer.JobStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Pre-create the occurrence in storage
	store.CreateJobOccurrence(context.Background(), &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	})

	report, err := executor.ExecuteJobOccurrence(context.Background(), job, occurrence)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if atomic.LoadInt32(&task1Called) != 1 {
		t.Error("Task 1 was not called")
	}
	if atomic.LoadInt32(&task2Called) != 1 {
		t.Error("Task 2 was not called")
	}
	if report.GroupOutcome != "completed" {
		t.Errorf("Expected group outcome 'completed', got '%s'", report.GroupOutcome)
	}
	if len(report.TaskReports) != 2 {
		t.Errorf("Expected 2 task reports, got %d", len(report.TaskReports))
	}
}

func TestExecutorRetryLogic(t *testing.T) {
	store := newMockStorage()
	executor := NewExecutor(store)

	retryPolicy := &jobistemer.RetryPolicy{
		MaxRetries:    2,
		RetryInterval: 10 * time.Millisecond,
		BackoffFactor: 1.0,
	}

	config := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
		TaskConfig: jobistemer.TaskConfig{
			RetryPolicy: retryPolicy,
		},
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	var attemptCount int32
	job.AddTask("task-1", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 3 {
			return errors.New("temporary failure")
		}
		return nil
	}, jobistemer.TaskConfig{})

	occurrence := &jobistemer.JobOccurrence{
		ID:            "occ-1",
		JobID:         job.ID,
		JobVersion:    job.Version,
		ScheduledTime: time.Now(),
		Status:        jobistemer.JobStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Pre-create the occurrence in storage
	store.CreateJobOccurrence(context.Background(), &storage.JobOccurrence{
		ID:            occurrence.ID,
		JobID:         occurrence.JobID,
		JobVersion:    occurrence.JobVersion,
		ScheduledTime: occurrence.ScheduledTime,
		Status:        occurrence.Status,
		CreatedAt:     occurrence.CreatedAt,
		UpdatedAt:     occurrence.UpdatedAt,
	})

	report, err := executor.ExecuteJobOccurrence(context.Background(), job, occurrence)

	if err != nil {
		t.Fatalf("Expected no error after retries, got %v", err)
	}
	if atomic.LoadInt32(&attemptCount) != 3 {
		t.Errorf("Expected 3 attempts, got %d", atomic.LoadInt32(&attemptCount))
	}
	if len(report.TaskReports) != 1 {
		t.Fatalf("Expected 1 task report, got %d", len(report.TaskReports))
	}
	if len(report.TaskReports[0].Attempts) != 3 {
		t.Errorf("Expected 3 attempt reports, got %d", len(report.TaskReports[0].Attempts))
	}
	if report.TaskReports[0].FinalStatus != jobistemer.TaskStatusSuccess {
		t.Errorf("Expected final status Success, got %v", report.TaskReports[0].FinalStatus)
	}
}
