package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/goqueue/internal/dashboard"
	"github.com/goqueue/internal/metrics"
	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

type Handler func(ctx context.Context, payload json.RawMessage) error

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(jobType string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[jobType] = h
}

func (r *Registry) Get(jobType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[jobType]
	return h, ok
}

type Pool struct {
	store       *storage.PostgresStorage
	queue       *queue.RedisQueue
	registry    *Registry
	queues      []string
	count       int
	jobTimeout  time.Duration
	redisClient *redis.Client
	logger      *slog.Logger
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

func NewPool(store *storage.PostgresStorage, q *queue.RedisQueue, registry *Registry, count int, jobTimeout time.Duration, redisClient *redis.Client, logger *slog.Logger) *Pool {
	return &Pool{
		store:       store,
		queue:       q,
		registry:    registry,
		count:       count,
		jobTimeout:  jobTimeout,
		redisClient: redisClient,
		logger:      logger,
	}
}

func (p *Pool) Start(ctx context.Context, queues []string) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.queues = queues
	for i := 0; i < p.count; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
	p.logger.Info("worker pool started", "count", p.count, "queues", queues)
}

func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.logger.Info("worker pool stopped")
}

func (p *Pool) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	logger := p.logger.With("worker_id", id)

	for {
		select {
		case <-ctx.Done():
			logger.Debug("worker shutting down")
			return
		default:
		}

		// Multi-key BRPOP: waits on all queues simultaneously
		jobID, err := p.queue.Pop(ctx, p.queues, 1*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("pop failed", "error", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if jobID == "" {
			continue
		}

		p.processJob(ctx, logger, jobID)
	}
}

// processJob은 큐에서 가져온 작업을 실행하고, 성공/실패에 따라 상태를 업데이트한다.
// 활성 워커 수, 처리 시간, 성공/실패 카운터 등 Prometheus 메트릭을 기록한다.
func (p *Pool) processJob(ctx context.Context, logger *slog.Logger, jobIDStr string) {
	// 활성 워커 수 추적 (작업 시작 시 증가, 종료 시 감소)
	metrics.ActiveWorkers.Inc()
	defer metrics.ActiveWorkers.Dec()

	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		logger.Error("invalid job ID", "id", jobIDStr, "error", err)
		return
	}

	job, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		logger.Error("get job failed", "id", jobID, "error", err)
		return
	}

	handler, ok := p.registry.Get(job.Type)
	if !ok {
		logger.Error("no handler for job type", "type", job.Type, "id", jobID)
		p.store.UpdateJobError(ctx, jobID, model.StatusFailed, job.RetryCount, fmt.Sprintf("no handler for type: %s", job.Type))
		return
	}

	if err := p.store.UpdateJobStatus(ctx, jobID, model.StatusRunning); err != nil {
		logger.Error("update status to running failed", "id", jobID, "error", err)
		return
	}
	p.publishEvent(ctx, "job.running", job)

	jobCtx, cancel := context.WithTimeout(ctx, p.jobTimeout)
	defer cancel()

	logger.Info("processing job", "id", jobID, "type", job.Type, "queue", job.Queue)

	// 작업 처리 시간 측정 시작
	start := time.Now()

	if err := handler(jobCtx, job.Payload); err != nil {
		logger.Error("job failed", "id", jobID, "error", err)
		// 실패한 작업의 처리 시간 및 에러 메트릭 기록
		metrics.JobDuration.WithLabelValues(job.Type).Observe(time.Since(start).Seconds())
		metrics.JobsProcessed.WithLabelValues(job.Type, "failed").Inc()
		metrics.ErrorsTotal.WithLabelValues(job.Type).Inc()
		p.handleFailure(ctx, job, err)
		return
	}

	// 성공한 작업의 처리 시간 및 완료 메트릭 기록
	metrics.JobDuration.WithLabelValues(job.Type).Observe(time.Since(start).Seconds())
	metrics.JobsProcessed.WithLabelValues(job.Type, "success").Inc()

	if err := p.store.UpdateJobStatus(ctx, jobID, model.StatusCompleted); err != nil {
		logger.Error("update status to completed failed", "id", jobID, "error", err)
	}
	p.publishEvent(ctx, "job.completed", job)
	logger.Info("job completed", "id", jobID)
}

// handleFailure는 작업 실패 시 재시도 또는 최종 실패(dead) 처리를 수행한다.
// 최대 재시도 횟수를 초과하면 dead 상태로 전환하고, 그렇지 않으면 지수 백오프로 재시도를 예약한다.
func (p *Pool) handleFailure(ctx context.Context, job *model.Job, jobErr error) {
	newRetryCount := job.RetryCount + 1

	if newRetryCount >= job.MaxRetries {
		p.store.UpdateJobError(ctx, job.ID, model.StatusDead, newRetryCount, jobErr.Error())
		p.publishEvent(ctx, "job.dead", job)
		p.logger.Warn("job is dead", "id", job.ID, "retries", newRetryCount)
		return
	}

	// 재시도 상태로 전환하고 재시도 메트릭 기록
	p.store.UpdateJobError(ctx, job.ID, model.StatusRetrying, newRetryCount, jobErr.Error())
	p.publishEvent(ctx, "job.retrying", job)
	metrics.RetriesTotal.WithLabelValues(job.Type).Inc()
	retryAt := time.Now().Add(model.BackoffDuration(newRetryCount))
	p.queue.Schedule(ctx, job.ID.String(), retryAt)
	p.logger.Info("job scheduled for retry", "id", job.ID, "retry", newRetryCount, "retry_at", retryAt)
}

// publishEvent는 작업 상태 변경 이벤트를 Redis Pub/Sub으로 발행한다.
func (p *Pool) publishEvent(ctx context.Context, eventType string, job *model.Job) {
	event := dashboard.JobEvent{
		Type:   eventType,
		JobID:  job.ID.String(),
		Queue:  job.Queue,
		Status: string(job.Status),
	}
	if err := dashboard.PublishJobEvent(ctx, p.redisClient, event); err != nil {
		p.logger.Error("publish job event failed", "type", eventType, "id", job.ID, "error", err)
	}
}
