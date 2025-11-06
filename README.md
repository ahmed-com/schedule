# Jobistemer Scheduling System – Technical Design Specification

## Introduction: From Isolated Fixes to Integrated Design

Jobistemer is an internal Go library for scheduling and executing jobs composed of multiple tasks. It provides persistent, fault-tolerant execution of recurring jobs, ensuring reliability even during failures or downtime. The design is **backend-agnostic** – while an example implementation uses BadgerDB (a key-value store) for persistence, the system is built to support pluggable storage layers.

This specification presents an enhanced architectural design that addresses three distinct, high-stakes challenges common to all non-trivial scheduling environments:

1. **Job Overlap**: The condition where a job's execution outlasts its scheduled interval, risking a "thundering herd" of self-overlapping runs.
2. **Distributed Locking**: The coordination problem in a multi-node environment, where multiple schedulers must be prevented from executing the same job occurrence simultaneously.
3. **Parallel Task Execution**: The need for a single job to "fan-out" multiple independent tasks and manage their collective success or failure as a single unit.

The proposed solutions—such as `context.WithDeadline`, Time-to-Live (TTL) leases, and `sync.WaitGroup` with `context.WithCancel`—are sound Go-native patterns. However, this design goes beyond isolated fixes by adopting a **foundation-focused architecture** that solves these problems as a natural and integrated consequence of its core model.

**Central Thesis**: A robust system built on principles of **Deterministic Idempotency** and **Atomic, Transactional Persistence** provides a unified and far more resilient solution. The problems of "Job Overlap" and "Distributed Locking" are symptoms of a single, deeper challenge: the absence of a reliable, distributed "source of truth" for a job's state. This design moves the system's state from ephemeral, in-memory flags or short-lived lease keys into a permanent, transactional, and auditable database record.

This specification outlines the system's architecture using **"Job"** to denote a scheduled job and **"Task"** to denote an individual unit of work. We describe the hierarchical execution flow, configuration management, persistence, failure handling, schedule updates, recovery mechanisms, and critical considerations like time zones and Go 1.22+ compatibility.

## Job Hierarchy and Execution Flow: The Foundation for Control

The prerequisite for solving concurrency and execution problems is a clear, hierarchical data model. A flat "job-and-run" model is insufficient. The Jobistemer architecture provides the necessary "nouns" that form the basis of all control and observability.

### Defining the System's "Nouns"

The scheduling system organizes work in a hierarchical model from high-level schedules down to individual execution attempts:

* **Job:** A top-level scheduled job definition that includes a recurrence rule (cron expression, interval, etc.) and job-level configuration. A Job corresponds to a recurring schedule (e.g. "daily at 5 AM") and serves as a container for one or more tasks to perform at each trigger time. Each Job has a **unique, deterministic ID** and can be enabled/disabled or paused as needed. Different jobs may share the same schedule pattern, and all active jobs run independently in parallel.

* **Task:** A specific unit of work or step within a Job. Each Job can contain multiple Tasks that should execute on each occurrence of the Job's schedule. Tasks within the same job run according to the Job's **TaskExecutionMode** (sequential by default, or parallel if configured). Each Task runs in an isolated execution context (e.g. a separate database transaction), so tasks do not interfere with each other's data operations.

* **Job Occurrence & Execution Group:** Each time a Job's schedule triggers, it creates a new **job execution occurrence**. This is the critical intermediate entity, representing a single trigger of the Job's schedule at a specific time. All Tasks of that Job are scheduled to run for that occurrence. The Job Occurrence acts as the **"logical transaction"** or **"execution group"** for coordinated handling of all tasks. It provides the precise entity to which a group-level `context.WithCancel` and a shared `FailureStrategy` can be attached—it is the "parent" that owns the `sync.WaitGroup` and manages the "abort all" signal. **Each Job Occurrence has a deterministically generated ID** derived from stable contextual information (e.g., `UUIDv5(JobID, ScheduledTimestamp)`), ensuring idempotent creation across distributed nodes.

* **Task Run and Execution Attempt:** When a scheduled time arrives (or a missed time is being caught up), the system creates a **run record** for each Task to track its execution. Each Task run may have multiple **Execution Attempts** if retries are needed. An **Execution Attempt** represents an individual try at running a Task's logic. The first attempt is the initial run, and if the Task fails and a retry policy is in place, subsequent attempts are logged as new Execution Attempts. The run record for a Task aggregates all attempts (e.g. counting how many tries, capturing the error of the final failure if any). Each attempt has its own start/end timestamps and status (success, failure, etc.), and tasks ultimately report a final status after either succeeding or exhausting retries. **Execution Attempt IDs are also deterministic**, based on the Task Run ID and attempt number.

### How the Hierarchy Solves Core Problems

This explicit hierarchy is the single most important architectural decision:

- **Parallel Execution (Problem 3)**: Without the Job Occurrence concept, managing "fan-out" parallel execution becomes architecturally unwieldy. The Job Occurrence provides the precise entity for group-level context and failure coordination.

- **Observability**: The distinction between Task Run and its child Execution Attempts decouples execution from retry logic, enabling superior observability. The system preserves a full audit trail instead of overwriting a single record on each retry. This allows comprehensive Execution Reports (e.g., "Task 'B' failed after 5 attempts with backoffs of 30s, 1m, 2m, 4m, 8m, causing the Fail-Fast strategy to trigger and cancel 8 other in-flight tasks").

- **Distributed Coordination (Problem 2)**: The deterministic Job Occurrence ID is the foundation for lock-free distributed coordination (detailed in Section IV).

### Execution Flow

At runtime, the scheduler triggers Jobs according to their schedules if they are active and not paused. For each triggered Job occurrence:

1. The system attempts to create a Job Occurrence record with a deterministically calculated ID
2. If successful (won the distributed "race"), it prepares an execution context with timing and persistence transaction
3. Tasks are invoked according to the Job's **TaskExecutionMode** (sequential or parallel)
4. Different Jobs run concurrently with no predetermined ordering between them
5. Within a single Job, task behavior depends on the **FailureStrategy**:
   - **Fail-Fast**: Abort remaining tasks on first failure
   - **Continue**: Run all tasks regardless of intermediate failures
6. Once all tasks finish (or as allowed by strategy), the Job's **OnComplete** hook is invoked with an **Execution Report** containing:
   - Failure strategy used
   - Each task's context, number of attempts, and final status
   - Group-level success/failure outcome
7. An optional **OnStart** hook can run before tasks for initialization or logging

## Scheduling Mechanism and Ticker Interface

Each Job is associated with a schedule that determines when it should run. Schedules are implemented via a pluggable **Ticker** interface, allowing different scheduling strategies (cron expressions, fixed intervals, one-time schedules, etc.). The library provides built-in Ticker implementations: **Cron** (for standard cron/tab schedules), **Iso8601** (for ISO8601 repeating interval definitions), and **Once** (for one-off or ad-hoc scheduling). Developers can also create custom Ticker implementations as needed, as long as they provide the required interface.

The Ticker interface exposes a read-only channel that emits an "execution context" at each tick, which signals the scheduler to execute the Job at that time. The execution context object includes information like the scheduled runtime, the actual current time, an optional last run time, and a transaction handle for database operations during task execution. This context is passed to tasks so they can know timing information and perform any needed DB work within the same transaction scope.

The Ticker interface also provides control methods to manage the schedule's state:

* **Start()**: Begin the schedule immediately (resetting or starting the ticker). For cron-based schedules, calling Start will initiate the cron schedule from now (note that the first actual task execution may still be at the next scheduled time, since Start just begins the ticking process).

* **Stop()**: Halt the schedule's ticker entirely. No further occurrences will be generated until it is started again. Used during graceful shutdown to prevent new work.

* **Pause()**: Temporarily pause triggering of new jobs without disabling the schedule. When paused, the internal ticker continues to compute upcoming times but the Job executions are held off. Any occurrences that would have run while paused are marked as skipped in history rather than executed. This allows the schedule to resume later without losing its place or recalculating next run times.

* **Resume()**: Resume a paused schedule, allowing it to continue executing Jobs from the next scheduled tick.

* Status checks like **IsActive()** (whether current time is within the schedule's start–end window), **IsPaused()** (if it's manually paused), **IsEnabled()** (active and not paused), **IsRunning()** (if an instance of that Job is currently in progress - **critical: this is a persistent query, not an in-memory flag**), and **NextRun()** (when the next execution will occur, if scheduled).

**Cron Schedules:** Cron-based Jobs use a standard cron expression (with fields for minute, hour, day of month, month, day of week) plus optional start and end times to delimit the active period. **Important:** the startTime in a cron schedule defines when the schedule becomes active, not necessarily the exact time of the first job execution. For example, a cron job configured to run "every day at 5 AM" with a startTime of today at 2 PM will still wait until 5 AM tomorrow for the first run – the ticker starts at 2 PM but the first matching cron occurrence is next day. The scheduler interprets cron expressions with full support for typical cron syntax and will skip or include occurrences appropriately (taking into account time zones and DST as described later).

**ISO8601 Schedules:** ISO8601-based schedules allow defining intervals in calendar units – e.g., "every 3 weeks" or "P1Y2M10D" etc. – optionally with a fixed start date and a set number of repetitions. This provides a way to schedule jobs at uniform intervals that may not align to the cron field-based model. The ticker for an ISO8601 schedule computes the next occurrence by adding the interval to the last run time (or start time), handling complex rules like month and year boundaries.

**One-Time Schedules:** The Once ticker simply triggers the job a single time at a specified moment. After it fires, the schedule can be considered complete or inactive unless rescheduled.

## Data Persistence and Deterministic IDs: The Foundation of Distributed Coordination

All scheduling metadata and runtime state are persisted in a durable store to support crash recovery and coordination across processes. The design abstracts the storage layer so that different backends can be plugged in (e.g., BadgerDB or other key-value stores, relational databases, etc.). The persistence layer must support basic CRUD operations, efficient retrieval of records by key or key prefix, and **atomic "create-if-not-exists" operations** (UNIQUE constraints in relational DBs, transactional create semantics in key-value stores).

### Hierarchical Key Schema

In a key-value store implementation, one effective strategy is to use a **hierarchical key schema** reflecting the Job/Task structure:

```
job/<JobID>
job/<JobID>/task/<TaskID>
job/<JobID>/occurrence/<OccurID>
job/<JobID>/occurrence/<OccurID>/task/<TaskID>/run
job/<JobID>/occurrence/<OccurID>/task/<TaskID>/run/attempt/<AttemptID>
```

This way, all records related to a given job or task share a common prefix, enabling fast lookup via prefix scans. In a relational model, equivalent functionality is achieved by foreign keys and indexed queries. Whole subtrees of data can be inserted or deleted in one transaction to maintain referential integrity.

### Deterministic ID Generation: The Lynchpin of Idempotency

**This is the lynchpin of the entire distributed coordination design.** Every entity in the hierarchy (Jobs, Tasks, scheduled occurrences, attempts, etc.) is assigned an ID in a **deterministic manner** rather than using auto-incrementing sequences or purely random UUIDs. The ID is derived from contextual information, ensuring that the same logical event always produces the same ID.

**Implementation Pattern**:
- **Job Occurrence ID**: `UUIDv5(JobID, ScheduledTimestamp)` or `hash(JobID + ScheduledTimestamp)`
- **Task Run ID**: `UUIDv5(OccurrenceID, TaskID)`
- **Execution Attempt ID**: `UUIDv5(TaskRunID, AttemptNumber)`

**Critical Benefits**:

1. **Idempotency**: If scheduling logic runs twice for the same time window, it will compute identical IDs and thus avoid duplicate entries. The database's atomic create-if-not-exists will reject duplicates.

2. **Lock-Free Distributed Coordination**: Multiple nodes can independently calculate schedule occurrences without coordination and still end up referring to the same records. This is the foundation for solving Problem 2 (distributed locking) without TTL-based leases.

3. **Safe Recovery**: If a generation step crashes and is later retried, it will attempt to create the same keys, which the database will recognize as already existing, preventing double-creation.

4. **Transactional Updates**: Schedule updates can be applied as atomic diff operations (additions and deletions) using these deterministic IDs.

### Stored Metadata

Each entity is stored with relevant metadata:

**Job Record**:
- Unique deterministic ID
- Schedule type (Cron, ISO8601, Once)
- Configuration (timezone, OverlapPolicy, TaskExecutionMode, FailureStrategy, RetryPolicy, ExecutionTimeout, RecoveryStrategy)
- Active/paused flags
- Current version (for optimistic locking)
- LastRunTime (for recovery calculations)

**Task Record**:
- Unique deterministic ID
- Parent Job ID
- Task-specific config overrides
- Order/sequence number (for sequential execution)

**Job Occurrence Record** (the "lock" for distributed coordination):
- Deterministic occurrence ID
- Job ID and version
- Scheduled timestamp
- Status (Pending, Running, Completed, Failed, Canceled, Failed_Stale)
- Owning node ID (for diagnostics)
- Start/end timestamps
- Effective configuration snapshot

**Task Run Record**:
- Deterministic run ID
- Parent occurrence ID and task ID
- Retry count
- Final status
- Start/end timestamps

**Execution Attempt Record**:
- Deterministic attempt ID
- Parent task run ID
- Attempt number
- Status, start/end timestamps, error message

The persistence model uses **diffs** at each level—each Task or Job only stores the fields it overrides from its parent, for space efficiency. Protocol Buffers or similar serialization can be used for config structures.

## Job Concurrency Control: Solving the Overlap Problem

### The OverlapPolicy Configuration

The system provides a first-class **Job.Config.OverlapPolicy** setting to declaratively handle job overlap scenarios. This setting determines what happens when a Job's schedule triggers but a previous instance is still running.

**Available Policies**:

1. **Skip (Recommended Default)**: When the Ticker fires, the scheduler queries the persistent store (`IsRunning()` check). If the query returns true (an occurrence with Status="Running" exists), the scheduler logs "Skipping overlapping run for Job X" and takes no further action. The missed occurrence is noted as skipped in history. **Critical: `IsRunning()` is a persistent query, not an in-memory flag**, ensuring correctness across distributed nodes.

2. **Allow**: If `IsRunning()` is true, the scheduler proceeds to create a new Job Occurrence with its own distinct deterministic ID and runs concurrently with the other. This policy supports multiple concurrent instances of the same job running in parallel. Generally not recommended but architecturally supported.

3. **Queue**: If `IsRunning()` is true, the scheduler creates the new Job Occurrence (with deterministic ID) but sets its initial status to "Queued". When the currently "Running" job instance finishes, its **OnComplete** hook checks the persistence layer for any "Queued" occurrences of the same Job and "wakes up" the next one in line by updating its status to "Pending" (or directly to "Running"). This provides serialized execution with queuing.

### Decoupling Execution Timeouts from Schedule Frequency

**Critical Design Principle**: The execution timeout for a job must be completely independent of its schedule frequency.

**Flawed Approach** (DO NOT USE): Setting `context.WithDeadline` to `nextRunTime` incorrectly conflates schedule frequency with execution constraints:
- A job scheduled `*/1 * * * *` (every minute) would get a 60-second deadline
- A job scheduled `@daily` would get a 24-hour deadline

This is nonsensical: a daily job processing a small file should not have a 24-hour timeout, and a 1-minute job processing complex data may need more than 60 seconds.

**Correct Design**:

1. **Job.Config.ExecutionTimeout**: An explicit duration field (e.g., "1h") completely independent of the Ticker's schedule.

2. **Job.Config.TaskTimeout**: Per-task timeout (can be overridden per-task via cascading config). This is a critical guardrail against stuck tasks.

3. **Runtime Application**:
   - When a Job Occurrence begins execution, create `context.WithDeadline` using `Job.Config.ExecutionTimeout`
   - For each Task Execution Attempt, create `context.WithTimeout` using the effective `TaskTimeout`
   - If a task exceeds its deadline, the context is canceled, the attempt is marked "Failed (Timeout)", and the RetryPolicy is triggered

4. **Dual Purpose**: The `ExecutionTimeout` serves as:
   - Primary guardrail preventing infinite job execution
   - TTL for the "reaper" dead-man's-switch (detailed in next section)

The `NextRun()` time is only used for scheduling the next tick, not for runtime execution control.

## Distributed Coordination: Eliminating TTL-Based Locking

### Critique of TTL Lease-Based Locking

A distributed "lock" implemented as a key in a database with a short TTL is ephemeral and clock-dependent, with severe failure modes:

1. **Clock Skew**: If Node A's and Node B's system clocks are not perfectly synchronized, Node B may see a lease as expired and "steal" the lock while Node A believes it's still valid.

2. **Long-Running Work**: A job correctly takes 45 seconds. TTL is 30 seconds. At 31 seconds, Node B sees the lease as expired, acquires the lock, and starts a duplicate run. **Result: Two nodes processing the same job, leading to data corruption.**

3. **GC Pauses / Network Partitions**: A node pauses longer than the TTL due to stop-the-world GC or brief network partition. The lease expires, another node takes over, and when the first node un-pauses, two nodes are actively processing the same job.

4. **Crash-on-Release**: A node finishes work successfully in 5 seconds but crashes before deleting the lease key. The job is done, but the lock remains for the full TTL (e.g., 30 seconds), blocking subsequent runs.

### Superior Model: Deterministic Idempotency (The "Race to Create")

This design uses a **lock-free, deterministic, persistent coordination model** that eliminates all TTL failure modes.

**The Four-Step Process**:

1. **Deterministic ID Generation**: All scheduler nodes, when they see a job is due at a specific time, independently calculate the **exact same unique ID** for that Job Occurrence: `UUIDv5(JobID, ScheduledTimestamp)`.

2. **Transactional "Acquisition"**: The shared persistence layer must support atomic "create-if-not-exists" operations:
   - **Relational DB**: UNIQUE constraint on ID column
   - **BadgerDB**: Create-if-not-exists transaction
   - **Other KV stores**: Atomic compare-and-set or similar primitives

3. **The Race**: At 5:00 AM, all active scheduler nodes (A, B, and C) wake up:
   - All three independently calculate the same ID: `occ_job_foo_20230101_0500`
   - All three attempt to atomically write the Job Occurrence record:
     ```sql
     BEGIN TRANSACTION;
     INSERT INTO JobOccurrences (ID, JobID, Status, ScheduledTime)
     VALUES ('occ_job_foo_20230101_0500', 'job_foo', 'Pending', '2023-01-01 05:00:00');
     COMMIT;
     ```

4. **The Winner Takes All**:
   - **Node A (Wins)**: The COMMIT succeeds. Node A knows it "won" the race. It updates the record:
     ```sql
     UPDATE JobOccurrences SET Status='Running', NodeID='NodeA', StartTime=NOW()
     WHERE ID='occ_job_foo_20230101_0500';
     ```
     Then begins executing the job's tasks.
   
   - **Nodes B and C (Lose)**: Their COMMIT fails with "Unique Key Violation" or "Key Already Exists". This is the **expected signal** that they "lost" the race. They log "Occurrence already claimed by another node" and move on.

**Benefits of This Design**:
- **No lock, no lease, no TTL**: Eliminates all clock-dependent failures
- **No clock skew**: Uses database transaction semantics, not wall-clock time
- **No renewal logic**: The Job Occurrence record *is* the lock
- **Persistent and auditable**: The lock state is the job state from moment of creation
- **Idempotent**: Multiple attempts to create the same ID are safely rejected

### The "Reaper" Dead-Man's-Switch

A background process (a "reaper" running on one or all nodes) periodically queries for stale jobs:

```sql
SELECT * FROM JobOccurrences
WHERE Status = 'Running'
AND StartTime < (NOW() - Job.Config.ExecutionTimeout)
```

**Critical Connection**: The `Job.Config.ExecutionTimeout` serves two purposes:
1. Provides the `context.WithDeadline` for the running job
2. Provides the "stale" threshold for the reaper

When the reaper finds a stale job, it atomically updates:
```sql
UPDATE JobOccurrences SET Status='Failed_Stale', Error='Execution exceeded timeout'
WHERE ID=? AND Status='Running';
```

This releases the "lock" and allows future runs (or manual Replay) to proceed. The timeout is based on the job's declared business logic, not an arbitrary network-coupled TTL.

**Fundamental Simplification**: The state of the lock is identical to the state of the job. There is no separate, ephemeral "lock" entity to manage, expire, or renew. The Job Occurrence record *is* the lock.

## Advanced Execution Patterns: Managing Parallel Tasks

### Formalizing Parallel Task Execution

The system integrates Go-native concurrency patterns (`sync.WaitGroup` + `context.WithCancel`) with declarative configuration for robust parallel task execution.

**Required Configuration Fields**:

1. **Job.TaskExecutionMode**: Enum with values:
   - `"Sequential"` (default): Tasks run in order, one after another
   - `"Parallel"`: All tasks run concurrently

2. **Job.FailureStrategy**: Enum (overridable per-task):
   - `"Fail-Fast"`: On first task failure, cancel all other in-flight tasks
   - `"Continue"`: Run all tasks regardless of failures
   - `"Retry_On_Failure"`: Pause at failed task and retry according to RetryPolicy

### Integrated Execution Flow for Parallel Jobs

When a Job configured with `TaskExecutionMode: "Parallel"` and `FailureStrategy: "Fail-Fast"` is triggered:

1. **Group Creation**: The scheduler wins the distributed race and creates the Job Occurrence record.

2. **Context and Group Initialization**:
   ```go
   jobCtx, cancel := context.WithCancel(parentCtx)
   var wg sync.WaitGroup
   defer cancel() // Ensure cleanup
   ```

3. **The Fan-Out**: For each task:
   ```go
   wg.Add(1)
   go func(t Task) {
       defer wg.Done()
       
       // Create task-specific timeout context
       taskCtx, taskCancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
       defer taskCancel()
       
       // Run the task with group context
       err := t.Run(taskCtx)
       
       // Critical: Check failure strategy
       if err != nil && job.GetEffectiveFailureStrategy(t) == "Fail-Fast" {
           // Signal all other tasks to abort
           cancel()
       }
   }(task)
   ```

4. **The Fan-In (Execution)**: Main goroutine blocks until all tasks complete:
   ```go
   wg.Wait()
   ```

5. **The Fan-In (Results) & Reporting**: After `wg.Wait()` unblocks:
   - Gather final status (Success, Failed, Canceled) from all tasks
   - Assemble comprehensive **Execution Report** with:
     - Job-level outcome
     - Each task's status, attempt count, duration
     - Failure strategy used
     - Cancellation propagation details

6. **The Completion**: Invoke `OnComplete` hook with Execution Report for logging, auditing, or alerting.

**Key Insight**: The `FailureStrategy` provides declarative logic for when to call `cancel()`. With `"Continue"` strategy, the error block would not call `cancel()`, allowing all tasks to run to completion.

## Configuration and Cascading Settings: A Framework for Flexibility

Jobs and tasks support a rich configuration model with inheritance (cascading) of settings through the hierarchy, similar to CSS styling rules.

### The "CSS-like" Inheritance Model

Configuration options can be specified at a higher level (Job) and **cascade down** to lower levels (Task, occurrence) unless explicitly overridden. A more specific level overrides a property only if it provides a value; otherwise it inherits the parent's setting.

**Example Hierarchy**:
```
Job.Config (defaults for all tasks)
  ├─ Timezone: "America/Los_Angeles"
  ├─ FailureStrategy: "Fail-Fast"
  ├─ RetryPolicy: MaxRetries(3)
  ├─ ExecutionTimeout: "30m"
  └─ TaskTimeout: "5m"
      │
      ├─ Task1.Config (inherits all from Job)
      │
      ├─ Task2.Config (overrides retry policy)
      │   └─ RetryPolicy: MaxRetries(10), BackoffFactor(2.0)
      │
      └─ Task3.Config (overrides failure strategy)
          └─ FailureStrategy: "Continue"
```

### Implementation Pattern

Configuration structures use "unset" vs "set" values:
- **Go**: Pointers (`*int`) or nil values indicate unset
- **Protobuf**: Optional fields or oneof wrappers

When computing **effective configuration** for a task execution:
1. Start with Job's base config
2. Apply Job-level overrides
3. Apply Task-level overrides
4. Arrive at final parameters

**Storage Optimization**: Store only the **diffs** (overrides) at each level. Recombine during lookup to get complete settings.

### Practical Use Case: Complex Heterogeneous Workflow

Consider a 10-step parallel ETL job:

**Job.Config** (defaults):
```json
{
  "TaskExecutionMode": "Parallel",
  "FailureStrategy": "Fail-Fast",
  "RetryPolicy": {"MaxRetries": 3, "RetryInterval": "30s"}
}
```

**Problem 1**: Task 3 calls an unstable third-party API—needs aggressive retries.

**Solution**: Override at task level:
```json
{
  "Task3.Config.RetryPolicy": {
    "MaxRetries": 10,
    "RetryInterval": "1m",
    "BackoffFactor": 2.0
  }
}
```

**Problem 2**: Task 10 is cleanup (close connections, delete temp files)—must run even if Task 7 failed.

**Solution**: Override failure strategy:
```json
{
  "Task10.Config.FailureStrategy": "Continue"
}
```

This task will ignore the `cancel()` signal from failing peers.

### Benefits

**Without cascading config**, developers are forced into anti-patterns:
1. **Job Sprawl**: Creating 10 separate Jobs just to give tasks different policies
2. **Brittle Logic**: Hardcoding retry/failure logic in application code, making execution behavior opaque

**With cascading config**: Logical grouping (Job) can have heterogeneous execution policies—essential for real-world systems.

## Failure Handling and Retry Policies: Clarifying the Distinction

### RetryPolicy vs. FailureStrategy

These are distinct, complementary mechanisms:

1. **RetryPolicy** (Task-level):
   - Manages a single Task's Execution Attempts
   - Defines: `MaxRetries`, `RetryInterval`, `BackoffFactor`
   - Answers: "This task just failed, should I try it again?"
   - Tracked via retry count in task's runtime status

2. **FailureStrategy** (Job/Task-level):
   - Manages Job Occurrence behavior after a task permanently fails (exhausted retries)
   - Defines: How the job proceeds when faced with task failures
   - Answers: "This task is confirmed as permanently failed. Should I Fail-Fast and abort the group, or Continue?"

**Example Flow**:
- Task fails on attempt 1
- RetryPolicy triggers: Wait 30s, retry (attempt 2)
- Task fails on attempt 2
- RetryPolicy triggers: Wait 1m (backoff 2x), retry (attempt 3)
- Task fails on attempt 3
- RetryPolicy exhausted (MaxRetries=3)
- **Now** FailureStrategy takes over:
  - If "Fail-Fast": Cancel all other tasks, mark job failed
  - If "Continue": Allow other tasks to proceed, mark this task failed

### Failure Strategy Options

1. **Fail-Fast**: The moment a task fails (after exhausting retries), the job's execution for that occurrence stops. No further tasks will run. The job is marked failed immediately.

2. **Continue (Ignore Failure)**: The scheduler continues to execute all subsequent tasks even if one or more tasks failed. The job completes a full run (marked "completed with errors").

3. **Retry_On_Failure**: On failure, the task will be retried according to RetryPolicy before considering it truly failed. The job pauses at the failed task rather than immediately moving on or aborting.

**Regardless of strategy**, the system always calls the Job's `OnComplete` hook at the end of an execution occurrence, providing a full Execution Report indicating which tasks succeeded or failed.

### Retry Policy Configuration

```go
type RetryPolicy struct {
    MaxRetries      int           // Maximum retry attempts beyond initial run
    RetryInterval   time.Duration // Base wait time between retries
    BackoffFactor   float64       // Multiplier for exponential backoff (1.0 = constant)
}
```

**Example**: `MaxRetries=3`, `RetryInterval=1m`, `BackoffFactor=2.0`
- Initial attempt fails
- Wait 1 minute → Retry 1
- Retry 1 fails → Wait 2 minutes → Retry 2
- Retry 2 fails → Wait 4 minutes → Retry 3
- Retry 3 fails → Task permanently failed, trigger FailureStrategy

The scheduler tracks attempts in the task's runtime status. Each Execution Attempt record captures one try, and the Execution Report notes the number of runs for each task.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
- "At most the last N occurrences" (e.g., N=100)
- "At most the last X time duration" (e.g., X=24 hours)

**Use Case**: High-frequency, important jobs. Balances completeness of "Full Backfill" against "Thundering Herd" risk.

**Example**: Hourly job was down for 200 hours with `BoundedWindow=24h`:
- Would have 200 missed runs
- Bound limits to last 24 hours = 24 runs
- Skips oldest 176 runs, executes most recent 24

**Analogy**: Similar to Kubernetes CronJob's `startingDeadlineSeconds` (stops catching up if >100 intervals missed).

**Trade-off**: Requires careful tuning of bound (24h vs 72h) to balance completeness against load.

#### 5. Compressed Catch-Up (Advanced)

**Description**: Multiple missed intervals processed by a single run, if the job's logic supports it.

**Use Case**: Jobs whose task code can handle a range of input (e.g., process last 5 hours in one execution).

**Example**: Instead of 5 separate hourly jobs, invoke one special run that processes the span of 5 hours.

**Requirements**:
- Task code must be aware of possibility
- Scheduler provides tasks with context indicating time span to cover
- Opted-in per job (not default)

**Benefit**: Reduces execution overhead while maintaining data completeness.

**Complexity**: Requires more sophisticated task implementation.

### Idempotent Recovery

All catch-up executions are enqueued using deterministic IDs. If the recovery process crashes and restarts:
- Re-running recovery is safe
- Same IDs will be computed
- Database will reject duplicate creations
- No double-processing of missed occurrences

Each missed Task instance is flagged (e.g., `IsRecoveryRun=true`) for monitoring and diagnostics.

### Manual Replay Functionality

In addition to automatic OnBoot recovery, the system provides manual **Replay** for operators:

```go
func Replay(jobOccurrenceID string, ctx *ExecutionContext) error
```

**Purpose**: Re-run specific jobs or tasks on demand (e.g., after fixing a bug or external service outage).

**Use Cases**:
- Job failed yesterday due to external service outage → replay after service restored
- Operator wants to verify job behavior → replay for testing
- Data correction needed → replay to reprocess

**Behavior**:
- Treats replay as a new occurrence (distinct from scheduled runs)
- Links to original job ID
- Records as "manual replay" in history
- Subject to same concurrency controls (won't run if job currently running, or may queue)
- Obeys same task sequence and failure strategy

**Example**:
```bash
./zp replay --job-occurrence occ_job_foo_20230101_0500 --reason "Retry after API fix"
```

This feature complements automatic recovery by providing a safety net for one-off re-executions.

## Production-Grade Operational Concerns

### Global Concurrency Control: Throttling the Thundering Herd

**Problem**: Without a global concurrency limit, the design is vulnerable to "thundering herd" scenarios:

1. **Scheduled Herd**: At common times (midnight, top of hour), dozens of jobs trigger simultaneously
2. **Recovery Herd**: "Execute All (Full Backfill)" after long downtime can overwhelm the system

**Solution**: Implement a **global, configurable worker pool**:

```go
type SchedulerConfig struct {
    MaxConcurrentJobs int // e.g., 50
}
```

**Implementation**:
- Every Job Occurrence ready to run (whether from live tick or OnBoot recovery) must acquire a "slot" from this pool before execution
- Job Occurrences wait in a queue if all slots are occupied
- Upon completion, the slot is released and next queued occurrence can proceed

**Benefits**:
- Central, system-wide safety valve ensuring stability
- Prevents overwhelming scheduler host, persistence layer, and downstream systems
- Works in conjunction with RecoveryStrategy (e.g., Bounded Window limits count, worker pool limits concurrency)

**Observability**: Expose metrics for queue depth (see next section).

### Real-Time Observability: Metrics and Monitoring

The design provides excellent post-hoc auditing via Execution Reports and OnComplete hooks. However, production systems require **real-time observability**.

**Problem**: Discrete status checks (`IsRunning()`, `IsPaused()`, `NextRun()`) are point-in-time queries. Operators cannot distinguish between:
- Healthy long-running job
- Job stuck in retry-loop
- Job waiting for slot in global concurrency queue

**Solution**: Instrument the system to expose **continuous metrics** (e.g., via Prometheus):

**Recommended Metrics**:

1. **Gauges** (current state):
   ```
   scheduler_jobs_running_total
   scheduler_jobs_in_queue_total
   scheduler_jobs_paused_total
   ```

2. **Histograms** (duration tracking):
   ```
   scheduler_job_execution_duration_seconds{job="foo"}
   scheduler_task_execution_duration_seconds{job="foo",task="bar"}
   ```

3. **Counters** (event tracking):
   ```
   scheduler_task_attempts_total{job="foo",task="bar",status="success|failure|timeout"}
   scheduler_job_occurrences_total{job="foo",status="completed|failed|canceled|skipped"}
   scheduler_recovery_runs_total{job="foo",strategy="full_backfill|latest_only|skip"}
   ```

**Integration Points**:
- Update gauges when jobs transition states
- Record histogram observations on job/task completion
- Increment counters on state changes and execution results

**Complement, Don't Replace**: Metrics complement the Execution Report system. Reports provide detailed audit trails; metrics provide real-time dashboards and alerting.

### Graceful Shutdown: Protecting In-Flight Work

**Problem**: If the scheduler service terminates immediately on shutdown (SIGTERM during deployment), all in-flight jobs are left in "Running" state, forcing reliance on OnBoot recovery and creating downtime.

**Solution**: Implement a **graceful shutdown procedure**:

1. **Stop New Work**: Call `Stop()` on all Tickers to prevent new job occurrences from being scheduled

2. **Signal Cancellation**: Signal cancellation to contexts of all in-flight Job Occurrences (propagates to all tasks)

3. **Grace Period**: Wait for a fixed grace period (e.g., 30 seconds) to allow running tasks to:
   - Respect the cancellation signal
   - Clean up resources
   - Terminate cleanly
   - Update their status to "Canceled" or "Completed"

4. **Forced Termination**: After grace period expires, forcefully exit the service

**Implementation Pattern**:
```go
func (s *Scheduler) Shutdown(ctx context.Context) error {
    // Stop all tickers
    for _, job := range s.jobs {
        job.Ticker.Stop()
    }
    
    // Cancel all in-flight work
    s.cancelAllJobs()
    
    // Wait for grace period or context cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-s.allJobsComplete():
        return nil
    }
}
```

**Benefits**:
- Most jobs terminate cleanly
- Minimizes reliance on OnBoot recovery
- Smoother experience during deployments
- Reduces "stale" job occurrences

**Fallback**: Jobs that don't complete within grace period will be cleaned up by the Reaper dead-man's-switch on next service start.

### Stuck Task Protection: Per-Task Execution Timeouts

**Gap**: The design specifies RetryPolicy (what to do after failure) and FailureStrategy (what to do when task permanently fails), but doesn't explicitly define per-task execution timeout.

**Problem**: A task could become "stuck":
- Waiting indefinitely on network resource
- Infinite loop
- Deadlock

**Consequence**: Stuck task holds its Execution Attempt open → parent Job Occurrence stays "Running" indefinitely → effectively a permanent lock, preventing all future runs (even with OverlapPolicy="Skip").

**Solution**: Add **Job.Config.TaskTimeout** (overridable per-task via cascading config):

```go
type TaskConfig struct {
    TaskTimeout time.Duration // e.g., "5m"
}
```

**Implementation**: Create `context.WithTimeout` for every Execution Attempt:

```go
taskCtx, cancel := context.WithTimeout(jobCtx, task.GetEffectiveConfig().TaskTimeout)
defer cancel()

err := task.Run(taskCtx)
if ctx.Err() == context.DeadlineExceeded {
    // Mark attempt as "Failed (Timeout)"
    // Trigger RetryPolicy
}
```

**Benefit**: Critical guardrail against buggy or non-responsive task code, preventing permanent job lockup.

## Updating Jobs and Schedules: Safe Maintenance

It is often necessary to update a Job's schedule or tasks – for example, changing a cron expression, adding/removing a task, or modifying configuration. The system is designed to handle updates safely and consistently through a combination of **versioning**, **update policies**, and a **diffing algorithm** for scheduled occurrences.

**Versioning:** Each Job definition carries a version number that increments on each update. This version is used for optimistic locking (to prevent conflicting updates) and to let running tasks know which version of the job they belong to. If two updates occur simultaneously, the one with the higher new version will take precedence, and any stale update attempt (lower version) will be rejected to avoid inconsistency. Versioning also provides an audit trail – if desired, old versions of the schedule could be kept for history or rollback (though in practice we may or may not store historical versions). Importantly, versioning allows the system to manage in-flight executions: a job occurrence that started under version 1 can continue running with that configuration, while new occurrences (after the update) use version 2\. This avoids mid-run changes to a job's behavior.

**Update Policies:** When a Job's schedule pattern is changed, we need to decide how to handle any already scheduled future runs under the old schedule. The system supports multiple update policies:

* **Immediate Recompute:** As soon as the Job is updated, the system will recalculate all future occurrences from "now" onward using the new schedule rules, completely replacing the old schedule. Any previously scheduled occurrences that have not run yet will be either removed or adjusted according to the new schedule. This approach makes the new definition take effect immediately, but it might cancel or alter runs that were planned under the old schedule (which could be acceptable or not depending on context).

* **Graceful (Apply Next Cycle):** The new schedule is prepared but not applied until the next scheduling cycle or a specified time. Essentially, allow the current set of scheduled occurrences to complete (or a current period to finish) under the old schedule, and only then switch to the new schedule. This avoids interrupting an in-progress sequence or cutting off already queued jobs. For example, if a job is scheduled daily and you switch to a new time, a graceful update might mean the next day's run still happens at the old time, and from the day after it uses the new time.

* **Windowed Update:** Recompute only a limited future horizon of occurrences (e.g., the next N occurrences or next X days) rather than all future instances. This can prevent thrashing or extensive recalculation if schedules change frequently. For example, you might only materialize the next 30 days of a daily schedule; if an update comes in, you only diff within that 30-day window and beyond that window the old instances might not even be generated yet.

The chosen policy affects how the system performs the diff and cleanup of existing schedule entries. The policy can be configured globally or per update operation. In all cases, **consistency** is maintained by using the versioning and diff approach below.

**Diffing Strategy:** To apply a schedule change without duplicating or losing occurrences, the system computes the difference between the old schedule's occurrences and the new schedule's occurrences (within an appropriate window/horizon). Conceptually, the update procedure is:

1. Generate the set of upcoming occurrence timestamps for the old schedule (from now or the update moment forward, up to a certain horizon).

2. Generate the set of occurrence timestamps for the new schedule over the same range.

3. Compare the two sets: identify which timestamps are **in the new set but not in the old**, and which are **in the old but not in the new**.

4. For each added timestamp (new only), create the corresponding Task instances. That means for each Task under the Job, insert a new scheduled occurrence at that timestamp (with a deterministically generated ID) if it doesn't already exist. These new instances will typically be in a "pending" state, ready to run at the designated time (or immediately if the time is now).

5. For each removed timestamp (old only, no longer scheduled), find the corresponding Task instances and mark them as canceled or delete them if they have not run yet. If a run was already scheduled (e.g. a pending run record), that record is updated to a canceled status or removed from the store. (If an occurrence was partially executed or in progress at the time of update, the policy might dictate letting it finish – typically, the diff focuses on future *not-yet-started* occurrences).

6. If a timestamp exists in both old and new sets, generally it means the occurrence remains the same. However, if the job's tasks or configurations changed, those existing occurrence records might need to be updated in place to reflect the new definition. For minor config changes, updating the stored config for future instances may suffice. If the schedule rule change caused an occurrence to shift slightly in time (e.g., daylight savings or cron expression modification resulting in a one-time offset), the system might handle it by treating it as a cancellation of the old time and an addition of the new time. In some cases, the identity of an occurrence can be preserved (for instance, if IDs were based on a sequence number rather than timestamp, one could update the timestamp on an existing record). But generally, it's safer to cancel and re-add to avoid confusion.

Using deterministic IDs in this process makes it **idempotent**. If for any reason the update process is interrupted or needs to be re-run, the same additions and deletions will be computed, and applying them again will have no adverse effect: the "add" operations will attempt to insert keys that already exist (which the database can ignore or update harmlessly), and the "remove" operations will attempt to delete keys that are already gone or marked, resulting in no change. This means a repeated update won't create duplicates or resurrect canceled instances. To further ensure atomicity, the diff application (the batch of adds and cancels) is done within a single transaction whenever the backend supports it. For example, in BadgerDB we use a single transaction to write all new keys and remove all obsolete keys, so the schedule update is all-or-nothing – either all changes commit or none do. This keeps the store consistent even if a failure occurs during the update.

**Updating Tasks within a Job:** In addition to changes in the schedule timing, the system handles modifications to the set of Tasks or their configurations:

* *Adding a Task:* If a new Task is added to a Job, the scheduler will create new Task instances for all future scheduled occurrences of the Job (from now onward). This is essentially another diff scenario: previously, those future timestamps had no instance of the new task, and now they should. The system iterates over upcoming occurrence times and inserts a Task occurrence for the new task at each time. This ensures the new task will execute on all future job runs.

* *Removing a Task:* If a Task is removed (or retired), the system will cancel or delete all its future scheduled instances while leaving other tasks of the Job intact. Any run records for that task that haven't executed yet are marked canceled or purged. Past records can be retained for history, often with a flag indicating the task is retired.

* *Updating Task Configuration:* If a Task's logic or configuration changes (but the Job's schedule timing remains the same), we usually do **not** need to alter the scheduled occurrence times. Instead, we update the stored configuration for that Task (and possibly bump the Job's version). All future Task runs will pick up the new config when they execute. If we cache "effective config" in each scheduled occurrence record, those might need to be updated to match the new settings. Often, it's sufficient to store the new config on the Task definition and have the execution logic always refer to the latest config at runtime (unless a specific versioning of config per run is desired).

All these changes (adding/removing tasks, changing task configs) are performed using batched, transactional updates similar to schedule changes. Thanks to deterministic keys, if an update operation is partially applied (e.g., the system crashes after adding some task instances), rerunning the operation is safe – it will attempt to add the remaining instances and skip those already added. This idempotence and use of transactions greatly simplify error handling for updates.

## Recovery and Replay Mechanisms: Proactive Resilience

### The Critical "OnBoot" Recovery

**Most significant gap in naive designs**: Lack of systemic failure handling for scheduler downtime. A production-grade system must assume failure and have a built-in recovery plan.

**OnBoot Recovery Flow**:

When the scheduler service starts:

1. **Load Jobs**: For each Job, load its configuration and metadata.

2. **Calculate Missed Window**:
   ```go
   lastRunTime := job.GetLastRunTime() // Or last heartbeat before shutdown
   missedOccurrences := job.Ticker.GetOccurrencesBetween(lastRunTime, time.Now())
   ```

3. **Apply RecoveryStrategy**: For each Job, apply its configured `Job.Config.RecoveryStrategy` to the list of missed occurrences.

4. **Enqueue Recovery Runs**: Based on the strategy, enqueue catch-up executions using the same deterministic ID generation (ensuring idempotency if recovery crashes).

5. **Resume Normal Operation**: Once caught up, jobs continue with next scheduled times.

### RecoveryStrategy Options

The RecoveryStrategy is a declarative Job setting that dictates catch-up behavior after downtime.

#### 1. Execute All (Full Backfill)

**Description**: Runs *all* missed occurrences in chronological order.

**Use Case**: Financial transactions; jobs where every interval represents unique, non-replaceable data (e.g., hourly aggregation).

**Example**: Job scheduled hourly was down for 5 hours → triggers 5 executions in quick succession.

**Risk**: **Thundering Herd** – can overwhelm the system, database, and external APIs after long downtime.

**Recommendation**: Use with global concurrency control (worker pool) to prevent overload.

#### 2. Execute Last (Latest-Only)

**Description**: Only runs the most recent missed occurrence. Skips all earlier ones.

**Use Case**: "State-Sync" jobs; any job that only cares about the latest data (e.g., "sync user profiles", "update cache").

**Example**: Job scheduled hourly was down for 5 hours → triggers 1 execution for the latest hour.

**Trade-off**: **Data Loss (by design)** – intermediate occurrences are never processed. Not suitable for transactional data.

**Benefit**: Quickly resumes normal schedule without backlog.

#### 3. Mark as Missed (Skip)

**Description**: Executes *none* of the missed occurrences. Logs them as "missed" and waits for next scheduled future time.

**Use Case**: Non-critical jobs; routine report generation where an old, delayed report has no value.

**Example**: Daily report job was down for 3 days → logs 3 missed runs, resumes with tomorrow's report.

**Trade-off**: **Data Loss (by design)**: guarantees no catch-up work.

**Benefit**: No system load from recovery.

#### 4. Bounded Catch-Up Window

**Description**: The pragmatic compromise. Runs missed occurrences like "Full Backfill" but with a safety limit.

**Configuration Options**:
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
