# GoQueue

Go로 구현한 경량 분산 작업 큐(Job Queue) 시스템. PostgreSQL을 영구 저장소로, Redis를 메시지 브로커로 사용하며, Prometheus 모니터링과 실시간 웹 대시보드, 스케줄러 고가용성(HA)을 지원한다.

> 포트폴리오 및 Go / 분산 시스템 학습 목적 프로젝트.

## 주요 기능

- **작업 관리**: 생성 / 조회 / 취소 / 재시도 / 삭제 REST API
- **지연 실행**: `run_at` 지정으로 특정 시각에 작업 예약
- **재시도 & Dead Letter Queue**: 지수 백오프(2^n초) 기반 재시도, 최대 재시도 초과 시 DLQ 이동
- **Worker Pool**: 설정 가능한 동시 워커 goroutine
- **스케줄러 HA**: 리더 일렉션 기반 다중 인스턴스 운영
- **실시간 대시보드**: HTMX + WebSocket 기반 웹 UI (`/dashboard`)
- **모니터링**: Prometheus 메트릭(`/metrics`) + Grafana 대시보드
- **API 문서**: Swagger UI (`/swagger`)

## 기술 스택

| 레이어 | 사용 기술 |
|--------|-----------|
| 언어 | Go 1.25 |
| HTTP 라우터 | chi/v5 |
| DB | PostgreSQL 16 (pgx/v5) |
| 큐 | Redis 7 (go-redis/v9) |
| 마이그레이션 | golang-migrate/v4 |
| 프론트엔드 | Go 템플릿 + HTMX |
| 관찰성 | Prometheus + Grafana, OpenTelemetry, slog |
| 테스트 | testify + testcontainers-go |
| 배포 | Docker Compose (멀티 스테이지 Dockerfile) |

## 아키텍처

```
cmd/goqueue/          애플리케이션 엔트리포인트
internal/
├── model/            Job 도메인 엔티티 & 상태 머신
├── storage/          PostgreSQL 영속 레이어
├── queue/            Redis 큐 (BRPOP, Sorted Set + Lua)
├── worker/           워커 풀 & 핸들러 레지스트리
├── scheduler/        예약/복구/stale 체크 3개 백그라운드 루프
├── leader/           스케줄러 리더 일렉션 (HA)
├── api/              REST 핸들러 + 미들웨어 + 라우터
├── dashboard/        HTMX + WebSocket 대시보드
├── metrics/          Prometheus 메트릭
└── config/           환경변수 기반 설정 로더
```

### 작업 상태 머신

```
pending   → running | scheduled | cancelled
running   → completed | failed | cancelled
failed    → retrying | dead
retrying  → running
scheduled → running | cancelled
dead      → pending     (API를 통한 수동 복구)
```

## 빠른 시작 (Docker Compose)

전체 스택(앱 2인스턴스 + Postgres + Redis + Prometheus + Grafana)을 한번에 실행한다.

```bash
make docker-up
```

| 서비스 | 주소 |
|--------|------|
| GoQueue API #1 | http://localhost:8080 |
| GoQueue API #2 | http://localhost:8081 |
| Dashboard | http://localhost:8080/dashboard |
| Swagger UI | http://localhost:8080/swagger/index.html |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin / admin) |

중지:

```bash
make docker-down
```

## 로컬 빌드 & 실행

로컬에서 PostgreSQL과 Redis가 실행 중이어야 한다.

```bash
make build    # bin/goqueue 생성
make run      # 빌드 후 실행
make test     # 전체 테스트 실행
```

## 환경 변수

| 변수명 | 기본값 | 설명 |
|--------|--------|------|
| `GOQUEUE_POSTGRES_URL` | (필수) | PostgreSQL 연결 문자열 |
| `GOQUEUE_API_KEY` | (필수) | API 인증 키 (`X-API-Key` 헤더) |
| `GOQUEUE_REDIS_URL` | `localhost:6379` | Redis 주소 |
| `GOQUEUE_HTTP_ADDR` | `:8080` | HTTP 서버 리슨 주소 |
| `GOQUEUE_WORKER_COUNT` | `10` | 워커 goroutine 수 |
| `GOQUEUE_JOB_TIMEOUT` | `30s` | 작업 실행 타임아웃 |
| `GOQUEUE_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown 타임아웃 |
| `GOQUEUE_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `GOQUEUE_INSTANCE_ID` | - | HA에서 인스턴스 식별자 |

## REST API

모든 `/api/v1/*` 요청은 `X-API-Key` 헤더 필요.

| Method | Path | 설명 |
|--------|------|------|
| POST | `/api/v1/jobs` | 작업 생성 |
| GET | `/api/v1/jobs` | 작업 목록 (필터 지원) |
| GET | `/api/v1/jobs/{id}` | 단일 작업 조회 |
| POST | `/api/v1/jobs/{id}/cancel` | 작업 취소 |
| POST | `/api/v1/jobs/{id}/retry` | 작업 재시도 |
| DELETE | `/api/v1/jobs/{id}` | 작업 삭제 |
| GET | `/api/v1/queues` | 큐 목록 |
| GET | `/api/v1/queues/{name}` | 큐별 통계 |
| GET | `/api/v1/stats` | 전체 통계 |

### 작업 생성 예시

```bash
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "X-API-Key: change-me-in-production" \
  -H "Content-Type: application/json" \
  -d '{
    "queue": "default",
    "type": "send_email",
    "payload": {"to": "user@example.com"},
    "max_retries": 3,
    "run_at": "2026-04-22T15:00:00Z"
  }'
```

### 비인증 엔드포인트

| Path | 설명 |
|------|------|
| `/health` | 헬스 체크 |
| `/ready` | Readiness probe (DB / Redis / 리더 상태) |
| `/metrics` | Prometheus 메트릭 |
| `/swagger/*` | Swagger UI |
| `/dashboard` | 실시간 웹 대시보드 |
| `/ws` | 대시보드 WebSocket |

## 테스트

`testcontainers-go`를 사용해 실제 PostgreSQL / Redis 컨테이너로 통합 테스트를 실행한다. Docker 데몬이 실행 중이어야 한다.

```bash
make test
```

## 문서

- [CODE_GUIDE.md](./CODE_GUIDE.md) — 코딩 컨벤션
- [PERFORMANCE.md](./PERFORMANCE.md) — 성능 측정 기록
- [docs/swagger.yaml](./docs/swagger.yaml) — OpenAPI 스펙

## 라이선스

Portfolio / 학습용 프로젝트.
