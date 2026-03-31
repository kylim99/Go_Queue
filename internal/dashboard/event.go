package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const eventChannel = "goqueue:events"

// JobEvent는 작업 상태 변경 이벤트를 나타낸다.
type JobEvent struct {
	Type      string    `json:"type"`
	JobID     string    `json:"job_id"`
	Queue     string    `json:"queue"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// PublishJobEvent는 작업 상태 변경 이벤트를 Redis Pub/Sub 채널에 발행한다.
func PublishJobEvent(ctx context.Context, rdb *redis.Client, event JobEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal job event: %w", err)
	}
	return rdb.Publish(ctx, eventChannel, data).Err()
}
