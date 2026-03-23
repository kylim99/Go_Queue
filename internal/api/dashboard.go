package api

import (
	"embed"
	"html/template"
	"net/http"

	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

type DashboardData struct {
	Jobs   []*model.Job
	Stats  map[string]int
	APIKey string
}

func DashboardHandler(store *storage.PostgresStorage, apiKey string) http.HandlerFunc {
	tmpl := template.Must(template.ParseFS(templateFS, "templates/dashboard.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		jobs, _, _ := store.ListJobs(r.Context(), storage.ListFilter{Page: 1, Limit: 50})

		queueStats, _ := store.GetQueueStats(r.Context())
		stats := make(map[string]int)
		for _, s := range queueStats {
			stats[s.Status] += s.Count
		}

		tmpl.Execute(w, DashboardData{Jobs: jobs, Stats: stats, APIKey: apiKey})
	}
}
