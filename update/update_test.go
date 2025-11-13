package update

import (
	"context"
	"testing"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/ticker"
)

// Mock storage for testing
type mockUpdateStorage struct {
	jobs        map[string]*storage.Job
	tasks       map[string]*storage.Task
	occurrences map[string]*storage.JobOccurrence
}

func newMockUpdateStorage() *mockUpdateStorage {
	return &mockUpdateStorage{
		jobs:        make(map[string]*storage.Job),
		tasks:       make(map[string]*storage.Task),
		occurrences: make(map[string]*storage.JobOccurrence),
	}
}

func (m *mockUpdateStorage) GetJob(ctx context.Context, jobID string) (*storage.Job, error) {
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, nil
	}
	return job, nil
}

func (m *mockUpdateStorage) UpdateJob(ctx context.Context, job *storage.Job) error {
	m.jobs[job.ID] = job
	return nil
}

func (m *mockUpdateStorage) CreateTask(ctx context.Context, task *storage.Task) error {
	m.tasks[task.ID] = task
	return nil
}

func (m *mockUpdateStorage) GetTask(ctx context.Context, taskID string) (*storage.Task, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, nil
	}
	return task, nil
}

func (m *mockUpdateStorage) UpdateTask(ctx context.Context, task *storage.Task) error {
	m.tasks[task.ID] = task
	return nil
}

func (m *mockUpdateStorage) DeleteTask(ctx context.Context, taskID string) error {
	delete(m.tasks, taskID)
	return nil
}

func (m *mockUpdateStorage) ListPendingOccurrences(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	var result []*storage.JobOccurrence
	for _, occ := range m.occurrences {
		if occ.JobID == jobID && occ.Status == jobistemer.JobStatusPending {
			result = append(result, occ)
		}
	}
	return result, nil
}

func (m *mockUpdateStorage) UpdateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	m.occurrences[occurrence.ID] = occurrence
	return nil
}

// Implement other required methods with no-ops
func (m *mockUpdateStorage) CreateJob(ctx context.Context, job *storage.Job) error { return nil }
func (m *mockUpdateStorage) DeleteJob(ctx context.Context, jobID string) error     { return nil }
func (m *mockUpdateStorage) ListJobs(ctx context.Context) ([]*storage.Job, error)  { return nil, nil }
func (m *mockUpdateStorage) ListTasksByJobID(ctx context.Context, jobID string) ([]*storage.Task, error) {
	return nil, nil
}
func (m *mockUpdateStorage) CreateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	return nil
}
func (m *mockUpdateStorage) GetJobOccurrence(ctx context.Context, occurrenceID string) (*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockUpdateStorage) DeleteJobOccurrence(ctx context.Context, occurrenceID string) error {
	return nil
}
func (m *mockUpdateStorage) ListRunningOccurrences(ctx context.Context) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockUpdateStorage) ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockUpdateStorage) ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockUpdateStorage) CreateTaskRun(ctx context.Context, run *storage.TaskRun) error { return nil }
func (m *mockUpdateStorage) GetTaskRun(ctx context.Context, runID string) (*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockUpdateStorage) UpdateTaskRun(ctx context.Context, run *storage.TaskRun) error { return nil }
func (m *mockUpdateStorage) DeleteTaskRun(ctx context.Context, runID string) error         { return nil }
func (m *mockUpdateStorage) ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockUpdateStorage) CreateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return nil
}
func (m *mockUpdateStorage) GetExecutionAttempt(ctx context.Context, attemptID string) (*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockUpdateStorage) UpdateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return nil
}
func (m *mockUpdateStorage) ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockUpdateStorage) Close() error { return nil }

func TestApplyUpdate_VersionMismatch(t *testing.T) {
	store := newMockUpdateStorage()
	mgr := NewUpdateManager(store)

	// Create a job with version 1
	job := &storage.Job{
		ID:      "test-job",
		Version: 1,
	}
	store.jobs["test-job"] = job

	// Try to update with wrong expected version
	update := &JobUpdate{
		JobID:           "test-job",
		ExpectedVersion: 2,
	}

	err := mgr.ApplyUpdate(context.Background(), update)
	if err == nil {
		t.Error("Expected version mismatch error, got nil")
	}
}

func TestApplyUpdate_Success(t *testing.T) {
	store := newMockUpdateStorage()
	mgr := NewUpdateManager(store)

	// Create a job with version 1
	job := &storage.Job{
		ID:      "test-job",
		Version: 1,
		Config:  []byte("{}"),
	}
	store.jobs["test-job"] = job

	// Apply update
	newConfig := &jobistemer.JobConfig{
		TaskExecutionMode: jobistemer.TaskExecutionModeParallel,
	}
	update := &JobUpdate{
		JobID:           "test-job",
		ExpectedVersion: 1,
		NewConfig:       newConfig,
		Policy:          jobistemer.UpdatePolicyImmediate,
	}

	err := mgr.ApplyUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify version was incremented
	updatedJob := store.jobs["test-job"]
	if updatedJob.Version != 2 {
		t.Errorf("Expected version 2, got %d", updatedJob.Version)
	}
}

func TestApplyUpdate_AddTask(t *testing.T) {
	store := newMockUpdateStorage()
	mgr := NewUpdateManager(store)

	job := &storage.Job{
		ID:      "test-job",
		Version: 1,
		Config:  []byte("{}"),
	}
	store.jobs["test-job"] = job

	// Add a new task
	newTask := &jobistemer.Task{
		ID:    "task-1",
		Name:  "New Task",
		Order: 0,
	}
	update := &JobUpdate{
		JobID:           "test-job",
		ExpectedVersion: 1,
		AddedTasks:      []*jobistemer.Task{newTask},
		Policy:          jobistemer.UpdatePolicyImmediate,
	}

	err := mgr.ApplyUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify task was added
	if _, exists := store.tasks["task-1"]; !exists {
		t.Error("Expected task to be added to storage")
	}
}

func TestApplyUpdate_RemoveTask(t *testing.T) {
	store := newMockUpdateStorage()
	mgr := NewUpdateManager(store)

	job := &storage.Job{
		ID:      "test-job",
		Version: 1,
		Config:  []byte("{}"),
	}
	store.jobs["test-job"] = job

	// Add a task first
	store.tasks["task-1"] = &storage.Task{
		ID:    "task-1",
		JobID: "test-job",
		Name:  "Task to Remove",
	}

	// Remove the task
	update := &JobUpdate{
		JobID:           "test-job",
		ExpectedVersion: 1,
		RemovedTaskIDs:  []string{"task-1"},
		Policy:          jobistemer.UpdatePolicyImmediate,
	}

	err := mgr.ApplyUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify task was removed
	if _, exists := store.tasks["task-1"]; exists {
		t.Error("Expected task to be removed from storage")
	}
}

func TestDiffSchedules(t *testing.T) {
	mgr := NewUpdateManager(nil)

	start := time.Now()
	end := start.Add(5 * time.Hour)

	// Create two tickers with different schedules
	oldTicker, _ := ticker.NewOnceTicker(start.Add(2*time.Hour), ticker.TickerConfig{})
	newTicker, _ := ticker.NewOnceTicker(start.Add(3*time.Hour), ticker.TickerConfig{})

	added, removed, err := mgr.DiffSchedules(oldTicker, newTicker, start, end)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Old had one occurrence at 2 hours, new has one at 3 hours
	if len(added) != 1 {
		t.Errorf("Expected 1 added time, got %d", len(added))
	}
	if len(removed) != 1 {
		t.Errorf("Expected 1 removed time, got %d", len(removed))
	}
}
