package jobistemer

import (
	"context"
	"sync"

	"github.com/ahmed-com/schedule/id"
	"github.com/ahmed-com/schedule/ticker"
)

// TaskFunc defines the function signature for task execution
type TaskFunc func(ctx context.Context, execCtx *ExecutionContext) error

// Task represents a unit of work within a Job
type Task struct {
	ID     string
	Name   string
	Config TaskConfig
	Order  int
	Fn     TaskFunc
}

// Job represents a scheduled job with multiple tasks
type Job struct {
	ID      string
	Name    string
	Config  JobConfig
	Ticker  ticker.Ticker
	Tasks   []*Task
	Version int64
	Active  bool
	Paused  bool

	// Hooks
	OnStart    func(ctx context.Context, occurrence *JobOccurrence)
	OnComplete func(ctx context.Context, report *ExecutionReport)

	mu sync.RWMutex
}

// NewJob creates a new job instance
func NewJob(name string, tick ticker.Ticker, config JobConfig) *Job {
	return &Job{
		ID:      id.GenerateJobID(name),
		Name:    name,
		Config:  config,
		Ticker:  tick,
		Tasks:   make([]*Task, 0),
		Version: 1,
		Active:  true,
		Paused:  false,
	}
}

// AddTask adds a task to the job
func (j *Job) AddTask(name string, fn TaskFunc, config TaskConfig) *Task {
	j.mu.Lock()
	defer j.mu.Unlock()

	task := &Task{
		ID:     id.GenerateTaskID(j.ID, name),
		Name:   name,
		Config: config,
		Order:  len(j.Tasks),
		Fn:     fn,
	}

	j.Tasks = append(j.Tasks, task)
	return task
}

// GetEffectiveConfig returns the effective configuration for a task
// Task-specific config overrides job-level config (cascading)
func (j *Job) GetEffectiveConfig(task *Task) TaskConfig {
	effective := j.Config.TaskConfig

	// Override with task-specific config
	if task.Config.FailureStrategy != nil {
		effective.FailureStrategy = task.Config.FailureStrategy
	}
	if task.Config.RetryPolicy != nil {
		effective.RetryPolicy = task.Config.RetryPolicy
	}
	if task.Config.TaskTimeout != nil {
		effective.TaskTimeout = task.Config.TaskTimeout
	}

	return effective
}

// IsRunning checks if the job has any running occurrences
// This is a persistent query, not an in-memory flag
// The storage interface is passed in to avoid import cycles
func (j *Job) IsRunning(ctx context.Context, listRunningFunc func(context.Context) ([]*JobOccurrence, error)) (bool, error) {
	occurrences, err := listRunningFunc(ctx)
	if err != nil {
		return false, err
	}

	for _, occ := range occurrences {
		if occ.JobID == j.ID && occ.Status == JobStatusRunning {
			return true, nil
		}
	}

	return false, nil
}
