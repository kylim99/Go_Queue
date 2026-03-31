package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/goqueue/internal/storage"
)

// DashboardData는 대시보드 페이지 렌더링에 필요한 데이터를 담는 구조체이다.
type DashboardData struct {
	Stats       map[string]int
	APIKey      string
	JobListData JobListData
	DLQData     DLQData
	ChartData   ChartFragmentData
}

// ServeWS는 HTTP 요청을 WebSocket으로 업그레이드하고 클라이언트를 Hub에 등록한다.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		slog.Error("websocket accept failed", "error", err)
		return
	}

	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	hub.register <- client

	ctx := context.Background()
	go client.writePump(ctx)
	go client.readPump(ctx)
}

// DashboardPageHandler는 대시보드 메인 페이지를 렌더링하는 HTTP 핸들러를 반환한다.
// 초기 로드 시 작업 목록과 DLQ 데이터를 함께 렌더링한다.
func DashboardPageHandler(renderer *TemplateRenderer, store *storage.PostgresStorage, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobs, total, _ := store.ListJobs(r.Context(), storage.ListFilter{Page: 1, Limit: 50})
		deadJobs, _, _ := store.ListJobs(r.Context(), storage.ListFilter{Status: "dead", Page: 1, Limit: 100})

		queueStats, _ := store.GetQueueStats(r.Context())
		stats := make(map[string]int)
		for _, s := range queueStats {
			stats[s.Status] += s.Count
		}

		totalPages := 1
		if total > 50 {
			totalPages = (total + 49) / 50
		}

		// 초기 차트 데이터를 빌드하여 페이지 로드 시 차트를 렌더링한다
		chartData, err := renderer.buildChartData(r.Context())
		var chartJSON string
		if err != nil {
			slog.Error("build chart data failed", "error", err)
			chartJSON = `{"labels":[],"completed":[],"failed":[],"dead":[]}`
		} else {
			chartBytes, _ := json.Marshal(chartData)
			chartJSON = string(chartBytes)
		}

		data := DashboardData{
			Stats:  stats,
			APIKey: apiKey,
			JobListData: JobListData{
				Jobs:       jobs,
				Page:       1,
				TotalPages: totalPages,
				Total:      total,
				Filter:     storage.ListFilter{Page: 1, Limit: 50},
				APIKey:     apiKey,
			},
			DLQData: DLQData{
				Jobs:   deadJobs,
				APIKey: apiKey,
			},
			ChartData: ChartFragmentData{
				ChartJSON: chartJSON,
			},
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := renderer.tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
			slog.Error("render dashboard failed", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

// JobListHandler는 필터링과 페이지네이션이 적용된 작업 목록 HTML 프래그먼트를 반환하는 핸들러이다.
func JobListHandler(renderer *TemplateRenderer, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := storage.ListFilter{
			Status: r.URL.Query().Get("filter-status"),
			Queue:  r.URL.Query().Get("filter-queue"),
			Type:   r.URL.Query().Get("filter-type"),
			Page:   1,
			Limit:  50,
		}

		if p := r.URL.Query().Get("page"); p != "" {
			if page, err := strconv.Atoi(p); err == nil && page > 0 {
				filter.Page = page
			}
		}

		html, err := renderer.RenderJobListFragment(r.Context(), filter, apiKey)
		if err != nil {
			slog.Error("render job list failed", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	}
}

// JobDetailHandler는 특정 작업의 상세 정보 HTML 프래그먼트를 반환하는 핸들러이다.
func JobDetailHandler(renderer *TemplateRenderer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "Invalid job ID", http.StatusBadRequest)
			return
		}

		html, err := renderer.RenderJobDetailFragment(r.Context(), id)
		if err != nil {
			slog.Error("render job detail failed", "error", err, "job_id", idStr)
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	}
}

// DLQHandler는 Dead Letter Queue HTML 프래그먼트를 반환하는 핸들러이다.
func DLQHandler(renderer *TemplateRenderer, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		html, err := renderer.RenderDLQFragment(r.Context(), apiKey)
		if err != nil {
			slog.Error("render dlq failed", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	}
}
