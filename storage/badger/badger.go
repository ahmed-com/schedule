package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ahmed-com/schedule"
	"github.com/ahmed-com/schedule/storage"
	"github.com/dgraph-io/badger/v4"
)

// BadgerStorage implements the Storage interface using BadgerDB
type BadgerStorage struct {
	db *badger.DB
}

// NewBadgerStorage creates a new BadgerDB storage instance
func NewBadgerStorage(path string) (*BadgerStorage, error) {
	opts := badger.DefaultOptions(path)
	opts.Logger = nil // Disable default logging
	
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &BadgerStorage{db: db}, nil
}

// Hierarchical key schema implementation
func (s *BadgerStorage) jobKey(id string) []byte {
	return []byte(fmt.Sprintf("job/%s", id))
}

func (s *BadgerStorage) taskKey(jobID, taskID string) []byte {
	return []byte(fmt.Sprintf("job/%s/task/%s", jobID, taskID))
}

func (s *BadgerStorage) occurrenceKey(jobID, occurrenceID string) []byte {
	return []byte(fmt.Sprintf("job/%s/occurrence/%s", jobID, occurrenceID))
}

func (s *BadgerStorage) taskRunKey(jobID, occurrenceID, taskID string) []byte {
	return []byte(fmt.Sprintf("job/%s/occurrence/%s/task/%s/run", jobID, occurrenceID, taskID))
}

func (s *BadgerStorage) attemptKey(jobID, occurrenceID, taskID, attemptID string) []byte {
	return []byte(fmt.Sprintf("job/%s/occurrence/%s/task/%s/run/attempt/%s", jobID, occurrenceID, taskID, attemptID))
}

// Job operations

func (s *BadgerStorage) CreateJob(ctx context.Context, job *storage.Job) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.jobKey(job.ID)
		
		// Check if already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("job already exists: %s", job.ID)
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
		
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) GetJob(ctx context.Context, jobID string) (*storage.Job, error) {
	var job storage.Job
	
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(s.jobKey(jobID))
		if err != nil {
			return err
		}
		
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &job)
		})
	})
	
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	if err != nil {
		return nil, err
	}
	
	return &job, nil
}

func (s *BadgerStorage) UpdateJob(ctx context.Context, job *storage.Job) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.jobKey(job.ID)
		
		// Check if exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("job not found: %s", job.ID)
		}
		if err != nil {
			return err
		}
		
		job.UpdatedAt = time.Now()
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) DeleteJob(ctx context.Context, jobID string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(s.jobKey(jobID))
	})
}

func (s *BadgerStorage) ListJobs(ctx context.Context) ([]*storage.Job, error) {
	var jobs []*storage.Job
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("job/")
		it := txn.NewIterator(opts)
		defer it.Close()
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())
			
			// Skip non-job keys (tasks, occurrences, etc.)
			if strings.Count(key, "/") > 1 {
				continue
			}
			
			err := item.Value(func(val []byte) error {
				var job storage.Job
				if err := json.Unmarshal(val, &job); err != nil {
					return err
				}
				jobs = append(jobs, &job)
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return jobs, err
}

// Task operations

func (s *BadgerStorage) CreateTask(ctx context.Context, task *storage.Task) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.taskKey(task.JobID, task.ID)
		
		// Check if already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("task already exists: %s", task.ID)
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
		
		data, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) GetTask(ctx context.Context, taskID string) (*storage.Task, error) {
	var task storage.Task
	var found bool
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		
		// Search for task by ID across all jobs
		prefix := []byte("job/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			
			if strings.Contains(key, "/task/") && strings.HasSuffix(key, taskID) {
				err := item.Value(func(val []byte) error {
					return json.Unmarshal(val, &task)
				})
				if err != nil {
					return err
				}
				found = true
				return nil
			}
		}
		
		return nil
	})
	
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	
	return &task, nil
}

func (s *BadgerStorage) UpdateTask(ctx context.Context, task *storage.Task) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.taskKey(task.JobID, task.ID)
		
		// Check if exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("task not found: %s", task.ID)
		}
		if err != nil {
			return err
		}
		
		task.UpdatedAt = time.Now()
		data, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) DeleteTask(ctx context.Context, taskID string) error {
	// Need to find the task first to get its job ID
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(s.taskKey(task.JobID, task.ID))
	})
}

func (s *BadgerStorage) ListTasksByJobID(ctx context.Context, jobID string) ([]*storage.Task, error) {
	var tasks []*storage.Task
	
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("job/%s/task/", jobID))
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			
			err := item.Value(func(val []byte) error {
				var task storage.Task
				if err := json.Unmarshal(val, &task); err != nil {
					return err
				}
				tasks = append(tasks, &task)
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return tasks, err
}

// JobOccurrence operations

func (s *BadgerStorage) CreateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.occurrenceKey(occurrence.JobID, occurrence.ID)
		
		// Check if already exists (atomic create-if-not-exists)
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("occurrence already exists: %s", occurrence.ID)
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
		
		data, err := json.Marshal(occurrence)
		if err != nil {
			return fmt.Errorf("failed to marshal occurrence: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) GetJobOccurrence(ctx context.Context, occurrenceID string) (*storage.JobOccurrence, error) {
	var occurrence storage.JobOccurrence
	var found bool
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		
		// Search for occurrence by ID across all jobs
		prefix := []byte("job/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			
			if strings.Contains(key, "/occurrence/") && strings.HasSuffix(key, occurrenceID) {
				err := item.Value(func(val []byte) error {
					return json.Unmarshal(val, &occurrence)
				})
				if err != nil {
					return err
				}
				found = true
				return nil
			}
		}
		
		return nil
	})
	
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("occurrence not found: %s", occurrenceID)
	}
	
	return &occurrence, nil
}

func (s *BadgerStorage) UpdateJobOccurrence(ctx context.Context, occurrence *storage.JobOccurrence) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.occurrenceKey(occurrence.JobID, occurrence.ID)
		
		// Check if exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("occurrence not found: %s", occurrence.ID)
		}
		if err != nil {
			return err
		}
		
		occurrence.UpdatedAt = time.Now()
		data, err := json.Marshal(occurrence)
		if err != nil {
			return fmt.Errorf("failed to marshal occurrence: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) DeleteJobOccurrence(ctx context.Context, occurrenceID string) error {
	// Need to find the occurrence first to get its job ID
	occurrence, err := s.GetJobOccurrence(ctx, occurrenceID)
	if err != nil {
		return err
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(s.occurrenceKey(occurrence.JobID, occurrence.ID))
	})
}

func (s *BadgerStorage) ListRunningOccurrences(ctx context.Context) ([]*storage.JobOccurrence, error) {
	var occurrences []*storage.JobOccurrence
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		
		prefix := []byte("job/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			
			if !strings.Contains(key, "/occurrence/") {
				continue
			}
			
			err := item.Value(func(val []byte) error {
				var occ storage.JobOccurrence
				if err := json.Unmarshal(val, &occ); err != nil {
					return err
				}
				if occ.Status == jobistemer.JobStatusRunning {
					occurrences = append(occurrences, &occ)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return occurrences, err
}

func (s *BadgerStorage) ListStaleOccurrences(ctx context.Context, threshold time.Duration) ([]*storage.JobOccurrence, error) {
	var occurrences []*storage.JobOccurrence
	cutoff := time.Now().Add(-threshold)
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		
		prefix := []byte("job/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			
			if !strings.Contains(key, "/occurrence/") {
				continue
			}
			
			err := item.Value(func(val []byte) error {
				var occ storage.JobOccurrence
				if err := json.Unmarshal(val, &occ); err != nil {
					return err
				}
				if occ.Status == jobistemer.JobStatusRunning && occ.StartTime != nil && occ.StartTime.Before(cutoff) {
					occurrences = append(occurrences, &occ)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return occurrences, err
}

func (s *BadgerStorage) ListQueuedOccurrencesByJobID(ctx context.Context, jobID string) ([]*storage.JobOccurrence, error) {
	var occurrences []*storage.JobOccurrence
	
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("job/%s/occurrence/", jobID))
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			
			err := item.Value(func(val []byte) error {
				var occ storage.JobOccurrence
				if err := json.Unmarshal(val, &occ); err != nil {
					return err
				}
				if occ.Status == jobistemer.JobStatusQueued {
					occurrences = append(occurrences, &occ)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return occurrences, err
}

// TaskRun operations

func (s *BadgerStorage) CreateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	// Need to extract job ID and occurrence ID from the structure
	// For simplicity, we'll store task runs with a simplified key
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf("taskrun/%s", run.ID))
		
		// Check if already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("task run already exists: %s", run.ID)
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
		
		data, err := json.Marshal(run)
		if err != nil {
			return fmt.Errorf("failed to marshal task run: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) GetTaskRun(ctx context.Context, runID string) (*storage.TaskRun, error) {
	var run storage.TaskRun
	
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(fmt.Sprintf("taskrun/%s", runID)))
		if err != nil {
			return err
		}
		
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &run)
		})
	})
	
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("task run not found: %s", runID)
	}
	if err != nil {
		return nil, err
	}
	
	return &run, nil
}

func (s *BadgerStorage) UpdateTaskRun(ctx context.Context, run *storage.TaskRun) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf("taskrun/%s", run.ID))
		
		// Check if exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("task run not found: %s", run.ID)
		}
		if err != nil {
			return err
		}
		
		run.UpdatedAt = time.Now()
		data, err := json.Marshal(run)
		if err != nil {
			return fmt.Errorf("failed to marshal task run: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) DeleteTaskRun(ctx context.Context, runID string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(fmt.Sprintf("taskrun/%s", runID)))
	})
}

func (s *BadgerStorage) ListTaskRunsByOccurrenceID(ctx context.Context, occurrenceID string) ([]*storage.TaskRun, error) {
	var runs []*storage.TaskRun
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("taskrun/")
		it := txn.NewIterator(opts)
		defer it.Close()
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			
			err := item.Value(func(val []byte) error {
				var run storage.TaskRun
				if err := json.Unmarshal(val, &run); err != nil {
					return err
				}
				if run.OccurrenceID == occurrenceID {
					runs = append(runs, &run)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return runs, err
}

// ExecutionAttempt operations

func (s *BadgerStorage) CreateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf("attempt/%s", attempt.ID))
		
		// Check if already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("execution attempt already exists: %s", attempt.ID)
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
		
		data, err := json.Marshal(attempt)
		if err != nil {
			return fmt.Errorf("failed to marshal execution attempt: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) GetExecutionAttempt(ctx context.Context, attemptID string) (*storage.ExecutionAttempt, error) {
	var attempt storage.ExecutionAttempt
	
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(fmt.Sprintf("attempt/%s", attemptID)))
		if err != nil {
			return err
		}
		
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &attempt)
		})
	})
	
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("execution attempt not found: %s", attemptID)
	}
	if err != nil {
		return nil, err
	}
	
	return &attempt, nil
}

func (s *BadgerStorage) UpdateExecutionAttempt(ctx context.Context, attempt *storage.ExecutionAttempt) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf("attempt/%s", attempt.ID))
		
		// Check if exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("execution attempt not found: %s", attempt.ID)
		}
		if err != nil {
			return err
		}
		
		data, err := json.Marshal(attempt)
		if err != nil {
			return fmt.Errorf("failed to marshal execution attempt: %w", err)
		}
		
		return txn.Set(key, data)
	})
}

func (s *BadgerStorage) ListExecutionAttemptsByTaskRunID(ctx context.Context, taskRunID string) ([]*storage.ExecutionAttempt, error) {
	var attempts []*storage.ExecutionAttempt
	
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("attempt/")
		it := txn.NewIterator(opts)
		defer it.Close()
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			
			err := item.Value(func(val []byte) error {
				var attempt storage.ExecutionAttempt
				if err := json.Unmarshal(val, &attempt); err != nil {
					return err
				}
				if attempt.TaskRunID == taskRunID {
					attempts = append(attempts, &attempt)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		
		return nil
	})
	
	return attempts, err
}

// Close closes the database connection
func (s *BadgerStorage) Close() error {
	return s.db.Close()
}
