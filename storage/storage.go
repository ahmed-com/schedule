package storage

import (
	"context"
	"time"
)

// Storage defines the interface for persisting job scheduling metadata and state
type Storage interface {
	// Job operations
	CreateJob(ctx context.Context, job *Job) error
	GetJob(ctx context.Context, jobID string) (*Job, error)
	UpdateJob(ctx context.Context, job *Job) error
	DeleteJob(ctx context.Context, jobID string) error
	ListJobs(ctx context.Context) ([]*Job, error)
	
	// Task operations
	CreateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, taskID string) (*Task, error)
	UpdateTask(ctx context.Context, task *Task) error
	DeleteTask(ctx context.Context, taskID string) error
	ListTasksByJobID(ctx context.Context, jobID string) ([]*Task, error)
	
	// Job Occurrence operations
	CreateJobOccurrence(ctx context.Context, occurrence *JobOccurrence) error
	GetJobOccurrence(ctx context.Context, occurrenceID string) (*JobOccurrence, error)
	UpdateJobOccurrence(ctx context.Context, occurrence *JobOccurrence) error
	DeleteJobOccurrence(ctx context.Context, occurrenceID string) error
	ListRunningOccurrences(ctx context.Context) ([]*JobOccurrence, error)
	ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*JobOccurrence, error)
	ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*JobOccurrence, error)
	
	// Task Run operations
	CreateTaskRun(ctx context.Context, run *TaskRun) error
	GetTaskRun(ctx context.Context, runID string) (*TaskRun, error)
	UpdateTaskRun(ctx context.Context, run *TaskRun) error
	DeleteTaskRun(ctx context.Context, runID string) error
	ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*TaskRun, error)
	
	// Execution Attempt operations
	CreateExecutionAttempt(ctx context.Context, attempt *ExecutionAttempt) error
	GetExecutionAttempt(ctx context.Context, attemptID string) (*ExecutionAttempt, error)
	UpdateExecutionAttempt(ctx context.Context, attempt *ExecutionAttempt) error
	ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*ExecutionAttempt, error)
	
	// Close closes the storage connection
	Close() error
}

// JobStatus represents the status of a job occurrence
type JobStatus string

const (
	JobStatusPending      JobStatus = "Pending"
	JobStatusRunning      JobStatus = "Running"
	JobStatusCompleted    JobStatus = "Completed"
	JobStatusFailed       JobStatus = "Failed"
	JobStatusCanceled     JobStatus = "Canceled"
	JobStatusFailedStale  JobStatus = "Failed_Stale"
	JobStatusQueued       JobStatus = "Queued"
)

// TaskStatus represents the status of a task run or execution attempt
type TaskStatus string

const (
	TaskStatusPending  TaskStatus = "Pending"
	TaskStatusRunning  TaskStatus = "Running"
	TaskStatusSuccess  TaskStatus = "Success"
	TaskStatusFailed   TaskStatus = "Failed"
	TaskStatusCanceled TaskStatus = "Canceled"
	TaskStatusTimeout  TaskStatus = "Timeout"
)

// Job represents a job definition in storage
type Job struct {
	ID             string
	Name           string
	ScheduleType   string // "cron", "iso8601", "once"
	ScheduleConfig []byte // serialized schedule configuration
	Config         []byte // serialized JobConfig
	Version        int64
	Active         bool
	Paused         bool
	LastRunTime    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Task represents a task definition in storage
type Task struct {
	ID        string
	JobID     string
	Name      string
	Config    []byte // serialized TaskConfig
	Order     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobOccurrence represents a single job execution instance in storage
type JobOccurrence struct {
	ID            string
	JobID         string
	JobVersion    int64
	ScheduledTime time.Time
	Status        JobStatus
	OwningNodeID  string
	StartTime     *time.Time
	EndTime       *time.Time
	Config        []byte // serialized JobConfig snapshot
	IsRecoveryRun bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TaskRun represents a task execution record in storage
type TaskRun struct {
	ID           string
	OccurrenceID string
	TaskID       string
	Status       TaskStatus
	RetryCount   int
	StartTime    *time.Time
	EndTime      *time.Time
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ExecutionAttempt represents a single execution attempt in storage
type ExecutionAttempt struct {
	ID            string
	TaskRunID     string
	AttemptNumber int
	Status        TaskStatus
	StartTime     time.Time
	EndTime       *time.Time
	ErrorMessage  string
	CreatedAt     time.Time
}
