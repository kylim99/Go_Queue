package queue

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestRedis(t *testing.T) (*RedisQueue, func()) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(30 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start redis: %v", err)
	}

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "6379")

	rq, err := NewRedisQueue(ctx, host+":"+port.Port())
	if err != nil {
		t.Fatalf("failed to create redis queue: %v", err)
	}

	return rq, func() {
		rq.Close()
		container.Terminate(ctx)
	}
}

func TestPushAndPop(t *testing.T) {
	rq, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	err := rq.Push(ctx, "test-queue", "job-123")
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	jobID, err := rq.Pop(ctx, []string{"test-queue"}, 2*time.Second)
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if jobID != "job-123" {
		t.Errorf("expected job-123, got %s", jobID)
	}
}

func TestPopTimeout(t *testing.T) {
	rq, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	jobID, err := rq.Pop(ctx, []string{"empty-queue"}, 1*time.Second)
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if jobID != "" {
		t.Errorf("expected empty string for timeout, got %s", jobID)
	}
}

func TestScheduleAndGetDue(t *testing.T) {
	rq, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	past := time.Now().Add(-1 * time.Second)
	err := rq.Schedule(ctx, "job-456", past)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	due, err := rq.GetDueJobs(ctx, time.Now())
	if err != nil {
		t.Fatalf("GetDueJobs failed: %v", err)
	}
	if len(due) != 1 || due[0] != "job-456" {
		t.Errorf("expected [job-456], got %v", due)
	}

	// Should be empty after retrieval (atomically removed)
	due2, _ := rq.GetDueJobs(ctx, time.Now())
	if len(due2) != 0 {
		t.Errorf("expected empty after retrieval, got %v", due2)
	}
}
