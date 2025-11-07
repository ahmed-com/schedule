package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/ticker"
)

// mockSchedulerStorage implements a minimal storage for scheduler tests
type mockSchedulerStorage struct {
	jobs              []*storage.Job
	tasks             []*storage.Task
	occurrences       []*storage.JobOccurrence
	taskRuns          []*storage.TaskRun
	executionAttempts []*storage.ExecutionAttempt
}

func newMockSchedulerStorage() *mockSchedulerStorage {
	return &mockSchedulerStorage{
		jobs:              make([]*storage.Job, 0),
		tasks:             make([]*storage.Task, 0),
		occurrences:       make([]*storage.JobOccurrence, 0),
		taskRuns:          make([]*storage.TaskRun, 0),
		executionAttempts: make([]*storage.ExecutionAttempt, 0),
	}
}

func (m *mockSchedulerStorage) CreateJob(ctx context.Context, job *storage.Job) error {
	m.jobs = append(m.jobs, job)
	return nil
}

func (m *mockSchedulerStorage) GetJob(ctx context.Context, jobID string) (*storage.Job, error) {
	for _, job := range m.jobs {
		if job.ID == jobID {
			return job, nil
		}
	}
	return nil, nil
}

func (m *mockSchedulerStorage) CreateTask(ctx context.Context, task *storage.Task) error {
	m.tasks = append(m.tasks, task)
	return nil
}

func (m *mockSchedulerStorage) CreateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	m.occurrences = append(m.occurrences, occurrence)
	return nil
}

func (m *mockSchedulerStorage) UpdateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	for i, occ := range m.occurrences {
		if occ.ID == occurrence.ID {
			m.occurrences[i] = occurrence
			return nil
		}
	}
	return nil
}

func (m *mockSchedulerStorage) ListRunningOccurrences(ctx context.Context) ([]*storage.JobOccurrence, error) {
	var running []*storage.JobOccurrence
	for _, occ := range m.occurrences {
		if occ.Status == jobistemer.JobStatusRunning {
			running = append(running, occ)
		}
	}
	return running, nil
}

func (m *mockSchedulerStorage) CreateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	m.taskRuns = append(m.taskRuns, run)
	return nil
}

func (m *mockSchedulerStorage) UpdateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	for i, tr := range m.taskRuns {
		if tr.ID == run.ID {
			m.taskRuns[i] = run
			return nil
		}
	}
	return nil
}

func (m *mockSchedulerStorage) CreateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	m.executionAttempts = append(m.executionAttempts, attempt)
	return nil
}

func (m *mockSchedulerStorage) UpdateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	for i, att := range m.executionAttempts {
		if att.ID == attempt.ID {
			m.executionAttempts[i] = attempt
			return nil
		}
	}
	return nil
}

// Implement other required methods with no-ops
func (m *mockSchedulerStorage) UpdateJob(ctx context.Context, job *storage.Job) error { return nil }
func (m *mockSchedulerStorage) DeleteJob(ctx context.Context, jobID string) error     { return nil }
func (m *mockSchedulerStorage) ListJobs(ctx context.Context) ([]*storage.Job, error)  { return nil, nil }
func (m *mockSchedulerStorage) GetTask(ctx context.Context, taskID string) (*storage.Task, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) UpdateTask(ctx context.Context, task *storage.Task) error { return nil }
func (m *mockSchedulerStorage) DeleteTask(ctx context.Context, taskID string) error      { return nil }
func (m *mockSchedulerStorage) ListTasksByJobID(ctx context.Context, jobID string) ([]*storage.Task, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) GetJobOccurrence(ctx context.Context, occurrenceID string) (*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) DeleteJobOccurrence(ctx context.Context, occurrenceID string) error {
	return nil
}
func (m *mockSchedulerStorage) ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) GetTaskRun(ctx context.Context, runID string) (*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) DeleteTaskRun(ctx context.Context, runID string) error { return nil }
func (m *mockSchedulerStorage) ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) GetExecutionAttempt(ctx context.Context, attemptID string) (*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockSchedulerStorage) Close() error { return nil }

func TestSchedulerRegisterJob(t *testing.T) {
	store := newMockSchedulerStorage()
	config := jobistemer.SchedulerConfig{
		MaxConcurrentJobs: 5,
		ReaperInterval:    5 * time.Minute,
		NodeID:            "test-node",
	}

	sched := NewScheduler(config, store)

	jobConfig := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, jobConfig)

	err := sched.RegisterJob(job)
	if err != nil {
		t.Fatalf("Failed to register job: %v", err)
	}

	// Verify job was registered
	retrievedJob, exists := sched.GetJob(job.ID)
	if !exists {
		t.Error("Job should exist after registration")
	}
	if retrievedJob.ID != job.ID {
		t.Errorf("Expected job ID %s, got %s", job.ID, retrievedJob.ID)
	}

	// Verify job was persisted
	if len(store.jobs) != 1 {
		t.Errorf("Expected 1 job in storage, got %d", len(store.jobs))
	}
}

func TestSchedulerListJobs(t *testing.T) {
	store := newMockSchedulerStorage()
	config := jobistemer.SchedulerConfig{
		MaxConcurrentJobs: 5,
		ReaperInterval:    5 * time.Minute,
		NodeID:            "test-node",
	}

	sched := NewScheduler(config, store)

	jobConfig := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick1, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job1 := jobistemer.NewJob("job-1", tick1, jobConfig)
	sched.RegisterJob(job1)

	tick2, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job2 := jobistemer.NewJob("job-2", tick2, jobConfig)
	sched.RegisterJob(job2)

	jobs := sched.ListJobs()
	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}
}

func TestSchedulerStartStop(t *testing.T) {
	store := newMockSchedulerStorage()
	config := jobistemer.SchedulerConfig{
		MaxConcurrentJobs: 5,
		ReaperInterval:    5 * time.Minute,
		NodeID:            "test-node",
	}

	sched := NewScheduler(config, store)

	jobConfig := jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeSequential,
		FailureStrategy:   jobistemer.FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
		RecoveryStrategy:  jobistemer.RecoveryStrategyMarkAsMissed,
	}

	// Use a ticker that fires soon
	tick, _ := ticker.NewOnceTicker(time.Now().Add(100*time.Millisecond), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, jobConfig)

	var executed int32
	job.AddTask("task-1", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
		atomic.StoreInt32(&executed, 1)
		return nil
	}, jobistemer.TaskConfig{})

	sched.RegisterJob(job)

	err := sched.Start()
	if err != nil {
		t.Fatalf("Failed to start scheduler: %v", err)
	}

	// Wait for job to execute
	time.Sleep(500 * time.Millisecond)

	err = sched.Shutdown(5 * time.Second)
	if err != nil {
		t.Fatalf("Failed to shutdown scheduler: %v", err)
	}

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("Task should have been executed")
	}
}
