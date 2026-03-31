package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/goqueue/docs"
	"github.com/goqueue/internal/dashboard"
	"github.com/goqueue/internal/leader"
	"github.com/goqueue/internal/metrics"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/storage"
)

func NewRouter(store *storage.PostgresStorage, q *queue.RedisQueue, apiKey string, elector *leader.LeaderElector, hub *dashboard.Hub, renderer *dashboard.TemplateRenderer, logger *slog.Logger) http.Handler {
	h := NewHandler(store, q, logger)
	r := chi.NewRouter()

	r.Use(RequestLogger(logger))

	// Health check 엔드포인트 (인증 불필요)
	r.Get("/health", HealthHandler())
	r.Get("/ready", ReadyHandler(elector, store))

	// Prometheus 메트릭 엔드포인트 (인증 불필요)
	r.Handle("/metrics", promhttp.HandlerFor(
		metrics.Registry,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	))

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(APIKeyAuth(apiKey))

		r.Post("/jobs", h.CreateJob)
		r.Get("/jobs", h.ListJobs)
		r.Get("/jobs/{id}", h.GetJob)
		r.Post("/jobs/{id}/cancel", h.CancelJob)
		r.Post("/jobs/{id}/retry", h.RetryJob)
		r.Delete("/jobs/{id}", h.DeleteJob)

		r.Get("/queues", h.GetQueues)
		r.Get("/queues/{name}", h.GetQueueByName)
		r.Get("/stats", h.GetStats)
	})

	// Swagger UI (인증 불필요)
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// Dashboard (no auth) - WebSocket 실시간 대시보드
	r.Get("/dashboard", dashboard.DashboardPageHandler(renderer, store, apiKey))
	r.Get("/dashboard/jobs", dashboard.JobListHandler(renderer, apiKey))
	r.Get("/dashboard/jobs/{id}", dashboard.JobDetailHandler(renderer))
	r.Get("/dashboard/dlq", dashboard.DLQHandler(renderer, apiKey))
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		dashboard.ServeWS(hub, w, r)
	})

	return r
}
