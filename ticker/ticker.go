package ticker

import (
	"time"
)

// MaxOccurrenceIterations is the safety limit for occurrence calculations
const MaxOccurrenceIterations = 10000

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
	IsActive() bool  // Within start-end window
	IsPaused() bool  // Manually paused
	IsEnabled() bool // Active and not paused
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
