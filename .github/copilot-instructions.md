# GitHub Copilot Instructions for Jobistemer Scheduling System

## Project Overview

Jobistemer is a production-ready Go library for distributed job scheduling with persistent, fault-tolerant execution. It solves three critical distributed systems problems through architectural design rather than band-aid fixes:

1. **Job Overlap** - Prevents self-overlapping runs via persistent state tracking
2. **Distributed Locking** - Lock-free coordination using deterministic IDs (no TTL leases)
3. **Parallel Task Execution** - Coordinated fan-out with failure handling

### Core Architecture (The "Why" Behind Everything)

**Deterministic Idempotency**: Every entity ID is derived from stable context (UUIDv5):
- `JobOccurrenceID = UUIDv5(JobID, ScheduledTimestamp)` - multiple nodes compute identical IDs
- Race to create occurrence in DB - first write wins, others get "already exists" (expected, not error)
- No separate lock entity, no TTL expiration, no clock skew issues

**Hierarchical State Model** (this structure solves the problems):
```
Job (schedule definition)
  └─ Task (unit of work)
      └─ JobOccurrence (single trigger = the "distributed lock")
          └─ TaskRun (execution record for task)
              └─ ExecutionAttempt (individual try with retries)
```

**Why this matters**: Without `JobOccurrence` as distinct entity, distributed coordination fails. It's the persistent record that IS the lock.

## Critical Implementation Patterns

### ID Generation (Package: `id/`)

**All IDs MUST be deterministic** - see `id/id.go` for canonical patterns:

```go
// Job occurrence - THIS is the distributed lock
occID := id.GenerateJobOccurrenceID(jobID, scheduledTime)
// Uses UTC RFC3339 format for time consistency

// Task run
runID := id.GenerateTaskRunID(occurrenceID, taskID)

// Execution attempt
attemptID := id.GenerateExecutionAttemptID(taskRunID, attemptNumber)
```

**Critical rule**: Never use `uuid.New()` or auto-increment for coordination entities. Random IDs break distributed idempotency.

### Storage Interface (Package: `storage/`)

**The contract that enables everything**:

```go
// Atomic create-if-not-exists - rejects duplicates (expected behavior)
CreateJobOccurrence(ctx, occurrence) error

// Query primitives for coordination
ListRunningOccurrences(ctx) ([]*JobOccurrence, error)
ListStaleOccurrences(ctx, threshold) ([]*JobOccurrence, error)
```

**Implementation notes**:
- BadgerDB: Use `txn.Get()` check before `txn.Set()` in same transaction
- SQL: Use UNIQUE constraint + INSERT (let DB reject duplicates)
- Hierarchical keys: `job/{jobID}/occurrence/{occID}/...` enables prefix queries

## Execution Flow (Package: `executor/`, `scheduler/`)

### The Race-to-Create Pattern (Distributed Lock)

From `scheduler/scheduler.go:handleJobTrigger()`:

```go
// 1. All nodes compute SAME ID independently
occurrenceID := id.GenerateJobOccurrenceID(job.ID, execCtx.ScheduledTime)

// 2. Atomic create attempt
err := s.store.CreateJobOccurrence(ctx, storageOcc)
if err != nil {
    // Another node won - this is EXPECTED, not an error
    return  // Gracefully exit
}

// 3. Winner proceeds with execution
s.pool.Submit(func() {
    s.exec.ExecuteJobOccurrence(ctx, job, occurrence)
})
```

**Never**:
- Use in-memory `running` bool - multiple nodes won't see it
- Create random occurrence IDs - breaks race-to-create pattern
- Log duplicate rejection as error - it's expected distributed behavior

### Parallel Execution with Fail-Fast

From `executor/executor.go:executeParallel()`:

```go
groupCtx, cancel := context.WithCancel(ctx)
defer cancel()

var wg sync.WaitGroup
for _, task := range job.Tasks {
    wg.Add(1)
    go func(t *Task) {
        defer wg.Done()
        
        taskReport := e.executeTask(groupCtx, job, t, occurrence)
        
        if taskReport.FinalStatus == TaskStatusFailed {
            if strategy == FailureStrategyFailFast {
                cancel() // Signals all other goroutines
            }
        }
    }(task)
}
wg.Wait()
```

**Key pattern**: `context.WithCancel` + `sync.WaitGroup` provides coordinated fan-out/fan-in.

### Config Cascading (The CSS Model)

From `job.go:GetEffectiveConfig()`:

```go
effective := j.Config.TaskConfig  // Start with job defaults

// Task-specific overrides (pointer = optional)
if task.Config.FailureStrategy != nil {
    effective.FailureStrategy = task.Config.FailureStrategy
}
if task.Config.RetryPolicy != nil {
    effective.RetryPolicy = task.Config.RetryPolicy
}
```

**Rule**: Use pointer fields (`*time.Duration`, `*RetryPolicy`) to distinguish "not set" from "set to zero value".

## Development Workflow

### Running Tests

```bash
# All tests with race detection
go test -race ./...

# Specific package
go test -race ./id

# Run demo
go run examples/phase1-3-demo.go

# Build verification
go build ./...
```

### Test Patterns (See `*_test.go` files)

**Determinism tests** (`id/id_test.go`):
```go
func TestIDDeterminismAcrossRuns(t *testing.T) {
    // Generate same ID twice - MUST be identical
    id1 := id.GenerateJobOccurrenceID(jobID, time1)
    id2 := id.GenerateJobOccurrenceID(jobID, time1)
    if id1 != id2 {
        t.Error("IDs not deterministic")
    }
}
```

**Concurrency tests** (`concurrency/pool_test.go`):
```go
func TestWorkerPoolConcurrencyLimit(t *testing.T) {
    // Use channels + sync primitives to verify max workers
    concurrent := &atomic.Int32{}
    // ... verify never exceeds pool.maxWorkers
}
```

**Table-driven tests** (common pattern):
```go
tests := []struct {
    name string
    input X
    want Y
}{
    {"case1", x1, y1},
    {"case2", x2, y2},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        // test logic
    })
}
```

## Package Organization & Key Files

```
jobistemer/                      # Root package - core types
├── types.go                     # Status enums, configs, reports
├── job.go                       # Job/Task definitions, GetEffectiveConfig()
├── id/                          # Deterministic ID generation
│   └── id.go                    # UUIDv5-based ID functions
├── storage/                     # Storage abstraction
│   ├── storage.go               # Storage interface (THE contract)
│   └── badger/                  # BadgerDB implementation
│       └── badger.go            # Hierarchical key schema
├── ticker/                      # Scheduling strategies
│   ├── ticker.go                # Ticker interface
│   ├── cron.go                  # Cron expression support
│   ├── iso8601.go               # ISO8601 intervals
│   └── once.go                  # One-time execution
├── scheduler/                   # Main orchestrator
│   └── scheduler.go             # Job registration, trigger handling
├── executor/                    # Execution engine
│   └── executor.go              # Sequential/parallel execution
├── concurrency/                 # Worker pool (thundering herd protection)
│   └── pool.go                  # Bounded concurrent execution
├── recovery/                    # OnBoot recovery & reaper
│   ├── recovery.go              # RecoveryStrategy implementations
│   └── reaper.go                # Stale occurrence cleanup
├── metrics/                     # Observability
│   └── metrics.go               # MetricsCollector interface
└── update/                      # Job versioning & updates
    └── update.go                # UpdateManager, diffing logic
```

**Where to start**:
- Adding storage backend? → Implement `storage/storage.go` interface
- New ticker strategy? → Implement `ticker/ticker.go` interface
- Understanding execution? → Read `executor/executor.go:executeParallel()`
- Debugging coordination? → Check `scheduler/scheduler.go:handleJobTrigger()`

## Common Modification Patterns

### Adding a New Ticker Type

```go
// 1. Create new file: ticker/mythicker.go
type MyTicker struct {
    // config fields
    ch chan ticker.ExecutionContext
}

// 2. Implement Ticker interface (11 methods)
func (t *MyTicker) Start() error { /* ... */ }
func (t *MyTicker) NextRun() (time.Time, error) { /* ... */ }
// ... implement all from ticker.go

// 3. Add GetOccurrencesBetween for recovery support
func (t *MyTicker) GetOccurrencesBetween(start, end time.Time) ([]time.Time, error) {
    // Critical: must return ALL occurrences in range
}

// 4. Test determinism and recovery
```

### Adding a Storage Backend

```go
// 1. Create storage/mydb/mydb.go
type MyDBStorage struct {
    conn *sql.DB
}

// 2. Implement ALL storage.Storage methods (35 methods!)
func (s *MyDBStorage) CreateJobOccurrence(ctx, occ) error {
    // CRITICAL: Use UNIQUE constraint on ID
    _, err := s.conn.ExecContext(ctx, 
        "INSERT INTO job_occurrences (id, ...) VALUES (?, ...)",
        occ.ID, ...)
    // Duplicate key error = expected, not failure
    return err
}

// 3. Hierarchical queries for prefix scans
func (s *MyDBStorage) ListTaskRunsByOccurrenceID(ctx, occID) {
    // WHERE id LIKE 'occ_abc%' equivalent
}
```

### Extending Config Options

```go
// 1. Add to types.go
type JobConfig struct {
    // existing fields...
    MyNewOption *MyOptionType `json:"my_new_option,omitempty"`
}

// 2. Update GetEffectiveConfig in job.go
func (j *Job) GetEffectiveConfig(task *Task) TaskConfig {
    effective := j.Config.TaskConfig
    if task.Config.MyNewOption != nil {
        effective.MyNewOption = task.Config.MyNewOption
    }
    return effective
}

// 3. Use in executor.go
config := job.GetEffectiveConfig(task)
if config.MyNewOption != nil {
    // Apply option
}
```

## Critical Anti-Patterns (DO NOT DO)

### ❌ Random IDs for Coordination Entities
```go
// WRONG - breaks distributed idempotency
occurrenceID := uuid.New().String()
```
```go
// CORRECT - deterministic from context
occurrenceID := id.GenerateJobOccurrenceID(jobID, scheduledTime)
```

### ❌ In-Memory "Lock" State
```go
// WRONG - other nodes can't see this
var jobRunning bool
if !jobRunning {
    jobRunning = true
    execute()
}
```
```go
// CORRECT - persistent query
isRunning, _ := job.IsRunning(ctx, store.ListRunningOccurrences)
if isRunning && policy == OverlapPolicySkip {
    return  // Skip
}
```

### ❌ Treating Duplicate Creation as Error
```go
// WRONG - logs noise, breaks distributed pattern
err := store.CreateJobOccurrence(ctx, occ)
if err != nil {
    log.Error("FAILED to create occurrence") // NO!
    return err
}
```
```go
// CORRECT - graceful exit when another node won
err := store.CreateJobOccurrence(ctx, occ)
if err != nil {
    // Another node won the race - this is EXPECTED
    return  // Silent exit, no error propagation
}
```

### ❌ Execution Timeout = Schedule Frequency
```go
// WRONG - couples unrelated concerns
interval := ticker.GetInterval()
ctx, _ := context.WithTimeout(ctx, interval)
```
```go
// CORRECT - explicit, independent timeout
ctx, _ := context.WithTimeout(ctx, job.Config.ExecutionTimeout)
```

### ❌ Overwriting Execution Attempts
```go
// WRONG - loses retry history
attempt.Status = "Failed"
store.UpdateExecutionAttempt(ctx, attempt)
// Next retry overwrites same record
```
```go
// CORRECT - new attempt per try
for attemptNum := 0; attemptNum <= maxRetries; attemptNum++ {
    attemptID := id.GenerateExecutionAttemptID(runID, attemptNum)
    attempt := &ExecutionAttempt{ID: attemptID, ...}
    store.CreateExecutionAttempt(ctx, attempt)
}
```

## Quick Reference Card

**When coordinating across nodes**: Use deterministic IDs + atomic create (race-to-win)  
**When handling task failure**: Check RetryPolicy first, then FailureStrategy  
**When adding config field**: Use pointer type for optional override  
**When implementing Storage**: Rejection of duplicate keys MUST be supported  
**When testing concurrency**: Always run with `-race` flag  
**When debugging "stuck" job**: Check reaper interval and ExecutionTimeout  

**Most important files to understand**:
1. `id/id.go` - How IDs enable coordination
2. `scheduler/scheduler.go:handleJobTrigger()` - The race-to-create pattern
3. `executor/executor.go:executeParallel()` - Fail-fast coordination
4. `storage/storage.go` - The storage contract

**See `README.md` for complete technical specification and rationale.**
