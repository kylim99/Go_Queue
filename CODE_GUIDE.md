# GoQueue 코드 상세 가이드

이 문서는 GoQueue 프로젝트의 모든 코드를 파일별로 상세히 설명한다. Go를 학습하면서 각 패턴과 개념을 이해하는 데 참고할 수 있다.

---

## 목차

1. [프로젝트 구조 개요](#1-프로젝트-구조-개요)
2. [go.mod — 모듈 정의](#2-gomod--모듈-정의)
3. [cmd/goqueue/main.go — 진입점](#3-cmdgoqueuemaingo--진입점)
4. [internal/config/config.go — 설정 관리](#4-internalconfigconfiggo--설정-관리)
5. [internal/model/job.go — Job 데이터 모델](#5-internalmodeljobgo--job-데이터-모델)
6. [internal/model/job_test.go — 모델 테스트](#6-internalmodeljobtestgo--모델-테스트)
7. [internal/storage/ — PostgreSQL 저장소](#7-internalstorage--postgresql-저장소)
8. [internal/queue/ — Redis 큐](#8-internalqueue--redis-큐)
9. [internal/worker/ — Worker Pool](#9-internalworker--worker-pool)
10. [internal/scheduler/ — 스케줄러](#10-internalscheduler--스케줄러)
11. [internal/api/ — REST API](#11-internalapi--rest-api)
12. [Dockerfile & docker-compose.yml — 배포](#12-dockerfile--docker-composeyml--배포)
13. [데이터 흐름 요약](#13-데이터-흐름-요약)

---

## 1. 프로젝트 구조 개요

```
goqueue/
├── cmd/goqueue/main.go          ← 앱 진입점 (모든 컴포넌트 연결)
├── internal/                    ← 외부 패키지에서 import 불가 (Go 규칙)
│   ├── config/config.go         ← 환경변수 → 구조체
│   ├── model/job.go             ← Job 구조체, 상태 전이 규칙
│   ├── storage/
│   │   ├── migrations/*.sql     ← DB 스키마 (embed)
│   │   ├── postgres.go          ← PostgreSQL CRUD
│   │   └── postgres_test.go     ← 통합 테스트 (testcontainers)
│   ├── queue/
│   │   ├── redis.go             ← Redis 큐 (BRPOP, Lua script)
│   │   └── redis_test.go        ← 통합 테스트
│   ├── worker/
│   │   ├── worker.go            ← goroutine Worker Pool
│   │   └── worker_test.go       ← 단위 테스트
│   ├── scheduler/
│   │   ├── scheduler.go         ← 지연 실행, 재시도, 복구
│   │   └── scheduler_test.go    ← 통합 테스트
│   └── api/
│       ├── response.go          ← JSON 응답 헬퍼
│       ├── middleware.go         ← 인증, 로깅 미들웨어
│       ├── handler.go           ← HTTP 핸들러 (Job CRUD)
│       ├── router.go            ← chi 라우터 설정
│       ├── dashboard.go         ← 웹 대시보드 핸들러
│       ├── templates/*.html     ← 대시보드 HTML (embed)
│       └── handler_test.go      ← API 단위 테스트
├── Dockerfile                   ← 멀티스테이지 빌드
├── docker-compose.yml           ← 앱 + PostgreSQL + Redis
├── Makefile                     ← 빌드/실행 명령어
├── go.mod / go.sum              ← 의존성 관리
```

### `internal/` 디렉토리의 의미

Go에서 `internal/` 하위의 패키지는 같은 모듈 내에서만 import할 수 있다. 외부 프로젝트에서 `github.com/goqueue/internal/model`을 import하면 컴파일 에러가 난다. 이렇게 하면 내부 구현을 캡슐화하고, 공개 API만 노출할 수 있다.

---

## 2. go.mod — 모듈 정의

```go
module github.com/goqueue
```

- `module` 선언: 이 프로젝트의 모듈 경로. 다른 패키지에서 import할 때 이 경로를 사용한다.
- `go 1.25.0`: 필요한 최소 Go 버전.
- `require (...)`: 의존성 목록. `go get`으로 추가하면 자동 갱신된다.

### 주요 의존성

| 패키지 | 역할 |
|--------|------|
| `github.com/jackc/pgx/v5` | PostgreSQL 드라이버 (database/sql보다 고성능) |
| `github.com/golang-migrate/migrate/v4` | DB 마이그레이션 도구 |
| `github.com/redis/go-redis/v9` | Redis 클라이언트 |
| `github.com/go-chi/chi/v5` | 경량 HTTP 라우터 |
| `github.com/google/uuid` | UUID 생성 |
| `github.com/testcontainers/testcontainers-go` | 테스트용 Docker 컨테이너 자동 관리 |

---

## 3. cmd/goqueue/main.go — 진입점

이 파일은 모든 컴포넌트를 초기화하고 연결하는 "접착제" 역할이다.

### 실행 흐름

```
main()
  ├── config.Load()              ← 환경변수에서 설정 로드
  ├── slog.New(...)              ← 구조화된 로거 생성
  ├── storage.NewPostgresStorage ← DB 연결 + 마이그레이션
  ├── queue.NewRedisQueue        ← Redis 연결
  ├── worker.NewPool → Start()   ← Worker goroutine 시작
  ├── scheduler.New → Start()    ← 스케줄러 goroutine 시작
  ├── api.NewRouter              ← HTTP 라우터 구성
  ├── srv.ListenAndServe()       ← HTTP 서버 시작 (goroutine)
  ├── <-quit                     ← OS 시그널 대기 (SIGINT/SIGTERM)
  └── Graceful Shutdown          ← 모든 컴포넌트 순차 종료
```

### 핵심 패턴 설명

```go
cfg, err := config.Load()
if err != nil {
    slog.Error("failed to load config", "error", err)
    os.Exit(1)
}
```
**에러 처리 패턴**: Go에서는 예외(exception)가 없다. 대신 함수가 `(결과, error)`를 반환하고, 호출자가 `if err != nil`로 검사한다. 이것이 Go 코드에서 가장 자주 보이는 패턴이다.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
```
**Context 패턴**: `context.Background()`는 최상위 context를 만든다. `WithCancel`은 취소 가능한 자식 context를 반환한다. 이 ctx를 모든 컴포넌트에 전달하면, `cancel()`을 호출할 때 모든 goroutine에 종료 신호가 전파된다.

```go
defer store.Close()
```
**defer**: 현재 함수가 끝날 때 (정상 종료든 에러든) 자동으로 실행된다. 리소스 정리에 사용한다. 선언 순서의 역순으로 실행된다.

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit
```
**채널을 이용한 시그널 대기**:
- `make(chan os.Signal, 1)` — 버퍼 1짜리 채널 생성.
- `signal.Notify` — SIGINT(Ctrl+C)나 SIGTERM을 이 채널로 보내달라고 OS에 등록.
- `<-quit` — 채널에서 값이 올 때까지 블로킹. 시그널이 오면 다음 줄로 넘어간다.

```go
go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        logger.Error(...)
    }
}()
```
**goroutine으로 서버 시작**: `go` 키워드는 함수를 별도의 경량 스레드(goroutine)에서 실행한다. HTTP 서버를 goroutine으로 띄워야 `<-quit`에서 메인 goroutine이 시그널을 기다릴 수 있다.

### Graceful Shutdown 순서

1. `cancel()` → context를 사용하는 모든 goroutine(Worker, Scheduler)에 종료 신호
2. `srv.Shutdown(shutdownCtx)` → 진행 중인 HTTP 요청이 끝날 때까지 기다린 후 서버 종료
3. `pool.Stop()` → Worker goroutine들이 모두 끝날 때까지 `WaitGroup`으로 대기

---

## 4. internal/config/config.go — 설정 관리

### Config 구조체

```go
type Config struct {
    HTTPAddr        string
    PostgresURL     string
    RedisURL        string
    APIKey          string
    WorkerCount     int
    JobTimeout      time.Duration
    ShutdownTimeout time.Duration
    LogLevel        string
}
```

Go의 구조체(struct)는 관련 데이터를 묶는 타입이다. Java의 클래스와 비슷하지만 상속이 없다.

### Load() 함수

```go
func Load() (*Config, error) {
```
- `*Config` — Config의 포인터를 반환. 포인터를 쓰면 구조체 전체를 복사하지 않고 참조만 전달한다.
- `error` — 두 번째 반환값. 문제가 없으면 `nil`.

```go
pgURL := os.Getenv("GOQUEUE_POSTGRES_URL")
if pgURL == "" {
    return nil, fmt.Errorf("GOQUEUE_POSTGRES_URL is required")
}
```
- 필수 환경변수가 없으면 에러를 반환한다.
- `fmt.Errorf` — 포맷된 에러 메시지를 생성한다.

### 헬퍼 함수들

```go
func getEnvOrDefault(key, defaultVal string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return defaultVal
}
```
- `:=` — 단축 변수 선언. 타입을 자동 추론한다. `var v string = os.Getenv(key)`와 같다.
- `if v := ...; v != ""` — if문 안에서 변수를 선언하고 바로 조건 검사. Go만의 관용구.

```go
func getEnvIntOrDefault(key string, defaultVal int) int {
    i, err := strconv.Atoi(v)
    if err != nil {
        slog.Warn("invalid int env var, using default", "key", key, ...)
        return defaultVal
    }
    return i
}
```
- `strconv.Atoi` — 문자열을 정수로 변환 (ASCII to Integer).
- 변환 실패 시 에러를 반환하지 않고 경고 로그만 남기고 기본값을 사용한다.
- `slog.Warn` — Go 1.21에 도입된 구조화된 로깅. 키-값 쌍으로 컨텍스트를 추가한다.

---

## 5. internal/model/job.go — Job 데이터 모델

### JobStatus 타입

```go
type JobStatus string

const (
    StatusPending   JobStatus = "pending"
    StatusScheduled JobStatus = "scheduled"
    // ...
)
```
- `type JobStatus string` — string 기반의 새로운 타입 정의. 일반 string과 호환되지 않아서 타입 안전성을 제공한다.
- `const (...)` — 상수 그룹. Go에는 enum이 없어서 이 패턴을 사용한다.

### 상태 전이 규칙

```go
var allowedTransitions = map[JobStatus][]JobStatus{
    StatusPending:   {StatusRunning, StatusScheduled, StatusCancelled},
    StatusScheduled: {StatusRunning, StatusCancelled},
    StatusRunning:   {StatusCompleted, StatusFailed, StatusCancelled},
    // ...
}
```
- `map[K]V` — Go의 해시맵. 키 타입 K, 값 타입 V.
- `[]JobStatus` — JobStatus의 슬라이스(가변 길이 배열).
- 이 맵은 **유한 상태 기계(FSM)**를 정의한다. 예: pending 상태에서는 running, scheduled, cancelled로만 전이 가능.

```
pending → running → completed
    ↓         ↓
scheduled  failed → retrying → running (재시도)
    ↓         ↓
cancelled  dead → pending (수동 재시도)
```

### 메서드

```go
func (s JobStatus) IsValid() bool {
    return validStatuses[s]
}

func (s JobStatus) CanTransitionTo(target JobStatus) bool {
    for _, allowed := range allowedTransitions[s] {
        if allowed == target {
            return true
        }
    }
    return false
}
```
- `func (s JobStatus)` — **메서드 리시버**. JobStatus 타입에 메서드를 붙인다. `s`는 this/self 같은 역할.
- `for _, allowed := range ...` — range 루프. 인덱스가 필요없으면 `_`로 무시한다.

### 지수 백오프

```go
func BackoffDuration(retryCount int) time.Duration {
    return time.Duration(math.Pow(2, float64(retryCount))) * time.Second
}
```
재시도 간격: 1회차 → 2초, 2회차 → 4초, 3회차 → 8초... 실패할수록 대기 시간이 기하급수적으로 늘어난다.

### Job 구조체

```go
type Job struct {
    ID          uuid.UUID       `json:"id"`
    Queue       string          `json:"queue"`
    Payload     json.RawMessage `json:"payload"`
    RunAt       *time.Time      `json:"run_at,omitempty"`
    // ...
}
```
- **struct tag** (`json:"id"`): JSON 직렬화 시 사용할 필드명. Go 컨벤션은 PascalCase지만, JSON은 snake_case.
- `json.RawMessage` — JSON을 파싱하지 않고 원시 바이트 그대로 보관한다. payload의 구조를 미리 알 필요가 없다.
- `*time.Time` — 포인터. `nil`이 될 수 있어서 "값이 없음(NULL)"을 표현한다.
- `omitempty` — JSON으로 변환할 때 nil/빈값이면 이 필드를 생략한다.

---

## 6. internal/model/job_test.go — 모델 테스트

### 테이블 주도 테스트 (Table-Driven Test)

```go
func TestJobStatus_IsValid(t *testing.T) {
    tests := []struct {
        status JobStatus
        valid  bool
    }{
        {StatusPending, true},
        {StatusScheduled, true},
        {JobStatus("invalid"), false},
    }

    for _, tt := range tests {
        if got := tt.status.IsValid(); got != tt.valid {
            t.Errorf("JobStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.valid)
        }
    }
}
```
- **Table-Driven Test** — Go 테스트의 핵심 패턴. 테스트 케이스를 구조체 슬라이스로 정의하고 루프로 실행한다. 케이스를 추가하기 쉽고 가독성이 좋다.
- `[]struct{...}` — 익명 구조체의 슬라이스. 이 테스트에서만 쓰이므로 별도 타입을 정의하지 않는다.
- `t.Errorf` — 테스트 실패를 기록하되, 나머지 케이스는 계속 실행한다. `t.Fatalf`는 즉시 중단.
- `%q` — 문자열을 따옴표로 감싸서 출력 (예: `"pending"`).
- `%v` — 값을 기본 형식으로 출력.

### 실행 방법

```bash
go test ./internal/model/ -v        # -v: 각 테스트 함수명 출력
go test ./internal/model/ -run Test  # 특정 패턴의 테스트만 실행
go test ./... -v                     # 전체 패키지 테스트
```

---

## 7. internal/storage/ — PostgreSQL 저장소

### 7.1 마이그레이션 SQL

```sql
CREATE TABLE jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    queue        VARCHAR(255) NOT NULL,
    type         VARCHAR(255) NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    status       VARCHAR(20) NOT NULL DEFAULT 'pending',
    max_retries  INT NOT NULL DEFAULT 3,
    retry_count  INT NOT NULL DEFAULT 0,
    run_at       TIMESTAMPTZ,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
- `UUID PRIMARY KEY DEFAULT gen_random_uuid()` — PostgreSQL이 자동으로 UUID를 생성한다.
- `JSONB` — 바이너리 JSON. 일반 JSON보다 쿼리/인덱싱이 빠르다.
- `TIMESTAMPTZ` — 타임존이 포함된 타임스탬프.
- `error TEXT NOT NULL DEFAULT ''` — NULL 대신 빈 문자열 사용. pgx에서 NULL을 Go string으로 스캔하면 에러가 발생하기 때문이다.

```sql
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_run_at ON jobs(run_at) WHERE run_at IS NOT NULL;
```
- 인덱스는 자주 조회하는 컬럼에 만든다.
- `WHERE run_at IS NOT NULL` — **부분 인덱스**. run_at이 NULL이 아닌 행만 인덱싱해서 크기를 줄인다.

### 7.2 postgres.go

#### embed로 SQL 파일 바이너리에 포함

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS
```
- `//go:embed` — 컴파일 시 파일을 바이너리에 포함시키는 Go 지시자. 배포 시 별도로 SQL 파일을 복사할 필요가 없다.
- `embed.FS` — 파일 시스템 인터페이스를 구현하므로 `os.Open`처럼 사용할 수 있다.

#### 커넥션 풀

```go
type PostgresStorage struct {
    pool    *pgxpool.Pool
    connStr string
}

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
```
- `pgxpool.Pool` — 커넥션 풀. 매번 DB에 새 연결을 맺는 대신, 미리 만들어둔 연결을 재사용한다.
- `%w` — **에러 래핑**. 원래 에러를 감싸서 원인 추적이 가능하다. `errors.Is(err, target)`으로 원인을 확인할 수 있다.
- `&PostgresStorage{...}` — 구조체를 생성하고 그 포인터를 반환. `&`는 주소 연산자.

#### 마이그레이션

```go
func (s *PostgresStorage) RunMigrations() error {
    d, err := iofs.New(migrationsFS, "migrations")
    m, err := migrate.NewWithSourceInstance("iofs", d, s.connStr)
    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        return fmt.Errorf("run migrations: %w", err)
    }
    return nil
}
```
- `iofs.New` — embed된 파일 시스템에서 마이그레이션 소스를 생성.
- `m.Up()` — 적용되지 않은 마이그레이션을 순서대로 실행.
- `migrate.ErrNoChange` — 이미 모든 마이그레이션이 적용된 상태. 이건 에러가 아니므로 무시.

#### CreateJob — INSERT + RETURNING

```go
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
```
- `$1, $2, ...` — **파라미터 바인딩**. SQL 인젝션을 방지한다. 직접 문자열을 끼워넣으면 안 된다.
- `RETURNING ...` — PostgreSQL 전용. INSERT 후 생성된 행을 바로 반환한다 (id, created_at 등 DB가 생성한 값 포함).
- `QueryRow` — 단일 행 반환 쿼리.

#### ListJobs — 동적 WHERE 절 조립

```go
func (s *PostgresStorage) ListJobs(ctx context.Context, filter ListFilter) ([]*model.Job, int, error) {
    where := "WHERE 1=1"
    args := []any{}
    argIdx := 1

    if filter.Status != "" {
        where += fmt.Sprintf(" AND status = $%d", argIdx)
        args = append(args, filter.Status)
        argIdx++
    }
    // ...
}
```
- `WHERE 1=1` — 항상 참인 조건. 이후 `AND`를 무조건 붙일 수 있어서 코드가 깔끔해진다.
- `[]any{}` — 빈 슬라이스. `any`는 `interface{}`의 별칭으로, 아무 타입이나 담을 수 있다.
- `append(args, filter.Status)` — 슬라이스에 원소 추가. Go 슬라이스는 동적 배열이다.
- `$1, $2, ...`의 번호를 `argIdx`로 관리해서 필터가 선택적이어도 번호가 꼬이지 않는다.

#### scan 함수 — 인터페이스 활용

```go
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
    // ...
}
```
- **인터페이스 정의**: `Scan` 메서드를 가진 모든 타입을 받을 수 있다. `pgx.Row`와 `pgx.Rows` 모두 `Scan`을 가지므로, 하나의 함수로 두 경우를 처리한다.
- Go의 인터페이스는 **암시적**이다. 타입이 인터페이스의 메서드를 구현하면 자동으로 만족한다 (`implements` 키워드 없음).
- `&j.ID` — Scan에 포인터를 전달해서, DB 값을 해당 필드에 직접 쓴다.

### 7.3 postgres_test.go — testcontainers

```go
func setupTestDB(t *testing.T) (*PostgresStorage, func()) {
    pgContainer, err := postgres.Run(ctx,
        "postgres:16-alpine",
        postgres.WithDatabase("goqueue_test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
        testcontainers.WithWaitStrategy(
            wait.ForLog("database system is ready to accept connections").
                WithOccurrence(2).
                WithStartupTimeout(30*time.Second),
        ),
    )
    // ...
    return store, cleanup
}
```
- **testcontainers-go** — 테스트 시 Docker 컨테이너를 자동으로 띄우고, 테스트 끝나면 정리한다.
- `WithOccurrence(2)` — PostgreSQL은 시작 시 로그를 두 번 출력한다 (한 번은 초기화, 한 번은 준비 완료). 두 번째를 기다려야 실제로 연결 가능하다.
- `cleanup` 함수를 반환해서 `defer cleanup()`으로 정리한다.

---

## 8. internal/queue/ — Redis 큐

### 8.1 redis.go

#### Lua 스크립트 — 원자적 연산

```go
var getDueJobsScript = redis.NewScript(`
local results = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if #results > 0 then
    redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
end
return results
`)
```
- **왜 Lua를 쓰는가?** Redis에서 "조회 → 삭제"를 두 명령으로 하면, 그 사이에 다른 클라이언트가 같은 데이터를 가져갈 수 있다 (race condition). Lua 스크립트는 Redis 서버에서 **원자적으로** 실행되어 이 문제를 방지한다.
- `ZRANGEBYSCORE` — Sorted Set에서 점수 범위 내의 멤버를 조회.
- `ZREMRANGEBYSCORE` — 해당 범위의 멤버를 삭제.
- `'-inf'` — 음의 무한대. 즉, "지금까지 예약된 모든 Job"을 의미.

#### Push & Pop

```go
func (q *RedisQueue) Push(ctx context.Context, queue string, jobID string) error {
    return q.client.LPush(ctx, queueKey(queue), jobID).Err()
}

func (q *RedisQueue) Pop(ctx context.Context, queues []string, timeout time.Duration) (string, error) {
    result, err := q.client.BRPop(ctx, timeout, keys...).Result()
    if err == redis.Nil {
        return "", nil  // 타임아웃, 에러 아님
    }
    return result[1], nil  // [0]=키이름, [1]=값
}
```
- `LPush` — 리스트 왼쪽에 추가 (FIFO의 입구).
- `BRPop` — 리스트 오른쪽에서 꺼냄 (FIFO의 출구). **B**는 Blocking — 데이터가 없으면 timeout까지 기다린다. 폴링 없이 효율적으로 대기할 수 있다.
- 여러 큐 키를 동시에 대기할 수 있다. 어느 큐든 Job이 오면 바로 꺼낸다.

#### Schedule — Sorted Set

```go
func (q *RedisQueue) Schedule(ctx context.Context, jobID string, runAt time.Time) error {
    return q.client.ZAdd(ctx, scheduledSetKey, redis.Z{
        Score:  float64(runAt.Unix()),
        Member: jobID,
    }).Err()
}
```
- `ZAdd` — Sorted Set에 추가. Score는 실행 시각(Unix timestamp), Member는 Job ID.
- Sorted Set은 Score로 자동 정렬되므로, "현재 시각 이하의 Score"를 조회하면 실행해야 할 Job을 시간순으로 가져올 수 있다.

---

## 9. internal/worker/ — Worker Pool

### Handler Registry

```go
type Handler func(ctx context.Context, payload json.RawMessage) error

type Registry struct {
    mu       sync.RWMutex
    handlers map[string]Handler
}
```
- `type Handler func(...)` — 함수 타입 정의. Go에서는 함수가 일급 시민이다 — 변수에 저장하고, 맵에 넣고, 인자로 전달할 수 있다.
- `sync.RWMutex` — 읽기-쓰기 뮤텍스. 읽기는 여러 goroutine이 동시에, 쓰기는 하나만.
  - `RLock()`/`RUnlock()` — 읽기 잠금 (Get할 때)
  - `Lock()`/`Unlock()` — 쓰기 잠금 (Register할 때)
- **사용법**: `registry.Register("send_email", emailHandler)`로 Job 타입별 핸들러를 등록한다.

### Worker Pool

```go
type Pool struct {
    store      *storage.PostgresStorage
    queue      *queue.RedisQueue
    registry   *Registry
    count      int
    jobTimeout time.Duration
    wg         sync.WaitGroup
    cancel     context.CancelFunc
}
```

#### Start — goroutine 생성

```go
func (p *Pool) Start(ctx context.Context, queues []string) {
    ctx, p.cancel = context.WithCancel(ctx)
    for i := 0; i < p.count; i++ {
        p.wg.Add(1)
        go p.worker(ctx, i)
    }
}
```
- `p.wg.Add(1)` — WaitGroup 카운터 증가. "goroutine 하나가 시작됨"을 등록.
- `go p.worker(ctx, i)` — 각 Worker를 별도 goroutine으로 실행. `count`가 10이면 10개의 goroutine이 동시에 Redis에서 Job을 가져간다.

#### Worker 루프

```go
func (p *Pool) worker(ctx context.Context, id int) {
    defer p.wg.Done()  // 이 goroutine이 끝나면 카운터 감소

    for {
        select {
        case <-ctx.Done():  // 종료 시그널 확인
            return
        default:
        }

        jobID, err := p.queue.Pop(ctx, p.queues, 1*time.Second)
        if jobID == "" {
            continue  // 타임아웃, 다시 루프
        }

        p.processJob(ctx, logger, jobID)
    }
}
```
- `select { case <-ctx.Done(): return; default: }` — non-blocking 종료 확인. context가 취소되면 goroutine을 종료한다.
- `defer p.wg.Done()` — 어떤 이유로든 worker가 끝나면 WaitGroup 카운터를 감소시킨다.

#### processJob — Job 실행 흐름

```go
func (p *Pool) processJob(ctx context.Context, logger *slog.Logger, jobIDStr string) {
    // 1. Job ID 파싱
    jobID, err := uuid.Parse(jobIDStr)

    // 2. DB에서 Job 정보 조회
    job, err := p.store.GetJob(ctx, jobID)

    // 3. 핸들러 찾기
    handler, ok := p.registry.Get(job.Type)
    if !ok {
        // 핸들러 없음 → 실패 처리
    }

    // 4. 상태를 running으로 업데이트
    p.store.UpdateJobStatus(ctx, jobID, model.StatusRunning)

    // 5. 타임아웃 설정 후 핸들러 실행
    jobCtx, cancel := context.WithTimeout(ctx, p.jobTimeout)
    defer cancel()

    if err := handler(jobCtx, job.Payload); err != nil {
        p.handleFailure(ctx, job, err)  // 실패 처리
        return
    }

    // 6. 성공 → completed로 업데이트
    p.store.UpdateJobStatus(ctx, jobID, model.StatusCompleted)
}
```
- `context.WithTimeout` — 지정 시간 초과 시 자동 취소되는 context. 핸들러가 무한히 걸리는 것을 방지한다.

#### handleFailure — 재시도 로직

```go
func (p *Pool) handleFailure(ctx context.Context, job *model.Job, jobErr error) {
    newRetryCount := job.RetryCount + 1

    if newRetryCount >= job.MaxRetries {
        // 최대 재시도 초과 → dead 상태
        p.store.UpdateJobError(ctx, job.ID, model.StatusDead, ...)
        return
    }

    // 재시도 스케줄링
    p.store.UpdateJobError(ctx, job.ID, model.StatusRetrying, ...)
    retryAt := time.Now().Add(model.BackoffDuration(newRetryCount))
    p.queue.Schedule(ctx, job.ID.String(), retryAt)
}
```
- 실패 시 재시도 횟수를 확인한다.
- 아직 횟수가 남았으면: 상태를 `retrying`으로 바꾸고, 지수 백오프 후에 실행되도록 Redis Sorted Set에 스케줄링한다.
- 횟수를 초과하면: `dead` 상태로 표시. API에서 수동 재시도할 수 있다.

---

## 10. internal/scheduler/ — 스케줄러

Scheduler는 3개의 goroutine 루프를 실행한다:

### Loop 1: 예약된 Job 처리 (1초 간격)

```go
func (s *Scheduler) runScheduledLoop(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.processScheduledJobs(ctx)
        }
    }
}
```
- `time.NewTicker` — 일정 간격으로 채널(`ticker.C`)에 시간을 보낸다.
- `select` — 여러 채널을 동시에 기다린다. `ctx.Done()`이 먼저 오면 종료, `ticker.C`가 오면 작업 실행.
- `defer ticker.Stop()` — ticker가 쓰는 리소스를 정리한다.

`processScheduledJobs`는:
1. Redis Sorted Set에서 현재 시각 이전의 Job ID를 원자적으로 가져온다.
2. DB에서 Job 정보를 조회한다.
3. 해당 큐의 Redis 리스트에 Push한다 (Worker가 가져갈 수 있도록).
4. `retrying` 상태였으면 `pending`으로 업데이트한다.

### Loop 2: 미큐잉 복구 (5초 간격)

```go
func (s *Scheduler) recoverUnqueuedJobs(ctx context.Context) {
    jobs, err := s.store.GetPendingUnqueued(ctx, 5*time.Second)
    for _, job := range jobs {
        s.queue.Push(ctx, job.Queue, job.ID.String())
    }
}
```
- DB에 `pending`이지만 Redis 큐에 없는 Job을 찾아서 큐에 넣는다.
- 이런 경우가 발생하는 이유: API에서 Job을 생성할 때 DB INSERT는 성공했지만 Redis Push가 실패했을 수 있다.

### Loop 3: 좀비 Job 복구 (10초 간격)

```go
func (s *Scheduler) recoverStaleJobs(ctx context.Context) {
    jobs, err := s.store.GetStaleRunningJobs(ctx, s.jobTimeout)
    for _, job := range jobs {
        s.store.UpdateJobError(ctx, job.ID, model.StatusFailed, ...)
    }
}
```
- `running` 상태인데 너무 오래된 Job을 찾는다 (Worker가 크래시했을 수 있음).
- `failed`로 전환해서 재시도 파이프라인을 탈 수 있게 한다.

---

## 11. internal/api/ — REST API

### 11.1 response.go — JSON 응답 헬퍼

```go
func writeJSON(w http.ResponseWriter, status int, data any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
    writeJSON(w, status, ErrorResponse{Error: msg})
}
```
- `http.ResponseWriter` — HTTP 응답을 쓰는 인터페이스.
- `json.NewEncoder(w).Encode(data)` — data를 JSON으로 변환해서 w에 직접 쓴다. `json.Marshal`과 달리 중간 바이트 슬라이스를 만들지 않아서 효율적이다.

### 11.2 middleware.go — 미들웨어

```go
func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            key := r.Header.Get("X-API-Key")
            if key != apiKey {
                writeError(w, http.StatusUnauthorized, "invalid or missing API key")
                return  // next를 호출하지 않으므로 요청이 여기서 끝남
            }
            next.ServeHTTP(w, r)  // 인증 통과 → 다음 핸들러로
        })
    }
}
```
**미들웨어 패턴**: 함수가 함수를 반환하는 구조.
- `APIKeyAuth("secret")` → 미들웨어 함수를 반환
- 그 미들웨어 함수는 `http.Handler`를 받아서 새로운 `http.Handler`를 반환
- 이렇게 핸들러를 **체이닝(chaining)** 할 수 있다: 로깅 → 인증 → 실제 핸들러

### 11.3 handler.go — HTTP 핸들러

#### CreateJob

```go
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
    var req CreateJobRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    // 검증 → Job 생성 → 큐에 Push → 응답
}
```
- `r.Body` — HTTP 요청 본문. `io.Reader` 인터페이스이므로 `json.NewDecoder`에 바로 전달할 수 있다.
- `&req` — req의 주소를 전달해서 Decode가 직접 값을 채운다.

#### GetJob — URL 파라미터

```go
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
```
- `chi.URLParam(r, "id")` — 라우터에 등록한 `{id}` 경로 변수의 값을 가져온다.

#### ListJobs — 쿼리 파라미터

```go
page, _ := strconv.Atoi(r.URL.Query().Get("page"))
```
- `r.URL.Query()` — `?page=1&limit=20` 같은 쿼리 파라미터를 맵으로 파싱.
- `_`로 에러를 무시 — 변환 실패하면 0이 반환되고, 아래에서 기본값으로 처리한다.

### 11.4 router.go — 라우터 설정

```go
func NewRouter(store *storage.PostgresStorage, q *queue.RedisQueue, apiKey string, logger *slog.Logger) http.Handler {
    h := NewHandler(store, q, logger)
    r := chi.NewRouter()

    r.Use(RequestLogger(logger))       // 모든 요청에 로깅

    r.Route("/api/v1", func(r chi.Router) {
        r.Use(APIKeyAuth(apiKey))      // /api/v1 하위에만 인증

        r.Post("/jobs", h.CreateJob)
        r.Get("/jobs", h.ListJobs)
        r.Get("/jobs/{id}", h.GetJob)
        r.Post("/jobs/{id}/cancel", h.CancelJob)
        r.Post("/jobs/{id}/retry", h.RetryJob)

        r.Get("/queues", h.GetQueues)
        r.Get("/queues/{name}", h.GetQueueByName)
        r.Get("/stats", h.GetStats)
    })

    r.Get("/dashboard", DashboardHandler(store, apiKey))  // 인증 없이 접근
    return r
}
```

#### API 엔드포인트 정리

| 메서드 | 경로 | 설명 |
|--------|------|------|
| POST | /api/v1/jobs | Job 생성 |
| GET | /api/v1/jobs | Job 목록 (필터링, 페이지네이션) |
| GET | /api/v1/jobs/{id} | 특정 Job 조회 |
| POST | /api/v1/jobs/{id}/cancel | Job 취소 |
| POST | /api/v1/jobs/{id}/retry | Dead Job 재시도 |
| GET | /api/v1/queues | 큐별 통계 |
| GET | /api/v1/queues/{name} | 특정 큐 통계 |
| GET | /api/v1/stats | 전체 통계 |
| GET | /dashboard | 웹 대시보드 (인증 불필요) |

### 11.5 dashboard.go — 웹 대시보드

```go
//go:embed templates/*.html
var templateFS embed.FS

func DashboardHandler(store *storage.PostgresStorage, apiKey string) http.HandlerFunc {
    tmpl := template.Must(template.ParseFS(templateFS, "templates/dashboard.html"))

    return func(w http.ResponseWriter, r *http.Request) {
        jobs, _, _ := store.ListJobs(r.Context(), storage.ListFilter{Page: 1, Limit: 50})
        // ...
        tmpl.Execute(w, DashboardData{Jobs: jobs, Stats: stats, APIKey: apiKey})
    }
}
```
- `template.Must` — 템플릿 파싱 실패 시 panic. 서버 시작 시 한 번만 파싱하므로 괜찮다.
- `template.ParseFS` — embed된 파일 시스템에서 템플릿을 로드한다.
- `tmpl.Execute(w, data)` — 템플릿에 데이터를 채워서 HTTP 응답으로 보낸다.

---

## 12. Dockerfile & docker-compose.yml — 배포

### Dockerfile — 멀티스테이지 빌드

```dockerfile
FROM golang:1.22-alpine AS builder    # 1단계: 빌드 환경
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download                   # 의존성 먼저 다운 (캐시 활용)
COPY . .
RUN CGO_ENABLED=0 go build -o /goqueue ./cmd/goqueue

FROM alpine:3.19                      # 2단계: 실행 환경 (경량)
RUN apk add --no-cache ca-certificates
COPY --from=builder /goqueue /goqueue  # 빌드 결과물만 복사
EXPOSE 8080
CMD ["/goqueue"]
```
- **멀티스테이지 빌드**: 빌드에 필요한 Go 도구체인(~1GB)은 최종 이미지에 포함되지 않는다. 최종 이미지는 alpine + 바이너리만 담아서 수십 MB.
- `CGO_ENABLED=0` — C 라이브러리 없이 순수 Go로 빌드. alpine에서 실행 가능하게 한다.
- `go mod download`를 COPY 전에 실행 — 코드가 바뀌어도 의존성이 같으면 Docker 캐시를 재사용한다.

### docker-compose.yml

```yaml
services:
  app:
    build: .
    depends_on: [postgres, redis]     # postgres, redis가 먼저 시작
    environment:
      GOQUEUE_POSTGRES_URL: "postgresql://goqueue:goqueue@postgres:5432/goqueue?sslmode=disable"
      GOQUEUE_REDIS_URL: "redis:6379"
```
- `postgres`, `redis` — 서비스 이름이 Docker 내부 DNS 호스트명이 된다. `localhost` 대신 `postgres`로 접근.
- `depends_on` — 시작 순서만 보장. 실제 준비 완료는 앱 내에서 Ping으로 확인한다.

---

## 13. 데이터 흐름 요약

### Job 즉시 실행

```
Client → POST /api/v1/jobs
  → handler.CreateJob()
    → store.CreateJob()           ← DB에 Job 저장 (status: pending)
    → queue.Push("default", id)   ← Redis 리스트에 Job ID 추가
  → 201 Created 응답

Worker goroutine (항상 실행 중)
  → queue.Pop(["default"], 1s)    ← Redis에서 Job ID를 꺼냄 (BRPOP)
  → store.GetJob(id)              ← DB에서 Job 상세 조회
  → store.UpdateJobStatus(running)
  → handler(ctx, payload)         ← 등록된 핸들러 실행
  → 성공: store.UpdateJobStatus(completed)
  → 실패: handleFailure()        ← 재시도 or dead 처리
```

### Job 예약 실행

```
Client → POST /api/v1/jobs (run_at: "2026-03-24T10:00:00Z")
  → store.CreateJob()             ← DB에 저장 (status: scheduled)
  → queue.Schedule(id, runAt)     ← Redis Sorted Set에 추가 (score=timestamp)

Scheduler (1초마다 실행)
  → queue.GetDueJobs(now)         ← 현재 시각 이전의 Job ID를 원자적으로 가져옴
  → queue.Push(queue, id)         ← 작업 큐에 넣음
  → 이제 Worker가 처리
```

### 재시도 흐름

```
Worker에서 핸들러 실패
  → retryCount < maxRetries?
    → Yes: status=retrying, queue.Schedule(id, now+2^retry초)
      → Scheduler가 나중에 꺼내서 다시 큐에 넣음 → Worker가 재실행
    → No: status=dead (수동 재시도 필요)
      → POST /api/v1/jobs/{id}/retry로 다시 시작 가능
```

---

## 부록: Go 핵심 개념 정리

| 개념 | 이 프로젝트에서 쓰인 곳 | 설명 |
|------|----------------------|------|
| goroutine | worker.go, scheduler.go | `go func()`으로 시작하는 경량 스레드. OS 스레드보다 훨씬 가볍다 (수천 개 가능). |
| channel | main.go (quit chan) | goroutine 간 통신 수단. `<-ch`로 수신, `ch <- v`로 송신. |
| select | scheduler.go, worker.go | 여러 채널을 동시에 대기. switch문의 채널 버전. |
| context | 거의 모든 곳 | 취소 신호, 타임아웃, 값 전달. 함수 첫 번째 인자로 전달하는 것이 Go 관례. |
| interface | storage.go (scannable) | 메서드 집합을 정의. 암시적 구현 (implements 불필요). |
| defer | main.go, worker.go | 함수 종료 시 실행할 코드를 등록. 리소스 정리에 사용. |
| error wrapping | storage.go (%w) | 에러를 감싸서 원인 체인을 유지. `errors.Is`/`errors.As`로 확인. |
| struct tag | model/job.go | 구조체 필드에 메타데이터 부여. JSON, DB 매핑 등에 활용. |
| embed | storage.go, dashboard.go | 파일을 바이너리에 포함. 배포 시 별도 파일 불필요. |
| sync.WaitGroup | worker.go | goroutine 완료 대기. Add(1)→Done()→Wait() 패턴. |
| sync.RWMutex | worker.go (Registry) | 동시 접근 보호. 읽기는 동시에, 쓰기는 배타적으로. |
