package polling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

func TestCalculateBackoff(t *testing.T) {
	s := NewSupervisor(nil, nil, Config{
		MinBackoff: 1 * time.Second,
		MaxBackoff: 30 * time.Second,
	})

	// Test 1: retryAfter specified
	bo := s.calculateBackoff(1, 10)
	if bo < 11*time.Second || bo > 12*time.Second {
		t.Errorf("expected backoff around 11s, got %v", bo)
	}

	// Test 2: Consecutive errors 1, 2, 3
	b1 := s.calculateBackoff(1, 0)
	if b1 < 1*time.Second || b1 > 2*time.Second {
		t.Errorf("expected backoff ~1s + jitter, got %v", b1)
	}

	b2 := s.calculateBackoff(2, 0)
	if b2 < 2*time.Second || b2 > 3*time.Second {
		t.Errorf("expected backoff ~2s + jitter, got %v", b2)
	}

	b3 := s.calculateBackoff(3, 0)
	if b3 < 4*time.Second || b3 > 5*time.Second {
		t.Errorf("expected backoff ~4s + jitter, got %v", b3)
	}

	// Test 3: Cap at MaxBackoff
	bHigh := s.calculateBackoff(10, 0)
	if bHigh > 31*time.Second {
		t.Errorf("expected backoff capped at maxBackoff (30s) + jitter, got %v", bHigh)
	}
}

func TestHandlePollingErrorClassification(t *testing.T) {
	s := NewSupervisor(nil, nil, DefaultConfig())

	// Test 1: 401 Unauthorized is FATAL
	fatalErr := &gotgbot.TelegramError{
		Code:        401,
		Description: "Unauthorized",
	}
	isFatal, _ := s.handlePollingError(context.Background(), fatalErr)
	if !isFatal {
		t.Errorf("expected 401 Unauthorized to be fatal")
	}

	// Test 2: 429 Too Many Requests is recoverable and respects RetryAfter
	rateLimitErr := &gotgbot.TelegramError{
		Code:        429,
		Description: "Too Many Requests",
		ResponseParams: &gotgbot.ResponseParameters{
			RetryAfter: 15,
		},
	}
	isFatal2, bo2 := s.handlePollingError(context.Background(), rateLimitErr)
	if isFatal2 {
		t.Errorf("expected 429 to be recoverable")
	}
	if bo2 < 16*time.Second {
		t.Errorf("expected at least 16s backoff for RetryAfter:15, got %v", bo2)
	}

	// Test 3: Generic network error is recoverable
	netErr := errors.New("connection reset by peer")
	isFatal3, bo3 := s.handlePollingError(context.Background(), netErr)
	if isFatal3 {
		t.Errorf("expected network error to be recoverable")
	}
	if bo3 <= 0 {
		t.Errorf("expected non-zero backoff for network error")
	}
}

func TestSupervisorHeartbeatAndHealth(t *testing.T) {
	s := NewSupervisor(nil, nil, DefaultConfig())

	// Before start, not healthy
	if s.IsHealthy(0) {
		t.Errorf("supervisor should not be healthy before start")
	}

	// Simulate running state
	s.isRunning.Store(true)
	s.state = StateRunning
	s.lastHeartbeat = time.Now()

	if !s.IsHealthy(60 * time.Second) {
		t.Errorf("supervisor should be healthy with fresh heartbeat")
	}

	// Simulate old heartbeat (stalled polling)
	s.lastHeartbeat = time.Now().Add(-120 * time.Second)
	if s.IsHealthy(60 * time.Second) {
		t.Errorf("supervisor should be unhealthy with heartbeat 120s ago")
	}

	// Fatal error state
	s.state = StateFatalError
	s.lastHeartbeat = time.Now()
	if s.IsHealthy(60 * time.Second) {
		t.Errorf("supervisor should not be healthy in StateFatalError")
	}
}

func TestDuplicateStartPrevention(t *testing.T) {
	dispatcher := ext.NewDispatcher(nil)
	bot := &gotgbot.Bot{BotClient: &gotgbot.BaseBotClient{}}
	s := NewSupervisor(bot, dispatcher, DefaultConfig())

	// Fake running state
	s.isRunning.Store(true)

	err := s.Start(context.Background())
	if err == nil {
		t.Fatalf("expected error on duplicate Start(), got nil")
	}
}

