package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State represents the current step in the authentication conversation.
type State string

const (
	StateIdle                      State = "Idle"
	StateSelectingProvider         State = "SelectingProvider"
	StateSelectingAuthMethod       State = "SelectingAuthMethod"
	StateAwaitingPhoneNumber       State = "AwaitingPhoneNumber"
	StateRequestingCode            State = "RequestingCode"
	StateAwaitingOTP               State = "AwaitingOTP"
	StateAwaitingTwoFactorPassword State = "AwaitingTwoFactorPassword"
	StateAwaitingQRAuthentication  State = "AwaitingQRAuthentication"
	StateGeneratingSession         State = "GeneratingSession"
	StateValidatingSession         State = "ValidatingSession"
	StateDeliveringSession         State = "DeliveringSession"
	StateCompleted                 State = "Completed"
	StateFailed                    State = "Failed"
	StateCancelled                 State = "Cancelled"
)

// ValidStateTransitions defines valid transitions between states to enforce integrity.
var validTransitions = map[State][]State{
	StateIdle: {
		StateSelectingProvider,
	},
	StateSelectingProvider: {
		StateSelectingAuthMethod,
		StateAwaitingPhoneNumber,
		StateAwaitingQRAuthentication,
		StateIdle,
		StateCancelled,
	},
	StateSelectingAuthMethod: {
		StateAwaitingPhoneNumber,
		StateAwaitingQRAuthentication,
		StateSelectingProvider,
		StateIdle,
		StateCancelled,
	},
	StateAwaitingPhoneNumber: {
		StateRequestingCode,
		StateSelectingProvider,
		StateIdle,
		StateCancelled,
	},
	StateRequestingCode: {
		StateAwaitingOTP,
		StateFailed,
		StateCancelled,
	},
	StateAwaitingOTP: {
		StateAwaitingTwoFactorPassword,
		StateValidatingSession,
		StateGeneratingSession,
		StateFailed,
		StateCancelled,
	},
	StateAwaitingTwoFactorPassword: {
		StateValidatingSession,
		StateGeneratingSession,
		StateFailed,
		StateCancelled,
	},
	StateAwaitingQRAuthentication: {
		StateAwaitingTwoFactorPassword,
		StateValidatingSession,
		StateGeneratingSession,
		StateFailed,
		StateCancelled,
	},
	StateGeneratingSession: {
		StateValidatingSession,
		StateFailed,
		StateCancelled,
	},
	StateValidatingSession: {
		StateGeneratingSession,
		StateDeliveringSession,
		StateFailed,
		StateCancelled,
	},
	StateDeliveringSession: {
		StateCompleted,
		StateFailed,
	},
	StateCompleted: {
		StateIdle,
		StateSelectingProvider,
	},
	StateFailed: {
		StateIdle,
		StateSelectingProvider,
	},
	StateCancelled: {
		StateIdle,
		StateSelectingProvider,
	},
}

// UserSession holds the ephemeral, in-memory state for a user undergoing authentication.
// Sensitive data (passwords, OTPs, raw session strings) are NEVER stored in long-term databases.
type UserSession struct {
	UserID        int64
	State         State
	ProviderName  string
	AuthMethod    string // "phone" or "qr"
	PhoneNumber   string // Ephemeral, cleared immediately upon completion/cancellation
	PhoneCodeHash string // Ephemeral MTProto hash
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SessionStore maintains in-memory user sessions with thread safety and timeouts.
type SessionStore struct {
	mu            sync.RWMutex
	sessions      map[int64]*UserSession
	expiryTimeout time.Duration
	onExpire      func(userID int64)
}

// NewSessionStore creates a new session store.
func NewSessionStore(expiryTimeout time.Duration, onExpire func(userID int64)) *SessionStore {
	store := &SessionStore{
		sessions:      make(map[int64]*UserSession),
		expiryTimeout: expiryTimeout,
		onExpire:      onExpire,
	}

	go store.reaper()
	return store
}

// Get retrieves an isolated snapshot of the session for a user, or nil if none exists or if it expired.
func (s *SessionStore) Get(userID int64) *UserSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, exists := s.sessions[userID]
	if !exists {
		return nil
	}

	if time.Since(sess.UpdatedAt) > s.expiryTimeout {
		return nil
	}

	clone := *sess
	return &clone
}

// GetOrCreate gets an isolated snapshot of the existing session or creates a new Idle session.
func (s *SessionStore) GetOrCreate(userID int64) *UserSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	sess, exists := s.sessions[userID]
	if !exists || now.Sub(sess.UpdatedAt) > s.expiryTimeout {
		sess = &UserSession{
			UserID:    userID,
			State:     StateIdle,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.sessions[userID] = sess
	}
	clone := *sess
	return &clone
}

// Update safely mutates a user's session under store write lock and refreshes UpdatedAt.
func (s *SessionStore) Update(userID int64, fn func(sess *UserSession)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, exists := s.sessions[userID]
	if !exists {
		return errors.New("no active session found to update")
	}

	fn(sess)
	sess.UpdatedAt = time.Now()
	return nil
}

// SetState transitions a user session to a new state with validation.
func (s *SessionStore) SetState(userID int64, newState State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, exists := s.sessions[userID]
	if !exists {
		if newState == StateIdle || newState == StateSelectingProvider {
			sess = &UserSession{
				UserID:    userID,
				State:     newState,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			s.sessions[userID] = sess
			return nil
		}
		return errors.New("no active session found to transition")
	}

	// Always allow transitions to Cancelled or Idle from any state
	if newState == StateCancelled || newState == StateIdle {
		sess.State = newState
		sess.UpdatedAt = time.Now()
		return nil
	}

	// Validate allowed transitions
	allowed := validTransitions[sess.State]
	valid := false
	for _, target := range allowed {
		if target == newState {
			valid = true
			break
		}
	}

	if !valid {
		return fmt.Errorf("invalid state transition from %s to %s", sess.State, newState)
	}

	sess.State = newState
	sess.UpdatedAt = time.Now()
	return nil
}

// Clear completely deletes a user's session from memory.
func (s *SessionStore) Clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, userID)
}

// reaper cleans up expired sessions periodically.
func (s *SessionStore) reaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		var expired []int64

		for uid, sess := range s.sessions {
			if now.Sub(sess.UpdatedAt) > s.expiryTimeout {
				expired = append(expired, uid)
			}
		}

		for _, uid := range expired {
			delete(s.sessions, uid)
		}
		s.mu.Unlock()

		if s.onExpire != nil {
			for _, uid := range expired {
				s.onExpire(uid)
			}
		}
	}
}
