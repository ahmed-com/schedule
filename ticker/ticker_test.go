package ticker

import (
	"testing"
	"time"
)

func TestCronTickerCreation(t *testing.T) {
	tests := []struct {
		name        string
		expression  string
		shouldError bool
	}{
		{"valid every minute", "*/1 * * * *", false},
		{"valid daily at midnight", "0 0 * * *", false},
		{"valid weekly on monday", "0 0 * * 1", false},
		{"invalid expression", "invalid", true},
		{"invalid too many fields", "* * * * * *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := TickerConfig{Timezone: "UTC"}
			_, err := NewCronTicker(tt.expression, config)
			
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for expression %s, got nil", tt.expression)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for expression %s: %v", tt.expression, err)
			}
		})
	}
}

func TestCronTickerNextRun(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	ticker, err := NewCronTicker("*/5 * * * *", config) // Every 5 minutes
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	nextRun, err := ticker.NextRun()
	if err != nil {
		t.Fatalf("Failed to get next run: %v", err)
	}
	if nextRun == nil {
		t.Fatal("Next run should not be nil")
	}

	now := time.Now().UTC()
	if nextRun.Before(now) {
		t.Errorf("Next run should be in the future, got %s (now: %s)", nextRun, now)
	}
}

func TestCronTickerGetOccurrencesBetween(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	ticker, err := NewCronTicker("0 * * * *", config) // Every hour
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	start := time.Date(2025, 11, 7, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 11, 7, 5, 0, 0, 0, time.UTC)

	occurrences, err := ticker.GetOccurrencesBetween(start, end)
	if err != nil {
		t.Fatalf("Failed to get occurrences: %v", err)
	}

	// Should have 5 occurrences: 1:00, 2:00, 3:00, 4:00, 5:00
	if len(occurrences) != 5 {
		t.Errorf("Expected 5 occurrences, got %d", len(occurrences))
	}

	// Verify they're at the right times
	for i, occ := range occurrences {
		expectedHour := i + 1
		if occ.Hour() != expectedHour {
			t.Errorf("Occurrence %d: expected hour %d, got %d", i, expectedHour, occ.Hour())
		}
		if occ.Minute() != 0 {
			t.Errorf("Occurrence %d: expected minute 0, got %d", i, occ.Minute())
		}
	}
}

func TestCronTickerWithTimeWindow(t *testing.T) {
	startTime := time.Date(2025, 11, 7, 9, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 11, 7, 17, 0, 0, 0, time.UTC)
	
	config := TickerConfig{
		Timezone:  "UTC",
		StartTime: &startTime,
		EndTime:   &endTime,
	}
	
	ticker, err := NewCronTicker("0 * * * *", config) // Every hour
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	queryStart := time.Date(2025, 11, 7, 0, 0, 0, 0, time.UTC)
	queryEnd := time.Date(2025, 11, 7, 23, 59, 0, 0, time.UTC)

	occurrences, err := ticker.GetOccurrencesBetween(queryStart, queryEnd)
	if err != nil {
		t.Fatalf("Failed to get occurrences: %v", err)
	}

	// Should only have occurrences between 9:00 and 17:00
	for _, occ := range occurrences {
		if occ.Before(startTime) || occ.After(endTime) {
			t.Errorf("Occurrence %s outside of time window [%s, %s]", occ, startTime, endTime)
		}
	}
}

func TestISO8601TickerCreation(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	
	// Test valid creation
	ticker, err := NewISO8601Ticker(1*time.Hour, time.Now(), 0, config)
	if err != nil {
		t.Errorf("Failed to create ticker: %v", err)
	}
	if ticker == nil {
		t.Error("Ticker should not be nil")
	}
	
	// Test invalid interval
	_, err = NewISO8601Ticker(-1*time.Hour, time.Now(), 0, config)
	if err == nil {
		t.Error("Expected error for negative interval")
	}
	
	// Test invalid timezone
	badConfig := TickerConfig{Timezone: "Invalid/Timezone"}
	_, err = NewISO8601Ticker(1*time.Hour, time.Now(), 0, badConfig)
	if err == nil {
		t.Error("Expected error for invalid timezone")
	}
}

func TestISO8601TickerNextRun(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	startTime := time.Now().Add(10 * time.Minute)
	
	ticker, err := NewISO8601Ticker(30*time.Minute, startTime, 5, config)
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	nextRun, err := ticker.NextRun()
	if err != nil {
		t.Fatalf("Failed to get next run: %v", err)
	}
	if nextRun == nil {
		t.Fatal("Next run should not be nil")
	}

	// Next run should be the start time
	if !nextRun.Equal(startTime) {
		t.Errorf("Next run should be %s, got %s", startTime, nextRun)
	}
}

func TestISO8601TickerGetOccurrencesBetween(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	startTime := time.Date(2025, 11, 7, 0, 0, 0, 0, time.UTC)
	
	ticker, err := NewISO8601Ticker(2*time.Hour, startTime, 5, config)
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	queryStart := time.Date(2025, 11, 7, 0, 0, 0, 0, time.UTC)
	queryEnd := time.Date(2025, 11, 7, 23, 59, 0, 0, time.UTC)

	occurrences, err := ticker.GetOccurrencesBetween(queryStart, queryEnd)
	if err != nil {
		t.Fatalf("Failed to get occurrences: %v", err)
	}

	// Should have exactly 5 occurrences (limited by repetitions)
	if len(occurrences) != 5 {
		t.Errorf("Expected 5 occurrences, got %d", len(occurrences))
	}

	// Verify intervals
	for i := 0; i < len(occurrences)-1; i++ {
		interval := occurrences[i+1].Sub(occurrences[i])
		if interval != 2*time.Hour {
			t.Errorf("Expected 2 hour interval, got %s", interval)
		}
	}
}

func TestOnceTickerCreation(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	scheduledTime := time.Now().Add(1 * time.Hour)
	
	ticker, err := NewOnceTicker(scheduledTime, config)
	if err != nil {
		t.Errorf("Failed to create ticker: %v", err)
	}
	if ticker == nil {
		t.Error("Ticker should not be nil")
	}
	
	// Test invalid timezone
	badConfig := TickerConfig{Timezone: "Invalid/Timezone"}
	_, err = NewOnceTicker(scheduledTime, badConfig)
	if err == nil {
		t.Error("Expected error for invalid timezone")
	}
}

func TestOnceTickerNextRun(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	scheduledTime := time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)
	
	ticker, err := NewOnceTicker(scheduledTime, config)
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	nextRun, err := ticker.NextRun()
	if err != nil {
		t.Fatalf("Failed to get next run: %v", err)
	}
	if nextRun == nil {
		t.Fatal("Next run should not be nil")
	}

	if !nextRun.Equal(scheduledTime) {
		t.Errorf("Next run should be %s, got %s", scheduledTime, nextRun)
	}
}

func TestOnceTickerGetOccurrencesBetween(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	scheduledTime := time.Date(2025, 11, 7, 12, 0, 0, 0, time.UTC)
	
	ticker, err := NewOnceTicker(scheduledTime, config)
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	// Query window that includes the scheduled time
	queryStart := time.Date(2025, 11, 7, 0, 0, 0, 0, time.UTC)
	queryEnd := time.Date(2025, 11, 7, 23, 59, 0, 0, time.UTC)

	occurrences, err := ticker.GetOccurrencesBetween(queryStart, queryEnd)
	if err != nil {
		t.Fatalf("Failed to get occurrences: %v", err)
	}

	// Should have exactly 1 occurrence
	if len(occurrences) != 1 {
		t.Errorf("Expected 1 occurrence, got %d", len(occurrences))
	}

	if len(occurrences) > 0 && !occurrences[0].Equal(scheduledTime) {
		t.Errorf("Occurrence should be %s, got %s", scheduledTime, occurrences[0])
	}

	// Query window that excludes the scheduled time
	queryStart2 := time.Date(2025, 11, 8, 0, 0, 0, 0, time.UTC)
	queryEnd2 := time.Date(2025, 11, 9, 0, 0, 0, 0, time.UTC)

	occurrences2, err := ticker.GetOccurrencesBetween(queryStart2, queryEnd2)
	if err != nil {
		t.Fatalf("Failed to get occurrences: %v", err)
	}

	if len(occurrences2) != 0 {
		t.Errorf("Expected 0 occurrences, got %d", len(occurrences2))
	}
}

func TestTickerControlMethods(t *testing.T) {
	config := TickerConfig{Timezone: "UTC"}
	ticker, err := NewCronTicker("*/1 * * * *", config)
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	// Initially not paused
	if ticker.IsPaused() {
		t.Error("Ticker should not be paused initially")
	}

	// Start the ticker
	err = ticker.Start()
	if err != nil {
		t.Errorf("Failed to start: %v", err)
	}
	
	// Give the goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Pause
	err = ticker.Pause()
	if err != nil {
		t.Errorf("Failed to pause: %v", err)
	}
	
	// Give the goroutine time to process
	time.Sleep(10 * time.Millisecond)
	
	if !ticker.IsPaused() {
		t.Error("Ticker should be paused")
	}

	// Resume
	err = ticker.Resume()
	if err != nil {
		t.Errorf("Failed to resume: %v", err)
	}
	
	// Give the goroutine time to process
	time.Sleep(10 * time.Millisecond)
	
	if ticker.IsPaused() {
		t.Error("Ticker should not be paused after resume")
	}

	// IsActive should be true
	if !ticker.IsActive() {
		t.Error("Ticker should be active")
	}

	// IsEnabled should be true when active and not paused
	if !ticker.IsEnabled() {
		t.Error("Ticker should be enabled")
	}
	
	// Stop the ticker
	err = ticker.Stop()
	if err != nil {
		t.Errorf("Failed to stop: %v", err)
	}
}
