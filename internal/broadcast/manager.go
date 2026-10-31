package broadcast

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"universal-session-generator/internal/database"
)

type BroadcastState string

const (
	StateIdle                 BroadcastState = "Idle"
	StateAwaitingContent      BroadcastState = "AwaitingContent"
	StateAwaitingConfirmation BroadcastState = "AwaitingConfirmation"
	StateRunning              BroadcastState = "Running"
)

type PendingBroadcast struct {
	FromChatID int64
	MessageID  int64
	Text       string
}

// Manager orchestrates owner-only broadcasts to all registered Telegram users.
type Manager struct {
	ownerID    int64
	db         *database.Database
	state      BroadcastState
	pending    *PendingBroadcast
	cancelFunc context.CancelFunc
	mu         sync.Mutex
	isRunning  atomic.Bool
}

// NewManager creates a new broadcast manager.
func NewManager(ownerID int64, db *database.Database) *Manager {
	return &Manager{
		ownerID: ownerID,
		db:      db,
		state:   StateIdle,
	}
}

// IsOwner returns true if the user matches the configured OWNER_ID.
func (m *Manager) IsOwner(userID int64) bool {
	return m.ownerID != 0 && userID == m.ownerID
}

// GetState returns the current broadcast state.
func (m *Manager) GetState() BroadcastState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// StartBroadcastPrompt begins the broadcast composition flow.
func (m *Manager) StartBroadcastPrompt(userID int64) error {
	if !m.IsOwner(userID) {
		return errors.New("unauthorized: owner privileges required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isRunning.Load() {
		return errors.New("a broadcast is currently running. Please wait for it to finish")
	}

	m.state = StateAwaitingContent
	m.pending = nil
	return nil
}

// SetPendingMessage registers content to be broadcast and transitions to confirmation.
func (m *Manager) SetPendingMessage(userID int64, fromChatID, messageID int64, previewText string) error {
	if !m.IsOwner(userID) {
		return errors.New("unauthorized: owner privileges required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != StateAwaitingContent {
		return errors.New("not awaiting broadcast content")
	}

	m.pending = &PendingBroadcast{
		FromChatID: fromChatID,
		MessageID:  messageID,
		Text:       previewText,
	}
	m.state = StateAwaitingConfirmation
	return nil
}

// Cancel terminates or cancels any pending broadcast.
func (m *Manager) Cancel(userID int64) error {
	if !m.IsOwner(userID) {
		return errors.New("unauthorized")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}

	m.state = StateIdle
	m.pending = nil
	m.isRunning.Store(false)
	return nil
}

// ExecuteBroadcast sends the pending broadcast to all registered users in the database.
func (m *Manager) ExecuteBroadcast(ctx context.Context, b *gotgbot.Bot, updateProgress func(progressMsg string), onComplete func(summary string)) error {
	m.mu.Lock()
	if m.state != StateAwaitingConfirmation || m.pending == nil {
		m.mu.Unlock()
		return errors.New("no broadcast message pending confirmation")
	}

	if m.isRunning.Load() {
		m.mu.Unlock()
		return errors.New("broadcast is already in progress")
	}

	m.state = StateRunning
	m.isRunning.Store(true)

	broadcastCtx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel
	pending := m.pending
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.state = StateIdle
			m.pending = nil
			m.cancelFunc = nil
			m.isRunning.Store(false)
			m.mu.Unlock()
		}()

		userIDs, err := m.db.GetAllUserIDs(broadcastCtx)
		if err != nil {
			slog.Error("Failed to fetch broadcast recipients from database", "error", err)
			onComplete(fmt.Sprintf("❌ Broadcast failed to fetch users: %v", err))
			return
		}

		total := len(userIDs)
		if total == 0 {
			onComplete("📢 Broadcast aborted: No registered users found in the database.")
			return
		}

		var successful int64
		var failed int64
		startTime := time.Now()
		lastProgressUpdate := time.Now()

		slog.Info("Starting broadcast", "total_users", total)

		for i, targetID := range userIDs {
			select {
			case <-broadcastCtx.Done():
				slog.Warn("Broadcast was cancelled by owner")
				onComplete(fmt.Sprintf("⚠️ *Broadcast Cancelled*\n\n• *Processed:* %d/%d\n• *Successful:* %d\n• *Failed:* %d\n• *Elapsed:* %v",
					i, total, successful, failed, time.Since(startTime).Round(time.Second)))
				return
			default:
			}

			// Copy message to target user
			_, err := b.CopyMessage(targetID, pending.FromChatID, pending.MessageID, nil)
			if err != nil {
				if tgErr, ok := err.(*gotgbot.TelegramError); ok {
					if tgErr.Code == 429 && tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
						sleepDur := time.Duration(tgErr.ResponseParams.RetryAfter+1) * time.Second
						slog.Warn("Broadcast hit FloodWait, sleeping", "seconds", tgErr.ResponseParams.RetryAfter)
						select {
						case <-broadcastCtx.Done():
							return
						case <-time.After(sleepDur):
						}
						// Retry once
						_, err = b.CopyMessage(targetID, pending.FromChatID, pending.MessageID, nil)
					}
				}
			}

			if err != nil {
				failed++
				slog.Debug("Failed to deliver broadcast message to user", "user_id", targetID, "error", err)
			} else {
				successful++
			}

			// Telegram broadcast rate limit: ~30 msgs/second
			select {
			case <-broadcastCtx.Done():
				return
			case <-time.After(35 * time.Millisecond):
			}

			// Progress reporting every 3 seconds or every 50 users
			if time.Since(lastProgressUpdate) > 3*time.Second || (i+1)%50 == 0 {
				lastProgressUpdate = time.Now()
				percent := float64(i+1) / float64(total) * 100
				progress := fmt.Sprintf("📢 *Broadcasting in progress...*\n\n• *Progress:* %.1f%% (%d/%d)\n• *Successful:* %d\n• *Failed:* %d\n• *Elapsed:* %v",
					percent, i+1, total, successful, failed, time.Since(startTime).Round(time.Second))
				updateProgress(progress)
			}
		}

		elapsed := time.Since(startTime).Round(time.Second)
		summary := fmt.Sprintf(`📢 *Broadcast Completed*

• *Total Recipients:* %d
• *Successful:* %d
• *Failed:* %d
• *Status:* Completed
• *Duration:* %v`, total, successful, failed, elapsed)

		slog.Info("Broadcast completed", "total", total, "successful", successful, "failed", failed, "duration", elapsed)
		onComplete(summary)
	}()

	return nil
}
