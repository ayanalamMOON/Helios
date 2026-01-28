package queue

import (
	"encoding/json"
	"fmt"
	"time"
)

// JobStatus represents the current status of a job
type JobStatus string

const (
	StatusPending    JobStatus = "PENDING"
	StatusInProgress JobStatus = "IN_PROGRESS"
	StatusDone       JobStatus = "DONE"
	StatusFailed     JobStatus = "FAILED"
	StatusDLQ        JobStatus = "DLQ" // Dead Letter Queue
)

// Job represents a job in the queue
type Job struct {
	ID                string                 `json:"id"`
	Payload           map[string]interface{} `json:"payload"`
	Status            JobStatus              `json:"status"`
	Attempts          int                    `json:"attempts"`
	MaxAttempts       int                    `json:"max_attempts"`
	LeaseUntil        time.Time              `json:"lease_until"`
	CreatedAt         time.Time              `json:"created_at"`
	LastUpdated       time.Time              `json:"last_updated"`
	VisibilityTimeout int64                  `json:"visibility_timeout"` // seconds
	DedupID           string                 `json:"dedup_id,omitempty"`
}

// Store defines the interface for queue storage
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl int64)
	Delete(key string) bool
	Scan(prefix string) []string
}

// Queue manages job queueing and processing
type Queue struct {
	store              Store
	defaultMaxAttempts int
	defaultVisibility  int64 // seconds
}

// Config holds queue configuration
type Config struct {
	DefaultMaxAttempts       int
	DefaultVisibilityTimeout int64
}

// NewQueue creates a new queue
func NewQueue(store Store, cfg *Config) *Queue {
	return &Queue{
		store:              store,
		defaultMaxAttempts: cfg.DefaultMaxAttempts,
		defaultVisibility:  cfg.DefaultVisibilityTimeout,
	}
}

// Enqueue adds a job to the queue
func (q *Queue) Enqueue(payload map[string]interface{}, dedupID string) (string, error) {
	// Check for duplicate if dedupID provided
	if dedupID != "" {
		dedupKey := fmt.Sprintf("job:dedup:%s", dedupID)
		if _, exists := q.store.Get(dedupKey); exists {
			return "", fmt.Errorf("duplicate job with dedup_id: %s", dedupID)
		}
	}

	// Generate job ID
	jobID := generateJobID()

	job := &Job{
		ID:                jobID,
		Payload:           payload,
		Status:            StatusPending,
		Attempts:          0,
		MaxAttempts:       q.defaultMaxAttempts,
		CreatedAt:         time.Now(),
		LastUpdated:       time.Now(),
		VisibilityTimeout: q.defaultVisibility,
		DedupID:           dedupID,
	}

	// Save job
	if err := q.saveJob(job); err != nil {
		return "", err
	}

	// Mark dedup if needed
	if dedupID != "" {
		dedupKey := fmt.Sprintf("job:dedup:%s", dedupID)
		q.store.Set(dedupKey, []byte(jobID), 3600) // expire after 1 hour
	}

	return jobID, nil
}

// Dequeue retrieves the next available job
func (q *Queue) Dequeue(workerID string) (*Job, error) {
	// Find pending jobs
	jobs := q.findPendingJobs()

	for _, jobID := range jobs {
		job, err := q.getJob(jobID)
		if err != nil {
			continue
		}

		// Check if job is eligible
		if job.Status == StatusPending ||
			(job.Status == StatusInProgress && time.Now().After(job.LeaseUntil)) {
			// Lease the job
			job.Status = StatusInProgress
			job.Attempts++
			job.LeaseUntil = time.Now().Add(time.Duration(job.VisibilityTimeout) * time.Second)
			job.LastUpdated = time.Now()

			if err := q.saveJob(job); err != nil {
				return nil, err
			}

			return job, nil
		}
	}

	return nil, fmt.Errorf("no jobs available")
}

// Ack acknowledges successful completion of a job
func (q *Queue) Ack(jobID string) error {
	job, err := q.getJob(jobID)
	if err != nil {
		return err
	}

	job.Status = StatusDone
	job.LastUpdated = time.Now()

	return q.saveJob(job)
}

// Nack indicates job processing failed
func (q *Queue) Nack(jobID string, reason string) error {
	job, err := q.getJob(jobID)
	if err != nil {
		return err
	}

	// Check if max attempts exceeded
	if job.Attempts >= job.MaxAttempts {
		job.Status = StatusDLQ
	} else {
		job.Status = StatusPending
		job.LeaseUntil = time.Time{} // reset lease
	}

	job.LastUpdated = time.Now()

	return q.saveJob(job)
}

// GetJob retrieves a job by ID
func (q *Queue) GetJob(jobID string) (*Job, error) {
	return q.getJob(jobID)
}

// DeleteJob deletes a job
func (q *Queue) DeleteJob(jobID string) error {
	jobKey := fmt.Sprintf("job:%s", jobID)
	q.store.Delete(jobKey)
	return nil
}

// ListJobs lists jobs by status
func (q *Queue) ListJobs(status JobStatus) ([]*Job, error) {
	jobKeys := q.store.Scan("job:")
	var jobs []*Job

	for _, key := range jobKeys {
		// Skip dedup keys
		if len(key) > 9 && key[:9] == "job:dedup" {
			continue
		}

		data, exists := q.store.Get(key)
		if !exists {
			continue
		}

		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}

		if status == "" || job.Status == status {
			jobs = append(jobs, &job)
		}
	}

	return jobs, nil
}

// ExpireLeases moves expired leases back to pending
func (q *Queue) ExpireLeases() error {
	jobs, err := q.ListJobs(StatusInProgress)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, job := range jobs {
		if now.After(job.LeaseUntil) {
			// Check max attempts
			if job.Attempts >= job.MaxAttempts {
				job.Status = StatusDLQ
			} else {
				job.Status = StatusPending
			}
			job.LastUpdated = now
			q.saveJob(job)
		}
	}

	return nil
}

// Helper functions

func (q *Queue) saveJob(job *Job) error {
	jobKey := fmt.Sprintf("job:%s", job.ID)
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	q.store.Set(jobKey, data, 0)
	return nil
}

func (q *Queue) getJob(jobID string) (*Job, error) {
	jobKey := fmt.Sprintf("job:%s", jobID)
	data, exists := q.store.Get(jobKey)
	if !exists {
		return nil, fmt.Errorf("job not found")
	}

	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

func (q *Queue) findPendingJobs() []string {
	allKeys := q.store.Scan("job:")
	var jobIDs []string

	for _, key := range allKeys {
		// Skip dedup keys
		if len(key) > 9 && key[:9] == "job:dedup" {
			continue
		}
		// Extract job ID from key "job:{id}"
		if len(key) > 4 {
			jobIDs = append(jobIDs, key[4:])
		}
	}

	return jobIDs
}

func generateJobID() string {
	return fmt.Sprintf("job_%d", time.Now().UnixNano())
}

// DefaultConfig returns default queue configuration
func DefaultConfig() *Config {
	return &Config{
		DefaultMaxAttempts:       3,
		DefaultVisibilityTimeout: 300, // 5 minutes
	}
}
