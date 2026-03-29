package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr        string
	PostgresURL     string
	RedisURL        string
	APIKey          string
	WorkerCount     int
	JobTimeout      time.Duration
	ShutdownTimeout    time.Duration
	LogLevel           string
	LeaderPollInterval time.Duration
}

func Load() (*Config, error) {
	pgURL := os.Getenv("GOQUEUE_POSTGRES_URL")
	if pgURL == "" {
		return nil, fmt.Errorf("GOQUEUE_POSTGRES_URL is required")
	}

	apiKey := os.Getenv("GOQUEUE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GOQUEUE_API_KEY is required")
	}

	return &Config{
		HTTPAddr:        getEnvOrDefault("GOQUEUE_HTTP_ADDR", ":8080"),
		PostgresURL:     pgURL,
		RedisURL:        getEnvOrDefault("GOQUEUE_REDIS_URL", "localhost:6379"),
		APIKey:          apiKey,
		WorkerCount:     getEnvIntOrDefault("GOQUEUE_WORKER_COUNT", 10),
		JobTimeout:      getEnvDurationOrDefault("GOQUEUE_JOB_TIMEOUT", 30*time.Second),
		ShutdownTimeout: getEnvDurationOrDefault("GOQUEUE_SHUTDOWN_TIMEOUT", 30*time.Second),
		LogLevel:           getEnvOrDefault("GOQUEUE_LOG_LEVEL", "info"),
		LeaderPollInterval: getEnvDurationOrDefault("GOQUEUE_LEADER_POLL_INTERVAL", 2*time.Second),
	}, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvIntOrDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid int env var, using default", "key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
	return i
}

func getEnvDurationOrDefault(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration env var, using default", "key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
	return d
}
