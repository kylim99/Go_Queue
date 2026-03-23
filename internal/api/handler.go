package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

type Handler struct {
	store  *storage.PostgresStorage
	queue  *queue.RedisQueue
	logger *slog.Logger
}

func NewHandler(store *storage.PostgresStorage, q *queue.RedisQueue, logger *slog.Logger) *Handler {
	return &Handler{store: store, queue: q, logger: logger}
}

type CreateJobRequest struct {
	Queue      string          `json:"queue"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	MaxRetries *int            `json:"max_retries,omitempty"`
	RunAt      *string         `json:"run_at,omitempty"`
}

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

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

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

	writeJSON(w, http.StatusOK, map[string]string{"status": "retrying"})
}

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
