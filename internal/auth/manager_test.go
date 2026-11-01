package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"universal-session-generator/internal/config"
	"universal-session-generator/internal/providers"
)

// mockProvider implements providers.Provider for unit testing the authentication manager.
type mockProvider struct {
	name            string
	supportsQR      bool
	authAccount     *providers.AccountInfo
	valAccount      *providers.AccountInfo
	sessionString   string
	savedMsgID      int64
	getAccountErr   error
	exportErr       error
	validateErr     error
	sendSavedErr    error
	sendSavedCalled bool
	cleanupCalled   bool
}

func (m *mockProvider) Name() string     { return m.name }
func (m *mockProvider) SupportsQR() bool { return m.supportsQR }
func (m *mockProvider) StartPhoneAuth(ctx context.Context, phoneNumber string) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusNeedOTP, PhoneCodeHash: "test_hash"}, nil
}
func (m *mockProvider) StartQRAuth(ctx context.Context) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusQRCode, QRTokenURL: "tg://login?token=xyz"}, nil
}
func (m *mockProvider) WaitForQRLogin(ctx context.Context) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusSuccess}, nil
}
func (m *mockProvider) SubmitOTP(ctx context.Context, phoneCode string) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusSuccess}, nil
}
func (m *mockProvider) SubmitPassword(ctx context.Context, password string) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusSuccess}, nil
}
func (m *mockProvider) ExportSession(ctx context.Context) (string, error) {
	if m.exportErr != nil {
		return "", m.exportErr
	}
	return m.sessionString, nil
}
func (m *mockProvider) ValidateSession(ctx context.Context, sessionString string) (*providers.AccountInfo, error) {
	if m.validateErr != nil {
		return nil, m.validateErr
	}
	return m.valAccount, nil
}
func (m *mockProvider) GetAccountInfo(ctx context.Context) (*providers.AccountInfo, error) {
	if m.getAccountErr != nil {
		return nil, m.getAccountErr
	}
	return m.authAccount, nil
}
func (m *mockProvider) SendToSavedMessages(ctx context.Context, sessionString string) (int64, error) {
	m.sendSavedCalled = true
	if m.sendSavedErr != nil {
		return 0, m.sendSavedErr
	}
	return m.savedMsgID, nil
}
func (m *mockProvider) Cancel(ctx context.Context) error {
	m.Cleanup()
	return nil
}
func (m *mockProvider) Cleanup() {
	m.cleanupCalled = true
}

func newTestManager() *Manager {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		APIID:                 12345,
		APIHash:               "test_api_hash",
		PythonPath:            "python",
	}
	return NewManager(cfg)
}

func TestSelectProviderFastLatency(t *testing.T) {
	mgr := newTestManager()
	userID := int64(999901)

	// Register a mock provider factory so pre-warming succeeds
	providers.Register("test_instant", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return &mockProvider{name: "TestInstant"}, nil
	})

	var totalDuration time.Duration
	iterations := 50

	for i := 0; i < iterations; i++ {
		uid := userID + int64(i)
		if err := mgr.StartSession(uid); err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		start := time.Now()
		supportsQR, err := mgr.SelectProviderFast(uid, "Pyrogram")
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("SelectProviderFast failed: %v", err)
		}
		if supportsQR {
			t.Errorf("Pyrogram should not support QR")
		}

		totalDuration += elapsed

		if elapsed > 10*time.Millisecond {
			t.Errorf("iteration %d exceeded 10ms deadline: %v", i, elapsed)
		}
	}

	avg := totalDuration / time.Duration(iterations)
	t.Logf("PASS: SelectProviderFast average latency across %d iterations: %v (target <1ms)", iterations, avg)
	if avg > 1*time.Millisecond {
		t.Errorf("expected average latency < 1ms, got %v", avg)
	}
}

func TestCompleteAndDeliverToSavedMessages_Success(t *testing.T) {
	mgr := newTestManager()
	userID := int64(888001)

	_ = mgr.StartSession(userID)
	_, _ = mgr.SelectProviderFast(userID, "Telethon")

	mock := &mockProvider{
		name:          "Telethon",
		supportsQR:    true,
		sessionString: "1BVtsOMQBu7...native_telethon_session...",
		savedMsgID:    45678,
		authAccount: &providers.AccountInfo{
			ID:        12345678,
			FirstName: "Alice",
			LastName:  "Smith",
			Username:  "alice_smith",
		},
		valAccount: &providers.AccountInfo{
			ID:        12345678,
			FirstName: "Alice",
			LastName:  "Smith",
			Username:  "alice_smith",
		},
	}

	// Directly assign mock provider to test manager
	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	delete(mgr.pendingInits, userID)
	mgr.mu.Unlock()

	ctx := context.Background()
	acc, msgID, err := mgr.CompleteAndDeliverToSavedMessages(ctx, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if acc.ID != 12345678 {
		t.Errorf("expected account ID 12345678, got %d", acc.ID)
	}
	if msgID != 45678 {
		t.Errorf("expected msgID 45678, got %d", msgID)
	}
	if !mock.sendSavedCalled {
		t.Errorf("expected SendToSavedMessages to be called")
	}
	if !mock.cleanupCalled {
		t.Errorf("expected provider Cleanup to be called after delivery")
	}

	// Verify zero credential leaks: active provider should be cleared from memory
	mgr.mu.Lock()
	_, active := mgr.activeProviders[userID]
	mgr.mu.Unlock()
	if active {
		t.Errorf("expected provider to be purged from activeProviders map")
	}
}

func TestCompleteAndDeliverToSavedMessages_IdentityMismatch(t *testing.T) {
	mgr := newTestManager()
	userID := int64(888002)

	_ = mgr.StartSession(userID)
	_, _ = mgr.SelectProviderFast(userID, "Pyrogram")

	mock := &mockProvider{
		name:          "Pyrogram",
		supportsQR:    false,
		sessionString: "1BVtsOMQBu7...pyrogram_session...",
		savedMsgID:    45679,
		authAccount: &providers.AccountInfo{
			ID:        11111,
			FirstName: "Alice",
		},
		valAccount: &providers.AccountInfo{
			ID:        99999, // Mismatched account!
			FirstName: "Bob",
		},
	}

	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	delete(mgr.pendingInits, userID)
	mgr.mu.Unlock()

	ctx := context.Background()
	_, _, err := mgr.CompleteAndDeliverToSavedMessages(ctx, userID)
	if err == nil {
		t.Fatalf("expected error due to account identity mismatch, got nil")
	}

	t.Logf("PASS: Identity mismatch caught: %v", err)

	// Crucial check: delivery to Saved Messages MUST NOT have been called!
	if mock.sendSavedCalled {
		t.Errorf("CRITICAL SECURITY ERROR: SendToSavedMessages was called despite identity mismatch!")
	}
	if !mock.cleanupCalled {
		t.Errorf("expected cleanup after failed identity validation")
	}
}

func TestCompleteAndDeliverToSavedMessages_LoginVerificationFails(t *testing.T) {
	mgr := newTestManager()
	userID := int64(888003)

	_ = mgr.StartSession(userID)
	_, _ = mgr.SelectProviderFast(userID, "Gogram")

	mock := &mockProvider{
		name:          "Gogram",
		getAccountErr: errors.New("client disconnected or unauthorized"),
	}

	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	delete(mgr.pendingInits, userID)
	mgr.mu.Unlock()

	ctx := context.Background()
	_, _, err := mgr.CompleteAndDeliverToSavedMessages(ctx, userID)
	if err == nil {
		t.Fatalf("expected error due to login verification failure, got nil")
	}

	if mock.sendSavedCalled {
		t.Errorf("SendToSavedMessages should not be called when login verification fails")
	}
}

func TestCompleteAndDeliverToSavedMessages_DeliveryFails(t *testing.T) {
	mgr := newTestManager()
	userID := int64(888004)

	_ = mgr.StartSession(userID)
	_, _ = mgr.SelectProviderFast(userID, "Gogram")

	mock := &mockProvider{
		name:          "Gogram",
		sessionString: "gogram_sess_str",
		authAccount:   &providers.AccountInfo{ID: 55555},
		valAccount:    &providers.AccountInfo{ID: 55555},
		sendSavedErr:  errors.New("network timeout communicating with Telegram DC"),
	}

	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	delete(mgr.pendingInits, userID)
	mgr.mu.Unlock()

	ctx := context.Background()
	_, _, err := mgr.CompleteAndDeliverToSavedMessages(ctx, userID)
	if err == nil {
		t.Fatalf("expected error when delivery to Saved Messages fails, got nil")
	}

	if !mock.cleanupCalled {
		t.Errorf("expected cleanup to be invoked even if delivery fails")
	}
}

func TestMaxConcurrentAuthEnforcement(t *testing.T) {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		MaxConcurrentAuth:     2,
	}
	mgr := NewManager(cfg)

	// User 1 acquires slot
	ctx := context.Background()
	if err := mgr.acquireSlot(ctx, 1001); err != nil {
		t.Fatalf("user 1001 failed to acquire slot: %v", err)
	}

	// User 2 acquires slot
	if err := mgr.acquireSlot(ctx, 1002); err != nil {
		t.Fatalf("user 1002 failed to acquire slot: %v", err)
	}

	// User 3 must be rejected because MaxConcurrentAuth=2
	err3 := mgr.acquireSlot(ctx, 1003)
	if err3 == nil || !strings.Contains(err3.Error(), "maximum concurrent authentications") {
		t.Fatalf("expected maximum concurrent error for user 1003, got: %v", err3)
	}

	// Release User 1 slot
	mgr.releaseSlot(1001)

	// User 3 can now acquire
	if err := mgr.acquireSlot(ctx, 1003); err != nil {
		t.Fatalf("user 1003 should acquire after slot release, got: %v", err)
	}
}

type failingMockProvider struct {
	mockProvider
}

func (f *failingMockProvider) SubmitOTP(ctx context.Context, phoneCode string) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusError, ErrorMessage: "Invalid code"}, nil
}

func (f *failingMockProvider) SubmitPassword(ctx context.Context, password string) (*providers.AuthResult, error) {
	return &providers.AuthResult{Status: providers.StatusError, ErrorMessage: "Invalid password"}, nil
}

func TestOTPBruteForceLockout(t *testing.T) {
	mgr := newTestManager()
	userID := int64(999111)

	_ = mgr.store.GetOrCreate(userID)
	_ = mgr.store.Update(userID, func(s *UserSession) {
		s.State = StateAwaitingOTP
	})
	mock := &failingMockProvider{mockProvider: mockProvider{name: "TestFail"}}
	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	mgr.mu.Unlock()

	ctx := context.Background()
	// Attempt 1
	res1, err1 := mgr.SubmitOTP(ctx, userID, "11111")
	if err1 != nil || res1.Status != providers.StatusError {
		t.Fatalf("attempt 1 expected error status, got res=%v, err=%v", res1, err1)
	}

	// Clear cooldown for test speed
	mgr.mu.Lock()
	delete(mgr.lastAttempt, userID)
	mgr.mu.Unlock()

	// Attempt 2
	res2, err2 := mgr.SubmitOTP(ctx, userID, "22222")
	if err2 != nil || res2.Status != providers.StatusError {
		t.Fatalf("attempt 2 expected error status, got res=%v, err=%v", res2, err2)
	}

	mgr.mu.Lock()
	delete(mgr.lastAttempt, userID)
	mgr.mu.Unlock()

	// Attempt 3: MUST trigger lockout and terminate session
	_, err3 := mgr.SubmitOTP(ctx, userID, "33333")
	if err3 == nil || !strings.Contains(err3.Error(), "too many failed verification code attempts") {
		t.Fatalf("expected 3-attempt lockout termination error, got: %v", err3)
	}

	// Session must be cleared from store
	if sess := mgr.store.Get(userID); sess != nil {
		t.Fatalf("expected session to be cleared after lockout, found: %+v", sess)
	}
}

func TestPasswordBruteForceLockout(t *testing.T) {
	mgr := newTestManager()
	userID := int64(999222)

	_ = mgr.store.GetOrCreate(userID)
	_ = mgr.store.Update(userID, func(s *UserSession) {
		s.State = StateAwaitingTwoFactorPassword
	})
	mock := &failingMockProvider{mockProvider: mockProvider{name: "TestFail"}}
	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	mgr.mu.Unlock()

	ctx := context.Background()
	// Attempt 1
	_, _ = mgr.SubmitPassword(ctx, userID, "pass1")
	mgr.mu.Lock()
	delete(mgr.lastAttempt, userID)
	mgr.mu.Unlock()

	// Attempt 2
	_, _ = mgr.SubmitPassword(ctx, userID, "pass2")
	mgr.mu.Lock()
	delete(mgr.lastAttempt, userID)
	mgr.mu.Unlock()

	// Attempt 3: MUST trigger lockout
	_, err3 := mgr.SubmitPassword(ctx, userID, "pass3")
	if err3 == nil || !strings.Contains(err3.Error(), "too many failed two-step verification attempts") {
		t.Fatalf("expected 3-attempt lockout error, got: %v", err3)
	}

	if sess := mgr.store.Get(userID); sess != nil {
		t.Fatalf("expected session to be cleared after lockout, found: %+v", sess)
	}
}

func TestConcurrentSessionAccessUnderRace(t *testing.T) {
	mgr := newTestManager()
	userID := int64(777666)
	_ = mgr.StartSession(userID)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = mgr.store.Get(userID)
			_ = mgr.store.Update(userID, func(s *UserSession) {
				s.PhoneNumber = fmt.Sprintf("+1000%d", idx)
			})
			_ = mgr.store.SetState(userID, StateSelectingProvider)
		}(i)
	}
	wg.Wait()
}
