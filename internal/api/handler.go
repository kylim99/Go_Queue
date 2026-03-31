package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/goqueue/internal/dashboard"
	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

type Handler struct {
	store       *storage.PostgresStorage
	queue       *queue.RedisQueue
	redisClient *redis.Client
	logger      *slog.Logger
}

func NewHandler(store *storage.PostgresStorage, q *queue.RedisQueue, logger *slog.Logger) *Handler {
	return &Handler{store: store, queue: q, redisClient: q.Client(), logger: logger}
}

type CreateJobRequest struct {
	Queue      string          `json:"queue"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	MaxRetries *int            `json:"max_retries,omitempty"`
	RunAt      *string         `json:"run_at,omitempty"`
}

// CreateJob godoc
// @Summary 새 작업 생성
// @Description 지정된 큐에 새 작업을 생성한다. run_at을 지정하면 예약 작업이 된다.
// @Tags jobs
// @Accept json
// @Produce json
// @Param request body CreateJobRequest true "작업 생성 요청"
// @Success 201 {object} model.Job "생성된 작업"
// @Failure 400 {object} ErrorResponse "잘못된 요청"
// @Failure 500 {object} ErrorResponse "서버 에러"
// @Security ApiKeyAuth
// @Router /jobs [post]
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Queue == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "queue and type are required")
		return
	}

	job := &model.Job{
		Queue:      req.Queue,
		Type:       req.Type,
		Payload:    req.Payload,
		Status:     model.StatusPending,
		MaxRetries: 3,
	}

	if req.MaxRetries != nil {
		job.MaxRetries = *req.MaxRetries
	}

	if req.Payload == nil {
		job.Payload = json.RawMessage(`{}`)
	}

	if req.RunAt != nil {
		t, err := time.Parse(time.RFC3339, *req.RunAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid run_at format, use RFC3339")
			return
		}
		job.RunAt = &t
		job.Status = model.StatusScheduled
	}

	created, err := h.store.CreateJob(r.Context(), job)
	if err != nil {
		h.logger.Error("create job failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create job")
		return
	}

	if job.RunAt != nil {
		if err := h.queue.Schedule(r.Context(), created.ID.String(), *job.RunAt); err != nil {
			h.logger.Error("schedule job failed", "id", created.ID, "error", err)
		}
	} else {
		if err := h.queue.Push(r.Context(), job.Queue, created.ID.String()); err != nil {
			h.logger.Error("push job failed", "id", created.ID, "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, created)
}

// GetJob godoc
// @Summary 작업 상세 조회
// @Description ID로 작업의 상세 정보를 조회한다.
// @Tags jobs
// @Produce json
// @Param id path string true "작업 ID (UUID)"
// @Success 200 {object} model.Job "작업 상세"
// @Failure 400 {object} ErrorResponse "잘못된 ID"
// @Failure 404 {object} ErrorResponse "작업 없음"
// @Security ApiKeyAuth
// @Router /jobs/{id} [get]
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	job, err := h.store.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

// ListJobs godoc
// @Summary 작업 목록 조회
// @Description 필터와 페이지네이션으로 작업 목록을 조회한다.
// @Tags jobs
// @Produce json
// @Param status query string false "상태 필터 (pending, running, completed, failed, dead, cancelled)"
// @Param queue query string false "큐 필터"
// @Param type query string false "작업 유형 필터"
// @Param page query int false "페이지 번호" default(1)
// @Param limit query int false "페이지 크기" default(20)
// @Success 200 {object} PaginatedResponse "작업 목록"
// @Failure 500 {object} ErrorResponse "서버 에러"
// @Security ApiKeyAuth
// @Router /jobs [get]
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filter := storage.ListFilter{
		Status: r.URL.Query().Get("status"),
		Queue:  r.URL.Query().Get("queue"),
		Type:   r.URL.Query().Get("type"),
		Page:   page,
		Limit:  limit,
	}

	jobs, total, err := h.store.ListJobs(r.Context(), filter)
	if err != nil {
		h.logger.Error("list jobs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}

	writeJSON(w, http.StatusOK, PaginatedResponse{
		Data: jobs,
		Pagination: Pagination{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: int(math.Ceil(float64(total) / float64(limit))),
		},
	})
}

// CancelJob godoc
// @Summary 작업 취소
// @Description 대기/예약 중인 작업을 취소한다.
// @Tags jobs
// @Produce json
// @Param id path string true "작업 ID (UUID)"
// @Success 200 {object} map[string]string "취소 결과"
// @Failure 400 {object} ErrorResponse "잘못된 ID"
// @Failure 404 {object} ErrorResponse "작업 없음"
// @Failure 409 {object} ErrorResponse "취소 불가 상태"
// @Security ApiKeyAuth
// @Router /jobs/{id}/cancel [post]
func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	job, err := h.store.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	if !job.Status.CanTransitionTo(model.StatusCancelled) {
		writeError(w, http.StatusConflict, "job cannot be cancelled in current state")
		return
	}

	if err := h.store.UpdateJobStatus(r.Context(), id, model.StatusCancelled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel job")
		return
	}

	h.publishEvent(r.Context(), "job.cancelled", job)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// RetryJob godoc
// @Summary Dead 작업 재시도
// @Description dead 상태의 작업을 다시 pending으로 변경하여 재시도한다.
// @Tags jobs
// @Produce json
// @Param id path string true "작업 ID (UUID)"
// @Success 200 {object} map[string]string "재시도 결과"
// @Failure 400 {object} ErrorResponse "잘못된 ID"
// @Failure 404 {object} ErrorResponse "작업 없음"
// @Failure 409 {object} ErrorResponse "dead 상태가 아님"
// @Security ApiKeyAuth
// @Router /jobs/{id}/retry [post]
func (h *Handler) RetryJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	job, err := h.store.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	if job.Status != model.StatusDead {
		writeError(w, http.StatusConflict, "only dead jobs can be retried")
		return
	}

	if err := h.store.UpdateJobError(r.Context(), id, model.StatusPending, 0, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry job")
		return
	}

	if err := h.queue.Push(r.Context(), job.Queue, id.String()); err != nil {
		h.logger.Error("push retry job failed", "id", id, "error", err)
	}

	h.publishEvent(r.Context(), "job.retried", job)
	writeJSON(w, http.StatusOK, map[string]string{"status": "retrying"})
}

// DeleteJob godoc
// @Summary Dead 작업 삭제
// @Description dead 상태의 작업을 영구 삭제한다. dead 상태가 아닌 작업은 삭제할 수 없다.
// @Tags jobs
// @Produce json
// @Param id path string true "작업 ID (UUID)"
// @Success 200 {object} map[string]string "삭제 결과"
// @Failure 400 {object} ErrorResponse "잘못된 ID"
// @Failure 409 {object} ErrorResponse "dead 상태가 아님"
// @Security ApiKeyAuth
// @Router /jobs/{id} [delete]
func (h *Handler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	if err := h.store.DeleteJob(r.Context(), id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GetQueues godoc
// @Summary 전체 큐 통계 조회
// @Description 모든 큐의 상태별 작업 수를 조회한다.
// @Tags queues
// @Produce json
// @Success 200 {object} map[string]map[string]int "큐별 상태 통계"
// @Failure 500 {object} ErrorResponse "서버 에러"
// @Security ApiKeyAuth
// @Router /queues [get]
func (h *Handler) GetQueues(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.GetQueueStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get queue stats")
		return
	}

	grouped := make(map[string]map[string]int)
	for _, s := range stats {
		if grouped[s.Queue] == nil {
			grouped[s.Queue] = make(map[string]int)
		}
		grouped[s.Queue][s.Status] = s.Count
	}

	writeJSON(w, http.StatusOK, grouped)
}

// GetQueueByName godoc
// @Summary 특정 큐 통계 조회
// @Description 지정된 큐의 상태별 작업 수를 조회한다.
// @Tags queues
// @Produce json
// @Param name path string true "큐 이름"
// @Success 200 {object} map[string]int "상태별 작업 수"
// @Failure 500 {object} ErrorResponse "서버 에러"
// @Security ApiKeyAuth
// @Router /queues/{name} [get]
func (h *Handler) GetQueueByName(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	stats, err := h.store.GetQueueStatsByName(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get queue stats")
		return
	}

	result := make(map[string]int)
	for _, s := range stats {
		result[s.Status] = s.Count
	}

	writeJSON(w, http.StatusOK, result)
}

// publishEvent는 작업 상태 변경 이벤트를 Redis Pub/Sub으로 발행한다.
func (h *Handler) publishEvent(ctx context.Context, eventType string, job *model.Job) {
	event := dashboard.JobEvent{
		Type:   eventType,
		JobID:  job.ID.String(),
		Queue:  job.Queue,
		Status: string(job.Status),
	}
	if err := dashboard.PublishJobEvent(ctx, h.redisClient, event); err != nil {
		h.logger.Error("publish job event failed", "type", eventType, "id", job.ID, "error", err)
	}
}

// GetStats godoc
// @Summary 전체 작업 통계 조회
// @Description 전체 작업의 상태별 합계를 조회한다.
// @Tags stats
// @Produce json
// @Success 200 {object} map[string]int "상태별 합계"
// @Failure 500 {object} ErrorResponse "서버 에러"
// @Security ApiKeyAuth
// @Router /stats [get]
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.GetQueueStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}

	totals := make(map[string]int)
	for _, s := range stats {
		totals[s.Status] += s.Count
	}

	writeJSON(w, http.StatusOK, totals)
}
