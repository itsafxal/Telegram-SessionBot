package polling

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

// State represents the current lifecycle state of the polling supervisor.
type State string

const (
	StateStopped    State = "stopped"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateBackoff    State = "backoff"
	StateRecovering State = "recovering"
	StateFatalError State = "fatal_error"
)

// Config configures polling supervisor timeouts and backoff strategies.
type Config struct {
	MinBackoff         time.Duration
	MaxBackoff         time.Duration
	PollTimeoutSeconds int
	RequestTimeout     time.Duration
	AllowedUpdates     []string
}

// DefaultConfig provides recommended production defaults.
func DefaultConfig() Config {
	return Config{
		MinBackoff:         1 * time.Second,
		MaxBackoff:         30 * time.Second,
		PollTimeoutSeconds: 30,
		RequestTimeout:     35 * time.Second,
		AllowedUpdates: []string{
			"message",
			"callback_query",
		},
	}
}

// Status contains thread-safe point-in-time metrics for health monitoring.
type Status struct {
	State               State     `json:"state"`
	IsAlive             bool      `json:"is_alive"`
	LastHeartbeat       time.Time `json:"last_heartbeat"`
	SecondsSinceBeat    float64   `json:"seconds_since_heartbeat"`
	LastUpdateID        int64     `json:"last_update_id"`
	ConsecutiveErrors   int64     `json:"consecutive_errors"`
	TotalUpdatesHandled int64     `json:"total_updates_handled"`
	LastError           string    `json:"last_error,omitempty"`
	LastErrorTime       time.Time `json:"last_error_time,omitempty"`
}

// Supervisor manages the long-polling lifecycle with controlled recovery, backoff, and heartbeat tracking.
type Supervisor struct {
	bot        *gotgbot.Bot
	dispatcher *ext.Dispatcher
	cfg        Config

	state               State
	lastHeartbeat       time.Time
	lastUpdateID        int64
	consecutiveErrors   int64
	totalUpdatesHandled int64
	lastError           string
	lastErrorTime       time.Time

	mu         sync.RWMutex
	cancelFunc context.CancelFunc
	doneChan   chan struct{}
	updateChan chan json.RawMessage
	isRunning  atomic.Bool
	stopOnce   sync.Once
	randGen    *rand.Rand
	randMu     sync.Mutex
}

// NewSupervisor creates a polling supervisor.
func NewSupervisor(b *gotgbot.Bot, d *ext.Dispatcher, cfg Config) *Supervisor {
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = 1 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.PollTimeoutSeconds <= 0 {
		cfg.PollTimeoutSeconds = 30
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 35 * time.Second
	}

	return &Supervisor{
		bot:        b,
		dispatcher: d,
		cfg:        cfg,
		state:      StateStopped,
		randGen:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start begins the supervised polling loop. Returns error if already running.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.isRunning.Load() {
		s.mu.Unlock()
		return errors.New("polling supervisor is already running")
	}

	pollCtx, cancel := context.WithCancel(ctx)
	s.cancelFunc = cancel
	s.doneChan = make(chan struct{})
	s.updateChan = make(chan json.RawMessage, 100)
	s.state = StateStarting
	s.lastHeartbeat = time.Now()
	s.isRunning.Store(true)
	s.stopOnce = sync.Once{}
	s.mu.Unlock()

	// Clear webhooks before polling
	delOpts := &gotgbot.DeleteWebhookOpts{DropPendingUpdates: true}
	if _, err := s.bot.DeleteWebhook(delOpts); err != nil {
		slog.Warn("Failed to delete webhook before starting polling (continuing)", "error", err)
	}

	// Start dispatcher consumer in background
	go s.dispatcher.Start(s.bot, s.updateChan)

	// Run polling loop in background
	go s.pollLoop(pollCtx)

	slog.Info("Polling supervisor started successfully", "poll_timeout", s.cfg.PollTimeoutSeconds)
	return nil
}

// Stop gracefully stops polling and waits for in-flight updates to complete.
func (s *Supervisor) Stop() error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		if !s.isRunning.Load() {
			s.mu.Unlock()
			return
		}
		s.state = StateStopped
		s.isRunning.Store(false)
		if s.cancelFunc != nil {
			s.cancelFunc()
		}
		s.mu.Unlock()

		// Wait for polling loop to exit
		if s.doneChan != nil {
			<-s.doneChan
		}

		// Close updates channel to signal dispatcher
		if s.updateChan != nil {
			close(s.updateChan)
		}

		// Wait for dispatcher to finish in-flight updates
		s.dispatcher.Stop()
		slog.Info("Polling supervisor stopped cleanly")
	})
	return nil
}

// pollLoop is the core supervised polling worker with resilient error handling.
func (s *Supervisor) pollLoop(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		if s.state != StateFatalError {
			s.state = StateStopped
		}
		s.isRunning.Store(false)
		s.mu.Unlock()
		close(s.doneChan)
	}()

	var offset int64

	params := map[string]any{
		"timeout": strconv.Itoa(s.cfg.PollTimeoutSeconds),
		"limit":   "100",
	}
	if len(s.cfg.AllowedUpdates) > 0 {
		if b, err := json.Marshal(s.cfg.AllowedUpdates); err == nil {
			params["allowed_updates"] = string(b)
		}
	}

	reqOpts := &gotgbot.RequestOpts{
		Timeout: s.cfg.RequestTimeout,
	}

	for {
		select {
		case <-ctx.Done():
			slog.Debug("Polling loop exiting due to context cancellation")
			return
		default:
		}

		if offset != 0 {
			params["offset"] = strconv.FormatInt(offset, 10)
		}

		// Update state to running
		s.mu.Lock()
		if s.state != StateRunning {
			s.state = StateRunning
		}
		s.mu.Unlock()

		// Execute getUpdates RPC
		rawResp, err := s.bot.RequestWithContext(ctx, "getUpdates", params, reqOpts)

		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}

			isFatal, backoffDur := s.handlePollingError(ctx, err)
			if isFatal {
				slog.Error("Fatal polling error encountered. Halting polling supervisor", "error", err)
				s.mu.Lock()
				s.state = StateFatalError
				s.lastError = err.Error()
				s.lastErrorTime = time.Now()
				s.mu.Unlock()
				return
			}

			// Apply backoff delay with interruptible sleep
			s.mu.Lock()
			s.state = StateBackoff
			s.mu.Unlock()

			slog.Warn("Telegram polling error, backing off",
				"error", err,
				"backoff_duration", backoffDur,
				"consecutive_errors", s.getConsecutiveErrors(),
			)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoffDur):
			}
			continue
		}

		// Successful network roundtrip: record heartbeat
		s.recordSuccess()

		if len(rawResp) == 0 {
			continue
		}

		var rawUpdates []json.RawMessage
		if err := json.Unmarshal(rawResp, &rawUpdates); err != nil {
			slog.Error("Failed to unmarshal raw updates from Telegram", "error", err)
			continue
		}

		if len(rawUpdates) == 0 {
			continue
		}

		// Parse last update ID to advance offset
		var lastUpdate struct {
			UpdateID int64 `json:"update_id"`
		}
		if err := json.Unmarshal(rawUpdates[len(rawUpdates)-1], &lastUpdate); err == nil && lastUpdate.UpdateID > 0 {
			offset = lastUpdate.UpdateID + 1
			s.mu.Lock()
			s.lastUpdateID = lastUpdate.UpdateID
			s.totalUpdatesHandled += int64(len(rawUpdates))
			s.mu.Unlock()
		}

		// Dispatch updates to channel
		for _, upd := range rawUpdates {
			select {
			case <-ctx.Done():
				return
			case s.updateChan <- upd:
			}
		}
	}
}

// handlePollingError classifies the error, updates metrics, and computes backoff duration.
func (s *Supervisor) handlePollingError(ctx context.Context, err error) (isFatal bool, backoff time.Duration) {
	s.mu.Lock()
	s.consecutiveErrors++
	consec := s.consecutiveErrors
	s.lastError = err.Error()
	s.lastErrorTime = time.Now()
	s.mu.Unlock()

	var tgErr *gotgbot.TelegramError
	if errors.As(err, &tgErr) {
		switch tgErr.Code {
		case 401: // Unauthorized (Invalid Token)
			return true, 0
		case 404: // Not Found (Invalid Bot URL/Token format)
			return true, 0
		case 409: // Conflict: another getUpdates instance or active webhook
			slog.Warn("Telegram 409 Conflict: removing webhook and clearing pending updates")
			delOpts := &gotgbot.DeleteWebhookOpts{DropPendingUpdates: false}
			_, _ = s.bot.DeleteWebhook(delOpts)
			return false, 5 * time.Second
		case 429: // Too Many Requests
			var retryAfter int
			if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
				retryAfter = int(tgErr.ResponseParams.RetryAfter)
			}
			backoff := s.calculateBackoff(consec, retryAfter)
			return false, backoff
		}
	}

	// Network drop / timeout / 502 / 503 / 504 / DNS failure
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		// Normal long-poll read timeout on client side: slight backoff
		return false, 500 * time.Millisecond
	}

	backoff = s.calculateBackoff(consec, 0)
	return false, backoff
}

// calculateBackoff computes exponential backoff with random jitter.
func (s *Supervisor) calculateBackoff(consecutive int64, retryAfter int) time.Duration {
	if retryAfter > 0 {
		return time.Duration(retryAfter+1) * time.Second
	}

	// Exponential backoff: minBackoff * 2^(consecutive - 1)
	multiplier := float64(int64(1) << uint(min(consecutive-1, 6)))
	backoff := time.Duration(float64(s.cfg.MinBackoff) * multiplier)
	if backoff > s.cfg.MaxBackoff {
		backoff = s.cfg.MaxBackoff
	}

	// Add 10-25% jitter
	s.randMu.Lock()
	jitterMs := s.randGen.Intn(500)
	s.randMu.Unlock()

	return backoff + time.Duration(jitterMs)*time.Millisecond
}

func (s *Supervisor) recordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeat = time.Now()
	s.consecutiveErrors = 0
}

func (s *Supervisor) getConsecutiveErrors() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.consecutiveErrors
}

// IsHealthy returns true if the supervisor is running and received a heartbeat recently.
func (s *Supervisor) IsHealthy(maxHeartbeatAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.isRunning.Load() {
		return false
	}
	if s.state == StateFatalError || s.state == StateStopped {
		return false
	}

	// If maxHeartbeatAge is 0, default to 90 seconds (3x poll timeout)
	if maxHeartbeatAge <= 0 {
		maxHeartbeatAge = time.Duration(s.cfg.PollTimeoutSeconds*3) * time.Second
	}

	return time.Since(s.lastHeartbeat) <= maxHeartbeatAge
}

// GetStatus returns a snapshot of supervisor metrics.
func (s *Supervisor) GetStatus() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Status{
		State:               s.state,
		IsAlive:             s.isRunning.Load(),
		LastHeartbeat:       s.lastHeartbeat,
		SecondsSinceBeat:    time.Since(s.lastHeartbeat).Seconds(),
		LastUpdateID:        s.lastUpdateID,
		ConsecutiveErrors:   s.consecutiveErrors,
		TotalUpdatesHandled: s.totalUpdatesHandled,
		LastError:           s.lastError,
		LastErrorTime:       s.lastErrorTime,
	}
}

// StartFakeRunning sets the supervisor into a simulated running state (for testing).
func (s *Supervisor) StartFakeRunning(heartbeat time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning.Store(true)
	s.state = StateRunning
	s.lastHeartbeat = heartbeat
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
