package jobistemer

import (
	"context"
	"testing"
	"time"

	"github.com/ahmed-com/schedule/ticker"
)

func TestNewJob(t *testing.T) {
	config := JobConfig{
		TaskExecutionMode: TaskExecutionModeSequential,
		FailureStrategy:   FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
		OverlapPolicy:     OverlapPolicySkip,
	}

	tick, err := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	if err != nil {
		t.Fatalf("Failed to create ticker: %v", err)
	}

	job := NewJob("test-job", tick, config)

	if job.Name != "test-job" {
		t.Errorf("Expected name 'test-job', got '%s'", job.Name)
	}
	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}
	if job.Version != 1 {
		t.Errorf("Expected version 1, got %d", job.Version)
	}
	if !job.Active {
		t.Error("Job should be active by default")
	}
	if job.Paused {
		t.Error("Job should not be paused by default")
	}
	if len(job.Tasks) != 0 {
		t.Errorf("Expected 0 tasks, got %d", len(job.Tasks))
	}
}

func TestAddTask(t *testing.T) {
	config := JobConfig{
		TaskExecutionMode: TaskExecutionModeSequential,
		FailureStrategy:   FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := NewJob("test-job", tick, config)

	taskFunc := func(ctx context.Context, execCtx *ExecutionContext) error {
		return nil
	}

	task1 := job.AddTask("task-1", taskFunc, TaskConfig{})
	if task1.Name != "task-1" {
		t.Errorf("Expected task name 'task-1', got '%s'", task1.Name)
	}
	if task1.Order != 0 {
		t.Errorf("Expected order 0, got %d", task1.Order)
	}

	task2 := job.AddTask("task-2", taskFunc, TaskConfig{})
	if task2.Order != 1 {
		t.Errorf("Expected order 1, got %d", task2.Order)
	}

	if len(job.Tasks) != 2 {
		t.Errorf("Expected 2 tasks, got %d", len(job.Tasks))
	}
}

func TestGetEffectiveConfig(t *testing.T) {
	timeout := 10 * time.Minute
	taskTimeout := 5 * time.Minute
	overrideTimeout := 2 * time.Minute

	retryPolicy := &RetryPolicy{
		MaxRetries:    3,
		RetryInterval: 1 * time.Minute,
		BackoffFactor: 2.0,
	}

	overrideRetryPolicy := &RetryPolicy{
		MaxRetries:    10,
		RetryInterval: 30 * time.Second,
		BackoffFactor: 1.5,
	}

	config := JobConfig{
		TaskExecutionMode: TaskExecutionModeSequential,
		FailureStrategy:   FailureStrategyFailFast,
		ExecutionTimeout:  timeout,
		TaskConfig: TaskConfig{
			TaskTimeout: &taskTimeout,
			RetryPolicy: retryPolicy,
		},
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := NewJob("test-job", tick, config)

	taskFunc := func(ctx context.Context, execCtx *ExecutionContext) error {
		return nil
	}

	// Task without overrides - should inherit job config
	task1 := job.AddTask("task-1", taskFunc, TaskConfig{})
	effective1 := job.GetEffectiveConfig(task1)

	if *effective1.TaskTimeout != taskTimeout {
		t.Errorf("Expected task timeout %v, got %v", taskTimeout, *effective1.TaskTimeout)
	}
	if effective1.RetryPolicy.MaxRetries != 3 {
		t.Errorf("Expected max retries 3, got %d", effective1.RetryPolicy.MaxRetries)
	}

	// Task with overrides - should use task-specific config
	overrideStrategy := FailureStrategyContinue
	task2 := job.AddTask("task-2", taskFunc, TaskConfig{
		FailureStrategy: &overrideStrategy,
		TaskTimeout:     &overrideTimeout,
		RetryPolicy:     overrideRetryPolicy,
	})
	effective2 := job.GetEffectiveConfig(task2)

	if *effective2.FailureStrategy != FailureStrategyContinue {
		t.Errorf("Expected failure strategy Continue, got %v", *effective2.FailureStrategy)
	}
	if *effective2.TaskTimeout != overrideTimeout {
		t.Errorf("Expected task timeout %v, got %v", overrideTimeout, *effective2.TaskTimeout)
	}
	if effective2.RetryPolicy.MaxRetries != 10 {
		t.Errorf("Expected max retries 10, got %d", effective2.RetryPolicy.MaxRetries)
	}
}

func TestJobDeterministicID(t *testing.T) {
	config := JobConfig{
		TaskExecutionMode: TaskExecutionModeSequential,
		FailureStrategy:   FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick1, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	tick2, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})

	job1 := NewJob("same-name", tick1, config)
	job2 := NewJob("same-name", tick2, config)

	if job1.ID != job2.ID {
		t.Errorf("Jobs with same name should have same ID. Got %s and %s", job1.ID, job2.ID)
	}

	job3 := NewJob("different-name", tick1, config)
	if job1.ID == job3.ID {
		t.Error("Jobs with different names should have different IDs")
	}
}

func TestTaskDeterministicID(t *testing.T) {
	config := JobConfig{
		TaskExecutionMode: TaskExecutionModeSequential,
		FailureStrategy:   FailureStrategyFailFast,
		ExecutionTimeout:  30 * time.Minute,
	}

	tick, _ := ticker.NewOnceTicker(time.Now().Add(1*time.Hour), ticker.TickerConfig{})
	job := NewJob("test-job", tick, config)

	taskFunc := func(ctx context.Context, execCtx *ExecutionContext) error {
		return nil
	}

	task1 := job.AddTask("same-task", taskFunc, TaskConfig{})
	task2 := job.AddTask("same-task", taskFunc, TaskConfig{})

	if task1.ID != task2.ID {
		t.Errorf("Tasks with same name should have same ID. Got %s and %s", task1.ID, task2.ID)
	}
}
