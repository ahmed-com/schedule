package ticker

import (
	"fmt"
	"sync"
	"time"
)

// OnceTicker implements the Ticker interface for one-time execution
type OnceTicker struct {
	scheduledTime time.Time
	config        TickerConfig

	ch       chan ExecutionContext
	stopCh   chan struct{}
	pauseCh  chan bool
	running  bool
	paused   bool
	executed bool
	location *time.Location
	mu       sync.RWMutex
}

// NewOnceTicker creates a new one-time ticker
func NewOnceTicker(scheduledTime time.Time, config TickerConfig) (*OnceTicker, error) {
	// Default to UTC if no timezone specified
	timezone := config.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", timezone, err)
	}

	return &OnceTicker{
		scheduledTime: scheduledTime.In(loc),
		config:        config,
		location:      loc,
		ch:            make(chan ExecutionContext, 1),
		stopCh:        make(chan struct{}),
		pauseCh:       make(chan bool, 1),
	}, nil
}

// Start begins the one-time schedule
func (t *OnceTicker) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	if t.executed {
		return fmt.Errorf("once ticker has already been executed")
	}

	t.running = true
	go t.run()
	return nil
}

// run is the main ticker loop
func (t *OnceTicker) run() {
	defer func() {
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
	}()

	for {
		t.mu.RLock()
		paused := t.paused
		executed := t.executed
		t.mu.RUnlock()

		if executed {
			return
		}

		if paused {
			select {
			case <-t.stopCh:
				return
			case resume := <-t.pauseCh:
				t.mu.Lock()
				t.paused = !resume
				t.mu.Unlock()
			}
			continue
		}

		now := time.Now().In(t.location)

		// Check if within active window
		if !t.isWithinWindow(t.scheduledTime) {
			// If scheduled time is past the end window, we can't execute
			if t.config.EndTime != nil && t.scheduledTime.After(*t.config.EndTime) {
				return
			}
			// Sleep briefly and check again
			timer := time.NewTimer(time.Minute)
			select {
			case <-timer.C:
				continue
			case <-t.stopCh:
				timer.Stop()
				return
			case pause := <-t.pauseCh:
				timer.Stop()
				t.mu.Lock()
				t.paused = pause
				t.mu.Unlock()
			}
			continue
		}

		// Calculate duration until scheduled time
		var duration time.Duration
		if t.scheduledTime.Before(now) {
			// Scheduled time is in the past, execute immediately
			duration = 0
		} else {
			duration = t.scheduledTime.Sub(now)
		}

		timer := time.NewTimer(duration)

		select {
		case <-timer.C:
			ctx := ExecutionContext{
				ScheduledTime: t.scheduledTime,
				ActualTime:    time.Now().In(t.location),
			}

			// Mark as executed
			t.mu.Lock()
			t.executed = true
			t.mu.Unlock()

			// Send execution context
			select {
			case t.ch <- ctx:
			default:
				// Channel full (shouldn't happen with buffer of 1)
			}

			return
		case <-t.stopCh:
			timer.Stop()
			return
		case pause := <-t.pauseCh:
			timer.Stop()
			t.mu.Lock()
			t.paused = pause
			t.mu.Unlock()
		}
	}
}

// isWithinWindow checks if the given time is within the configured time window
func (t *OnceTicker) isWithinWindow(checkTime time.Time) bool {
	if t.config.StartTime != nil && checkTime.Before(*t.config.StartTime) {
		return false
	}
	if t.config.EndTime != nil && checkTime.After(*t.config.EndTime) {
		return false
	}
	return true
}

// Channel returns the execution context channel
func (t *OnceTicker) Channel() <-chan ExecutionContext {
	return t.ch
}

// Stop halts the ticker
func (t *OnceTicker) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return nil
	}

	close(t.stopCh)
	t.running = false
	return nil
}

// Pause temporarily pauses the ticker
func (t *OnceTicker) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.paused {
		return nil
	}

	select {
	case t.pauseCh <- true:
		t.paused = true
	default:
	}
	return nil
}

// Resume resumes a paused ticker
func (t *OnceTicker) Resume() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.paused {
		return nil
	}

	select {
	case t.pauseCh <- false:
		t.paused = false
	default:
	}
	return nil
}

// IsActive checks if current time is within the active window
func (t *OnceTicker) IsActive() bool {
	now := time.Now().In(t.location)
	return t.isWithinWindow(now)
}

// IsPaused returns whether the ticker is paused
func (t *OnceTicker) IsPaused() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.paused
}

// IsEnabled returns whether the ticker is active and not paused
func (t *OnceTicker) IsEnabled() bool {
	return t.IsActive() && !t.IsPaused()
}

// NextRun returns the scheduled time if not yet executed
func (t *OnceTicker) NextRun() (*time.Time, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.executed {
		return nil, nil
	}

	if !t.isWithinWindow(t.scheduledTime) {
		return nil, nil
	}

	return &t.scheduledTime, nil
}

// GetOccurrencesBetween returns the scheduled time if it falls within the range
func (t *OnceTicker) GetOccurrencesBetween(start, end time.Time) ([]time.Time, error) {
	start = start.In(t.location)
	end = end.In(t.location)

	if t.scheduledTime.After(start) && t.scheduledTime.Before(end) && t.isWithinWindow(t.scheduledTime) {
		return []time.Time{t.scheduledTime}, nil
	}

	return []time.Time{}, nil
}
