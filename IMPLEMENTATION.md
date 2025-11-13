# Jobistemer - Go Job Scheduling Library

A production-ready Go library for scheduling and executing jobs with persistent, fault-tolerant execution. Built on principles of deterministic idempotency and atomic, transactional persistence.

## Implementation Status

### ✅ Completed Phases (1-8)

#### Phase 1: Core Types and ID Generation
- ✅ Complete type system for jobs, tasks, occurrences, and execution attempts
- ✅ Deterministic ID generation using UUIDv5
- ✅ Configuration structures with cascading support
- ✅ Job and task status enums
- ✅ Execution context and reporting structures

**Key Features:**
- Deterministic IDs ensure idempotent operations across distributed systems
- IDs based on stable context: `UUIDv5(namespace, contextual_data)`
- Example: Job Occurrence ID = `UUIDv5(JobID, ScheduledTimestamp)`

#### Phase 2: Storage Layer
- ✅ Storage interface defining all persistence operations
- ✅ BadgerDB implementation with hierarchical key schema
- ✅ Atomic create-if-not-exists operations (critical for distributed coordination)
- ✅ CRUD operations for jobs, tasks, occurrences, runs, and attempts
- ✅ Query operations for running, stale, and queued occurrences

**Key Features:**
- Backend-agnostic design (easy to add PostgreSQL, MySQL, etc.)
- Hierarchical key schema: `job/{jobID}/occurrence/{occID}/task/{taskID}/run/attempt/{attemptID}`
- Atomic operations prevent race conditions in distributed environments
- Efficient prefix-based queries

#### Phase 3: Ticker Interface and Implementations
- ✅ Ticker interface for pluggable scheduling strategies
- ✅ Cron ticker with full cron expression support
- ✅ ISO8601 ticker for interval-based scheduling
- ✅ Once ticker for one-time execution
- ✅ Timezone support for all tickers
- ✅ Start/Stop/Pause/Resume control methods
- ✅ Recovery support (GetOccurrencesBetween for missed runs)

**Key Features:**
- Cron: Standard cron expressions with timezone support
- ISO8601: Repeating intervals with optional repetition limits
- Once: One-time execution at a specific time
- Active window support (start/end time constraints)
- Non-blocking channel design prevents ticker stalls

#### Phase 4: Job and Task Definitions
- ✅ Job type with deterministic ID generation
- ✅ Task type with cascading configuration
- ✅ AddTask method for composing jobs with multiple tasks
- ✅ GetEffectiveConfig for config resolution hierarchy
- ✅ IsRunning persistent query for distributed coordination
- ✅ OnStart and OnComplete hooks for job lifecycle

**Key Features:**
- Deterministic job and task IDs based on names
- Cascading config: task-specific overrides job-level defaults
- Persistent IsRunning check avoids in-memory flags
- Task function signature with context and execution metadata

#### Phase 5: Execution Engine
- ✅ Executor type managing job occurrence execution
- ✅ Sequential task execution with fail-fast support
- ✅ Parallel task execution with sync.WaitGroup
- ✅ Retry logic with exponential backoff
- ✅ Task-level timeout handling
- ✅ Execution attempt tracking and reporting
- ✅ Comprehensive execution reports

**Key Features:**
- Sequential and parallel task execution modes
- Fail-fast and continue failure strategies
- Configurable retry policies with backoff
- Context-based timeout and cancellation
- Detailed execution reports with attempt history

#### Phase 6: Main Scheduler
- ✅ Scheduler orchestrating jobs, tickers, and execution
- ✅ RegisterJob for job registration and persistence
- ✅ Start method with recovery and ticker watching
- ✅ watchJob goroutines monitoring ticker events
- ✅ handleJobTrigger with overlap policy enforcement
- ✅ Graceful Shutdown with configurable timeout
- ✅ GetJob and ListJobs for job management

**Key Features:**
- Distributed coordination via deterministic IDs
- Overlap policies: Skip, Allow, Queue
- OnBoot recovery for missed occurrences
- Graceful shutdown waits for in-flight jobs
- Worker pool integration for concurrency control

#### Phase 7: Recovery System
- ✅ RecoveryHandler for missed occurrence strategies
- ✅ ExecuteAll: full backfill of all missed runs
- ✅ ExecuteLast: latest-only recovery
- ✅ MarkAsMissed: skip missed runs
- ✅ BoundedWindow: limited backfill with constraints
- ✅ Reaper for cleaning stale job occurrences
- ✅ Idempotent recovery with deterministic IDs

**Key Features:**
- Multiple recovery strategies for different use cases
- Bounded window with max occurrences and duration
- Automatic reaper for stale job cleanup
- Idempotent recovery safe for restarts

#### Phase 8: Concurrency Control
- ✅ WorkerPool managing concurrent job execution
- ✅ Configurable max concurrent jobs
- ✅ Task queue with buffering
- ✅ Submit method for job queuing
- ✅ Start and Stop methods for pool lifecycle
- ✅ QueueLength and MaxWorkers queries

**Key Features:**
- Prevents thundering herd scenarios
- Configurable concurrency limits
- Graceful shutdown waits for workers
- Thread-safe task submission

#### Phase 9: Observability & Metrics
- ✅ Metrics package with MetricsCollector interface
- ✅ InMemoryMetrics and NoOpMetrics implementations
- ✅ Gauges for jobs running, in queue, and paused
- ✅ Counters for task attempts, job occurrences, and recovery runs
- ✅ Histograms for job and task execution duration
- ✅ Integration in scheduler, executor, and recovery handler
- ✅ Comprehensive tests for metrics functionality

**Key Features:**
- Prometheus-style metrics interface
- Thread-safe in-memory collector for testing and basic monitoring
- No-op collector for production when external metrics system is used
- Real-time observability of system state

#### Phase 10: Update and Versioning
- ✅ JobUpdate type with version checking
- ✅ Update policies (Immediate, Graceful, Windowed)
- ✅ Diffing algorithm for schedule changes
- ✅ Support for adding/removing tasks
- ✅ Support for updating task configurations
- ✅ Optimistic locking with version numbers
- ✅ Transactional update operations
- ✅ UpdateJob method in scheduler
- ✅ Tests for job updates and versioning

**Key Features:**
- Version-based optimistic locking prevents concurrent updates
- Multiple update policies for different use cases
- Safe schedule changes without losing occurrences
- Idempotent update operations using deterministic IDs

### ✅ All Phases Complete!

The Jobistemer scheduling system is now fully implemented with all 10 phases complete. The system is ready for production use with:
- Deterministic idempotency and lock-free distributed coordination
- Fault-tolerant execution with persistent state
- Real-time observability and metrics
- Safe job updates with versioning
- Comprehensive test coverage

## Quick Start

### Installation

```bash
go get github.com/ahmed-com/schedule
```

### Example Usage

See `examples/phase1-3-demo.go` for a complete demonstration:

```bash
go run examples/phase1-3-demo.go
```

### Phase 1: Deterministic ID Generation

```go
import "github.com/ahmed-com/schedule/id"

// Generate deterministic job ID
jobID := id.GenerateJobID("daily-backup")

// Generate task ID
taskID := id.GenerateTaskID(jobID, "backup-database")

// Generate occurrence ID
scheduledTime := time.Now()
occurrenceID := id.GenerateJobOccurrenceID(jobID, scheduledTime)
```

### Phase 2: Storage Operations

```go
import (
    "github.com/ahmed-com/schedule/storage"
    "github.com/ahmed-com/schedule/storage/badger"
)

// Create storage
store, err := badger.NewBadgerStorage("/path/to/db")
defer store.Close()

// Create a job
job := &storage.Job{
    ID:           jobID,
    Name:         "daily-backup",
    ScheduleType: "cron",
    Version:      1,
    Active:       true,
    CreatedAt:    time.Now(),
}
err = store.CreateJob(ctx, job)

// Atomic create-if-not-exists for occurrences
occurrence := &storage.JobOccurrence{
    ID:            occurrenceID,
    JobID:         jobID,
    ScheduledTime: scheduledTime,
    Status:        storage.JobStatusPending,
    CreatedAt:     time.Now(),
}
err = store.CreateJobOccurrence(ctx, occurrence)
```

### Phase 3: Ticker Usage

```go
import "github.com/ahmed-com/schedule/ticker"

// Cron ticker - runs every day at 2 AM
config := ticker.TickerConfig{Timezone: "America/New_York"}
cronTicker, err := ticker.NewCronTicker("0 2 * * *", config)
cronTicker.Start()

// ISO8601 ticker - every 6 hours, 10 times
iso8601Ticker, err := ticker.NewISO8601Ticker(
    6*time.Hour,
    time.Now(),
    10,
    config,
)

// Once ticker - run at specific time
onceTicker, err := ticker.NewOnceTicker(
    time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC),
    config,
)

// Get next run time
nextRun, err := cronTicker.NextRun()

// Get all occurrences in a time range (for recovery)
occurrences, err := cronTicker.GetOccurrencesBetween(
    time.Now().Add(-24*time.Hour),
    time.Now(),
)
```

### Phase 4-8: Complete Job Scheduling

```go
import (
    "context"
    "time"
    
    "github.com/ahmed-com/schedule"
    "github.com/ahmed-com/schedule/scheduler"
    "github.com/ahmed-com/schedule/storage/badger"
    "github.com/ahmed-com/schedule/ticker"
)

// Create storage backend
store, err := badger.NewBadgerStorage("/path/to/db")
if err != nil {
    panic(err)
}
defer store.Close()

// Create scheduler with configuration
config := jobistemer.SchedulerConfig{
    MaxConcurrentJobs: 10,
    ReaperInterval:    5 * time.Minute,
    NodeID:            "node-1",
}
sched := scheduler.NewScheduler(config, store)

// Configure job with cascading settings
jobConfig := jobistemer.JobConfig{
    TaskExecutionMode: jobistemer.TaskExecutionModeParallel,
    FailureStrategy:   jobistemer.FailureStrategyFailFast,
    ExecutionTimeout:  30 * time.Minute,
    OverlapPolicy:     jobistemer.OverlapPolicySkip,
    RecoveryStrategy:  jobistemer.RecoveryStrategyBoundedWindow,
    BoundedWindow: &jobistemer.BoundedWindow{
        MaxOccurrences: 10,
        MaxDuration:    24 * time.Hour,
    },
    TaskConfig: jobistemer.TaskConfig{
        TaskTimeout: timePtr(5 * time.Minute),
        RetryPolicy: &jobistemer.RetryPolicy{
            MaxRetries:    3,
            RetryInterval: 30 * time.Second,
            BackoffFactor: 2.0,
        },
    },
}

// Create ticker (cron expression: every day at 2 AM)
tick, err := ticker.NewCronTicker("0 2 * * *", ticker.TickerConfig{
    Timezone: "America/New_York",
})
if err != nil {
    panic(err)
}

// Create job
job := jobistemer.NewJob("daily-backup", tick, jobConfig)

// Add tasks to job
job.AddTask("backup-database", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
    // Your backup logic here
    fmt.Println("Backing up database...")
    return nil
}, jobistemer.TaskConfig{})

// Task with custom retry policy
customRetry := &jobistemer.RetryPolicy{
    MaxRetries:    10,
    RetryInterval: 1 * time.Minute,
    BackoffFactor: 1.5,
}
job.AddTask("sync-to-cloud", func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
    // Sync logic with aggressive retries
    fmt.Println("Syncing to cloud...")
    return nil
}, jobistemer.TaskConfig{
    RetryPolicy: customRetry,
})

// Add lifecycle hooks
job.OnStart = func(ctx context.Context, occurrence *jobistemer.JobOccurrence) {
    fmt.Printf("Job %s starting at %v\n", occurrence.JobID, occurrence.ScheduledTime)
}

job.OnComplete = func(ctx context.Context, report *jobistemer.ExecutionReport) {
    fmt.Printf("Job completed with outcome: %s\n", report.GroupOutcome)
    for _, taskReport := range report.TaskReports {
        fmt.Printf("  Task %s: %s (%d attempts)\n", 
            taskReport.TaskName, taskReport.FinalStatus, len(taskReport.Attempts))
    }
}

// Register job with scheduler
err = sched.RegisterJob(job)
if err != nil {
    panic(err)
}

// Start scheduler (performs recovery and starts tickers)
err = sched.Start()
if err != nil {
    panic(err)
}

// Let scheduler run...
// When ready to shutdown:
sched.Shutdown(30 * time.Second)

func timePtr(d time.Duration) *time.Duration {
    return &d
}
```

### Phase 9: Observability & Metrics Usage

```go
import (
    "github.com/ahmed-com/schedule"
    "github.com/ahmed-com/schedule/metrics"
    "github.com/ahmed-com/schedule/scheduler"
    "github.com/ahmed-com/schedule/storage/badger"
)

// Create metrics collector
metricsCollector := metrics.NewInMemoryMetrics()

// Configure scheduler with metrics
config := jobistemer.SchedulerConfig{
    MaxConcurrentJobs: 10,
    ReaperInterval:    5 * time.Minute,
    Metrics:           metricsCollector,
}

store, _ := badger.NewBadgerStorage("/path/to/db")
sched := scheduler.NewScheduler(config, store)

// ... register and start jobs ...

// Query metrics
fmt.Printf("Jobs running: %d\n", metricsCollector.GetJobsRunning())
fmt.Printf("Jobs in queue: %d\n", metricsCollector.GetJobsInQueue())
fmt.Printf("Jobs paused: %d\n", metricsCollector.GetJobsPaused())

// Get task attempt counts
attempts := metricsCollector.GetTaskAttempts("my-job", "my-task", "success")
fmt.Printf("Task attempts (success): %d\n", attempts)

// Get job occurrence counts
occurrences := metricsCollector.GetJobOccurrences("my-job", "completed")
fmt.Printf("Job occurrences (completed): %d\n", occurrences)

// Get job duration histogram
durations := metricsCollector.GetJobDurations("my-job")
fmt.Printf("Job durations: %v\n", durations)
```

### Phase 10: Update and Versioning Usage

```go
import (
    "github.com/ahmed-com/schedule"
    "github.com/ahmed-com/schedule/scheduler"
    "github.com/ahmed-com/schedule/ticker"
    "github.com/ahmed-com/schedule/update"
)

// Get the current job
job, exists := sched.GetJob("my-job")
if !exists {
    panic("job not found")
}

// Update job configuration
newConfig := jobistemer.JobConfig{
    TaskExecutionMode: jobistemer.TaskExecutionModeParallel,
    FailureStrategy:   jobistemer.FailureStrategyContinue,
    ExecutionTimeout:  1 * time.Hour,
}

// Create a new ticker (e.g., change from daily to hourly)
newTicker, _ := ticker.NewCronTicker("0 * * * *", ticker.TickerConfig{
    Timezone: "America/New_York",
})

// Add a new task
newTask := &jobistemer.Task{
    ID:    id.GenerateTaskID(job.ID, "cleanup"),
    Name:  "cleanup",
    Order: len(job.Tasks),
    Fn: func(ctx context.Context, execCtx *jobistemer.ExecutionContext) error {
        fmt.Println("Running cleanup task")
        return nil
    },
}

// Create update request
jobUpdate := &update.JobUpdate{
    JobID:           job.ID,
    ExpectedVersion: job.Version, // Optimistic locking
    NewConfig:       &newConfig,
    NewTicker:       newTicker,
    AddedTasks:      []*jobistemer.Task{newTask},
    RemovedTaskIDs:  []string{}, // Remove any task IDs
    UpdatedTasks:    []*jobistemer.Task{}, // Update any tasks
    Policy:          jobistemer.UpdatePolicyImmediate, // Or Graceful, Windowed
}

// Apply the update
err := sched.UpdateJob(jobUpdate)
if err != nil {
    fmt.Printf("Update failed: %v\n", err)
    // Handle version mismatch or other errors
}

// Version is automatically incremented
fmt.Printf("Job updated to version %d\n", job.Version)
```

**Update Policies:**

- **Immediate**: Cancel all pending occurrences and apply new schedule immediately
- **Graceful**: Let the next scheduled occurrence run under old schedule, then switch
- **Windowed**: Only update occurrences within a time window (e.g., 7 days)

**Version Control:**

Updates use optimistic locking to prevent concurrent modifications:
```go
// If another process updated the job, your update will fail with version mismatch
err := sched.UpdateJob(jobUpdate)
if err != nil && strings.Contains(err.Error(), "version mismatch") {
    // Reload job and retry
    job, _ = sched.GetJob(job.ID)
    jobUpdate.ExpectedVersion = job.Version
    err = sched.UpdateJob(jobUpdate)
}
```

## Architecture Highlights

### Deterministic Idempotency

All entities use deterministic IDs based on contextual information:
- Same inputs always produce the same ID
- Multiple nodes can independently calculate identical IDs
- Database rejections of duplicates are expected, not errors
- Safe retry and recovery without duplicate work

### Lock-Free Distributed Coordination

Instead of TTL-based locks (prone to clock skew and failure):
- Job occurrences are created atomically with deterministic IDs
- First node to successfully create wins the "race"
- Other nodes receive "already exists" error (expected behavior)
- No separate lock entity, no expiration, no renewal needed

### Hierarchical Storage Schema

```
job/{jobID}
job/{jobID}/task/{taskID}
job/{jobID}/occurrence/{occID}
job/{jobID}/occurrence/{occID}/task/{taskID}/run
job/{jobID}/occurrence/{occID}/task/{taskID}/run/attempt/{attemptID}
```

This enables:
- Efficient prefix-based queries
- Transactional updates of related entities
- Easy cleanup and archival

## Dependencies

- **github.com/google/uuid** - UUID generation
- **github.com/dgraph-io/badger/v4** - BadgerDB key-value store
- **github.com/robfig/cron/v3** - Cron expression parsing

## Testing

```bash
# Build all packages
go build ./...

# Run the demo
go run examples/phase1-3-demo.go
```

## Contributing

This project follows the phased implementation plan outlined in the technical specification. Contributions should align with the documented architecture and design principles.

## License

[To be determined]

## References

See `README.md` (the original technical specification) for complete architectural details, rationale, and remaining implementation phases.
