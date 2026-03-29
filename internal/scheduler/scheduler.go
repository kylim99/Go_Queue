package scheduler

import (
	"context"
	"log/slog"
	"sync"
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
	mu         sync.Mutex         // Start/Stop 동기화를 위한 뮤텍스
	running    bool               // 현재 실행 중 여부
	ctx        context.Context    // 현재 실행 컨텍스트
	cancel     context.CancelFunc // 실행 컨텍스트 취소 함수
	wg         sync.WaitGroup     // 루프 종료 대기를 위한 WaitGroup
}

func New(store *storage.PostgresStorage, q *queue.RedisQueue, jobTimeout time.Duration, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		store:      store,
		queue:      q,
		jobTimeout: jobTimeout,
		logger:     logger,
	}
}

// Start는 스케줄러 루프를 시작한다. 리더 선출 콜백에서 호출된다.
// 이미 실행 중이면 즉시 반환한다.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(3)
	go func() { defer s.wg.Done(); s.runScheduledLoop(s.ctx) }()
	go func() { defer s.wg.Done(); s.runRecoveryLoop(s.ctx) }()
	go func() { defer s.wg.Done(); s.runStaleCheckLoop(s.ctx) }()
	s.running = true
	metrics.SchedulerActive.Set(1)
	s.logger.Info("scheduler started (leader)")
}

// Stop은 스케줄러 루프를 중지하고 모든 루프가 종료될 때까지 대기한다.
// 리더 해제 콜백에서 호출된다.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.cancel()
	s.wg.Wait()
	s.running = false
	metrics.SchedulerActive.Set(0)
	s.logger.Info("scheduler stopped (standby)")
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
