package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ahmed-com/schedule/id"
	"github.com/ahmed-com/schedule/storage"
	"github.com/ahmed-com/schedule/storage/badger"
	"github.com/ahmed-com/schedule/ticker"
)

func main() {
	fmt.Println("=== Jobistemer Phase 1-3 Demo ===\n")

	// Phase 1: Demonstrate deterministic ID generation
	fmt.Println("Phase 1: Deterministic ID Generation")
	fmt.Println("-------------------------------------")
	
	jobID := id.GenerateJobID("daily-backup")
	fmt.Printf("Job ID for 'daily-backup': %s\n", jobID)
	
	taskID := id.GenerateTaskID(jobID, "backup-database")
	fmt.Printf("Task ID for 'backup-database': %s\n", taskID)
	
	scheduledTime := time.Date(2025, 11, 7, 12, 0, 0, 0, time.UTC)
	occurrenceID := id.GenerateJobOccurrenceID(jobID, scheduledTime)
	fmt.Printf("Occurrence ID for %s: %s\n", scheduledTime.Format(time.RFC3339), occurrenceID)
	
	// Verify determinism - same inputs produce same IDs
	jobID2 := id.GenerateJobID("daily-backup")
	if jobID == jobID2 {
		fmt.Println("✓ ID generation is deterministic (same input = same output)")
	}
	fmt.Println()

	// Phase 2: Demonstrate storage layer
	fmt.Println("Phase 2: Storage Layer (BadgerDB)")
	fmt.Println("----------------------------------")
	
	// Create temporary directory for BadgerDB
	dbPath := "/tmp/jobistemer-demo"
	store, err := badger.NewBadgerStorage(dbPath)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()
	
	ctx := context.Background()
	
	// Create a job
	job := &storage.Job{
		ID:           jobID,
		Name:         "daily-backup",
		ScheduleType: "cron",
		Config:       []byte(`{"task_execution_mode":"Sequential"}`),
		Version:      1,
		Active:       true,
		Paused:       false,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	
	err = store.CreateJob(ctx, job)
	if err != nil {
		log.Fatalf("Failed to create job: %v", err)
	}
	fmt.Printf("✓ Created job: %s\n", job.Name)
	
	// Retrieve the job
	retrievedJob, err := store.GetJob(ctx, jobID)
	if err != nil {
		log.Fatalf("Failed to retrieve job: %v", err)
	}
	fmt.Printf("✓ Retrieved job: %s (Version: %d)\n", retrievedJob.Name, retrievedJob.Version)
	
	// Create a task
	task := &storage.Task{
		ID:        taskID,
		JobID:     jobID,
		Name:      "backup-database",
		Config:    []byte(`{"task_timeout":"5m"}`),
		Order:     0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	
	err = store.CreateTask(ctx, task)
	if err != nil {
		log.Fatalf("Failed to create task: %v", err)
	}
	fmt.Printf("✓ Created task: %s\n", task.Name)
	
	// Test atomic create-if-not-exists for job occurrence
	occurrence := &storage.JobOccurrence{
		ID:            occurrenceID,
		JobID:         jobID,
		JobVersion:    1,
		ScheduledTime: scheduledTime,
		Status:        storage.JobStatusPending,
		Config:        []byte(`{}`),
		IsRecoveryRun: false,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	
	err = store.CreateJobOccurrence(ctx, occurrence)
	if err != nil {
		log.Fatalf("Failed to create occurrence: %v", err)
	}
	fmt.Printf("✓ Created occurrence: %s\n", occurrence.ID)
	
	// Try to create the same occurrence again (should fail)
	err = store.CreateJobOccurrence(ctx, occurrence)
	if err != nil {
		fmt.Printf("✓ Atomic create-if-not-exists works (duplicate rejected): %v\n", err)
	}
	fmt.Println()

	// Phase 3: Demonstrate ticker implementations
	fmt.Println("Phase 3: Ticker Implementations")
	fmt.Println("--------------------------------")
	
	// Cron Ticker Example
	fmt.Println("\n1. Cron Ticker (every minute)")
	cronConfig := ticker.TickerConfig{
		Timezone: "UTC",
	}
	cronTicker, err := ticker.NewCronTicker("*/1 * * * *", cronConfig)
	if err != nil {
		log.Fatalf("Failed to create cron ticker: %v", err)
	}
	
	nextRun, _ := cronTicker.NextRun()
	if nextRun != nil {
		fmt.Printf("   Next run: %s\n", nextRun.Format(time.RFC3339))
	}
	
	// Get occurrences for next hour
	start := time.Now()
	end := start.Add(1 * time.Hour)
	occurrences, err := cronTicker.GetOccurrencesBetween(start, end)
	if err != nil {
		log.Fatalf("Failed to get occurrences: %v", err)
	}
	fmt.Printf("   Occurrences in next hour: %d\n", len(occurrences))
	
	// ISO8601 Ticker Example
	fmt.Println("\n2. ISO8601 Ticker (every 30 minutes, 5 repetitions)")
	iso8601Config := ticker.TickerConfig{
		Timezone: "UTC",
	}
	iso8601Ticker, err := ticker.NewISO8601Ticker(
		30*time.Minute,
		time.Now(),
		5,
		iso8601Config,
	)
	if err != nil {
		log.Fatalf("Failed to create ISO8601 ticker: %v", err)
	}
	
	nextRun, _ = iso8601Ticker.NextRun()
	if nextRun != nil {
		fmt.Printf("   Next run: %s\n", nextRun.Format(time.RFC3339))
	}
	
	occurrences, err = iso8601Ticker.GetOccurrencesBetween(start, end.Add(3*time.Hour))
	if err != nil {
		log.Fatalf("Failed to get occurrences: %v", err)
	}
	fmt.Printf("   Total occurrences: %d (limited by repetitions)\n", len(occurrences))
	
	// Once Ticker Example
	fmt.Println("\n3. Once Ticker (runs at a specific time)")
	onceTime := time.Now().Add(5 * time.Second)
	onceConfig := ticker.TickerConfig{
		Timezone: "UTC",
	}
	onceTicker, err := ticker.NewOnceTicker(onceTime, onceConfig)
	if err != nil {
		log.Fatalf("Failed to create once ticker: %v", err)
	}
	
	nextRun, _ = onceTicker.NextRun()
	if nextRun != nil {
		fmt.Printf("   Scheduled time: %s\n", nextRun.Format(time.RFC3339))
	}
	
	occurrences, err = onceTicker.GetOccurrencesBetween(time.Now(), time.Now().Add(1*time.Hour))
	if err != nil {
		log.Fatalf("Failed to get occurrences: %v", err)
	}
	fmt.Printf("   Occurrences: %d\n", len(occurrences))
	
	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("\nAll three phases implemented successfully:")
	fmt.Println("✓ Phase 1: Core types and deterministic ID generation")
	fmt.Println("✓ Phase 2: Storage interface and BadgerDB implementation")
	fmt.Println("✓ Phase 3: Ticker interface with Cron, ISO8601, and Once implementations")
}
