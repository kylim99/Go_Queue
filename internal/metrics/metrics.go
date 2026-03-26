package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Registry는 GoQueue 전용 Prometheus 메트릭 레지스트리이다.
// 기본 레지스트리와 분리하여 테스트 및 /metrics 엔드포인트에서 사용한다.
var Registry = prometheus.NewRegistry()

func init() {
	// Go 런타임 및 프로세스 메트릭 수집기 등록
	Registry.MustRegister(prometheus.NewGoCollector())
	Registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
}

// JobsProcessed는 처리된 작업의 총 수를 type과 status 레이블로 추적하는 카운터이다.
var JobsProcessed = promauto.With(Registry).NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "goqueue",
		Name:      "jobs_processed_total",
		Help:      "Total number of jobs processed, by type and status",
	},
	[]string{"type", "status"},
)

// QueueDepth는 각 큐에 대기 중인 작업 수를 나타내는 게이지이다.
var QueueDepth = promauto.With(Registry).NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Name:      "queue_depth",
		Help:      "Current number of jobs waiting in each queue",
	},
	[]string{"queue"},
)

// JobDuration은 작업 처리 소요 시간을 type별로 기록하는 히스토그램이다.
var JobDuration = promauto.With(Registry).NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "goqueue",
		Name:      "job_duration_seconds",
		Help:      "Duration of job processing in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.01, 2, 15),
	},
	[]string{"type"},
)

// ActiveWorkers는 현재 작업을 처리 중인 워커 수를 나타내는 게이지이다.
var ActiveWorkers = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Name:      "active_workers",
		Help:      "Number of workers currently processing jobs",
	},
)

// ErrorsTotal은 작업 처리 중 발생한 에러의 총 수를 type별로 추적하는 카운터이다.
var ErrorsTotal = promauto.With(Registry).NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "goqueue",
		Name:      "errors_total",
		Help:      "Total number of job processing errors, by type",
	},
	[]string{"type"},
)

// RetriesTotal은 재시도된 작업의 총 수를 type별로 추적하는 카운터이다.
var RetriesTotal = promauto.With(Registry).NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "goqueue",
		Name:      "retries_total",
		Help:      "Total number of job retries, by type",
	},
	[]string{"type"},
)

// SchedulerActive는 스케줄러의 활성 상태를 나타내는 게이지이다 (1=활성, 0=비활성).
var SchedulerActive = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Name:      "scheduler_active",
		Help:      "Whether the scheduler is active (1) or inactive (0)",
	},
)

// DBPoolAcquired는 현재 사용 중인 DB 커넥션 수를 나타내는 게이지이다.
var DBPoolAcquired = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Subsystem: "db_pool",
		Name:      "acquired_conns",
		Help:      "Number of currently acquired database connections",
	},
)

// DBPoolIdle은 유휴 상태의 DB 커넥션 수를 나타내는 게이지이다.
var DBPoolIdle = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Subsystem: "db_pool",
		Name:      "idle_conns",
		Help:      "Number of idle database connections",
	},
)

// DBPoolMax는 DB 커넥션 풀의 최대 커넥션 수를 나타내는 게이지이다.
var DBPoolMax = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Subsystem: "db_pool",
		Name:      "max_conns",
		Help:      "Maximum number of database connections in the pool",
	},
)

// DBPoolTotal은 DB 커넥션 풀의 전체 커넥션 수를 나타내는 게이지이다.
var DBPoolTotal = promauto.With(Registry).NewGauge(
	prometheus.GaugeOpts{
		Namespace: "goqueue",
		Subsystem: "db_pool",
		Name:      "total_conns",
		Help:      "Total number of database connections in the pool",
	},
)
