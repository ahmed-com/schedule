package id

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
	
	"github.com/google/uuid"
)

// Namespace UUIDs for different entity types (UUIDv5 requires a namespace)
var (
	JobNamespace           = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	TaskNamespace          = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	OccurrenceNamespace    = uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
	TaskRunNamespace       = uuid.MustParse("6ba7b813-9dad-11d1-80b4-00c04fd430c8")
	AttemptNamespace       = uuid.MustParse("6ba7b814-9dad-11d1-80b4-00c04fd430c8")
)

// GenerateJobID generates a deterministic ID for a job based on its name
func GenerateJobID(name string) string {
	id := uuid.NewSHA1(JobNamespace, []byte(name))
	return fmt.Sprintf("job_%s", id.String())
}

// GenerateTaskID generates a deterministic ID for a task based on job ID and task name
func GenerateTaskID(jobID, taskName string) string {
	combined := fmt.Sprintf("%s:%s", jobID, taskName)
	id := uuid.NewSHA1(TaskNamespace, []byte(combined))
	return fmt.Sprintf("task_%s", id.String())
}

// GenerateJobOccurrenceID generates a deterministic ID for a job occurrence
// based on job ID and scheduled time
func GenerateJobOccurrenceID(jobID string, scheduledTime time.Time) string {
	// Use RFC3339 format for consistent time representation
	timeStr := scheduledTime.UTC().Format(time.RFC3339)
	combined := fmt.Sprintf("%s:%s", jobID, timeStr)
	id := uuid.NewSHA1(OccurrenceNamespace, []byte(combined))
	return fmt.Sprintf("occ_%s", id.String())
}

// GenerateTaskRunID generates a deterministic ID for a task run
// based on occurrence ID and task ID
func GenerateTaskRunID(occurrenceID, taskID string) string {
	combined := fmt.Sprintf("%s:%s", occurrenceID, taskID)
	id := uuid.NewSHA1(TaskRunNamespace, []byte(combined))
	return fmt.Sprintf("run_%s", id.String())
}

// GenerateExecutionAttemptID generates a deterministic ID for an execution attempt
// based on task run ID and attempt number
func GenerateExecutionAttemptID(taskRunID string, attemptNumber int) string {
	combined := fmt.Sprintf("%s:%d", taskRunID, attemptNumber)
	id := uuid.NewSHA1(AttemptNamespace, []byte(combined))
	return fmt.Sprintf("attempt_%s", id.String())
}

// HashString generates a simple SHA256 hash of a string (alternative to UUID)
func HashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
