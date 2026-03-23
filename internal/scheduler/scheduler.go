package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

type Scheduler struct {
	store      *storage.PostgresStorage
	queue      *queue.RedisQueue
	jobTimeout time.Duration
	logger     *slog.Logger
}

func New(store *storage.PostgresStorage, q *queue.RedisQueue, jobTimeout time.Duration, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		store:      store,
		queue:      q,
		jobTimeout: jobTimeout,
		logger:     logger,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.runScheduledLoop(ctx)
	go s.runRecoveryLoop(ctx)
	go s.runStaleCheckLoop(ctx)
	s.logger.Info("scheduler started")
}

func (s *Scheduler) runScheduledLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processScheduledJobs(ctx)
		}
	}
}

func (s *Scheduler) processScheduledJobs(ctx context.Context) {
	dueJobIDs, err := s.queue.GetDueJobs(ctx, time.Now())
	if err != nil {
		s.logger.Error("get due jobs failed", "error", err)
		return
	}

	for _, jobIDStr := range dueJobIDs {
		id, err := uuid.Parse(jobIDStr)
		if err != nil {
			s.logger.Error("invalid job ID in schedule", "id", jobIDStr, "error", err)
			continue
		}

		job, err := s.store.GetJob(ctx, id)
		if err != nil {
			s.logger.Error("get scheduled job failed", "id", jobIDStr, "error", err)
			continue
		}

		if err := s.queue.Push(ctx, job.Queue, jobIDStr); err != nil {
			s.logger.Error("push scheduled job failed", "id", jobIDStr, "error", err)
			continue
		}

		if job.Status == model.StatusRetrying {
			s.store.UpdateJobStatus(ctx, job.ID, model.StatusPending)
		}

		s.logger.Debug("scheduled job enqueued", "id", jobIDStr, "queue", job.Queue)
	}
}

func (s *Scheduler) runRecoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.recoverUnqueuedJobs(ctx)
		}
	}
}

func (s *Scheduler) recoverUnqueuedJobs(ctx context.Context) {
	jobs, err := s.store.GetPendingUnqueued(ctx, 5*time.Second)
	if err != nil {
		s.logger.Error("get pending unqueued failed", "error", err)
		return
	}

	for _, job := range jobs {
		if err := s.queue.Push(ctx, job.Queue, job.ID.String()); err != nil {
			s.logger.Error("recover push failed", "id", job.ID, "error", err)
			continue
		}
		s.logger.Info("recovered unqueued job", "id", job.ID)
	}
}

func (s *Scheduler) runStaleCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.recoverStaleJobs(ctx)
		}
	}
}

func (s *Scheduler) recoverStaleJobs(ctx context.Context) {
	jobs, err := s.store.GetStaleRunningJobs(ctx, s.jobTimeout)
	if err != nil {
		s.logger.Error("get stale jobs failed", "error", err)
		return
	}

	for _, job := range jobs {
		s.store.UpdateJobError(ctx, job.ID, model.StatusFailed, job.RetryCount, "job timed out (stale)")
		s.logger.Warn("recovered stale job", "id", job.ID)
	}
}
