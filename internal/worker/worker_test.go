package worker

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goqueue/internal/model"
)

func TestHandlerRegistry(t *testing.T) {
	r := NewRegistry()

	called := atomic.Bool{}
	r.Register("test_job", func(ctx context.Context, payload json.RawMessage) error {
		called.Store(true)
		return nil
	})

	handler, ok := r.Get("test_job")
	if !ok {
		t.Fatal("expected handler to be registered")
	}

	err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called.Load() {
		t.Error("handler was not called")
	}

	_, ok = r.Get("unknown")
	if ok {
		t.Error("expected no handler for unknown type")
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		retry    int
		expected time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
	}

	for _, tt := range tests {
		got := model.BackoffDuration(tt.retry)
		if got != tt.expected {
			t.Errorf("BackoffDuration(%d) = %v, want %v", tt.retry, got, tt.expected)
		}
	}
}
