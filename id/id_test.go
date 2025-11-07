package id

import (
	"testing"
	"time"
)

func TestGenerateJobID(t *testing.T) {
	jobName := "test-job"
	
	// Generate ID twice with same input
	id1 := GenerateJobID(jobName)
	id2 := GenerateJobID(jobName)
	
	// Should be deterministic
	if id1 != id2 {
		t.Errorf("GenerateJobID not deterministic: %s != %s", id1, id2)
	}
	
	// Should start with "job_" prefix
	if len(id1) < 4 || id1[:4] != "job_" {
		t.Errorf("Job ID should start with 'job_', got: %s", id1)
	}
	
	// Different names should produce different IDs
	id3 := GenerateJobID("different-job")
	if id1 == id3 {
		t.Errorf("Different job names should produce different IDs")
	}
}

func TestGenerateTaskID(t *testing.T) {
	jobID := GenerateJobID("test-job")
	taskName := "test-task"
	
	// Generate ID twice with same input
	id1 := GenerateTaskID(jobID, taskName)
	id2 := GenerateTaskID(jobID, taskName)
	
	// Should be deterministic
	if id1 != id2 {
		t.Errorf("GenerateTaskID not deterministic: %s != %s", id1, id2)
	}
	
	// Should start with "task_" prefix
	if len(id1) < 5 || id1[:5] != "task_" {
		t.Errorf("Task ID should start with 'task_', got: %s", id1)
	}
	
	// Different task names should produce different IDs
	id3 := GenerateTaskID(jobID, "different-task")
	if id1 == id3 {
		t.Errorf("Different task names should produce different IDs")
	}
	
	// Different job IDs should produce different task IDs even with same name
	differentJobID := GenerateJobID("different-job")
	id4 := GenerateTaskID(differentJobID, taskName)
	if id1 == id4 {
		t.Errorf("Different job IDs should produce different task IDs")
	}
}

func TestGenerateJobOccurrenceID(t *testing.T) {
	jobID := GenerateJobID("test-job")
	scheduledTime := time.Date(2025, 11, 7, 12, 0, 0, 0, time.UTC)
	
	// Generate ID twice with same input
	id1 := GenerateJobOccurrenceID(jobID, scheduledTime)
	id2 := GenerateJobOccurrenceID(jobID, scheduledTime)
	
	// Should be deterministic
	if id1 != id2 {
		t.Errorf("GenerateJobOccurrenceID not deterministic: %s != %s", id1, id2)
	}
	
	// Should start with "occ_" prefix
	if len(id1) < 4 || id1[:4] != "occ_" {
		t.Errorf("Occurrence ID should start with 'occ_', got: %s", id1)
	}
	
	// Different times should produce different IDs
	differentTime := scheduledTime.Add(1 * time.Hour)
	id3 := GenerateJobOccurrenceID(jobID, differentTime)
	if id1 == id3 {
		t.Errorf("Different scheduled times should produce different IDs")
	}
	
	// Different job IDs should produce different occurrence IDs
	differentJobID := GenerateJobID("different-job")
	id4 := GenerateJobOccurrenceID(differentJobID, scheduledTime)
	if id1 == id4 {
		t.Errorf("Different job IDs should produce different occurrence IDs")
	}
}

func TestGenerateTaskRunID(t *testing.T) {
	jobID := GenerateJobID("test-job")
	scheduledTime := time.Date(2025, 11, 7, 12, 0, 0, 0, time.UTC)
	occurrenceID := GenerateJobOccurrenceID(jobID, scheduledTime)
	taskID := GenerateTaskID(jobID, "test-task")
	
	// Generate ID twice with same input
	id1 := GenerateTaskRunID(occurrenceID, taskID)
	id2 := GenerateTaskRunID(occurrenceID, taskID)
	
	// Should be deterministic
	if id1 != id2 {
		t.Errorf("GenerateTaskRunID not deterministic: %s != %s", id1, id2)
	}
	
	// Should start with "run_" prefix
	if len(id1) < 4 || id1[:4] != "run_" {
		t.Errorf("Task run ID should start with 'run_', got: %s", id1)
	}
}

func TestGenerateExecutionAttemptID(t *testing.T) {
	jobID := GenerateJobID("test-job")
	scheduledTime := time.Date(2025, 11, 7, 12, 0, 0, 0, time.UTC)
	occurrenceID := GenerateJobOccurrenceID(jobID, scheduledTime)
	taskID := GenerateTaskID(jobID, "test-task")
	taskRunID := GenerateTaskRunID(occurrenceID, taskID)
	
	// Generate ID for attempt 0
	id1 := GenerateExecutionAttemptID(taskRunID, 0)
	id2 := GenerateExecutionAttemptID(taskRunID, 0)
	
	// Should be deterministic
	if id1 != id2 {
		t.Errorf("GenerateExecutionAttemptID not deterministic: %s != %s", id1, id2)
	}
	
	// Should start with "attempt_" prefix
	if len(id1) < 8 || id1[:8] != "attempt_" {
		t.Errorf("Execution attempt ID should start with 'attempt_', got: %s", id1)
	}
	
	// Different attempt numbers should produce different IDs
	id3 := GenerateExecutionAttemptID(taskRunID, 1)
	if id1 == id3 {
		t.Errorf("Different attempt numbers should produce different IDs")
	}
}

func TestIDDeterminismAcrossRuns(t *testing.T) {
	// Simulate generating IDs on different nodes at different times
	// with the same inputs - should always get the same results
	
	jobName := "production-backup"
	taskName := "backup-database"
	scheduledTime := time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)
	
	// Node 1 generates IDs
	node1JobID := GenerateJobID(jobName)
	node1TaskID := GenerateTaskID(node1JobID, taskName)
	node1OccurrenceID := GenerateJobOccurrenceID(node1JobID, scheduledTime)
	node1RunID := GenerateTaskRunID(node1OccurrenceID, node1TaskID)
	node1AttemptID := GenerateExecutionAttemptID(node1RunID, 0)
	
	// Node 2 generates IDs independently
	node2JobID := GenerateJobID(jobName)
	node2TaskID := GenerateTaskID(node2JobID, taskName)
	node2OccurrenceID := GenerateJobOccurrenceID(node2JobID, scheduledTime)
	node2RunID := GenerateTaskRunID(node2OccurrenceID, node2TaskID)
	node2AttemptID := GenerateExecutionAttemptID(node2RunID, 0)
	
	// All IDs should match
	if node1JobID != node2JobID {
		t.Errorf("Job IDs don't match across nodes: %s != %s", node1JobID, node2JobID)
	}
	if node1TaskID != node2TaskID {
		t.Errorf("Task IDs don't match across nodes: %s != %s", node1TaskID, node2TaskID)
	}
	if node1OccurrenceID != node2OccurrenceID {
		t.Errorf("Occurrence IDs don't match across nodes: %s != %s", node1OccurrenceID, node2OccurrenceID)
	}
	if node1RunID != node2RunID {
		t.Errorf("Run IDs don't match across nodes: %s != %s", node1RunID, node2RunID)
	}
	if node1AttemptID != node2AttemptID {
		t.Errorf("Attempt IDs don't match across nodes: %s != %s", node1AttemptID, node2AttemptID)
	}
}
