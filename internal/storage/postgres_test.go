package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/goqueue/internal/model"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) (*PostgresStorage, func()) {
	t.Helper()
	ctx := context.Background()

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
		t.Fatalf("failed to get connection string: %v", err)
	}

	store, err := NewPostgresStorage(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	if err := store.RunMigrations(); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	cleanup := func() {
		store.Close()
		pgContainer.Terminate(ctx)
	}

	return store, cleanup
}

func TestCreateAndGetJob(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	job := &model.Job{
		Queue:      "email",
		Type:       "send_welcome",
		Payload:    json.RawMessage(`{"to":"user@test.com"}`),
		Status:     model.StatusPending,
		MaxRetries: 3,
	}

	created, err := store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	if created.ID.String() == "" {
		t.Error("expected non-empty ID")
	}
	if created.Status != model.StatusPending {
		t.Errorf("expected status pending, got %s", created.Status)
	}

	fetched, err := store.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if fetched.Queue != "email" {
		t.Errorf("expected queue email, got %s", fetched.Queue)
	}
}

func TestUpdateJobStatus(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	job := &model.Job{
		Queue:      "test",
		Type:       "test_job",
		Payload:    json.RawMessage(`{}`),
		Status:     model.StatusPending,
		MaxRetries: 3,
	}

	created, err := store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	err = store.UpdateJobStatus(ctx, created.ID, model.StatusRunning)
	if err != nil {
		t.Fatalf("UpdateJobStatus failed: %v", err)
	}

	fetched, err := store.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if fetched.Status != model.StatusRunning {
		t.Errorf("expected status running, got %s", fetched.Status)
	}
}

func TestListJobs(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		store.CreateJob(ctx, &model.Job{
			Queue:      "email",
			Type:       "send",
			Payload:    json.RawMessage(`{}`),
			Status:     model.StatusPending,
			MaxRetries: 3,
		})
	}

	jobs, total, err := store.ListJobs(ctx, ListFilter{
		Status: string(model.StatusPending),
		Queue:  "email",
		Page:   1,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(jobs))
	}
}
