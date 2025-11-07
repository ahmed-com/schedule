# Jobistemer - Go Job Scheduling Library

A production-ready Go library for scheduling and executing jobs with persistent, fault-tolerant execution. Built on principles of deterministic idempotency and atomic, transactional persistence.

## Implementation Status

### ✅ Completed Phases (1-3)

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

### 🚧 Remaining Phases (4-10)

The following phases are outlined in the technical specification but not yet implemented:

- **Phase 4:** Job and Task Definitions
- **Phase 5:** Execution Engine
- **Phase 6:** Main Scheduler
- **Phase 7:** Recovery System
- **Phase 8:** Concurrency Control
- **Phase 9:** Observability & Metrics
- **Phase 10:** Update and Versioning

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
