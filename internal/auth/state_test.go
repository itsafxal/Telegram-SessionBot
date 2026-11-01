package auth

import (
	"testing"
	"time"
)

func TestStateTransitions(t *testing.T) {
	store := NewSessionStore(5*time.Minute, nil)
	userID := int64(123456789)

	// Initial get or create should be Idle
	sess := store.GetOrCreate(userID)
	if sess.State != StateIdle {
		t.Fatalf("expected initial state Idle, got %s", sess.State)
	}

	// Valid transition: Idle -> SelectingProvider
	if err := store.SetState(userID, StateSelectingProvider); err != nil {
		t.Fatalf("unexpected error on valid transition to SelectingProvider: %v", err)
	}

	// Valid transition: SelectingProvider -> AwaitingPhoneNumber
	if err := store.SetState(userID, StateAwaitingPhoneNumber); err != nil {
		t.Fatalf("unexpected error on valid transition to AwaitingPhoneNumber: %v", err)
	}

	// Invalid transition: AwaitingPhoneNumber directly to DeliveringSession
	if err := store.SetState(userID, StateDeliveringSession); err == nil {
		t.Fatalf("expected error on invalid transition from AwaitingPhoneNumber to DeliveringSession, got nil")
	}

	// Valid transition: AwaitingPhoneNumber -> RequestingCode -> AwaitingOTP
	if err := store.SetState(userID, StateRequestingCode); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.SetState(userID, StateAwaitingOTP); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cancellation allowed from any state
	if err := store.SetState(userID, StateCancelled); err != nil {
		t.Fatalf("unexpected error on transition to Cancelled: %v", err)
	}

	curr := store.Get(userID)
	if curr.State != StateCancelled {
		t.Fatalf("expected state Cancelled, got %s", curr.State)
	}
}

func TestSessionExpiry(t *testing.T) {
	expired := false
	shortTimeout := 50 * time.Millisecond
	store := NewSessionStore(shortTimeout, func(uid int64) {
		expired = true
	})

	userID := int64(987654321)
	store.GetOrCreate(userID)

	time.Sleep(100 * time.Millisecond)

	// Get should return nil for expired session
	sess := store.Get(userID)
	if sess != nil {
		t.Fatalf("expected nil for expired session, got %v", sess)
	}
	_ = expired
}

func TestSessionClear(t *testing.T) {
	store := NewSessionStore(5*time.Minute, nil)
	userID := int64(55555)

	store.GetOrCreate(userID)
	store.Clear(userID)

	if sess := store.Get(userID); sess != nil {
		t.Fatalf("expected nil after Clear, got %v", sess)
	}
}

func TestRetryTransitionsTable(t *testing.T) {
	store := NewSessionStore(5*time.Minute, nil)
	userID := int64(112233)

	tests := []struct {
		name       string
		fromState  State
		toState    State
		shouldPass bool
	}{
		// Priority 5 Bug fix: RequestingCode -> AwaitingPhoneNumber MUST succeed
		{"Retry phone code request", StateRequestingCode, StateAwaitingPhoneNumber, true},
		{"OTP to phone retry", StateAwaitingOTP, StateAwaitingPhoneNumber, true},
		{"QR to selecting auth method", StateAwaitingQRAuthentication, StateSelectingAuthMethod, true},
		{"2FA to OTP retry", StateAwaitingTwoFactorPassword, StateAwaitingOTP, true},
		{"AwaitingPhoneNumber to DeliveringSession (Illegal)", StateAwaitingPhoneNumber, StateDeliveringSession, false},
		{"Idle directly to AwaitingOTP (Illegal)", StateIdle, StateAwaitingOTP, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = store.GetOrCreate(userID)
			_ = store.Update(userID, func(s *UserSession) {
				s.State = tt.fromState
			})

			err := store.SetState(userID, tt.toState)
			if tt.shouldPass && err != nil {
				t.Errorf("expected transition %s -> %s to succeed, got error: %v", tt.fromState, tt.toState, err)
			}
			if !tt.shouldPass && err == nil {
				t.Errorf("expected transition %s -> %s to fail, but succeeded", tt.fromState, tt.toState)
			}
		})
	}
}
