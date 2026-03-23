package model

import (
	"testing"
	"time"
)

func TestJobStatus_IsValid(t *testing.T) {
	tests := []struct {
		status JobStatus
		valid  bool
	}{
		{StatusPending, true},
		{StatusScheduled, true},
		{StatusRunning, true},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusRetrying, true},
		{StatusDead, true},
		{StatusCancelled, true},
		{JobStatus("invalid"), false},
	}

	for _, tt := range tests {
		if got := tt.status.IsValid(); got != tt.valid {
			t.Errorf("JobStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.valid)
		}
	}
}

func TestJobStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		from, to JobStatus
		allowed  bool
	}{
		{StatusPending, StatusRunning, true},
		{StatusPending, StatusScheduled, true},
		{StatusPending, StatusCancelled, true},
		{StatusScheduled, StatusRunning, true},
		{StatusScheduled, StatusCancelled, true},
		{StatusRunning, StatusCompleted, true},
		{StatusRunning, StatusFailed, true},
		{StatusRunning, StatusCancelled, true},
		{StatusFailed, StatusRetrying, true},
		{StatusFailed, StatusDead, true},
		{StatusRetrying, StatusRunning, true},
		{StatusDead, StatusPending, true},
		{StatusCompleted, StatusRunning, false},
		{StatusCancelled, StatusRunning, false},
	}

	for _, tt := range tests {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.allowed {
			t.Errorf("%s -> %s: got %v, want %v", tt.from, tt.to, got, tt.allowed)
		}
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
	}

	for _, tt := range tests {
		got := BackoffDuration(tt.retryCount)
		if got != tt.expected {
			t.Errorf("BackoffDuration(%d) = %v, want %v", tt.retryCount, got, tt.expected)
		}
	}
}
