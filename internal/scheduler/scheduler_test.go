package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestInfra(t *testing.T) (*storage.PostgresStorage, *queue.RedisQueue, func()) {
	t.Helper()
	ctx := context.Background()

	// Start PostgreSQL
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("goqueue_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get pg connection string: %v", err)
	}

	store, err := storage.NewPostgresStorage(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if err := store.RunMigrations(); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Start Redis
	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("failed to start redis: %v", err)
	}

	redisEndpoint, err := redisContainer.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("failed to get redis endpoint: %v", err)
	}

	rq, err := queue.NewRedisQueue(ctx, redisEndpoint)
	if err != nil {
		t.Fatalf("failed to create redis queue: %v", err)
	}

	cleanup := func() {
		rq.Close()
		store.Close()
		pgContainer.Terminate(ctx)
		redisContainer.Terminate(ctx)
	}

	return store, rq, cleanup
}

func TestScheduler_ProcessScheduledJobs(t *testing.T) {
	store, rq, cleanup := setupTestInfra(t)
	defer cleanup()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create a job with run_at in the past
	job, err := store.CreateJob(ctx, &model.Job{
		Queue:      "test",
		Type:       "scheduled_job",
		Payload:    json.RawMessage(`{}`),
		Status:     model.StatusScheduled,
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	// Schedule it for the past
	past := time.Now().Add(-1 * time.Second)
	if err := rq.Schedule(ctx, job.ID.String(), past); err != nil {
		t.Fatalf("schedule failed: %v", err)
	}

	// Run scheduler once
	s := New(store, rq, 30*time.Second, logger)
	s.processScheduledJobs(ctx)

	// The job should now be in the work queue
	jobID, err := rq.Pop(ctx, []string{"test"}, 2*time.Second)
	if err != nil {
		t.Fatalf("pop failed: %v", err)
	}
	if jobID != job.ID.String() {
		t.Errorf("expected job %s, got %s", job.ID, jobID)
	}
}

func TestScheduler_RecoverStaleJobs(t *testing.T) {
	store, rq, cleanup := setupTestInfra(t)
	defer cleanup()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create a job and set it to running with a very old started_at
	job, _ := store.CreateJob(ctx, &model.Job{
		Queue:      "test",
		Type:       "stale_job",
		Payload:    json.RawMessage(`{}`),
		Status:     model.StatusPending,
		MaxRetries: 3,
	})
	store.UpdateJobStatus(ctx, job.ID, model.StatusRunning)

	// Manually backdate started_at to make it stale (hacky but works for test)
	// The GetStaleRunningJobs checks started_at < now - 2*timeout
	// With 1s timeout, it needs started_at older than 2s ago
	time.Sleep(3 * time.Second)

	s := New(store, rq, 1*time.Second, logger)
	s.recoverStaleJobs(ctx)

	// Job should now be failed
	updated, _ := store.GetJob(ctx, job.ID)
	if updated.Status != model.StatusFailed {
		t.Errorf("expected status failed, got %s", updated.Status)
	}
}
