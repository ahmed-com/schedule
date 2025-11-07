package recovery

import (
	"context"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/storage"
)

// Reaper periodically scans for and cleans up stale job occurrences
type Reaper struct {
	store    storage.Storage
	interval time.Duration
	stopCh   chan struct{}
}

// NewReaper creates a new reaper instance
func NewReaper(store storage.Storage, interval time.Duration) *Reaper {
	if interval == 0 {
		interval = 5 * time.Minute // default
	}

	return &Reaper{
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start starts the reaper goroutine
func (r *Reaper) Start(ctx context.Context) {
	go r.run(ctx)
}

// run is the main reaper loop
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

// reapStaleJobs finds and marks stale job occurrences as failed
func (r *Reaper) reapStaleJobs(ctx context.Context) {
	// Query for stale jobs (default threshold of 1 hour)
	staleOccurrences, err := r.store.ListStaleOccurrences(ctx, 1*time.Hour)
	if err != nil {
		// Log error (in production, use proper logging)
		return
	}

	for _, occ := range staleOccurrences {
		// Atomically update to Failed_Stale
		occ.Status = jobistemer.JobStatusFailedStale
		now := time.Now()
		occ.EndTime = &now
		occ.UpdatedAt = now
		r.store.UpdateJobOccurrence(ctx, occ)
	}
}

// Stop stops the reaper
func (r *Reaper) Stop() {
	close(r.stopCh)
}
