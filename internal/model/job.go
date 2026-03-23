package model

import (
	"encoding/json"
	"math"
	"time"

	"github.com/google/uuid"
)

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusScheduled JobStatus = "scheduled"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusRetrying  JobStatus = "retrying"
	StatusDead      JobStatus = "dead"
	StatusCancelled JobStatus = "cancelled"
)

var validStatuses = map[JobStatus]bool{
	StatusPending:   true,
	StatusScheduled: true,
	StatusRunning:   true,
	StatusCompleted: true,
	StatusFailed:    true,
	StatusRetrying:  true,
	StatusDead:      true,
	StatusCancelled: true,
}

var allowedTransitions = map[JobStatus][]JobStatus{
	StatusPending:   {StatusRunning, StatusScheduled, StatusCancelled},
	StatusScheduled: {StatusRunning, StatusCancelled},
	StatusRunning:   {StatusCompleted, StatusFailed, StatusCancelled},
	StatusFailed:    {StatusRetrying, StatusDead},
	StatusRetrying:  {StatusRunning},
	StatusDead:      {StatusPending},
}

func (s JobStatus) IsValid() bool {
	return validStatuses[s]
}

func (s JobStatus) CanTransitionTo(target JobStatus) bool {
	for _, allowed := range allowedTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// BackoffDuration returns the retry delay: 2^retryCount seconds.
func BackoffDuration(retryCount int) time.Duration {
	return time.Duration(math.Pow(2, float64(retryCount))) * time.Second
}

type Job struct {
	ID          uuid.UUID       `json:"id"`
	Queue       string          `json:"queue"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      JobStatus       `json:"status"`
	MaxRetries  int             `json:"max_retries"`
	RetryCount  int             `json:"retry_count"`
	RunAt       *time.Time      `json:"run_at,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
