package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const scheduledSetKey = "goqueue:scheduled"

// Lua script for atomic get-and-remove of due jobs from sorted set.
var getDueJobsScript = redis.NewScript(`
local results = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if #results > 0 then
    redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
end
return results
`)

type RedisQueue struct {
	client *redis.Client
}

// NewRedisQueue는 Redis에 연결하고 큐 인스턴스를 생성한다.
func NewRedisQueue(ctx context.Context, addr string) (*RedisQueue, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return &RedisQueue{client: client}, nil
}

func (q *RedisQueue) Close() {
	q.client.Close()
}

// QueueLength는 지정된 큐의 현재 대기 작업 수를 반환한다.
func (q *RedisQueue) QueueLength(ctx context.Context, queueName string) (int64, error) {
	return q.client.LLen(ctx, queueKey(queueName)).Result()
}

func queueKey(queue string) string {
	return "goqueue:queue:" + queue
}

// Push는 작업 ID를 지정된 큐의 왼쪽에 추가한다 (LPUSH).
func (q *RedisQueue) Push(ctx context.Context, queue string, jobID string) error {
	return q.client.LPush(ctx, queueKey(queue), jobID).Err()
}

// Pop blocks on multiple queue keys simultaneously using BRPOP.
func (q *RedisQueue) Pop(ctx context.Context, queues []string, timeout time.Duration) (string, error) {
	keys := make([]string, len(queues))
	for i, name := range queues {
		keys[i] = queueKey(name)
	}

	result, err := q.client.BRPop(ctx, timeout, keys...).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("brpop: %w", err)
	}
	// BRPop returns [key, value]
	return result[1], nil
}

// Schedule은 작업을 지정된 실행 시간에 예약한다 (Redis Sorted Set 사용).
func (q *RedisQueue) Schedule(ctx context.Context, jobID string, runAt time.Time) error {
	return q.client.ZAdd(ctx, scheduledSetKey, redis.Z{
		Score:  float64(runAt.Unix()),
		Member: jobID,
	}).Err()
}

// Client는 내부 Redis 클라이언트를 반환한다. Pub/Sub 등 직접 접근이 필요할 때 사용한다.
func (q *RedisQueue) Client() *redis.Client {
	return q.client
}

// GetDueJobs는 실행 시간이 도래한 예약 작업을 원자적으로 조회하고 제거한다.
func (q *RedisQueue) GetDueJobs(ctx context.Context, now time.Time) ([]string, error) {
	max := fmt.Sprintf("%d", now.Unix())
	result, err := getDueJobsScript.Run(ctx, q.client, []string{scheduledSetKey}, max).StringSlice()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get due jobs: %w", err)
	}
	return result, nil
}
