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

func queueKey(queue string) string {
	return "goqueue:queue:" + queue
}

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

func (q *RedisQueue) Schedule(ctx context.Context, jobID string, runAt time.Time) error {
	return q.client.ZAdd(ctx, scheduledSetKey, redis.Z{
		Score:  float64(runAt.Unix()),
		Member: jobID,
	}).Err()
}

// GetDueJobs atomically retrieves and removes jobs whose run_at has passed.
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
