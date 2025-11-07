package concurrency

import (
	"sync"
)

// WorkerPool manages a pool of workers to limit concurrent job execution
type WorkerPool struct {
	maxWorkers int
	taskQueue  chan func()
	wg         sync.WaitGroup
	stopCh     chan struct{}
	started    bool
	mu         sync.Mutex
}

// NewWorkerPool creates a new worker pool with the specified max workers
func NewWorkerPool(maxWorkers int) *WorkerPool {
	if maxWorkers <= 0 {
		maxWorkers = 10 // default
	}

	return &WorkerPool{
		maxWorkers: maxWorkers,
		taskQueue:  make(chan func(), maxWorkers*2), // buffer to avoid blocking
		stopCh:     make(chan struct{}),
		started:    false,
	}
}

// Start starts the worker pool
func (p *WorkerPool) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return
	}

	p.started = true

	// Start worker goroutines
	for i := 0; i < p.maxWorkers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

// worker is the main worker goroutine that processes tasks
func (p *WorkerPool) worker() {
	defer p.wg.Done()

	for {
		select {
		case <-p.stopCh:
			return
		case task, ok := <-p.taskQueue:
			if !ok {
				return
			}
			task()
		}
	}
}

// Submit submits a task to the worker pool
// This will block if the queue is full
func (p *WorkerPool) Submit(task func()) {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()

	if !started {
		// If pool not started, execute task synchronously
		task()
		return
	}

	select {
	case <-p.stopCh:
		// Pool is stopped, don't accept new tasks
		return
	case p.taskQueue <- task:
		// Task queued successfully
	}
}

// Stop stops the worker pool and waits for all workers to finish
func (p *WorkerPool) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return
	}

	close(p.stopCh)
	close(p.taskQueue)
	p.wg.Wait()
	p.started = false
}

// QueueLength returns the current number of tasks in the queue
func (p *WorkerPool) QueueLength() int {
	return len(p.taskQueue)
}

// MaxWorkers returns the maximum number of workers in the pool
func (p *WorkerPool) MaxWorkers() int {
	return p.maxWorkers
}
