package main

// @title GoQueue API
// @version 2.0
// @description 분산 작업 큐 시스템 GoQueue의 REST API
// @host localhost:8080
// @BasePath /api/v1
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goqueue/internal/api"
	"github.com/goqueue/internal/config"
	"github.com/goqueue/internal/dashboard"
	"github.com/goqueue/internal/leader"
	"github.com/goqueue/internal/metrics"
	"github.com/goqueue/internal/queue"
	"github.com/goqueue/internal/scheduler"
	"github.com/goqueue/internal/storage"
	"github.com/goqueue/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := storage.NewPostgresStorage(ctx, cfg.PostgresURL)
	if err != nil {
		logger.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.RunMigrations(); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	rq, err := queue.NewRedisQueue(ctx, cfg.RedisURL)
	if err != nil {
		logger.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer rq.Close()

	// WebSocket Hub 및 대시보드 렌더러 생성
	hub := dashboard.NewHub(logger)
	go hub.Run(ctx)
	renderer := dashboard.NewTemplateRenderer(store, cfg.APIKey)
	go dashboard.StartSubscriber(ctx, rq.Client(), hub, renderer, logger)

	registry := worker.NewRegistry()
	// 데모 핸들러 등록: 각 작업 타입별로 간단한 처리 로직
	demoHandler := func(ctx context.Context, payload json.RawMessage) error {
		time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)
		return nil
	}
	// 10% 확률로 실패하는 핸들러 (에러 시나리오 테스트용)
	flakyHandler := func(ctx context.Context, payload json.RawMessage) error {
		time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)
		if rand.Intn(10) == 0 {
			return fmt.Errorf("random failure for testing")
		}
		return nil
	}
	registry.Register("process", demoHandler)
	registry.Register("send_email", flakyHandler)
	registry.Register("push_notify", demoHandler)
	registry.Register("generate_report", flakyHandler)

	queues := []string{"default", "email", "notification", "report"}

	pool := worker.NewPool(store, rq, registry, cfg.WorkerCount, cfg.JobTimeout, rq.Client(), logger)
	pool.Start(ctx, queues)

	sched := scheduler.New(store, rq, cfg.JobTimeout, rq.Client(), logger)

	// 리더 선출기 생성: 리더 획득 시 스케줄러 시작, 해제 시 중지
	elector := leader.New(
		cfg.PostgresURL,
		cfg.LeaderPollInterval,
		func() { sched.Start(ctx) }, // onAcquire: 스케줄러 시작
		func() { sched.Stop() },     // onRelease: 스케줄러 중지
		logger,
	)

	// 리더 선출 루프를 별도 고루틴에서 시작
	go func() {
		if err := elector.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("leader election failed", "error", err)
		}
	}()

	// DB 커넥션 풀 메트릭 수집 고루틴 시작
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stat := store.Pool().Stat()
				metrics.DBPoolAcquired.Set(float64(stat.AcquiredConns()))
				metrics.DBPoolIdle.Set(float64(stat.IdleConns()))
				metrics.DBPoolMax.Set(float64(stat.MaxConns()))
				metrics.DBPoolTotal.Set(float64(stat.TotalConns()))
			}
		}
	}()

	// 큐 깊이 메트릭 수집 고루틴 시작
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, q := range queues {
					length, err := rq.QueueLength(ctx, q)
					if err == nil {
						metrics.QueueDepth.WithLabelValues(q).Set(float64(length))
					}
				}
			}
		}
	}()

	router := api.NewRouter(store, rq, cfg.APIKey, elector, hub, renderer, logger)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}

	go func() {
		logger.Info("HTTP server starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")

	cancel()

	// WebSocket Hub 종료
	hub.Shutdown()

	// 리더 선출 중지 및 advisory lock 해제
	elector.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	pool.Stop()
	logger.Info("GoQueue stopped")
}
