package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/goqueue/internal/metrics"
)

// schedulerLockID는 스케줄러 리더 선출에 사용하는 PostgreSQL advisory lock ID이다.
const schedulerLockID int64 = 1337_7001

// LeaderElector는 PostgreSQL advisory lock을 사용하여 리더 선출을 관리한다.
// 전용 pgx.Conn을 사용하여 세션 수준 잠금을 유지하며, 커넥션이 끊어지면 자동으로 잠금이 해제된다.
type LeaderElector struct {
	connStr      string
	conn         *pgx.Conn // 전용 커넥션 (풀 외부)
	isLeader     atomic.Bool
	onAcquire    func() // 리더 획득 시 콜백
	onRelease    func() // 리더 해제 시 콜백
	logger       *slog.Logger
	pollInterval time.Duration
}

// New는 새로운 LeaderElector를 생성한다.
func New(connStr string, pollInterval time.Duration, onAcquire, onRelease func(), logger *slog.Logger) *LeaderElector {
	return &LeaderElector{
		connStr:      connStr,
		onAcquire:    onAcquire,
		onRelease:    onRelease,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

// Start는 리더 선출 루프를 시작한다. ctx가 취소될 때까지 반복하며,
// advisory lock 획득을 시도하고, 획득 시 holdLock으로 유지한다.
func (e *LeaderElector) Start(ctx context.Context) error {
	e.logger.Info("leader elector started", "poll_interval", e.pollInterval)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// advisory lock 획득 시도
		err := e.tryAcquire(ctx)
		if err != nil {
			e.logger.Warn("leader election attempt failed", "error", err)
			metrics.LeaderElectionsTotal.WithLabelValues("failed").Inc()
			e.sleep(ctx, e.pollInterval)
			continue
		}

		// 리더 획득 성공
		e.isLeader.Store(true)
		metrics.LeaderStatus.Set(1)
		metrics.LeaderElectionsTotal.WithLabelValues("acquired").Inc()
		e.logger.Info("leader acquired")

		if e.onAcquire != nil {
			e.onAcquire()
		}

		// 잠금 유지 (블로킹)
		e.holdLock(ctx)

		// 잠금 해제됨
		e.isLeader.Store(false)
		metrics.LeaderStatus.Set(0)
		metrics.LeaderElectionsTotal.WithLabelValues("lost").Inc()
		e.logger.Warn("leader lost")

		if e.onRelease != nil {
			e.onRelease()
		}
	}
}

// Stop은 전용 커넥션을 닫아 advisory lock을 해제한다. 그레이스풀 셧다운 시 호출된다.
func (e *LeaderElector) Stop() {
	if e.conn != nil && !e.conn.IsClosed() {
		e.conn.Close(context.Background())
		e.conn = nil
	}
	e.isLeader.Store(false)
	metrics.LeaderStatus.Set(0)
	e.logger.Info("leader elector stopped")
}

// IsLeader는 현재 인스턴스가 리더인지 여부를 반환한다.
func (e *LeaderElector) IsLeader() bool {
	return e.isLeader.Load()
}

// tryAcquire는 PostgreSQL advisory lock 획득을 시도한다.
// 전용 커넥션을 생성하고 pg_try_advisory_lock을 실행한다.
func (e *LeaderElector) tryAcquire(ctx context.Context) error {
	// 커넥션이 없거나 닫혀있으면 새로 생성
	if e.conn == nil || e.conn.IsClosed() {
		conn, err := pgx.Connect(ctx, e.connStr)
		if err != nil {
			return fmt.Errorf("connect to postgres: %w", err)
		}
		e.conn = conn
	}

	var acquired bool
	err := e.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", schedulerLockID).Scan(&acquired)
	if err != nil {
		e.conn.Close(ctx)
		e.conn = nil
		return fmt.Errorf("advisory lock query: %w", err)
	}

	if !acquired {
		e.conn.Close(ctx)
		e.conn = nil
		return fmt.Errorf("lock not acquired")
	}

	return nil
}

// holdLock은 하트비트를 통해 advisory lock 유지를 확인한다.
// 잠금이 유효하지 않거나 ctx가 취소되면 반환된다.
func (e *LeaderElector) holdLock(ctx context.Context) {
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 그레이스풀 셧다운: 커넥션 닫기
			if e.conn != nil && !e.conn.IsClosed() {
				e.conn.Close(context.Background())
				e.conn = nil
			}
			return
		case <-ticker.C:
			held, err := e.verifyLock(ctx)
			if err != nil {
				e.logger.Error("lock verification failed", "error", err)
				if e.conn != nil && !e.conn.IsClosed() {
					e.conn.Close(context.Background())
					e.conn = nil
				}
				return
			}
			if !held {
				e.logger.Warn("advisory lock no longer held")
				if e.conn != nil && !e.conn.IsClosed() {
					e.conn.Close(context.Background())
					e.conn = nil
				}
				return
			}
		}
	}
}

// verifyLock은 pg_locks를 조회하여 advisory lock이 여전히 유효한지 확인한다.
func (e *LeaderElector) verifyLock(ctx context.Context) (bool, error) {
	var count int
	err := e.conn.QueryRow(ctx,
		"SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'advisory' AND objid = $1",
		schedulerLockID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("verify lock query: %w", err)
	}
	return count > 0, nil
}

// sleep은 지정된 시간만큼 대기하되, ctx가 취소되면 즉시 반환한다.
func (e *LeaderElector) sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
