package api

import (
	"net/http"

	"github.com/goqueue/internal/leader"
	"github.com/goqueue/internal/storage"
)

// HealthHandler는 인스턴스 활성 상태를 반환한다 (liveness probe).
// 프로세스가 살아 있으면 항상 200을 반환한다.
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ReadyHandler는 인스턴스 준비 상태를 반환한다 (readiness probe).
// DB 연결 상태와 리더 역할을 확인하여 응답한다.
func ReadyHandler(elector *leader.LeaderElector, store *storage.PostgresStorage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbOK := store.Pool().Ping(r.Context()) == nil
		role := "standby"
		if elector.IsLeader() {
			role = "leader"
		}

		status := http.StatusOK
		if !dbOK {
			status = http.StatusServiceUnavailable
		}

		writeJSON(w, status, map[string]any{
			"status": role,
			"ready":  dbOK,
			"db":     dbOK,
		})
	}
}
