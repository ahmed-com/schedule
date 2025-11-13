package metrics

import (
	"testing"
	"time"
)

func TestInMemoryMetrics_Gauges(t *testing.T) {
	m := NewInMemoryMetrics()

	// Test initial state
	if got := m.GetJobsRunning(); got != 0 {
		t.Errorf("GetJobsRunning() = %d, want 0", got)
	}

	// Test setting gauges
	m.SetJobsRunning(5)
	if got := m.GetJobsRunning(); got != 5 {
		t.Errorf("GetJobsRunning() = %d, want 5", got)
	}

	m.SetJobsInQueue(3)
	if got := m.GetJobsInQueue(); got != 3 {
		t.Errorf("GetJobsInQueue() = %d, want 3", got)
	}

	m.SetJobsPaused(2)
	if got := m.GetJobsPaused(); got != 2 {
		t.Errorf("GetJobsPaused() = %d, want 2", got)
	}

	// Test updating gauges
	m.SetJobsRunning(10)
	if got := m.GetJobsRunning(); got != 10 {
		t.Errorf("GetJobsRunning() after update = %d, want 10", got)
	}
}

func TestInMemoryMetrics_Counters(t *testing.T) {
	m := NewInMemoryMetrics()

	// Test task attempts counter
	m.IncTaskAttempts("job1", "task1", "success")
	m.IncTaskAttempts("job1", "task1", "success")
	m.IncTaskAttempts("job1", "task1", "failure")

	if got := m.GetTaskAttempts("job1", "task1", "success"); got != 2 {
		t.Errorf("GetTaskAttempts(job1, task1, success) = %d, want 2", got)
	}
	if got := m.GetTaskAttempts("job1", "task1", "failure"); got != 1 {
		t.Errorf("GetTaskAttempts(job1, task1, failure) = %d, want 1", got)
	}

	// Test job occurrences counter
	m.IncJobOccurrences("job1", "completed")
	m.IncJobOccurrences("job1", "completed")
	m.IncJobOccurrences("job1", "failed")

	if got := m.GetJobOccurrences("job1", "completed"); got != 2 {
		t.Errorf("GetJobOccurrences(job1, completed) = %d, want 2", got)
	}
	if got := m.GetJobOccurrences("job1", "failed"); got != 1 {
		t.Errorf("GetJobOccurrences(job1, failed) = %d, want 1", got)
	}

	// Test recovery runs counter
	m.IncRecoveryRuns("job1", "full_backfill")
	m.IncRecoveryRuns("job1", "latest_only")

	if got := m.GetRecoveryRuns("job1", "full_backfill"); got != 1 {
		t.Errorf("GetRecoveryRuns(job1, full_backfill) = %d, want 1", got)
	}
	if got := m.GetRecoveryRuns("job1", "latest_only"); got != 1 {
		t.Errorf("GetRecoveryRuns(job1, latest_only) = %d, want 1", got)
	}
}

func TestInMemoryMetrics_Histograms(t *testing.T) {
	m := NewInMemoryMetrics()

	// Test job duration observations
	m.ObserveJobDuration("job1", 1*time.Second)
	m.ObserveJobDuration("job1", 2*time.Second)
	m.ObserveJobDuration("job1", 3*time.Second)

	durations := m.GetJobDurations("job1")
	if len(durations) != 3 {
		t.Errorf("GetJobDurations(job1) length = %d, want 3", len(durations))
	}
	if durations[0] != 1*time.Second {
		t.Errorf("durations[0] = %v, want 1s", durations[0])
	}

	// Test task duration observations
	m.ObserveTaskDuration("job1", "task1", 500*time.Millisecond)
	m.ObserveTaskDuration("job1", "task1", 750*time.Millisecond)

	taskDurations := m.GetTaskDurations("job1", "task1")
	if len(taskDurations) != 2 {
		t.Errorf("GetTaskDurations(job1, task1) length = %d, want 2", len(taskDurations))
	}
}

func TestInMemoryMetrics_Reset(t *testing.T) {
	m := NewInMemoryMetrics()

	// Set some metrics
	m.SetJobsRunning(5)
	m.IncTaskAttempts("job1", "task1", "success")
	m.ObserveJobDuration("job1", 1*time.Second)

	// Verify they're set
	if m.GetJobsRunning() == 0 {
		t.Error("Expected jobsRunning to be non-zero before reset")
	}

	// Reset
	m.Reset()

	// Verify everything is cleared
	if got := m.GetJobsRunning(); got != 0 {
		t.Errorf("GetJobsRunning() after reset = %d, want 0", got)
	}
	if got := m.GetTaskAttempts("job1", "task1", "success"); got != 0 {
		t.Errorf("GetTaskAttempts() after reset = %d, want 0", got)
	}
	if durations := m.GetJobDurations("job1"); len(durations) != 0 {
		t.Errorf("GetJobDurations() after reset length = %d, want 0", len(durations))
	}
}

func TestInMemoryMetrics_Concurrency(t *testing.T) {
	m := NewInMemoryMetrics()

	// Test concurrent increments
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				m.IncTaskAttempts("job1", "task1", "success")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 1000 total increments
	if got := m.GetTaskAttempts("job1", "task1", "success"); got != 1000 {
		t.Errorf("GetTaskAttempts() after concurrent increments = %d, want 1000", got)
	}
}

func TestNoOpMetrics(t *testing.T) {
	m := NewNoOpMetrics()

	// Verify all operations are no-ops and don't panic
	m.SetJobsRunning(5)
	m.SetJobsInQueue(3)
	m.SetJobsPaused(2)
	m.IncTaskAttempts("job1", "task1", "success")
	m.IncJobOccurrences("job1", "completed")
	m.IncRecoveryRuns("job1", "full_backfill")
	m.ObserveJobDuration("job1", 1*time.Second)
	m.ObserveTaskDuration("job1", "task1", 500*time.Millisecond)

	// All getters should return zero values
	if got := m.GetJobsRunning(); got != 0 {
		t.Errorf("NoOpMetrics.GetJobsRunning() = %d, want 0", got)
	}
	if got := m.GetTaskAttempts("job1", "task1", "success"); got != 0 {
		t.Errorf("NoOpMetrics.GetTaskAttempts() = %d, want 0", got)
	}
}
