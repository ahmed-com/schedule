# GitHub Copilot Instructions for Jobistemer Scheduling System

## Project Overview

Jobistemer is a Go library for scheduling and executing jobs composed of multiple tasks. It provides persistent, fault-tolerant execution of recurring jobs with the following key characteristics:

- **Backend-agnostic design**: Supports pluggable storage layers (example implementation uses BadgerDB)
- **Distributed coordination**: Lock-free coordination using deterministic IDs
- **Fault-tolerant execution**: Persistent state with atomic transactional operations
- **Hierarchical job model**: Jobs → Tasks → Job Occurrences → Task Runs → Execution Attempts

### Core Design Principles

1. **Deterministic Idempotency**: All entities use deterministically generated IDs (e.g., UUIDv5) to ensure idempotent operations across distributed nodes
2. **Atomic, Transactional Persistence**: State is moved from ephemeral in-memory flags to permanent, transactional database records
3. **Hierarchical Data Model**: Clear separation between Jobs, Tasks, Job Occurrences, Task Runs, and Execution Attempts

## Architecture Guidelines

### Key Problems Addressed

1. **Job Overlap**: Prevention of self-overlapping job runs using persistent state tracking
2. **Distributed Locking**: Lock-free coordination via deterministic Job Occurrence IDs
3. **Parallel Task Execution**: Fan-out execution with coordinated failure handling using `sync.WaitGroup` and `context.WithCancel`

### Entity Hierarchy

When implementing code, respect this hierarchy:

```
Job (recurring schedule definition)
  └── Task (unit of work within a job)
       └── Job Occurrence (single trigger of job's schedule)
            └── Task Run (execution record for a task)
                 └── Execution Attempt (individual try, including retries)
```

### Deterministic ID Generation

- **Job Occurrence ID**: `UUIDv5(JobID, ScheduledTimestamp)`
- **Execution Attempt ID**: Based on Task Run ID and attempt number
- Always use deterministic ID generation for distributed coordination

### Storage Layer

- Implement backend-agnostic persistence using interface abstractions
- Support atomic "create-if-not-exists" operations (UNIQUE constraints)
- Use hierarchical key schema for key-value stores:
  ```
  job/<JobID>
  job/<JobID>/task/<TaskID>
  job/<JobID>/occurrence/<OccurID>
  job/<JobID>/occurrence/<OccurID>/task/<TaskID>/run
  job/<JobID>/occurrence/<OccurID>/task/<TaskID>/run/attempt/<AttemptID>
  ```

## Go Coding Standards

### General Conventions

- Follow standard Go formatting (use `gofmt` or `goimports`)
- Use Go 1.22+ features where appropriate
- Write idiomatic Go code following effective Go principles
- Keep functions small and focused on a single responsibility

### Concurrency Patterns

- Use `context.WithDeadline` for job execution timeouts
- Use `context.WithCancel` for coordinating parallel task cancellation
- Use `sync.WaitGroup` for managing parallel task execution
- Always respect context cancellation in long-running operations
- Avoid in-memory flags for distributed state - use persistent records instead

### Error Handling

- Return errors explicitly, don't panic except for truly unrecoverable situations
- Wrap errors with context using `fmt.Errorf` with `%w` verb
- Log errors at appropriate levels with structured logging
- Preserve full error chains for debugging

### Interfaces and Abstractions

- Design interfaces to be minimal and focused
- Support pluggable implementations (storage backends, schedulers, etc.)
- Use dependency injection for testability
- Key interfaces to implement:
  - **Ticker**: Scheduling strategy (Cron, ISO8601, Once, custom)
  - **Storage**: Persistence layer abstraction
  - **Task**: Unit of work interface

### Naming Conventions

- Use clear, descriptive names that reflect the entity hierarchy
- Prefix interface names with 'I' only if it adds clarity (prefer simple names)
- Use PascalCase for exported identifiers
- Use camelCase for unexported identifiers

## Testing Requirements

### Test Coverage

- Aim for high test coverage, especially for:
  - Deterministic ID generation
  - Concurrent execution scenarios
  - Failure and retry logic
  - Storage layer operations

### Test Types

1. **Unit Tests**: Test individual components in isolation
   - Mock dependencies using interfaces
   - Test edge cases and error conditions
   - Verify deterministic behavior

2. **Integration Tests**: Test component interactions
   - Test with actual storage backends
   - Verify distributed coordination scenarios
   - Test failure recovery mechanisms

3. **Concurrency Tests**: Test parallel execution
   - Use `-race` flag to detect race conditions
   - Test `sync.WaitGroup` coordination
   - Verify context cancellation propagation

### Test Organization

- Place tests in `_test.go` files alongside source files
- Use table-driven tests for multiple scenarios
- Use subtests with `t.Run()` for better organization
- Include benchmarks for performance-critical code

## Feature Implementation Guidelines

### Implementing a New Ticker

1. Implement the Ticker interface methods:
   - Start(), Stop(), Pause(), Resume()
   - IsActive(), IsPaused(), IsEnabled(), IsRunning(), NextRun()
   - Provide a read-only channel for execution contexts

2. Handle time zones correctly (see timezone considerations in spec)
3. Respect startTime and endTime boundaries
4. Support persistence of ticker state

### Implementing Task Execution

1. Tasks run in isolated execution contexts (separate DB transactions)
2. Support both sequential and parallel execution modes
3. Implement failure strategies:
   - **Fail-Fast**: Cancel remaining tasks on first failure
   - **Continue**: Execute all tasks regardless of failures
4. Generate execution reports with full audit trail
5. Support retry policies with configurable backoff

### Implementing Storage Backend

1. Implement atomic "create-if-not-exists" semantics
2. Support efficient retrieval by key or key prefix
3. Provide transaction support for atomic operations
4. Handle concurrent access gracefully
5. Implement proper cleanup/garbage collection for old records

## Development Workflow

### Before Starting Work

1. Review the technical specification in README.md
2. Understand the entity hierarchy and relationships
3. Identify which interfaces need to be implemented or extended

### Code Changes

1. Make minimal, focused changes
2. Ensure backward compatibility unless explicitly breaking
3. Update documentation for API changes
4. Add or update tests to cover new functionality

### Documentation

- Document all exported types, functions, and methods with clear godoc comments
- Include usage examples in documentation
- Update README.md for significant architectural changes
- Document any deviation from the specification with rationale

### Commit Messages

- Use conventional commit format: `type(scope): description`
- Types: feat, fix, docs, test, refactor, perf, chore
- Include issue references where applicable

## Time Zone and DST Considerations

- Always use timezone-aware time handling
- Consider DST transitions in cron schedules
- Document timezone behavior clearly
- Test with various timezones including edge cases

## Performance Considerations

- Minimize database round-trips using batch operations
- Use connection pooling for storage backends
- Implement efficient key prefix scanning for hierarchical queries
- Consider memory usage for long-running schedulers
- Profile and benchmark performance-critical paths

## Security Considerations

- Validate all user inputs (cron expressions, intervals, etc.)
- Prevent resource exhaustion (limit concurrent tasks, execution timeouts)
- Secure credential handling for storage backends
- Implement proper access controls if exposing APIs
- Audit log security-relevant events

## Common Pitfalls to Avoid

1. **Don't use in-memory state for distributed coordination** - Always use persistent, deterministic records
2. **Don't use non-deterministic IDs** - Use UUIDv5 or similar deterministic schemes
3. **Don't ignore context cancellation** - Always respect context in long-running operations
4. **Don't overwrite execution history** - Preserve full audit trail (separate Execution Attempts)
5. **Don't assume single-process execution** - Design for distributed, multi-node scenarios
6. **Don't use short-lived lease keys** - Use transactional, persistent state instead

## References

- Full technical specification: See README.md
- Go best practices: https://go.dev/doc/effective_go
- Context package: https://pkg.go.dev/context
- Time handling: https://pkg.go.dev/time
