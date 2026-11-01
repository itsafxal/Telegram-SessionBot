package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"universal-session-generator/internal/auth"
	"universal-session-generator/internal/bot/polling"
	"universal-session-generator/internal/database"
)

// OverallStatus classifies the holistic health of the application.
type OverallStatus string

const (
	StatusHealthy   OverallStatus = "healthy"
	StatusDegraded  OverallStatus = "degraded"
	StatusUnhealthy OverallStatus = "unhealthy"
)

// DatabaseHealth represents database connection metrics.
type DatabaseHealth struct {
	Status    string  `json:"status"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// AuthHealth represents active authentication metrics.
type AuthHealth struct {
	ActiveSessions int `json:"active_sessions"`
}

// SafePollingStatus exposes only sanitized polling health without leaking Telegram IDs or error strings.
type SafePollingStatus struct {
	State   string `json:"state"`
	IsAlive bool   `json:"is_alive"`
}

// Report holds the minimal, safe public health response for /health without leaking internal architecture or credentials.
type Report struct {
	Status        OverallStatus     `json:"status"`
	Service       string            `json:"service"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	Timestamp     time.Time         `json:"timestamp"`
	Polling       SafePollingStatus `json:"polling"`
}

// Monitor coordinates health checks across all subsystems.
type Monitor struct {
	startTime  time.Time
	supervisor *polling.Supervisor
	db         *database.Database
	authMgr    *auth.Manager
}

// NewMonitor initializes a new health monitor.
func NewMonitor(s *polling.Supervisor, db *database.Database, a *auth.Manager) *Monitor {
	return &Monitor{
		startTime:  time.Now(),
		supervisor: s,
		db:         db,
		authMgr:    a,
	}
}

// RegisterRoutes registers /healthz, /readyz, /health, and / on the provided mux.
func (m *Monitor) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", m.RootHandler)
	mux.HandleFunc("/healthz", m.LivenessHandler)
	mux.HandleFunc("/readyz", m.ReadinessHandler)
	mux.HandleFunc("/health", m.HealthHandler)
}

// RootHandler returns a friendly JSON message for root HTTP hits.
func (m *Monitor) RootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service": "Universal Telegram MTProto Session Generator",
		"status":  "running",
		"time":    time.Now().UTC(),
	})
}

// LivenessHandler (/healthz) verifies that the HTTP server process is running and not deadlocked.
func (m *Monitor) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// ReadinessHandler (/readyz) verifies that the bot is ready to serve: DB is reachable and polling is active.
func (m *Monitor) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// 1. Check Database connection
	var dbErr error
	if m.db != nil {
		dbErr = m.db.Ping(ctx)
	}

	// 2. Check Polling Supervisor (idle bot is normal; check if polling is running and beating)
	pollingHealthy := true
	if m.supervisor != nil {
		pollingHealthy = m.supervisor.IsHealthy(90 * time.Second)
	}

	w.Header().Set("Content-Type", "application/json")

	if dbErr != nil || !pollingHealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":           false,
			"database_ready":  dbErr == nil,
			"polling_healthy": pollingHealthy,
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready":           true,
		"polling_healthy": true,
		"database_ready":  true,
	})
}

// HealthHandler (/health) outputs structured JSON metrics without performing blocking Telegram network calls.
func (m *Monitor) HealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// 1. Database check (redact raw errors to prevent information disclosure)
	dbHealth := DatabaseHealth{Status: "connected"}
	dbStart := time.Now()
	if m.db != nil {
		if err := m.db.Ping(ctx); err != nil {
			dbHealth.Status = "disconnected"
		} else {
			dbHealth.LatencyMS = float64(time.Since(dbStart).Microseconds()) / 1000.0
		}
	}

	// 2. Polling status
	var pollStatus polling.Status
	pollingHealthy := true
	if m.supervisor != nil {
		pollStatus = m.supervisor.GetStatus()
		pollingHealthy = m.supervisor.IsHealthy(90 * time.Second)
	}

	// 3. Overall status classification
	overall := StatusHealthy
	if dbHealth.Status != "connected" || !pollingHealthy {
		if !pollingHealthy && pollStatus.State == polling.StateFatalError {
			overall = StatusUnhealthy
		} else if dbHealth.Status != "connected" {
			overall = StatusUnhealthy
		} else {
			overall = StatusDegraded
		}
	}

	report := Report{
		Status:        overall,
		Service:       "universal-session-generator",
		UptimeSeconds: time.Since(m.startTime).Seconds(),
		Timestamp:     time.Now().UTC(),
		Polling: SafePollingStatus{
			State:   string(pollStatus.State),
			IsAlive: pollStatus.IsAlive,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if overall == StatusUnhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(report)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
