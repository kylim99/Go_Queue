package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

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
	store      *storage.PostgresStorage
	queue      *queue.RedisQueue
	registry   *Registry
	queues     []string
	count      int
	jobTimeout time.Duration
	logger     *slog.Logger
	wg         sync.WaitGroup
	cancel     context.CancelFunc
}

func NewPool(store *storage.PostgresStorage, q *queue.RedisQueue, registry *Registry, count int, jobTimeout time.Duration, logger *slog.Logger) *Pool {
	return &Pool{
		store:      store,
		queue:      q,
		registry:   registry,
		count:      count,
		jobTimeout: jobTimeout,
		logger:     logger,
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

func (p *Pool) processJob(ctx context.Context, logger *slog.Logger, jobIDStr string) {
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

	jobCtx, cancel := context.WithTimeout(ctx, p.jobTimeout)
	defer cancel()

	logger.Info("processing job", "id", jobID, "type", job.Type, "queue", job.Queue)

	if err := handler(jobCtx, job.Payload); err != nil {
		logger.Error("job failed", "id", jobID, "error", err)
		p.handleFailure(ctx, job, err)
		return
	}

	if err := p.store.UpdateJobStatus(ctx, jobID, model.StatusCompleted); err != nil {
		logger.Error("update status to completed failed", "id", jobID, "error", err)
	}
	logger.Info("job completed", "id", jobID)
}

func (p *Pool) handleFailure(ctx context.Context, job *model.Job, jobErr error) {
	newRetryCount := job.RetryCount + 1

	if newRetryCount >= job.MaxRetries {
		p.store.UpdateJobError(ctx, job.ID, model.StatusDead, newRetryCount, jobErr.Error())
		p.logger.Warn("job is dead", "id", job.ID, "retries", newRetryCount)
		return
	}

	p.store.UpdateJobError(ctx, job.ID, model.StatusRetrying, newRetryCount, jobErr.Error())
	retryAt := time.Now().Add(model.BackoffDuration(newRetryCount))
	p.queue.Schedule(ctx, job.ID.String(), retryAt)
	p.logger.Info("job scheduled for retry", "id", job.ID, "retry", newRetryCount, "retry_at", retryAt)
}
