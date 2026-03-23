package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

func NewRouter(store *storage.PostgresStorage, q *queue.RedisQueue, apiKey string, logger *slog.Logger) http.Handler {
	h := NewHandler(store, q, logger)
	r := chi.NewRouter()

	r.Use(RequestLogger(logger))

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(APIKeyAuth(apiKey))

		r.Post("/jobs", h.CreateJob)
		r.Get("/jobs", h.ListJobs)
		r.Get("/jobs/{id}", h.GetJob)
		r.Post("/jobs/{id}/cancel", h.CancelJob)
		r.Post("/jobs/{id}/retry", h.RetryJob)

		r.Get("/queues", h.GetQueues)
		r.Get("/queues/{name}", h.GetQueueByName)
		r.Get("/stats", h.GetStats)
	})

	// Dashboard (no auth)
	r.Get("/dashboard", DashboardHandler(store, apiKey))

	return r
}
