package metrics

import (
	"sync"
	"time"
)

// MetricsCollector defines the interface for collecting metrics
type MetricsCollector interface {
	// Gauges - current state
	SetJobsRunning(count int)
	SetJobsInQueue(count int)
	SetJobsPaused(count int)

	// Counters - event tracking
	IncTaskAttempts(jobName, taskName, status string)
	IncJobOccurrences(jobName, status string)
	IncRecoveryRuns(jobName, strategy string)

	// Histograms - duration tracking
	ObserveJobDuration(jobName string, duration time.Duration)
	ObserveTaskDuration(jobName, taskName string, duration time.Duration)

	// Query methods for testing and monitoring
	GetJobsRunning() int
	GetJobsInQueue() int
	GetJobsPaused() int
	GetTaskAttempts(jobName, taskName, status string) int64
	GetJobOccurrences(jobName, status string) int64
	GetRecoveryRuns(jobName, strategy string) int64
}

// NoOpMetrics is a metrics collector that does nothing
type NoOpMetrics struct{}

func NewNoOpMetrics() *NoOpMetrics {
	return &NoOpMetrics{}
}

func (m *NoOpMetrics) SetJobsRunning(count int)                                    {}
func (m *NoOpMetrics) SetJobsInQueue(count int)                                    {}
func (m *NoOpMetrics) SetJobsPaused(count int)                                     {}
func (m *NoOpMetrics) IncTaskAttempts(jobName, taskName, status string)           {}
func (m *NoOpMetrics) IncJobOccurrences(jobName, status string)                   {}
func (m *NoOpMetrics) IncRecoveryRuns(jobName, strategy string)                   {}
func (m *NoOpMetrics) ObserveJobDuration(jobName string, duration time.Duration)  {}
func (m *NoOpMetrics) ObserveTaskDuration(jobName, taskName string, duration time.Duration) {
}
func (m *NoOpMetrics) GetJobsRunning() int                                      { return 0 }
func (m *NoOpMetrics) GetJobsInQueue() int                                      { return 0 }
func (m *NoOpMetrics) GetJobsPaused() int                                       { return 0 }
func (m *NoOpMetrics) GetTaskAttempts(jobName, taskName, status string) int64  { return 0 }
func (m *NoOpMetrics) GetJobOccurrences(jobName, status string) int64          { return 0 }
func (m *NoOpMetrics) GetRecoveryRuns(jobName, strategy string) int64          { return 0 }

// InMemoryMetrics is a simple in-memory metrics collector for testing and basic monitoring
type InMemoryMetrics struct {
	mu sync.RWMutex

	// Gauges
	jobsRunning int
	jobsInQueue int
	jobsPaused  int

	// Counters - using map with composite key
	taskAttempts  map[string]int64 // key: "job:task:status"
	jobOccurrences map[string]int64 // key: "job:status"
	recoveryRuns  map[string]int64 // key: "job:strategy"

	// Histograms - storing observations
	jobDurations  map[string][]time.Duration // key: "job"
	taskDurations map[string][]time.Duration // key: "job:task"
}

func NewInMemoryMetrics() *InMemoryMetrics {
	return &InMemoryMetrics{
		taskAttempts:   make(map[string]int64),
		jobOccurrences: make(map[string]int64),
		recoveryRuns:   make(map[string]int64),
		jobDurations:   make(map[string][]time.Duration),
		taskDurations:  make(map[string][]time.Duration),
	}
}

// Gauges
func (m *InMemoryMetrics) SetJobsRunning(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobsRunning = count
}

func (m *InMemoryMetrics) SetJobsInQueue(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobsInQueue = count
}

func (m *InMemoryMetrics) SetJobsPaused(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobsPaused = count
}

func (m *InMemoryMetrics) GetJobsRunning() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobsRunning
}

func (m *InMemoryMetrics) GetJobsInQueue() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobsInQueue
}

func (m *InMemoryMetrics) GetJobsPaused() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobsPaused
}

// Counters
func (m *InMemoryMetrics) IncTaskAttempts(jobName, taskName, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := jobName + ":" + taskName + ":" + status
	m.taskAttempts[key]++
}

func (m *InMemoryMetrics) IncJobOccurrences(jobName, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := jobName + ":" + status
	m.jobOccurrences[key]++
}

func (m *InMemoryMetrics) IncRecoveryRuns(jobName, strategy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := jobName + ":" + strategy
	m.recoveryRuns[key]++
}

func (m *InMemoryMetrics) GetTaskAttempts(jobName, taskName, status string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := jobName + ":" + taskName + ":" + status
	return m.taskAttempts[key]
}

func (m *InMemoryMetrics) GetJobOccurrences(jobName, status string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := jobName + ":" + status
	return m.jobOccurrences[key]
}

func (m *InMemoryMetrics) GetRecoveryRuns(jobName, strategy string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := jobName + ":" + strategy
	return m.recoveryRuns[key]
}

// Histograms
func (m *InMemoryMetrics) ObserveJobDuration(jobName string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobDurations[jobName] = append(m.jobDurations[jobName], duration)
}

func (m *InMemoryMetrics) ObserveTaskDuration(jobName, taskName string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := jobName + ":" + taskName
	m.taskDurations[key] = append(m.taskDurations[key], duration)
}

// Helper methods for getting histogram statistics
func (m *InMemoryMetrics) GetJobDurations(jobName string) []time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	durations := m.jobDurations[jobName]
	result := make([]time.Duration, len(durations))
	copy(result, durations)
	return result
}

func (m *InMemoryMetrics) GetTaskDurations(jobName, taskName string) []time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := jobName + ":" + taskName
	durations := m.taskDurations[key]
	result := make([]time.Duration, len(durations))
	copy(result, durations)
	return result
}

// Reset clears all metrics (useful for testing)
func (m *InMemoryMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobsRunning = 0
	m.jobsInQueue = 0
	m.jobsPaused = 0
	m.taskAttempts = make(map[string]int64)
	m.jobOccurrences = make(map[string]int64)
	m.recoveryRuns = make(map[string]int64)
	m.jobDurations = make(map[string][]time.Duration)
	m.taskDurations = make(map[string][]time.Duration)
}
