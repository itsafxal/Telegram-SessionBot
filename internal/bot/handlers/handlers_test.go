package handlers

import (
	"context"
	"testing"
	"time"

	"universal-session-generator/internal/auth"
	"universal-session-generator/internal/config"
)

func TestIsAbortButton(t *testing.T) {
	testCases := []struct {
		input    string
		expected bool
	}{
		{"🛑 Abort", true},
		{"Abort", true},
		{"abort", true},
		{"/abort", true},
		{"❌ Cancel", true},
		{"Cancel", true},
		{"cancel", true},
		{"/cancel", true},
		{"🚀 Generate Session", false},
		{"📖 About", false},
		{"ℹ️ Help", false},
		{"+1234567890", false},
		{"12345", false},
	}

	for _, tc := range testCases {
		res := isAbortButton(tc.input)
		if res != tc.expected {
			t.Errorf("isAbortButton(%q) = %v; expected %v", tc.input, res, tc.expected)
		}
	}
}

func TestAbortClearsFSMState(t *testing.T) {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
	}
	authMgr := auth.NewManager(cfg)
	userID := int64(987123)

	// Start session -> StateSelectingProvider
	if err := authMgr.StartSession(userID); err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	sess := authMgr.GetSession(userID)
	if sess == nil || sess.State != auth.StateSelectingProvider {
		t.Fatalf("expected state StateSelectingProvider, got %v", sess)
	}

	// Trigger Cancel/Abort
	if err := authMgr.Cancel(context.Background(), userID); err != nil {
		t.Fatalf("failed to cancel session: %v", err)
	}

	// Verify state cleared
	postSess := authMgr.GetSession(userID)
	if postSess != nil && postSess.State != auth.StateCancelled && postSess.State != auth.StateIdle {
		t.Errorf("expected session to be cleared or cancelled, got: %v", postSess)
	}
	if authMgr.ActiveSessionsCount() != 0 {
		t.Errorf("expected 0 active sessions after abort, got: %d", authMgr.ActiveSessionsCount())
	}
}
