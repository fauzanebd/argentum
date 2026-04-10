package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Status represents job status
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

// Job represents an async processing job
type Job struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Status      Status                 `json:"status"`
	Payload     map[string]interface{} `json:"payload"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Progress    int                    `json:"progress"` // 0-100
	SessionID   string                 `json:"session_id"`
	PhoneNumber string                 `json:"phone_number"`
}

// Manager handles async job processing
type Manager struct {
	jobs     map[string]*Job
	queue    chan string // job ID queue
	mu       sync.RWMutex
	workers  int
	handlers map[string]JobHandler
}

// JobHandler processes a specific job type
type JobHandler func(ctx context.Context, job *Job) error

// NewManager creates a new job manager
func NewManager(workers int) *Manager {
	return &Manager{
		jobs:     make(map[string]*Job),
		queue:    make(chan string, 1000),
		workers:  workers,
		handlers: make(map[string]JobHandler),
	}
}

// RegisterHandler registers a handler for a job type
func (m *Manager) RegisterHandler(jobType string, handler JobHandler) {
	m.handlers[jobType] = handler
}

// CreateJob creates a new async job
func (m *Manager) CreateJob(jobType string, payload map[string]interface{}, sessionID, phoneNumber string) (*Job, error) {
	job := &Job{
		ID:          uuid.New().String(),
		Type:        jobType,
		Status:      StatusPending,
		Payload:     payload,
		CreatedAt:   time.Now(),
		Progress:    0,
		SessionID:   sessionID,
		PhoneNumber: phoneNumber,
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()

	// Add to queue
	select {
	case m.queue <- job.ID:
		logrus.Infof("Job %s created and queued (type: %s)", job.ID, jobType)
	default:
		return nil, fmt.Errorf("job queue is full")
	}

	return job, nil
}

// GetJob retrieves a job by ID
func (m *Manager) GetJob(jobID string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, exists := m.jobs[jobID]
	if !exists {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	return job, nil
}

// Start begins processing jobs with workers
func (m *Manager) Start(ctx context.Context) {
	logrus.Infof("Starting job manager with %d workers", m.workers)

	for i := 0; i < m.workers; i++ {
		go m.worker(ctx, i)
	}
}

// worker processes jobs from the queue
func (m *Manager) worker(ctx context.Context, id int) {
	logrus.Infof("Job worker %d started", id)

	for {
		select {
		case <-ctx.Done():
			logrus.Infof("Job worker %d stopping", id)
			return
		case jobID := <-m.queue:
			if err := m.processJob(ctx, jobID); err != nil {
				logrus.Errorf("Worker %d failed to process job %s: %v", id, jobID, err)
			}
		}
	}
}

// processJob handles a single job
func (m *Manager) processJob(ctx context.Context, jobID string) error {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Update status
	now := time.Now()
	job.Status = StatusProcessing
	job.StartedAt = &now
	m.mu.Unlock()

	logrus.Infof("Processing job %s (type: %s)", jobID, job.Type)

	// Get handler
	handler, exists := m.handlers[job.Type]
	if !exists {
		m.failJob(jobID, fmt.Sprintf("no handler for job type: %s", job.Type))
		return fmt.Errorf("no handler for job type: %s", job.Type)
	}

	// Execute handler
	if err := handler(ctx, job); err != nil {
		m.failJob(jobID, err.Error())
		return err
	}

	// Mark completed
	m.completeJob(jobID, nil)
	return nil
}

// UpdateProgress updates job progress
func (m *Manager) UpdateProgress(jobID string, progress int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, exists := m.jobs[jobID]; exists {
		job.Progress = progress
	}
}

// SetResult sets job result
func (m *Manager) SetResult(jobID string, result map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, exists := m.jobs[jobID]; exists {
		job.Result = result
	}
}

// failJob marks a job as failed
func (m *Manager) failJob(jobID, errorMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, exists := m.jobs[jobID]; exists {
		job.Status = StatusFailed
		job.Error = errorMsg
		now := time.Now()
		job.CompletedAt = &now
		logrus.Errorf("Job %s failed: %s", jobID, errorMsg)
	}
}

// completeJob marks a job as completed
func (m *Manager) completeJob(jobID string, result map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, exists := m.jobs[jobID]; exists {
		job.Status = StatusCompleted
		job.Progress = 100
		if result != nil {
			job.Result = result
		}
		now := time.Now()
		job.CompletedAt = &now
		logrus.Infof("Job %s completed successfully", jobID)
	}
}

// CancelJob cancels a pending job
func (m *Manager) CancelJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, exists := m.jobs[jobID]
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if job.Status != StatusPending {
		return fmt.Errorf("cannot cancel job with status: %s", job.Status)
	}

	job.Status = StatusCancelled
	now := time.Now()
	job.CompletedAt = &now

	return nil
}

// GetJobStats returns statistics about jobs
func (m *Manager) GetJobStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]int{
		string(StatusPending):    0,
		string(StatusProcessing): 0,
		string(StatusCompleted):  0,
		string(StatusFailed):     0,
		string(StatusCancelled):  0,
	}

	var totalDuration time.Duration
	var completedCount int

	for _, job := range m.jobs {
		stats[string(job.Status)]++

		if job.Status == StatusCompleted && job.StartedAt != nil && job.CompletedAt != nil {
			totalDuration += job.CompletedAt.Sub(*job.StartedAt)
			completedCount++
		}
	}

	avgDuration := time.Duration(0)
	if completedCount > 0 {
		avgDuration = totalDuration / time.Duration(completedCount)
	}

	return map[string]interface{}{
		"total_jobs":      len(m.jobs),
		"status_counts":   stats,
		"queue_depth":     len(m.queue),
		"avg_duration_ms": avgDuration.Milliseconds(),
	}
}

// CleanupOldJobs removes old completed jobs
func (m *Manager) CleanupOldJobs(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for id, job := range m.jobs {
		if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusCancelled {
			if job.CompletedAt != nil && job.CompletedAt.Before(cutoff) {
				delete(m.jobs, id)
			}
		}
	}
}

// Serialize returns JSON representation of a job
func (j *Job) Serialize() ([]byte, error) {
	return json.Marshal(j)
}
