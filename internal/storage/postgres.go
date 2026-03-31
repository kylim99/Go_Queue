package storage

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/goqueue/internal/model"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type ListFilter struct {
	Status string
	Queue  string
	Type   string
	Page   int
	Limit  int
}

type QueueStat struct {
	Queue  string `json:"queue"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type TimeSeriesStat struct {
	Bucket time.Time `json:"bucket"`
	Status string    `json:"status"`
	Count  int       `json:"count"`
}

type PostgresStorage struct {
	pool    *pgxpool.Pool
	connStr string
}

// NewPostgresStorage는 PostgreSQL 연결 풀을 생성하고 연결을 확인한 후 스토리지 인스턴스를 반환한다.
func NewPostgresStorage(ctx context.Context, connStr string) (*PostgresStorage, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStorage{pool: pool, connStr: connStr}, nil
}

func (s *PostgresStorage) RunMigrations() error {
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, s.connStr)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// Pool은 DB 커넥션 풀 통계 수집을 위해 pgxpool.Pool을 반환한다.
func (s *PostgresStorage) Pool() *pgxpool.Pool {
	return s.pool
}

// Close는 PostgreSQL 연결 풀을 닫고 모든 커넥션을 해제한다.
func (s *PostgresStorage) Close() {
	s.pool.Close()
}

// CreateJob은 새로운 작업을 PostgreSQL에 저장하고 생성된 작업 정보를 반환한다.
func (s *PostgresStorage) CreateJob(ctx context.Context, job *model.Job) (*model.Job, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO jobs (queue, type, payload, status, max_retries, retry_count, run_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, queue, type, payload, status, max_retries, retry_count,
		           run_at, started_at, completed_at, error, created_at, updated_at`,
		job.Queue, job.Type, job.Payload, job.Status, job.MaxRetries, job.RetryCount, job.RunAt,
	)
	return scanJob(row)
}

// GetJob은 ID로 작업을 조회하여 반환한다.
func (s *PostgresStorage) GetJob(ctx context.Context, id uuid.UUID) (*model.Job, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, queue, type, payload, status, max_retries, retry_count,
		        run_at, started_at, completed_at, error, created_at, updated_at
		 FROM jobs WHERE id = $1`, id,
	)
	return scanJob(row)
}

// UpdateJobStatus는 작업의 상태를 변경하고, running/completed 전환 시 시간 정보를 기록한다.
func (s *PostgresStorage) UpdateJobStatus(ctx context.Context, id uuid.UUID, status model.JobStatus) error {
	now := time.Now()
	var startedAt, completedAt *time.Time

	switch status {
	case model.StatusRunning:
		startedAt = &now
	case model.StatusCompleted:
		completedAt = &now
	}

	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, started_at = COALESCE($2, started_at),
		 completed_at = COALESCE($3, completed_at), updated_at = now()
		 WHERE id = $4`,
		status, startedAt, completedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}
	return nil
}

func (s *PostgresStorage) UpdateJobError(ctx context.Context, id uuid.UUID, status model.JobStatus, retryCount int, jobErr string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, retry_count = $2, error = $3, updated_at = now()
		 WHERE id = $4`,
		status, retryCount, jobErr, id,
	)
	if err != nil {
		return fmt.Errorf("update job error: %w", err)
	}
	return nil
}

func (s *PostgresStorage) ListJobs(ctx context.Context, filter ListFilter) ([]*model.Job, int, error) {
	where := "WHERE 1=1"
	args := []any{}
	argIdx := 1

	if filter.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, filter.Status)
		argIdx++
	}
	if filter.Queue != "" {
		where += fmt.Sprintf(" AND queue = $%d", argIdx)
		args = append(args, filter.Queue)
		argIdx++
	}
	if filter.Type != "" {
		where += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, filter.Type)
		argIdx++
	}

	var total int
	err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM jobs "+where, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	offset := (filter.Page - 1) * filter.Limit

	query := fmt.Sprintf(
		`SELECT id, queue, type, payload, status, max_retries, retry_count,
		        run_at, started_at, completed_at, error, created_at, updated_at
		 FROM jobs %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		where, argIdx, argIdx+1,
	)
	args = append(args, filter.Limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		job, err := scanJobFromRows(rows)
		if err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	return jobs, total, nil
}

func (s *PostgresStorage) GetPendingUnqueued(ctx context.Context, olderThan time.Duration) ([]*model.Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, queue, type, payload, status, max_retries, retry_count,
		        run_at, started_at, completed_at, error, created_at, updated_at
		 FROM jobs
		 WHERE status = 'pending' AND run_at IS NULL AND created_at < $1
		 ORDER BY created_at ASC LIMIT 100`,
		time.Now().Add(-olderThan),
	)
	if err != nil {
		return nil, fmt.Errorf("get pending unqueued: %w", err)
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		job, err := scanJobFromRows(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *PostgresStorage) GetStaleRunningJobs(ctx context.Context, timeout time.Duration) ([]*model.Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, queue, type, payload, status, max_retries, retry_count,
		        run_at, started_at, completed_at, error, created_at, updated_at
		 FROM jobs
		 WHERE status = 'running' AND started_at < $1
		 ORDER BY started_at ASC LIMIT 100`,
		time.Now().Add(-2*timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("get stale running jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		job, err := scanJobFromRows(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *PostgresStorage) GetQueueStats(ctx context.Context) ([]QueueStat, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT queue, status, COUNT(*) as count
		 FROM jobs GROUP BY queue, status ORDER BY queue`,
	)
	if err != nil {
		return nil, fmt.Errorf("get queue stats: %w", err)
	}
	defer rows.Close()

	var stats []QueueStat
	for rows.Next() {
		var qs QueueStat
		if err := rows.Scan(&qs.Queue, &qs.Status, &qs.Count); err != nil {
			return nil, err
		}
		stats = append(stats, qs)
	}
	return stats, nil
}

func (s *PostgresStorage) GetQueueStatsByName(ctx context.Context, queueName string) ([]QueueStat, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT queue, status, COUNT(*) as count
		 FROM jobs WHERE queue = $1 GROUP BY queue, status`,
		queueName,
	)
	if err != nil {
		return nil, fmt.Errorf("get queue stats by name: %w", err)
	}
	defer rows.Close()

	var stats []QueueStat
	for rows.Next() {
		var qs QueueStat
		if err := rows.Scan(&qs.Queue, &qs.Status, &qs.Count); err != nil {
			return nil, err
		}
		stats = append(stats, qs)
	}
	return stats, nil
}

// DeleteJob은 dead 상태의 작업을 삭제한다. dead 상태가 아닌 작업은 삭제하지 않는다.
func (s *PostgresStorage) DeleteJob(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx,
		`DELETE FROM jobs WHERE id = $1 AND status = 'dead'`, id,
	)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("job not found or not in dead status")
	}
	return nil
}

// GetTimeSeries는 지정된 기간의 작업 완료/실패 추이를 시간 버킷별로 반환한다.
func (s *PostgresStorage) GetTimeSeries(ctx context.Context, duration time.Duration, interval string) ([]TimeSeriesStat, error) {
	query := `SELECT date_trunc($1, updated_at) AS bucket, status, COUNT(*) AS count
	          FROM jobs
	          WHERE updated_at > now() - $2::interval
	            AND status IN ('completed', 'failed', 'dead')
	          GROUP BY bucket, status
	          ORDER BY bucket`
	rows, err := s.pool.Query(ctx, query, interval, duration.String())
	if err != nil {
		return nil, fmt.Errorf("get time series: %w", err)
	}
	defer rows.Close()

	var stats []TimeSeriesStat
	for rows.Next() {
		var stat TimeSeriesStat
		if err := rows.Scan(&stat.Bucket, &stat.Status, &stat.Count); err != nil {
			return nil, fmt.Errorf("scan time series: %w", err)
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanJob(row scannable) (*model.Job, error) {
	var j model.Job
	err := row.Scan(
		&j.ID, &j.Queue, &j.Type, &j.Payload, &j.Status,
		&j.MaxRetries, &j.RetryCount, &j.RunAt,
		&j.StartedAt, &j.CompletedAt, &j.Error,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	return &j, nil
}

func scanJobFromRows(rows pgx.Rows) (*model.Job, error) {
	var j model.Job
	err := rows.Scan(
		&j.ID, &j.Queue, &j.Type, &j.Payload, &j.Status,
		&j.MaxRetries, &j.RetryCount, &j.RunAt,
		&j.StartedAt, &j.CompletedAt, &j.Error,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan job row: %w", err)
	}
	return &j, nil
}
