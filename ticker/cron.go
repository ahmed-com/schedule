package ticker

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// CronTicker implements the Ticker interface using cron expressions
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
	mu       sync.RWMutex
}

// NewCronTicker creates a new cron-based ticker
func NewCronTicker(expression string, config TickerConfig) (*CronTicker, error) {
	// Default to UTC if no timezone specified
	timezone := config.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", timezone, err)
	}

	// Parse cron expression with standard fields
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %s: %w", expression, err)
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

// Start begins the cron schedule
func (t *CronTicker) Start() error {
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
func (t *CronTicker) run() {
	for {
		t.mu.RLock()
		paused := t.paused
		t.mu.RUnlock()

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
		next := t.schedule.Next(now)

		// Check if within active window
		if !t.isWithinWindow(next) {
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

		duration := next.Sub(now)
		timer := time.NewTimer(duration)

		select {
		case <-timer.C:
			ctx := ExecutionContext{
				ScheduledTime: next,
				ActualTime:    time.Now().In(t.location),
			}
			
			// Non-blocking send
			select {
			case t.ch <- ctx:
			default:
				// Channel full, skip this tick (prevents blocking)
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

// isWithinWindow checks if the given time is within the configured time window
func (t *CronTicker) isWithinWindow(checkTime time.Time) bool {
	if t.config.StartTime != nil && checkTime.Before(*t.config.StartTime) {
		return false
	}
	if t.config.EndTime != nil && checkTime.After(*t.config.EndTime) {
		return false
	}
	return true
}

// Channel returns the execution context channel
func (t *CronTicker) Channel() <-chan ExecutionContext {
	return t.ch
}

// Stop halts the ticker
func (t *CronTicker) Stop() error {
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
func (t *CronTicker) Pause() error {
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
func (t *CronTicker) Resume() error {
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
func (t *CronTicker) IsActive() bool {
	now := time.Now().In(t.location)
	return t.isWithinWindow(now)
}

// IsPaused returns whether the ticker is paused
func (t *CronTicker) IsPaused() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.paused
}

// IsEnabled returns whether the ticker is active and not paused
func (t *CronTicker) IsEnabled() bool {
	return t.IsActive() && !t.IsPaused()
}

// NextRun calculates the next scheduled run time
func (t *CronTicker) NextRun() (*time.Time, error) {
	now := time.Now().In(t.location)
	next := t.schedule.Next(now)
	
	// If next run is outside the window, return nil
	if !t.isWithinWindow(next) {
		return nil, nil
	}
	
	return &next, nil
}

// GetOccurrencesBetween returns all scheduled times between start and end
func (t *CronTicker) GetOccurrencesBetween(start, end time.Time) ([]time.Time, error) {
	var occurrences []time.Time
	current := start.In(t.location)
	end = end.In(t.location)

	// Limit iterations to prevent infinite loops
	maxIterations := 10000
	iterations := 0

	for current.Before(end) {
		iterations++
		if iterations > maxIterations {
			return nil, fmt.Errorf("too many iterations while computing occurrences")
		}

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
