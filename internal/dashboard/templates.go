package dashboard

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/goqueue/internal/model"
	"github.com/goqueue/internal/storage"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

// TemplateRenderer는 대시보드 HTML 템플릿을 렌더링하는 구조체이다.
type TemplateRenderer struct {
	tmpl   *template.Template
	store  *storage.PostgresStorage
	apiKey string
}

// JobListData는 작업 목록 템플릿에 전달하는 데이터 구조체이다.
type JobListData struct {
	Jobs       []*model.Job
	Page       int
	TotalPages int
	Total      int
	Filter     storage.ListFilter
	APIKey     string
}

// JobDetailData는 작업 상세 템플릿에 전달하는 데이터 구조체이다.
type JobDetailData struct {
	*model.Job
	APIKey string
}

// DLQData는 Dead Letter Queue 템플릿에 전달하는 데이터 구조체이다.
type DLQData struct {
	Jobs   []*model.Job
	APIKey string
}

// ChartData는 시계열 차트에 전달하는 데이터 구조체이다.
// Labels는 시간 레이블("15:04" 형식), Completed/Failed/Dead는 각 상태별 개수이다.
type ChartData struct {
	Labels    []string `json:"labels"`
	Completed []int    `json:"completed"`
	Failed    []int    `json:"failed"`
	Dead      []int    `json:"dead"`
}

// ChartFragmentData는 차트 프래그먼트 템플릿에 전달하는 데이터 구조체이다.
type ChartFragmentData struct {
	ChartJSON string
}

// NewTemplateRenderer는 템플릿을 파싱하고 새로운 TemplateRenderer를 생성한다.
func NewTemplateRenderer(store *storage.PostgresStorage, apiKey string) *TemplateRenderer {
	funcMap := template.FuncMap{
		"add":      func(a, b int) int { return a + b },
		"subtract": func(a, b int) int { return a - b },
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS,
		"templates/layout.html",
		"templates/partials/stats.html",
		"templates/partials/ws_status.html",
		"templates/partials/job_list.html",
		"templates/partials/job_detail.html",
		"templates/partials/dlq.html",
		"templates/partials/charts.html",
	))
	return &TemplateRenderer{
		tmpl:   tmpl,
		store:  store,
		apiKey: apiKey,
	}
}

// RenderStatsFragment는 현재 큐 통계를 HTML 프래그먼트로 렌더링한다.
// hx-swap-oob="true" 속성으로 HTMX가 자동으로 DOM을 교체한다.
func (t *TemplateRenderer) RenderStatsFragment(ctx context.Context) ([]byte, error) {
	queueStats, err := t.store.GetQueueStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get queue stats: %w", err)
	}

	stats := make(map[string]int)
	for _, s := range queueStats {
		stats[s.Status] += s.Count
	}

	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, "stats", stats); err != nil {
		return nil, fmt.Errorf("render stats: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderWSStatus는 WebSocket 연결 상태 표시기를 HTML로 렌더링한다.
func (t *TemplateRenderer) RenderWSStatus(connected bool) ([]byte, error) {
	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, "ws_status", connected); err != nil {
		return nil, fmt.Errorf("render ws status: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderJobListFragment는 필터링된 작업 목록을 HTML 프래그먼트로 렌더링한다.
// hx-swap-oob="true" 속성으로 WebSocket을 통해 실시간 업데이트된다.
func (t *TemplateRenderer) RenderJobListFragment(ctx context.Context, filter storage.ListFilter, apiKey string) ([]byte, error) {
	jobs, total, err := t.store.ListJobs(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}

	if filter.Limit < 1 {
		filter.Limit = 20
	}
	totalPages := int(math.Ceil(float64(total) / float64(filter.Limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	data := JobListData{
		Jobs:       jobs,
		Page:       filter.Page,
		TotalPages: totalPages,
		Total:      total,
		Filter:     filter,
		APIKey:     apiKey,
	}

	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, "job_list", data); err != nil {
		return nil, fmt.Errorf("render job list: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderJobDetailFragment는 작업 상세 정보를 HTML 프래그먼트로 렌더링한다.
func (t *TemplateRenderer) RenderJobDetailFragment(ctx context.Context, id uuid.UUID) ([]byte, error) {
	job, err := t.store.GetJob(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}

	data := JobDetailData{
		Job:    job,
		APIKey: t.apiKey,
	}

	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, "job_detail", data); err != nil {
		return nil, fmt.Errorf("render job detail: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderDLQFragment는 Dead Letter Queue(죽은 작업 목록)를 HTML 프래그먼트로 렌더링한다.
func (t *TemplateRenderer) RenderDLQFragment(ctx context.Context, apiKey string) ([]byte, error) {
	jobs, _, err := t.store.ListJobs(ctx, storage.ListFilter{
		Status: "dead",
		Page:   1,
		Limit:  100,
	})
	if err != nil {
		return nil, fmt.Errorf("list dead jobs: %w", err)
	}

	data := DLQData{
		Jobs:   jobs,
		APIKey: apiKey,
	}

	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, "dlq", data); err != nil {
		return nil, fmt.Errorf("render dlq: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderChartDataFragment는 최근 1시간의 시계열 데이터를 차트용 JSON으로 렌더링한다.
// hx-swap-oob="true" 속성이 포함된 hidden div를 반환하여 WebSocket으로 차트를 업데이트한다.
func (t *TemplateRenderer) RenderChartDataFragment(ctx context.Context) ([]byte, error) {
	chartData, err := t.buildChartData(ctx)
	if err != nil {
		return nil, err
	}

	chartJSON, err := json.Marshal(chartData)
	if err != nil {
		return nil, fmt.Errorf("marshal chart data: %w", err)
	}

	// OOB swap으로 chart-data 요소의 data-chart 속성을 업데이트한다
	html := fmt.Sprintf(`<div id="chart-data" hx-swap-oob="true" style="display:none" data-chart='%s'></div>`, string(chartJSON))
	return []byte(html), nil
}

// buildChartData는 PostgreSQL에서 시계열 데이터를 조회하여 ChartData로 변환한다.
// 최근 1시간을 분 단위로 집계하며, 누락된 상태는 0으로 채운다.
func (t *TemplateRenderer) buildChartData(ctx context.Context) (*ChartData, error) {
	stats, err := t.store.GetTimeSeries(ctx, 1*time.Hour, "minute")
	if err != nil {
		return nil, fmt.Errorf("get time series: %w", err)
	}

	// 시간 버킷별로 상태별 카운트를 집계한다
	type bucketData struct {
		completed int
		failed    int
		dead      int
	}
	bucketMap := make(map[string]*bucketData)
	var orderedLabels []string

	for _, s := range stats {
		label := s.Bucket.Format("15:04")
		if _, exists := bucketMap[label]; !exists {
			bucketMap[label] = &bucketData{}
			orderedLabels = append(orderedLabels, label)
		}
		bd := bucketMap[label]
		switch s.Status {
		case "completed":
			bd.completed += s.Count
		case "failed":
			bd.failed += s.Count
		case "dead":
			bd.dead += s.Count
		}
	}

	chartData := &ChartData{
		Labels:    make([]string, len(orderedLabels)),
		Completed: make([]int, len(orderedLabels)),
		Failed:    make([]int, len(orderedLabels)),
		Dead:      make([]int, len(orderedLabels)),
	}

	for i, label := range orderedLabels {
		bd := bucketMap[label]
		chartData.Labels[i] = label
		chartData.Completed[i] = bd.completed
		chartData.Failed[i] = bd.failed
		chartData.Dead[i] = bd.dead
	}

	return chartData, nil
}
