package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"universal-session-generator/internal/bot/polling"
)

func TestLivenessHandler(t *testing.T) {
	m := NewMonitor(nil, nil, nil)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	m.LivenessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if rr.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got %q", rr.Body.String())
	}
}

func TestReadinessHandlerHealthy(t *testing.T) {
	s := polling.NewSupervisor(nil, nil, polling.DefaultConfig())
	// Simulate running supervisor
	s.StartFakeRunning(time.Now())

	m := NewMonitor(s, nil, nil)
	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()

	m.ReadinessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestReadinessHandlerUnhealthyWhenPollingDead(t *testing.T) {
	s := polling.NewSupervisor(nil, nil, polling.DefaultConfig())
	// Not started / dead
	m := NewMonitor(s, nil, nil)
	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()

	m.ReadinessHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable when polling dead, got %d", rr.Code)
	}
}

func TestHealthHandlerStructuredJSON(t *testing.T) {
	s := polling.NewSupervisor(nil, nil, polling.DefaultConfig())
	s.StartFakeRunning(time.Now())

	m := NewMonitor(s, nil, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	m.HealthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	var report Report
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if report.Status != StatusHealthy {
		t.Errorf("expected status healthy, got %s", report.Status)
	}
	if report.Service != "universal-session-generator" {
		t.Errorf("unexpected service name: %s", report.Service)
	}
	if !report.Polling.IsAlive {
		t.Errorf("expected polling to be alive")
	}
}

func TestHealthHandlerNoInformationDisclosure(t *testing.T) {
	s := polling.NewSupervisor(nil, nil, polling.DefaultConfig())
	s.StartFakeRunning(time.Now())

	m := NewMonitor(s, nil, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	m.HealthHandler(rr, req)

	var rawMap map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rawMap); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	bodyStr := rr.Body.String()
	sensitiveFields := []string{"last_error", "last_update_id", "consecutive_errors", "total_updates_handled"}
	for _, field := range sensitiveFields {
		if strings.Contains(bodyStr, field) {
			t.Errorf("information disclosure vulnerability: /health response contains %q", field)
		}
	}
}
