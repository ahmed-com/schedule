package ticker

import (
	"fmt"
	"sync"
	"time"
)

// ISO8601Ticker implements the Ticker interface using ISO8601 duration intervals
type ISO8601Ticker struct {
	interval   time.Duration
	startTime  time.Time
	repetitions int // 0 means infinite
	config     TickerConfig

	ch          chan ExecutionContext
	stopCh      chan struct{}
	pauseCh     chan bool
	running     bool
	paused      bool
	location    *time.Location
	runCount    int
	lastRunTime *time.Time
	mu          sync.RWMutex
}

// NewISO8601Ticker creates a new ISO8601-based ticker
// interval: the duration between executions (e.g., time.Hour * 24 for daily)
// startTime: when to start the schedule
// repetitions: number of times to repeat (0 for infinite)
func NewISO8601Ticker(interval time.Duration, startTime time.Time, repetitions int, config TickerConfig) (*ISO8601Ticker, error) {
	// Default to UTC if no timezone specified
	timezone := config.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", timezone, err)
	}

	if interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	return &ISO8601Ticker{
		interval:    interval,
		startTime:   startTime.In(loc),
		repetitions: repetitions,
		config:      config,
		location:    loc,
		ch:          make(chan ExecutionContext, 10),
		stopCh:      make(chan struct{}),
		pauseCh:     make(chan bool, 1),
	}, nil
}

// Start begins the ISO8601 schedule
func (t *ISO8601Ticker) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	t.running = true
	go t.run()
	return nil
}

// run is the main ticker loop
func (t *ISO8601Ticker) run() {
	for {
		t.mu.RLock()
		paused := t.paused
		runCount := t.runCount
		t.mu.RUnlock()

		// Check if we've reached the repetition limit
		if t.repetitions > 0 && runCount >= t.repetitions {
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
		next := t.calculateNextRun(now)

		// Check if within active window
		if !t.isWithinWindow(next) {
			// If we're past the end time, stop
			if t.config.EndTime != nil && now.After(*t.config.EndTime) {
				return
			}
			// Sleep for a minute and check again
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

		// If next run is in the past, execute immediately
		var duration time.Duration
		if next.Before(now) {
			duration = 0
		} else {
			duration = next.Sub(now)
		}

		timer := time.NewTimer(duration)

		select {
		case <-timer.C:
			ctx := ExecutionContext{
				ScheduledTime: next,
				ActualTime:    time.Now().In(t.location),
				LastRunTime:   t.lastRunTime,
			}

			// Update state
			t.mu.Lock()
			t.runCount++
			nowTime := time.Now().In(t.location)
			t.lastRunTime = &nowTime
			t.mu.Unlock()

			// Non-blocking send
			select {
			case t.ch <- ctx:
			default:
				// Channel full, skip this tick
			}
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

// calculateNextRun calculates the next run time
func (t *ISO8601Ticker) calculateNextRun(from time.Time) time.Time {
	t.mu.RLock()
	runCount := t.runCount
	t.mu.RUnlock()

	// Calculate next occurrence based on start time and interval
	return t.startTime.Add(time.Duration(runCount) * t.interval)
}

// isWithinWindow checks if the given time is within the configured time window
func (t *ISO8601Ticker) isWithinWindow(checkTime time.Time) bool {
	if t.config.StartTime != nil && checkTime.Before(*t.config.StartTime) {
		return false
	}
	if t.config.EndTime != nil && checkTime.After(*t.config.EndTime) {
		return false
	}
	return true
}

// Channel returns the execution context channel
func (t *ISO8601Ticker) Channel() <-chan ExecutionContext {
	return t.ch
}

// Stop halts the ticker
func (t *ISO8601Ticker) Stop() error {
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
func (t *ISO8601Ticker) Pause() error {
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
func (t *ISO8601Ticker) Resume() error {
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
func (t *ISO8601Ticker) IsActive() bool {
	now := time.Now().In(t.location)
	return t.isWithinWindow(now)
}

// IsPaused returns whether the ticker is paused
func (t *ISO8601Ticker) IsPaused() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.paused
}

// IsEnabled returns whether the ticker is active and not paused
func (t *ISO8601Ticker) IsEnabled() bool {
	return t.IsActive() && !t.IsPaused()
}

// NextRun calculates the next scheduled run time
func (t *ISO8601Ticker) NextRun() (*time.Time, error) {
	t.mu.RLock()
	runCount := t.runCount
	t.mu.RUnlock()

	// Check if we've reached the repetition limit
	if t.repetitions > 0 && runCount >= t.repetitions {
		return nil, nil
	}

	now := time.Now().In(t.location)
	next := t.calculateNextRun(now)

	// If next run is outside the window, return nil
	if !t.isWithinWindow(next) {
		return nil, nil
	}

	return &next, nil
}

// GetOccurrencesBetween returns all scheduled times between start and end
func (t *ISO8601Ticker) GetOccurrencesBetween(start, end time.Time) ([]time.Time, error) {
	var occurrences []time.Time
	start = start.In(t.location)
	end = end.In(t.location)

	current := t.startTime
	count := 0

	for current.Before(end) {
		if t.repetitions > 0 && count >= t.repetitions {
			break
		}

		if (current.After(start) || current.Equal(start)) && t.isWithinWindow(current) {
			occurrences = append(occurrences, current)
		}

		current = current.Add(t.interval)
		count++

		// Safety check
		if count > MaxOccurrenceIterations {
			return nil, fmt.Errorf("too many occurrences")
		}
	}

	return occurrences, nil
}
