# Jobistemer Implementation Plan

## Overview
This document provides a detailed implementation plan for the Jobistemer job scheduling library based on the technical design specification in README.md.

## Package Structure

```
jobistemer/
├── go.mod
├── go.sum
├── scheduler.go           # Main scheduler implementation
├── job.go                 # Job definition and management
├── task.go                # Task definition and execution
├── occurrence.go          # Job occurrence handling
├── config.go              # Configuration structures
├── types.go               # Core types and enums
├── id/
│   └── generator.go       # Deterministic ID generation (UUIDv5)
├── ticker/
│   ├── ticker.go          # Ticker interface
│   ├── cron.go            # Cron-based ticker
│   ├── iso8601.go         # ISO8601 interval ticker
│   └── once.go            # One-time ticker
├── storage/
│   ├── storage.go         # Storage interface
│   ├── badger/
│   │   ├── badger.go      # BadgerDB implementation
│   │   └── transaction.go # Transaction handling
│   └── memory/
│       └── memory.go      # In-memory implementation (for testing)
├── executor/
│   ├── executor.go        # Execution engine
│   ├── context.go         # Execution context
│   ├── retry.go           # Retry logic with backoff
│   └── report.go          # Execution report generation
├── concurrency/
│   ├── pool.go            # Global worker pool
│   ├── coordinator.go     # Distributed coordination
│   └── overlap.go         # Overlap policy enforcement
├── recovery/
│   ├── recovery.go        # OnBoot recovery system
│   ├── strategy.go        # Recovery strategy implementations
│   ├── reaper.go          # Dead-man's-switch for stale jobs
│   └── replay.go          # Manual replay functionality
├── update/
│   ├── diff.go            # Schedule diffing algorithm
│   ├── version.go         # Job versioning
│   └── policy.go          # Update policies
├── observability/
│   ├── hooks.go           # OnStart/OnComplete hooks
│   ├── logger.go          # Structured logging
│   └── metrics.go         # Metrics instrumentation
├── examples/
│   ├── basic/
│   │   └── main.go        # Basic usage example
│   ├── parallel/
│   │   └── main.go        # Parallel task execution
│   └── recovery/
│       └── main.go        # Recovery and replay example
└── test/
    ├── integration/       # Integration tests
    └── benchmark/         # Performance benchmarks
```

## Implementation Phases

### Phase 1: Foundation (Core Types and Utilities)

**Files to create:**
- `go.mod` - Go module definition
- `types.go` - Core enums and types
- `config.go` - Configuration structures
- `id/generator.go` - Deterministic ID generation

**Types to define in `types.go`:**
```go
// Enums
type OverlapPolicy string
const (
    OverlapPolicySkip  OverlapPolicy = "skip"
    OverlapPolicyAllow OverlapPolicy = "allow"
    OverlapPolicyQueue OverlapPolicy = "queue"
)

type TaskExecutionMode string
const (
    TaskExecutionModeSequential TaskExecutionMode = "sequential"
    TaskExecutionModeParallel   TaskExecutionMode = "parallel"
)

type FailureStrategy string
const (
    FailureStrategyFailFast       FailureStrategy = "fail_fast"
    FailureStrategyContinue       FailureStrategy = "continue"
    FailureStrategyRetryOnFailure FailureStrategy = "retry_on_failure"
)

type RecoveryStrategy string
const (
    RecoveryStrategyExecuteAll    RecoveryStrategy = "execute_all"
    RecoveryStrategyExecuteLast   RecoveryStrategy = "execute_last"
    RecoveryStrategyMarkAsMissed  RecoveryStrategy = "mark_as_missed"
    RecoveryStrategyBoundedWindow RecoveryStrategy = "bounded_window"
    RecoveryStrategyCompressed    RecoveryStrategy = "compressed"
)

type UpdatePolicy string
const (
    UpdatePolicyImmediate UpdatePolicy = "immediate"
    UpdatePolicyGraceful  UpdatePolicy = "graceful"
    UpdatePolicyWindowed  UpdatePolicy = "windowed"
)

type JobStatus string
const (
    JobStatusPending      JobStatus = "pending"
    JobStatusRunning      JobStatus = "running"
    JobStatusCompleted    JobStatus = "completed"
    JobStatusFailed       JobStatus = "failed"
    JobStatusCanceled     JobStatus = "canceled"
    JobStatusFailedStale  JobStatus = "failed_stale"
    JobStatusQueued       JobStatus = "queued"
)

type TaskStatus string
const (
    TaskStatusPending   TaskStatus = "pending"
    TaskStatusRunning   TaskStatus = "running"
    TaskStatusSuccess   TaskStatus = "success"
    TaskStatusFailed    TaskStatus = "failed"
    TaskStatusTimeout   TaskStatus = "timeout"
    TaskStatusCanceled  TaskStatus = "canceled"
)
```

**Configuration structures in `config.go`:**
```go
type RetryPolicy struct {
    MaxRetries    int
    RetryInterval time.Duration
    BackoffFactor float64
}

type BoundedWindow struct {
    MaxOccurrences int
    MaxDuration    time.Duration
}

type JobConfig struct {
    Timezone         string
    OverlapPolicy    OverlapPolicy
    TaskExecutionMode TaskExecutionMode
    FailureStrategy  FailureStrategy
    RetryPolicy      *RetryPolicy
    ExecutionTimeout time.Duration
    TaskTimeout      time.Duration
    RecoveryStrategy RecoveryStrategy
    BoundedWindow    *BoundedWindow
}

type TaskConfig struct {
    FailureStrategy *FailureStrategy
    RetryPolicy     *RetryPolicy
    TaskTimeout     *time.Duration
}

type SchedulerConfig struct {
    MaxConcurrentJobs int
    GracePeriod       time.Duration
    ReaperInterval    time.Duration
}
```

**ID generation in `id/generator.go`:**
```go
package id

import (
    "crypto/sha1"
    "encoding/hex"
    "fmt"
    "github.com/google/uuid"
    "time"
)

// GenerateJobOccurrenceID creates a deterministic ID for a job occurrence
func GenerateJobOccurrenceID(jobID string, scheduledTime time.Time) string {
    namespace := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // UUID namespace
    data := fmt.Sprintf("%s:%d", jobID, scheduledTime.Unix())
    id := uuid.NewSHA1(namespace, []byte(data))
    return fmt.Sprintf("occ_%s", id.String())
}

// GenerateTaskRunID creates a deterministic ID for a task run
func GenerateTaskRunID(occurrenceID, taskID string) string {
    namespace := uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
    data := fmt.Sprintf("%s:%s", occurrenceID, taskID)
    id := uuid.NewSHA1(namespace, []byte(data))
    return fmt.Sprintf("run_%s", id.String())
}

// GenerateExecutionAttemptID creates a deterministic ID for an execution attempt
func GenerateExecutionAttemptID(taskRunID string, attemptNumber int) string {
    namespace := uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
    data := fmt.Sprintf("%s:%d", taskRunID, attemptNumber)
    id := uuid.NewSHA1(namespace, []byte(data))
    return fmt.Sprintf("attempt_%s", id.String())
}

// GenerateJobID creates a unique ID for a job
func GenerateJobID(name string) string {
    h := sha1.New()
    h.Write([]byte(fmt.Sprintf("job:%s:%d", name, time.Now().UnixNano())))
    return fmt.Sprintf("job_%s", hex.EncodeToString(h.Sum(nil)))
}

// GenerateTaskID creates a unique ID for a task
func GenerateTaskID(jobID, taskName string) string {
    h := sha1.New()
    h.Write([]byte(fmt.Sprintf("task:%s:%s:%d", jobID, taskName, time.Now().UnixNano())))
    return fmt.Sprintf("task_%s", hex.EncodeToString(h.Sum(nil)))
}
```

### Phase 2: Storage Layer

**Storage interface in `storage/storage.go`:**
```go
package storage

import (
    "context"
    "time"
)

// Storage defines the persistence layer interface
type Storage interface {
    // Transaction management
    BeginTx(ctx context.Context) (Transaction, error)
    
    // Job operations
    CreateJob(ctx context.Context, job *Job) error
    GetJob(ctx context.Context, id string) (*Job, error)
    UpdateJob(ctx context.Context, job *Job) error
    DeleteJob(ctx context.Context, id string) error
    ListJobs(ctx context.Context) ([]*Job, error)
    
    // Task operations
    CreateTask(ctx context.Context, task *Task) error
    GetTask(ctx context.Context, id string) (*Task, error)
    UpdateTask(ctx context.Context, task *Task) error
    DeleteTask(ctx context.Context, id string) error
    ListTasksByJob(ctx context.Context, jobID string) ([]*Task, error)
    
    // Job occurrence operations
    CreateJobOccurrence(ctx context.Context, occurrence *JobOccurrence) error
    GetJobOccurrence(ctx context.Context, id string) (*JobOccurrence, error)
    UpdateJobOccurrence(ctx context.Context, occurrence *JobOccurrence) error
    ListJobOccurrencesByJob(ctx context.Context, jobID string) ([]*JobOccurrence, error)
    ListRunningOccurrences(ctx context.Context) ([]*JobOccurrence, error)
    ListStaleOccurrences(ctx context.Context, timeout time.Duration) ([]*JobOccurrence, error)
    
    // Task run operations
    CreateTaskRun(ctx context.Context, run *TaskRun) error
    GetTaskRun(ctx context.Context, id string) (*TaskRun, error)
    UpdateTaskRun(ctx context.Context, run *TaskRun) error
    ListTaskRunsByOccurrence(ctx context.Context, occurrenceID string) ([]*TaskRun, error)
    
    // Execution attempt operations
    CreateExecutionAttempt(ctx context.Context, attempt *ExecutionAttempt) error
    GetExecutionAttempt(ctx context.Context, id string) (*ExecutionAttempt, error)
    UpdateExecutionAttempt(ctx context.Context, attempt *ExecutionAttempt) error
    ListExecutionAttemptsByRun(ctx context.Context, runID string) ([]*ExecutionAttempt, error)
    
    // Utility operations
    Close() error
}

// Transaction defines a database transaction
type Transaction interface {
    Commit() error
    Rollback() error
    
    // Same operations as Storage but within transaction context
    CreateJobOccurrence(ctx context.Context, occurrence *JobOccurrence) error
    CreateTaskRun(ctx context.Context, run *TaskRun) error
    // ... other operations
}

// Domain models for storage
type Job struct {
    ID               string
    Name             string
    ScheduleType     string // "cron", "iso8601", "once"
    ScheduleConfig   []byte // serialized schedule config
    Config           JobConfig
    Version          int64
    Active           bool
    Paused           bool
    LastRunTime      *time.Time
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

type Task struct {
    ID        string
    JobID     string
    Name      string
    Config    TaskConfig
    Order     int
    CreatedAt time.Time
    UpdatedAt time.Time
}

type JobOccurrence struct {
    ID             string
    JobID          string
    JobVersion     int64
    ScheduledTime  time.Time
    Status         JobStatus
    OwningNodeID   string
    StartTime      *time.Time
    EndTime        *time.Time
    Config         JobConfig // effective config snapshot
    IsRecoveryRun  bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type TaskRun struct {
    ID            string
    OccurrenceID  string
    TaskID        string
    Status        TaskStatus
    RetryCount    int
    StartTime     *time.Time
    EndTime       *time.Time
    ErrorMessage  string
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

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
```

**BadgerDB implementation in `storage/badger/badger.go`:**
```go
package badger

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
    
    "github.com/dgraph-io/badger/v4"
    "github.com/ahmed-com/schedule/storage"
)

type BadgerStorage struct {
    db *badger.DB
}

func NewBadgerStorage(path string) (*BadgerStorage, error) {
    opts := badger.DefaultOptions(path)
    db, err := badger.Open(opts)
    if err != nil {
        return nil, err
    }
    
    return &BadgerStorage{db: db}, nil
}

// Implement hierarchical key schema
func (s *BadgerStorage) jobKey(id string) []byte {
    return []byte(fmt.Sprintf("job/%s", id))
}

func (s *BadgerStorage) taskKey(jobID, taskID string) []byte {
    return []byte(fmt.Sprintf("job/%s/task/%s", jobID, taskID))
}

func (s *BadgerStorage) occurrenceKey(jobID, occurrenceID string) []byte {
    return []byte(fmt.Sprintf("job/%s/occurrence/%s", jobID, occurrenceID))
}

func (s *BadgerStorage) taskRunKey(jobID, occurrenceID, taskID string) []byte {
    return []byte(fmt.Sprintf("job/%s/occurrence/%s/task/%s/run", jobID, occurrenceID, taskID))
}

func (s *BadgerStorage) attemptKey(jobID, occurrenceID, taskID, attemptID string) []byte {
    return []byte(fmt.Sprintf("job/%s/occurrence/%s/task/%s/run/attempt/%s", jobID, occurrenceID, taskID, attemptID))
}

// CreateJobOccurrence with atomic create-if-not-exists
func (s *BadgerStorage) CreateJobOccurrence(ctx context.Context, occ *storage.JobOccurrence) error {
    return s.db.Update(func(txn *badger.Txn) error {
        key := s.occurrenceKey(occ.JobID, occ.ID)
        
        // Check if already exists (atomic)
        _, err := txn.Get(key)
        if err == nil {
            return fmt.Errorf("occurrence already exists: %s", occ.ID)
        }
        if err != badger.ErrKeyNotFound {
            return err
        }
        
        // Serialize and store
        data, err := json.Marshal(occ)
        if err != nil {
            return err
        }
        
        return txn.Set(key, data)
    })
}

// ... implement other methods
```

### Phase 3: Ticker Interface and Implementations

**Ticker interface in `ticker/ticker.go`:**
```go
package ticker

import (
    "context"
    "time"
)

// ExecutionContext provides timing and transaction info for task execution
type ExecutionContext struct {
    ScheduledTime time.Time
    ActualTime    time.Time
    LastRunTime   *time.Time
    Transaction   interface{} // storage transaction handle
}

// Ticker defines the scheduling mechanism interface
type Ticker interface {
    // Channel returns a read-only channel that emits execution contexts
    Channel() <-chan ExecutionContext
    
    // Control methods
    Start() error
    Stop() error
    Pause() error
    Resume() error
    
    // Status checks
    IsActive() bool     // Within start-end window
    IsPaused() bool     // Manually paused
    IsEnabled() bool    // Active and not paused
    NextRun() (*time.Time, error)
    
    // Recovery support
    GetOccurrencesBetween(start, end time.Time) ([]time.Time, error)
}

// TickerConfig provides common configuration for all tickers
type TickerConfig struct {
    StartTime *time.Time
    EndTime   *time.Time
    Timezone  string
}
```

**Cron ticker in `ticker/cron.go`:**
```go
package ticker

import (
    "context"
    "time"
    
    "github.com/robfig/cron/v3"
)

type CronTicker struct {
    expression string
    config     TickerConfig
    parser     cron.Parser
    schedule   cron.Schedule
    
    ch       chan ExecutionContext
    stopCh   chan struct{}
    pauseCh  chan bool
    running  bool
    paused   bool
    location *time.Location
}

func NewCronTicker(expression string, config TickerConfig) (*CronTicker, error) {
    loc, err := time.LoadLocation(config.Timezone)
    if err != nil {
        return nil, err
    }
    
    parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
    schedule, err := parser.Parse(expression)
    if err != nil {
        return nil, err
    }
    
    return &CronTicker{
        expression: expression,
        config:     config,
        parser:     parser,
        schedule:   schedule,
        location:   loc,
        ch:         make(chan ExecutionContext, 10),
        stopCh:     make(chan struct{}),
        pauseCh:    make(chan bool, 1),
    }, nil
}

func (t *CronTicker) Start() error {
    if t.running {
        return nil
    }
    
    t.running = true
    go t.run()
    return nil
}

func (t *CronTicker) run() {
    for {
        if t.paused {
            select {
            case <-t.stopCh:
                return
            case resume := <-t.pauseCh:
                if resume {
                    t.paused = false
                }
            }
            continue
        }
        
        now := time.Now().In(t.location)
        next := t.schedule.Next(now)
        
        // Check if within active window
        if !t.isWithinWindow(next) {
            time.Sleep(time.Minute)
            continue
        }
        
        duration := next.Sub(now)
        timer := time.NewTimer(duration)
        
        select {
        case <-timer.C:
            ctx := ExecutionContext{
                ScheduledTime: next,
                ActualTime:    time.Now().In(t.location),
            }
            t.ch <- ctx
        case <-t.stopCh:
            timer.Stop()
            return
        case pause := <-t.pauseCh:
            timer.Stop()
            t.paused = pause
        }
    }
}

func (t *CronTicker) isWithinWindow(checkTime time.Time) bool {
    if t.config.StartTime != nil && checkTime.Before(*t.config.StartTime) {
        return false
    }
    if t.config.EndTime != nil && checkTime.After(*t.config.EndTime) {
        return false
    }
    return true
}

func (t *CronTicker) Channel() <-chan ExecutionContext {
    return t.ch
}

func (t *CronTicker) Stop() error {
    if !t.running {
        return nil
    }
    close(t.stopCh)
    t.running = false
    return nil
}

func (t *CronTicker) Pause() error {
    t.pauseCh <- true
    return nil
}

func (t *CronTicker) Resume() error {
    t.pauseCh <- false
    return nil
}

func (t *CronTicker) IsActive() bool {
    now := time.Now().In(t.location)
    return t.isWithinWindow(now)
}

func (t *CronTicker) IsPaused() bool {
    return t.paused
}

func (t *CronTicker) IsEnabled() bool {
    return t.IsActive() && !t.IsPaused()
}

func (t *CronTicker) NextRun() (*time.Time, error) {
    now := time.Now().In(t.location)
    next := t.schedule.Next(now)
    return &next, nil
}

func (t *CronTicker) GetOccurrencesBetween(start, end time.Time) ([]time.Time, error) {
    var occurrences []time.Time
    current := start
    
    for current.Before(end) {
        next := t.schedule.Next(current)
        if next.After(end) {
            break
        }
        if t.isWithinWindow(next) {
            occurrences = append(occurrences, next)
        }
        current = next
    }
    
    return occurrences, nil
}
```

### Phase 4: Job and Task Definitions

**Job in `job.go`:**
```go
package jobistemer

import (
    "context"
    "sync"
    "time"
    
    "github.com/ahmed-com/schedule/ticker"
    "github.com/ahmed-com/schedule/storage"
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
    ID       string
    Name     string
    Config   JobConfig
    Ticker   ticker.Ticker
    Tasks    []*Task
    Version  int64
    Active   bool
    Paused   bool
    
    // Hooks
    OnStart    func(ctx context.Context, occurrence *JobOccurrence)
    OnComplete func(ctx context.Context, report *ExecutionReport)
    
    mu sync.RWMutex
}

// NewJob creates a new job instance
func NewJob(name string, ticker ticker.Ticker, config JobConfig) *Job {
    return &Job{
        ID:      id.GenerateJobID(name),
        Name:    name,
        Config:  config,
        Ticker:  ticker,
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
func (j *Job) IsRunning(ctx context.Context, store storage.Storage) (bool, error) {
    occurrences, err := store.ListRunningOccurrences(ctx)
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
```

### Phase 5: Execution Engine

**Executor in `executor/executor.go`:**
```go
package executor

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/ahmed-com/schedule/id"
    "github.com/ahmed-com/schedule/storage"
)

type Executor struct {
    store storage.Storage
}

func NewExecutor(store storage.Storage) *Executor {
    return &Executor{store: store}
}

// ExecuteJobOccurrence executes a job occurrence
func (e *Executor) ExecuteJobOccurrence(ctx context.Context, job *Job, occurrence *JobOccurrence) (*ExecutionReport, error) {
    // Create job-level context with timeout
    jobCtx, cancel := context.WithTimeout(ctx, job.Config.ExecutionTimeout)
    defer cancel()
    
    // Update occurrence status to Running
    occurrence.Status = JobStatusRunning
    occurrence.StartTime = timePtr(time.Now())
    if err := e.store.UpdateJobOccurrence(jobCtx, occurrence); err != nil {
        return nil, err
    }
    
    // Execute OnStart hook
    if job.OnStart != nil {
        job.OnStart(jobCtx, occurrence)
    }
    
    var report *ExecutionReport
    var err error
    
    // Execute based on TaskExecutionMode
    switch job.Config.TaskExecutionMode {
    case TaskExecutionModeSequential:
        report, err = e.executeSequential(jobCtx, job, occurrence)
    case TaskExecutionModeParallel:
        report, err = e.executeParallel(jobCtx, job, occurrence)
    default:
        report, err = e.executeSequential(jobCtx, job, occurrence)
    }
    
    // Update final status
    occurrence.EndTime = timePtr(time.Now())
    if err != nil {
        occurrence.Status = JobStatusFailed
    } else {
        occurrence.Status = JobStatusCompleted
    }
    e.store.UpdateJobOccurrence(jobCtx, occurrence)
    
    // Execute OnComplete hook
    if job.OnComplete != nil {
        job.OnComplete(jobCtx, report)
    }
    
    return report, err
}

// executeSequential executes tasks sequentially
func (e *Executor) executeSequential(ctx context.Context, job *Job, occurrence *JobOccurrence) (*ExecutionReport, error) {
    report := NewExecutionReport(occurrence.ID, job.Config.FailureStrategy)
    
    for _, task := range job.Tasks {
        taskReport := e.executeTask(ctx, job, task, occurrence)
        report.AddTaskReport(taskReport)
        
        // Check failure strategy
        if taskReport.FinalStatus == TaskStatusFailed {
            if job.Config.FailureStrategy == FailureStrategyFailFast {
                report.GroupOutcome = "failed"
                return report, fmt.Errorf("task %s failed, aborting due to fail-fast", task.Name)
            }
        }
        
        // Check context cancellation
        select {
        case <-ctx.Done():
            report.GroupOutcome = "canceled"
            return report, ctx.Err()
        default:
        }
    }
    
    report.GroupOutcome = "completed"
    return report, nil
}

// executeParallel executes tasks in parallel
func (e *Executor) executeParallel(ctx context.Context, job *Job, occurrence *JobOccurrence) (*ExecutionReport, error) {
    report := NewExecutionReport(occurrence.ID, job.Config.FailureStrategy)
    
    // Create cancellable context for fail-fast
    groupCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    
    var wg sync.WaitGroup
    var mu sync.Mutex
    
    for _, task := range job.Tasks {
        wg.Add(1)
        
        go func(t *Task) {
            defer wg.Done()
            
            taskReport := e.executeTask(groupCtx, job, t, occurrence)
            
            mu.Lock()
            report.AddTaskReport(taskReport)
            mu.Unlock()
            
            // Check failure strategy
            if taskReport.FinalStatus == TaskStatusFailed {
                taskConfig := job.GetEffectiveConfig(t)
                strategy := job.Config.FailureStrategy
                if taskConfig.FailureStrategy != nil {
                    strategy = *taskConfig.FailureStrategy
                }
                
                if strategy == FailureStrategyFailFast {
                    // Signal all other tasks to abort
                    cancel()
                }
            }
        }(task)
    }
    
    // Wait for all tasks to complete
    wg.Wait()
    
    // Determine group outcome
    if ctx.Err() != nil {
        report.GroupOutcome = "canceled"
    } else {
        hasFailure := false
        for _, tr := range report.TaskReports {
            if tr.FinalStatus == TaskStatusFailed {
                hasFailure = true
                break
            }
        }
        if hasFailure {
            report.GroupOutcome = "failed"
        } else {
            report.GroupOutcome = "completed"
        }
    }
    
    return report, nil
}

// executeTask executes a single task with retry logic
func (e *Executor) executeTask(ctx context.Context, job *Job, task *Task, occurrence *JobOccurrence) *TaskReport {
    taskConfig := job.GetEffectiveConfig(task)
    
    // Create task run record
    runID := id.GenerateTaskRunID(occurrence.ID, task.ID)
    taskRun := &storage.TaskRun{
        ID:           runID,
        OccurrenceID: occurrence.ID,
        TaskID:       task.ID,
        Status:       TaskStatusPending,
        RetryCount:   0,
        CreatedAt:    time.Now(),
    }
    e.store.CreateTaskRun(ctx, taskRun)
    
    report := &TaskReport{
        TaskID:   task.ID,
        TaskName: task.Name,
        Attempts: make([]*AttemptReport, 0),
    }
    
    retryPolicy := taskConfig.RetryPolicy
    if retryPolicy == nil {
        retryPolicy = &RetryPolicy{MaxRetries: 0}
    }
    
    var lastErr error
    for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
        attemptReport := e.executeAttempt(ctx, job, task, taskRun, attempt, taskConfig)
        report.Attempts = append(report.Attempts, attemptReport)
        
        if attemptReport.Status == TaskStatusSuccess {
            report.FinalStatus = TaskStatusSuccess
            taskRun.Status = TaskStatusSuccess
            taskRun.EndTime = timePtr(time.Now())
            e.store.UpdateTaskRun(ctx, taskRun)
            return report
        }
        
        lastErr = fmt.Errorf(attemptReport.ErrorMessage)
        
        // Check if we should retry
        if attempt < retryPolicy.MaxRetries {
            // Calculate backoff
            backoff := retryPolicy.RetryInterval
            if retryPolicy.BackoffFactor > 1.0 {
                backoff = time.Duration(float64(backoff) * pow(retryPolicy.BackoffFactor, float64(attempt)))
            }
            
            // Wait before retry
            select {
            case <-ctx.Done():
                report.FinalStatus = TaskStatusCanceled
                taskRun.Status = TaskStatusCanceled
                taskRun.EndTime = timePtr(time.Now())
                e.store.UpdateTaskRun(ctx, taskRun)
                return report
            case <-time.After(backoff):
                // Continue to next attempt
            }
        }
    }
    
    // All attempts failed
    report.FinalStatus = TaskStatusFailed
    taskRun.Status = TaskStatusFailed
    taskRun.RetryCount = len(report.Attempts)
    taskRun.ErrorMessage = lastErr.Error()
    taskRun.EndTime = timePtr(time.Now())
    e.store.UpdateTaskRun(ctx, taskRun)
    
    return report
}

// executeAttempt executes a single attempt of a task
func (e *Executor) executeAttempt(ctx context.Context, job *Job, task *Task, taskRun *storage.TaskRun, attemptNum int, config TaskConfig) *AttemptReport {
    attemptID := id.GenerateExecutionAttemptID(taskRun.ID, attemptNum)
    
    attempt := &storage.ExecutionAttempt{
        ID:            attemptID,
        TaskRunID:     taskRun.ID,
        AttemptNumber: attemptNum,
        Status:        TaskStatusRunning,
        StartTime:     time.Now(),
        CreatedAt:     time.Now(),
    }
    e.store.CreateExecutionAttempt(ctx, attempt)
    
    report := &AttemptReport{
        AttemptNumber: attemptNum,
        StartTime:     attempt.StartTime,
    }
    
    // Create task-specific timeout context
    timeout := config.TaskTimeout
    if timeout == nil {
        defaultTimeout := 5 * time.Minute
        timeout = &defaultTimeout
    }
    
    taskCtx, cancel := context.WithTimeout(ctx, *timeout)
    defer cancel()
    
    // Execute task function
    execCtx := &ExecutionContext{
        OccurrenceID: taskRun.OccurrenceID,
        TaskRunID:    taskRun.ID,
        AttemptID:    attemptID,
    }
    
    err := task.Fn(taskCtx, execCtx)
    
    endTime := time.Now()
    report.EndTime = &endTime
    report.Duration = endTime.Sub(attempt.StartTime)
    
    // Update attempt record
    attempt.EndTime = &endTime
    
    if err != nil {
        if taskCtx.Err() == context.DeadlineExceeded {
            report.Status = TaskStatusTimeout
            attempt.Status = TaskStatusTimeout
            report.ErrorMessage = "task execution timeout"
        } else {
            report.Status = TaskStatusFailed
            attempt.Status = TaskStatusFailed
            report.ErrorMessage = err.Error()
        }
        attempt.ErrorMessage = report.ErrorMessage
    } else {
        report.Status = TaskStatusSuccess
        attempt.Status = TaskStatusSuccess
    }
    
    e.store.UpdateExecutionAttempt(ctx, attempt)
    
    return report
}

func timePtr(t time.Time) *time.Time {
    return &t
}

func pow(base float64, exp float64) float64 {
    result := 1.0
    for i := 0; i < int(exp); i++ {
        result *= base
    }
    return result
}
```

### Phase 6: Main Scheduler

**Scheduler in `scheduler.go`:**
```go
package jobistemer

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/ahmed-com/schedule/storage"
    "github.com/ahmed-com/schedule/executor"
    "github.com/ahmed-com/schedule/concurrency"
    "github.com/ahmed-com/schedule/recovery"
    "github.com/ahmed-com/schedule/id"
)

// Scheduler is the main orchestrator for job scheduling and execution
type Scheduler struct {
    config    SchedulerConfig
    store     storage.Storage
    executor  *executor.Executor
    pool      *concurrency.WorkerPool
    reaper    *recovery.Reaper
    
    jobs      map[string]*Job
    jobsMu    sync.RWMutex
    
    running   bool
    runningMu sync.RWMutex
    
    ctx       context.Context
    cancel    context.CancelFunc
    wg        sync.WaitGroup
}

// NewScheduler creates a new scheduler instance
func NewScheduler(config SchedulerConfig, store storage.Storage) *Scheduler {
    ctx, cancel := context.WithCancel(context.Background())
    
    exec := executor.NewExecutor(store)
    pool := concurrency.NewWorkerPool(config.MaxConcurrentJobs)
    reaper := recovery.NewReaper(store, config.ReaperInterval)
    
    return &Scheduler{
        config:   config,
        store:    store,
        executor: exec,
        pool:     pool,
        reaper:   reaper,
        jobs:     make(map[string]*Job),
        ctx:      ctx,
        cancel:   cancel,
    }
}

// RegisterJob registers a job with the scheduler
func (s *Scheduler) RegisterJob(job *Job) error {
    s.jobsMu.Lock()
    defer s.jobsMu.Unlock()
    
    if _, exists := s.jobs[job.ID]; exists {
        return fmt.Errorf("job already registered: %s", job.ID)
    }
    
    // Persist job to storage
    storageJob := &storage.Job{
        ID:             job.ID,
        Name:           job.Name,
        ScheduleType:   "cron", // TODO: detect from ticker type
        Config:         job.Config,
        Version:        job.Version,
        Active:         job.Active,
        Paused:         job.Paused,
        CreatedAt:      time.Now(),
        UpdatedAt:      time.Now(),
    }
    
    if err := s.store.CreateJob(s.ctx, storageJob); err != nil {
        return err
    }
    
    // Persist tasks
    for _, task := range job.Tasks {
        storageTask := &storage.Task{
            ID:        task.ID,
            JobID:     job.ID,
            Name:      task.Name,
            Config:    task.Config,
            Order:     task.Order,
            CreatedAt: time.Now(),
            UpdatedAt: time.Now(),
        }
        if err := s.store.CreateTask(s.ctx, storageTask); err != nil {
            return err
        }
    }
    
    s.jobs[job.ID] = job
    return nil
}

// Start starts the scheduler
func (s *Scheduler) Start() error {
    s.runningMu.Lock()
    defer s.runningMu.Unlock()
    
    if s.running {
        return fmt.Errorf("scheduler already running")
    }
    
    // Start recovery process
    if err := s.performRecovery(); err != nil {
        return fmt.Errorf("recovery failed: %w", err)
    }
    
    // Start reaper
    s.reaper.Start(s.ctx)
    
    // Start worker pool
    s.pool.Start()
    
    // Start all job tickers
    s.jobsMu.RLock()
    for _, job := range s.jobs {
        if job.Active && !job.Paused {
            job.Ticker.Start()
            s.wg.Add(1)
            go s.watchJob(job)
        }
    }
    s.jobsMu.RUnlock()
    
    s.running = true
    return nil
}

// watchJob monitors a job's ticker and triggers executions
func (s *Scheduler) watchJob(job *Job) {
    defer s.wg.Done()
    
    for {
        select {
        case <-s.ctx.Done():
            return
        case execCtx := <-job.Ticker.Channel():
            s.handleJobTrigger(job, execCtx)
        }
    }
}

// handleJobTrigger handles a job trigger event
func (s *Scheduler) handleJobTrigger(job *Job, execCtx ticker.ExecutionContext) {
    // Check overlap policy
    isRunning, err := job.IsRunning(s.ctx, s.store)
    if err != nil {
        // Log error
        return
    }
    
    if isRunning {
        switch job.Config.OverlapPolicy {
        case OverlapPolicySkip:
            // Skip this occurrence
            return
        case OverlapPolicyQueue:
            // Create occurrence with Queued status
            // Will be picked up by OnComplete hook
        case OverlapPolicyAllow:
            // Continue with creation
        }
    }
    
    // Create deterministic job occurrence ID
    occurrenceID := id.GenerateJobOccurrenceID(job.ID, execCtx.ScheduledTime)
    
    occurrence := &storage.JobOccurrence{
        ID:            occurrenceID,
        JobID:         job.ID,
        JobVersion:    job.Version,
        ScheduledTime: execCtx.ScheduledTime,
        Status:        JobStatusPending,
        Config:        job.Config,
        IsRecoveryRun: false,
        CreatedAt:     time.Now(),
        UpdatedAt:     time.Now(),
    }
    
    // Atomic create (race to win)
    err = s.store.CreateJobOccurrence(s.ctx, occurrence)
    if err != nil {
        // Another node won the race or already exists
        return
    }
    
    // Submit to worker pool
    s.pool.Submit(func() {
        _, err := s.executor.ExecuteJobOccurrence(s.ctx, job, occurrence)
        if err != nil {
            // Log error
        }
    })
}

// performRecovery performs OnBoot recovery
func (s *Scheduler) performRecovery() error {
    s.jobsMu.RLock()
    defer s.jobsMu.RUnlock()
    
    for _, job := range s.jobs {
        if !job.Active {
            continue
        }
        
        // Get last run time
        var lastRunTime time.Time
        storageJob, err := s.store.GetJob(s.ctx, job.ID)
        if err != nil {
            return err
        }
        
        if storageJob.LastRunTime != nil {
            lastRunTime = *storageJob.LastRunTime
        } else {
            lastRunTime = time.Now().Add(-24 * time.Hour) // Default to 24h ago
        }
        
        // Get missed occurrences
        missedTimes, err := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
        if err != nil {
            return err
        }
        
        // Apply recovery strategy
        recoveryHandler := recovery.NewRecoveryHandler(s.store, s.executor, s.pool)
        err = recoveryHandler.ApplyStrategy(s.ctx, job, missedTimes)
        if err != nil {
            return err
        }
    }
    
    return nil
}

// Shutdown gracefully shuts down the scheduler
func (s *Scheduler) Shutdown(timeout time.Duration) error {
    s.runningMu.Lock()
    defer s.runningMu.Unlock()
    
    if !s.running {
        return nil
    }
    
    // Stop all tickers
    s.jobsMu.RLock()
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    s.jobsMu.RUnlock()
    
    // Signal cancellation
    s.cancel()
    
    // Wait for grace period or all jobs to complete
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        // All jobs completed gracefully
    case <-time.After(timeout):
        // Timeout - force shutdown
    }
    
    // Stop worker pool
    s.pool.Stop()
    
    // Stop reaper
    s.reaper.Stop()
    
    s.running = false
    return nil
}
```

### Phase 7: Recovery System

**Recovery handler in `recovery/recovery.go`:**
```go
package recovery

import (
    "context"
    "time"
    
    "github.com/ahmed-com/schedule/storage"
    "github.com/ahmed-com/schedule/executor"
    "github.com/ahmed-com/schedule/concurrency"
    "github.com/ahmed-com/schedule/id"
)

type RecoveryHandler struct {
    store    storage.Storage
    executor *executor.Executor
    pool     *concurrency.WorkerPool
}

func NewRecoveryHandler(store storage.Storage, exec *executor.Executor, pool *concurrency.WorkerPool) *RecoveryHandler {
    return &RecoveryHandler{
        store:    store,
        executor: exec,
        pool:     pool,
    }
}

// ApplyStrategy applies the configured recovery strategy
func (r *RecoveryHandler) ApplyStrategy(ctx context.Context, job *Job, missedTimes []time.Time) error {
    if len(missedTimes) == 0 {
        return nil
    }
    
    switch job.Config.RecoveryStrategy {
    case RecoveryStrategyExecuteAll:
        return r.executeAll(ctx, job, missedTimes)
    case RecoveryStrategyExecuteLast:
        return r.executeLast(ctx, job, missedTimes)
    case RecoveryStrategyMarkAsMissed:
        return r.markAsMissed(ctx, job, missedTimes)
    case RecoveryStrategyBoundedWindow:
        return r.boundedWindow(ctx, job, missedTimes)
    default:
        return r.executeAll(ctx, job, missedTimes)
    }
}

func (r *RecoveryHandler) executeAll(ctx context.Context, job *Job, missedTimes []time.Time) error {
    for _, t := range missedTimes {
        if err := r.scheduleRecoveryRun(ctx, job, t); err != nil {
            return err
        }
    }
    return nil
}

func (r *RecoveryHandler) executeLast(ctx context.Context, job *Job, missedTimes []time.Time) error {
    if len(missedTimes) == 0 {
        return nil
    }
    lastTime := missedTimes[len(missedTimes)-1]
    return r.scheduleRecoveryRun(ctx, job, lastTime)
}

func (r *RecoveryHandler) markAsMissed(ctx context.Context, job *Job, missedTimes []time.Time) error {
    // Just log, don't execute
    return nil
}

func (r *RecoveryHandler) boundedWindow(ctx context.Context, job *Job, missedTimes []time.Time) error {
    if job.Config.BoundedWindow == nil {
        return r.executeAll(ctx, job, missedTimes)
    }
    
    window := job.Config.BoundedWindow
    var filteredTimes []time.Time
    
    // Apply max duration filter
    if window.MaxDuration > 0 {
        cutoff := time.Now().Add(-window.MaxDuration)
        for _, t := range missedTimes {
            if t.After(cutoff) {
                filteredTimes = append(filteredTimes, t)
            }
        }
    } else {
        filteredTimes = missedTimes
    }
    
    // Apply max occurrences filter
    if window.MaxOccurrences > 0 && len(filteredTimes) > window.MaxOccurrences {
        filteredTimes = filteredTimes[len(filteredTimes)-window.MaxOccurrences:]
    }
    
    return r.executeAll(ctx, job, filteredTimes)
}

func (r *RecoveryHandler) scheduleRecoveryRun(ctx context.Context, job *Job, scheduledTime time.Time) error {
    occurrenceID := id.GenerateJobOccurrenceID(job.ID, scheduledTime)
    
    occurrence := &storage.JobOccurrence{
        ID:            occurrenceID,
        JobID:         job.ID,
        JobVersion:    job.Version,
        ScheduledTime: scheduledTime,
        Status:        JobStatusPending,
        Config:        job.Config,
        IsRecoveryRun: true,
        CreatedAt:     time.Now(),
        UpdatedAt:     time.Now(),
    }
    
    // Atomic create (idempotent)
    err := r.store.CreateJobOccurrence(ctx, occurrence)
    if err != nil {
        // Already exists, skip
        return nil
    }
    
    // Submit to worker pool
    r.pool.Submit(func() {
        r.executor.ExecuteJobOccurrence(ctx, job, occurrence)
    })
    
    return nil
}
```

**Reaper in `recovery/reaper.go`:**
```go
package recovery

import (
    "context"
    "time"
    
    "github.com/ahmed-com/schedule/storage"
)

type Reaper struct {
    store    storage.Storage
    interval time.Duration
    stopCh   chan struct{}
}

func NewReaper(store storage.Storage, interval time.Duration) *Reaper {
    return &Reaper{
        store:    store,
        interval: interval,
        stopCh:   make(chan struct{}),
    }
}

func (r *Reaper) Start(ctx context.Context) {
    go r.run(ctx)
}

func (r *Reaper) run(ctx context.Context) {
    ticker := time.NewTicker(r.interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-r.stopCh:
            return
        case <-ticker.C:
            r.reapStaleJobs(ctx)
        }
    }
}

func (r *Reaper) reapStaleJobs(ctx context.Context) {
    // Query for stale jobs
    staleOccurrences, err := r.store.ListStaleOccurrences(ctx, 1*time.Hour)
    if err != nil {
        // Log error
        return
    }
    
    for _, occ := range staleOccurrences {
        // Atomically update to Failed_Stale
        occ.Status = JobStatusFailedStale
        occ.EndTime = timePtr(time.Now())
        r.store.UpdateJobOccurrence(ctx, occ)
    }
}

func (r *Reaper) Stop() {
    close(r.stopCh)
}

func timePtr(t time.Time) *time.Time {
    return &t
}
```

## Testing Strategy

### Unit Tests
- Test each component in isolation with mocked dependencies
- Test deterministic ID generation
- Test configuration cascading
- Test retry logic and backoff calculations
- Test recovery strategy implementations

### Integration Tests
- Test end-to-end job execution
- Test distributed coordination (multiple scheduler instances)
- Test persistence with BadgerDB
- Test graceful shutdown
- Test recovery after simulated crashes

### Performance Tests
- Benchmark job scheduling throughput
- Test worker pool under load
- Test storage performance with large datasets
- Memory profiling for long-running schedulers

## Documentation

### GoDoc
- Comprehensive package documentation
- Example code for common use cases
- API reference for all public types and methods

### User Guide
- Getting started tutorial
- Configuration guide
- Best practices
- Troubleshooting guide

### Architecture Documentation
- System architecture diagrams
- Data flow diagrams
- Sequence diagrams for key operations

## Dependencies

### Required
- `github.com/dgraph-io/badger/v4` - BadgerDB storage
- `github.com/robfig/cron/v3` - Cron expression parsing
- `github.com/google/uuid` - UUID generation

### Optional (for examples and testing)
- `github.com/prometheus/client_golang` - Metrics
- `github.com/stretchr/testify` - Testing utilities
- `go.uber.org/zap` - Structured logging

## Implementation Timeline

### Week 1-2: Foundation
- Phase 1: Core types and ID generation
- Phase 2: Storage layer with BadgerDB
- Unit tests for foundation

### Week 3-4: Scheduling
- Phase 3: Ticker implementations
- Phase 4: Job and task definitions
- Integration tests for scheduling

### Week 5-6: Execution
- Phase 5: Execution engine
- Phase 6: Concurrency control
- Performance tests

### Week 7-8: Resilience
- Phase 7: Recovery system
- Phase 8: Update and versioning
- Failure scenario tests

### Week 9-10: Polish
- Phase 9: Observability
- Phase 10: Main scheduler integration
- Documentation and examples

### Week 11-12: Testing and Release
- Comprehensive testing
- Performance optimization
- Documentation completion
- Release preparation

## Next Steps

1. Initialize Go module with `go mod init github.com/ahmed-com/schedule`
2. Create directory structure
3. Implement Phase 1 (Foundation)
4. Set up CI/CD pipeline
5. Begin iterative development following the phases above
