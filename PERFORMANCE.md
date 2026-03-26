# GoQueue Performance Record

이 문서는 각 Phase 완료 시 성능 측정 결과를 누적 기록한다.

## Phase 1: Prometheus Monitoring + Grafana

**측정일:** 2026-03-26
**환경:** Docker Compose (app + postgres:16-alpine + redis:7-alpine)

### 메트릭 오버헤드 측정 (Before/After)

| 지표 | Before (메트릭 없음) | After (메트릭 포함) | 변화 | 분석 |
|------|---------------------|--------------------|----|------|
| API 응답 시간 p50 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| API 응답 시간 p95 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| API 응답 시간 p99 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| 초당 작업 처리량 (throughput) | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| CPU 사용량 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| 메모리 사용량 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |
| 고루틴 수 | 측정 예정 | 측정 예정 | - | Phase 1 완료 후 측정 |

### /metrics 엔드포인트 성능

| 지표 | 값 | 비고 |
|------|---|------|
| /metrics 응답 시간 | 측정 예정 | Prometheus 포맷 렌더링 시간 |
| /metrics 응답 크기 | 측정 예정 | 시계열 수에 비례 |

### 기준선 요약

Phase 1은 모니터링 인프라 추가가 주 목적이므로, 이 테이블은 메트릭 수집이 애플리케이션 성능에 미치는 오버헤드를 측정하는 기준선이다. 실제 값은 Docker Compose 환경에서 부하 테스트 후 기록한다.
