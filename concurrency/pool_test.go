package concurrency

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNewWorkerPool(t *testing.T) {
	pool := NewWorkerPool(5)

	if pool.MaxWorkers() != 5 {
		t.Errorf("Expected max workers 5, got %d", pool.MaxWorkers())
	}

	// Test default value
	pool2 := NewWorkerPool(0)
	if pool2.MaxWorkers() != 10 {
		t.Errorf("Expected default max workers 10, got %d", pool2.MaxWorkers())
	}
}

func TestWorkerPoolStartStop(t *testing.T) {
	pool := NewWorkerPool(3)

	// Should not be started initially
	if pool.started {
		t.Error("Pool should not be started initially")
	}

	pool.Start()
	if !pool.started {
		t.Error("Pool should be started after Start()")
	}

	// Starting again should be a no-op
	pool.Start()
	if !pool.started {
		t.Error("Pool should still be started")
	}

	pool.Stop()
	if pool.started {
		t.Error("Pool should be stopped after Stop()")
	}
}

func TestWorkerPoolExecutesTasks(t *testing.T) {
	pool := NewWorkerPool(3)
	pool.Start()
	defer pool.Stop()

	var counter int32
	numTasks := 10

	for i := 0; i < numTasks; i++ {
		pool.Submit(func() {
			atomic.AddInt32(&counter, 1)
		})
	}

	// Wait a bit for tasks to complete
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&counter) != int32(numTasks) {
		t.Errorf("Expected %d tasks executed, got %d", numTasks, atomic.LoadInt32(&counter))
	}
}

func TestWorkerPoolConcurrencyLimit(t *testing.T) {
	maxWorkers := 3
	pool := NewWorkerPool(maxWorkers)
	pool.Start()
	defer pool.Stop()

	var currentConcurrent int32
	var maxConcurrent int32
	var completed int32

	taskDuration := 50 * time.Millisecond
	numTasks := 10

	for i := 0; i < numTasks; i++ {
		pool.Submit(func() {
			current := atomic.AddInt32(&currentConcurrent, 1)

			// Update max concurrent
			for {
				max := atomic.LoadInt32(&maxConcurrent)
				if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
					break
				}
			}

			time.Sleep(taskDuration)
			atomic.AddInt32(&currentConcurrent, -1)
			atomic.AddInt32(&completed, 1)
		})
	}

	// Wait for all tasks to complete
	for atomic.LoadInt32(&completed) < int32(numTasks) {
		time.Sleep(10 * time.Millisecond)
	}

	maxObserved := atomic.LoadInt32(&maxConcurrent)
	if maxObserved > int32(maxWorkers) {
		t.Errorf("Expected max concurrent workers %d, but observed %d", maxWorkers, maxObserved)
	}
	if maxObserved < int32(maxWorkers) {
		t.Logf("Warning: Expected to reach max workers %d, but only observed %d (may be timing dependent)", maxWorkers, maxObserved)
	}
}

func TestWorkerPoolSubmitBeforeStart(t *testing.T) {
	pool := NewWorkerPool(3)

	var executed bool
	pool.Submit(func() {
		executed = true
	})

	// Should execute synchronously when not started
	if !executed {
		t.Error("Task should have been executed synchronously before pool start")
	}
}

func TestWorkerPoolQueueLength(t *testing.T) {
	pool := NewWorkerPool(1)
	pool.Start()
	defer pool.Stop()

	// Block the single worker
	blockCh := make(chan struct{})
	pool.Submit(func() {
		<-blockCh
	})

	// Wait for worker to pick up task
	time.Sleep(50 * time.Millisecond)

	// Submit task to queue (only 1 since buffer is 1*2=2 and 1 is already in worker)
	pool.Submit(func() {
		time.Sleep(1 * time.Millisecond)
	})

	// Queue should have a task
	queueLen := pool.QueueLength()
	if queueLen == 0 {
		t.Error("Expected task in queue, got 0")
	}

	// Unblock worker
	close(blockCh)

	// Wait for queue to drain
	time.Sleep(100 * time.Millisecond)

	queueLen = pool.QueueLength()
	if queueLen != 0 {
		t.Errorf("Expected empty queue after completion, got %d", queueLen)
	}
}

func TestWorkerPoolStopWaitsForCompletion(t *testing.T) {
	pool := NewWorkerPool(2)
	pool.Start()

	var completed int32
	numTasks := 3 // Small number to ensure they all fit in queue

	for i := 0; i < numTasks; i++ {
		pool.Submit(func() {
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&completed, 1)
		})
	}

	// Give time for tasks to be queued
	time.Sleep(10 * time.Millisecond)

	pool.Stop()

	// After Stop(), all tasks should be completed
	completedCount := atomic.LoadInt32(&completed)
	if completedCount != int32(numTasks) {
		t.Errorf("Expected %d tasks completed after Stop(), got %d", numTasks, completedCount)
	}
}
