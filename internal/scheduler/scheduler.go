package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/goqueue/internal/metrics"
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

// Start는 스케줄러의 세 가지 백그라운드 루프를 시작한다.
// 1) 예약된 작업 처리 2) 미큐잉 작업 복구 3) 정체된(stale) 작업 복구
func (s *Scheduler) Start(ctx context.Context) {
	go s.runScheduledLoop(ctx)
	go s.runRecoveryLoop(ctx)
	go s.runStaleCheckLoop(ctx)
	// 스케줄러 활성 상태 메트릭 설정
	metrics.SchedulerActive.Set(1)
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

// processScheduledJobs는 실행 시간이 도래한 예약 작업을 Redis에서 가져와 해당 큐에 넣는다.
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

// recoverUnqueuedJobs는 pending 상태이지만 큐에 들어가지 않은 작업을 찾아 다시 큐에 넣는다.
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

// recoverStaleJobs는 실행 시간이 초과된 정체(stale) 작업을 찾아 실패 처리한다.
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
