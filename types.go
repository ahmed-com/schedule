package jobistemer

import (
	"time"
)

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

// TaskExecutionMode defines how tasks within a job should be executed
type TaskExecutionMode string

const (
	TaskExecutionModeSequential TaskExecutionMode = "Sequential"
	TaskExecutionModeParallel   TaskExecutionMode = "Parallel"
)

// FailureStrategy defines how to handle task failures
type FailureStrategy string

const (
	FailureStrategyFailFast       FailureStrategy = "Fail-Fast"
	FailureStrategyContinue       FailureStrategy = "Continue"
	FailureStrategyRetryOnFailure FailureStrategy = "Retry_On_Failure"
)

// OverlapPolicy defines how to handle overlapping job executions
type OverlapPolicy string

const (
	OverlapPolicySkip  OverlapPolicy = "Skip"
	OverlapPolicyAllow OverlapPolicy = "Allow"
	OverlapPolicyQueue OverlapPolicy = "Queue"
)

// RecoveryStrategy defines how to handle missed job occurrences after downtime
type RecoveryStrategy string

const (
	RecoveryStrategyExecuteAll     RecoveryStrategy = "Execute_All"
	RecoveryStrategyExecuteLast    RecoveryStrategy = "Execute_Last"
	RecoveryStrategyMarkAsMissed   RecoveryStrategy = "Mark_As_Missed"
	RecoveryStrategyBoundedWindow  RecoveryStrategy = "Bounded_Window"
	RecoveryStrategyCompressedCatchUp RecoveryStrategy = "Compressed_Catch_Up"
)

// RetryPolicy defines retry behavior for task execution
type RetryPolicy struct {
	MaxRetries     int           `json:"max_retries"`
	RetryInterval  time.Duration `json:"retry_interval"`
	BackoffFactor  float64       `json:"backoff_factor"`
}

// BoundedWindow defines limits for bounded recovery strategy
type BoundedWindow struct {
	MaxOccurrences int           `json:"max_occurrences"`
	MaxDuration    time.Duration `json:"max_duration"`
}

// TaskConfig holds task-level configuration (can override job config)
type TaskConfig struct {
	FailureStrategy *FailureStrategy `json:"failure_strategy,omitempty"`
	RetryPolicy     *RetryPolicy     `json:"retry_policy,omitempty"`
	TaskTimeout     *time.Duration   `json:"task_timeout,omitempty"`
}

// JobConfig holds job-level configuration
type JobConfig struct {
	// Execution settings
	TaskExecutionMode TaskExecutionMode `json:"task_execution_mode"`
	FailureStrategy   FailureStrategy   `json:"failure_strategy"`
	ExecutionTimeout  time.Duration     `json:"execution_timeout"`
	OverlapPolicy     OverlapPolicy     `json:"overlap_policy"`
	
	// Recovery settings
	RecoveryStrategy RecoveryStrategy `json:"recovery_strategy"`
	BoundedWindow    *BoundedWindow   `json:"bounded_window,omitempty"`
	
	// Cascading task config (defaults for all tasks)
	TaskConfig TaskConfig `json:"task_config"`
}

// SchedulerConfig holds scheduler-level configuration
type SchedulerConfig struct {
	MaxConcurrentJobs int           `json:"max_concurrent_jobs"`
	ReaperInterval    time.Duration `json:"reaper_interval"`
	NodeID            string        `json:"node_id"`
	Metrics           interface{}   `json:"-"` // MetricsCollector interface (not serialized)
}

// ExecutionContext provides context information for task execution
type ExecutionContext struct {
	OccurrenceID string
	TaskRunID    string
	AttemptID    string
}

// JobOccurrence represents a single instance of a job execution
type JobOccurrence struct {
	ID             string
	JobID          string
	JobVersion     int64
	ScheduledTime  time.Time
	Status         JobStatus
	OwningNodeID   string
	StartTime      *time.Time
	EndTime        *time.Time
	Config         JobConfig
	IsRecoveryRun  bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ExecutionReport provides detailed information about job execution
type ExecutionReport struct {
	OccurrenceID     string
	FailureStrategy  FailureStrategy
	TaskReports      []*TaskReport
	GroupOutcome     string
}

// TaskReport provides detailed information about task execution
type TaskReport struct {
	TaskID      string
	TaskName    string
	Attempts    []*AttemptReport
	FinalStatus TaskStatus
}

// AttemptReport provides detailed information about a single execution attempt
type AttemptReport struct {
	AttemptNumber int
	Status        TaskStatus
	StartTime     time.Time
	EndTime       *time.Time
	Duration      time.Duration
	ErrorMessage  string
}

// NewExecutionReport creates a new execution report
func NewExecutionReport(occurrenceID string, strategy FailureStrategy) *ExecutionReport {
	return &ExecutionReport{
		OccurrenceID:    occurrenceID,
		FailureStrategy: strategy,
		TaskReports:     make([]*TaskReport, 0),
	}
}

// AddTaskReport adds a task report to the execution report
func (r *ExecutionReport) AddTaskReport(report *TaskReport) {
	r.TaskReports = append(r.TaskReports, report)
}
