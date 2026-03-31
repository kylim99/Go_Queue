package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/goqueue/internal/storage"
	"github.com/redis/go-redis/v9"
)

// StartSubscriber는 Redis Pub/Sub 채널을 구독하고 수신 이벤트를 Hub로 브로드캐스트한다.
// 통계, 작업 목록, DLQ 세 가지 프래그먼트를 모두 렌더링하여 실시간 업데이트한다.
// 또한 5초마다 전체 상태를 갱신하여 메시지 유실에 대비한다.
func StartSubscriber(ctx context.Context, rdb *redis.Client, hub *Hub, renderer *TemplateRenderer, logger *slog.Logger) {
	pubsub := rdb.Subscribe(ctx, eventChannel)
	defer pubsub.Close()

	ch := pubsub.Channel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var event JobEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				logger.Error("unmarshal event failed", "error", err)
				continue
			}
			// 이벤트 수신 시 모든 대시보드 프래그먼트를 렌더링하여 브로드캐스트
			broadcastAllFragments(ctx, hub, renderer, logger)
		case <-ticker.C:
			// 주기적 전체 상태 갱신: Redis Pub/Sub 메시지 유실에 대비
			broadcastAllFragments(ctx, hub, renderer, logger)
		}
	}
}

// broadcastAllFragments는 통계, 작업 목록, DLQ, 차트 데이터 프래그먼트를 모두 렌더링하여 Hub로 브로드캐스트한다.
func broadcastAllFragments(ctx context.Context, hub *Hub, renderer *TemplateRenderer, logger *slog.Logger) {
	var allHTML []byte

	// 통계 프래그먼트 렌더링
	statsHTML, err := renderer.RenderStatsFragment(ctx)
	if err != nil {
		logger.Error("render stats failed", "error", err)
	} else {
		allHTML = append(allHTML, statsHTML...)
	}

	// 작업 목록 프래그먼트 렌더링 (기본 필터: 전체 상태, 1페이지, 50개)
	jobListHTML, err := renderer.RenderJobListFragment(ctx, storage.ListFilter{
		Page:  1,
		Limit: 50,
	}, renderer.apiKey)
	if err != nil {
		logger.Error("render job list failed", "error", err)
	} else {
		allHTML = append(allHTML, jobListHTML...)
	}

	// DLQ 프래그먼트 렌더링
	dlqHTML, err := renderer.RenderDLQFragment(ctx, renderer.apiKey)
	if err != nil {
		logger.Error("render dlq failed", "error", err)
	} else {
		allHTML = append(allHTML, dlqHTML...)
	}

	// 차트 데이터 프래그먼트 렌더링 (시계열 throughput/error 차트 업데이트)
	chartHTML, err := renderer.RenderChartDataFragment(ctx)
	if err != nil {
		logger.Error("render chart data failed", "error", err)
	} else {
		allHTML = append(allHTML, chartHTML...)
	}

	if len(allHTML) > 0 {
		hub.broadcast <- allHTML
	}
}
