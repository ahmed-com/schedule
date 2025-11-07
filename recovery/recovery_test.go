package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/concurrency"
	"github.com/ahmed-com/schedule/executor"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/ticker"
)

// mockStorage for recovery tests (simplified version)
type mockStorage struct {
	occurrences []*storage.JobOccurrence
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		occurrences: make([]*storage.JobOccurrence, 0),
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
	return nil
}

func (m *mockStorage) ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*storage.JobOccurrence, error) {
	var stale []*storage.JobOccurrence
	cutoff := time.Now().Add(-threshold)
	
	for _, occ := range m.occurrences {
		if occ.Status == jobistemer.JobStatusRunning && occ.StartTime != nil && occ.StartTime.Before(cutoff) {
			stale = append(stale, occ)
		}
	}
	return stale, nil
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
func (m *mockStorage) ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	return nil, nil
}
func (m *mockStorage) CreateTaskRun(ctx context.Context, run *storage.TaskRun) error { return nil }
func (m *mockStorage) GetTaskRun(ctx context.Context, runID string) (*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockStorage) UpdateTaskRun(ctx context.Context, run *storage.TaskRun) error { return nil }
func (m *mockStorage) DeleteTaskRun(ctx context.Context, runID string) error         { return nil }
func (m *mockStorage) ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*storage.TaskRun, error) {
	return nil, nil
}
func (m *mockStorage) CreateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return nil
}
func (m *mockStorage) GetExecutionAttempt(ctx context.Context, attemptID string) (*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockStorage) UpdateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return nil
}
func (m *mockStorage) ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*storage.ExecutionAttempt, error) {
	return nil, nil
}
func (m *mockStorage) Close() error { return nil }

func TestRecoveryExecuteAll(t *testing.T) {
	store := newMockStorage()
	exec := executor.NewExecutor(store)
	pool := concurrency.NewWorkerPool(5)
	pool.Start()
	defer pool.Stop()

	handler := NewRecoveryHandler(store, exec, pool)

	config := jobistemer.JobConfig{
		RecoveryStrategy: jobistemer.RecoveryStrategyExecuteAll,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	missedTimes := []time.Time{
		time.Now().Add(-3 * time.Hour),
		time.Now().Add(-2 * time.Hour),
		time.Now().Add(-1 * time.Hour),
	}

	err := handler.ApplyStrategy(context.Background(), job, missedTimes)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Wait a bit for tasks to be scheduled
	time.Sleep(100 * time.Millisecond)

	if len(store.occurrences) != 3 {
		t.Errorf("Expected 3 occurrences created, got %d", len(store.occurrences))
	}

	for _, occ := range store.occurrences {
		if !occ.IsRecoveryRun {
			t.Error("Occurrence should be marked as recovery run")
		}
	}
}

func TestRecoveryExecuteLast(t *testing.T) {
	store := newMockStorage()
	exec := executor.NewExecutor(store)
	pool := concurrency.NewWorkerPool(5)
	pool.Start()
	defer pool.Stop()

	handler := NewRecoveryHandler(store, exec, pool)

	config := jobistemer.JobConfig{
		RecoveryStrategy: jobistemer.RecoveryStrategyExecuteLast,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	missedTimes := []time.Time{
		time.Now().Add(-3 * time.Hour),
		time.Now().Add(-2 * time.Hour),
		time.Now().Add(-1 * time.Hour),
	}

	err := handler.ApplyStrategy(context.Background(), job, missedTimes)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Wait a bit for tasks to be scheduled
	time.Sleep(100 * time.Millisecond)

	if len(store.occurrences) != 1 {
		t.Errorf("Expected 1 occurrence created, got %d", len(store.occurrences))
	}

	if len(store.occurrences) > 0 {
		lastTime := missedTimes[len(missedTimes)-1]
		if !store.occurrences[0].ScheduledTime.Equal(lastTime) {
			t.Error("Should have scheduled the last occurrence")
		}
	}
}

func TestRecoveryBoundedWindow(t *testing.T) {
	store := newMockStorage()
	exec := executor.NewExecutor(store)
	pool := concurrency.NewWorkerPool(5)
	pool.Start()
	defer pool.Stop()

	handler := NewRecoveryHandler(store, exec, pool)

	config := jobistemer.JobConfig{
		RecoveryStrategy: jobistemer.RecoveryStrategyBoundedWindow,
		BoundedWindow: &jobistemer.BoundedWindow{
			MaxOccurrences: 2,
		},
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := jobistemer.NewJob("test-job", tick, config)

	missedTimes := []time.Time{
		time.Now().Add(-5 * time.Hour),
		time.Now().Add(-4 * time.Hour),
		time.Now().Add(-3 * time.Hour),
		time.Now().Add(-2 * time.Hour),
		time.Now().Add(-1 * time.Hour),
	}

	err := handler.ApplyStrategy(context.Background(), job, missedTimes)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Wait a bit for tasks to be scheduled
	time.Sleep(100 * time.Millisecond)

	if len(store.occurrences) != 2 {
		t.Errorf("Expected 2 occurrences created (bounded window), got %d", len(store.occurrences))
	}
}

func TestReaperStaleJobs(t *testing.T) {
	store := newMockStorage()

	// Create a stale occurrence
	staleTime := time.Now().Add(-2 * time.Hour)
	store.occurrences = append(store.occurrences, &storage.JobOccurrence{
		ID:        "stale-1",
		JobID:     "job-1",
		Status:    jobistemer.JobStatusRunning,
		StartTime: &staleTime,
	})

	reaper := NewReaper(store, 1*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	reaper.Start(ctx)

	// Wait for reaper to run
	time.Sleep(1500 * time.Millisecond)

	cancel()
	reaper.Stop()

	// Check if stale job was marked as failed
	found := false
	for _, occ := range store.occurrences {
		if occ.ID == "stale-1" && occ.Status == jobistemer.JobStatusFailedStale {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected stale occurrence to be marked as failed")
	}
}
