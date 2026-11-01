package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
func (m *mockProvider) IsAlive() bool {
	return !m.cleanupCalled
}
func (m *mockProvider) Cleanup() {
	m.cleanupCalled = true
}

func newTestManager() *Manager {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		MaxConcurrentAuth:     100,
		SlotAcquireTimeout:    5 * time.Second,
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
		supportsQR, err := mgr.SelectProviderFast(uid, "test_instant")
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("SelectProviderFast failed: %v", err)
		}
		if supportsQR {
			t.Errorf("test_instant should not support QR")
		}

		totalDuration += elapsed
		mgr.Cleanup(uid)

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
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_ = mgr.store.SetState(userID, StateRequestingCode)
	_ = mgr.store.SetState(userID, StateAwaitingOTP)

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
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_ = mgr.store.SetState(userID, StateRequestingCode)
	_ = mgr.store.SetState(userID, StateAwaitingOTP)

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
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_ = mgr.store.SetState(userID, StateRequestingCode)
	_ = mgr.store.SetState(userID, StateAwaitingOTP)

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
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_ = mgr.store.SetState(userID, StateRequestingCode)
	_ = mgr.store.SetState(userID, StateAwaitingOTP)

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

func TestConcurrencyAdmissionLimit(t *testing.T) {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		MaxConcurrentAuth:     2,
		SlotAcquireTimeout:    50 * time.Millisecond,
		APIID:                 12345,
		APIHash:               "test_api_hash",
		PythonPath:            "python",
	}
	mgr := NewManager(cfg)

	providers.Register("test_slot_provider", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return &mockProvider{name: "TestSlot"}, nil
	})

	user1 := int64(1001)
	user2 := int64(1002)
	user3 := int64(1003)

	_ = mgr.StartSession(user1)
	_ = mgr.StartSession(user2)
	_ = mgr.StartSession(user3)

	_, err1 := mgr.SelectProviderFast(user1, "test_slot_provider")
	if err1 != nil {
		t.Fatalf("user1 expected success, got: %v", err1)
	}

	_, err2 := mgr.SelectProviderFast(user2, "test_slot_provider")
	if err2 != nil {
		t.Fatalf("user2 expected success, got: %v", err2)
	}

	// Slot limit 2 is now saturated: user3 MUST be rejected
	_, err3 := mgr.SelectProviderFast(user3, "test_slot_provider")
	if err3 == nil || !strings.Contains(err3.Error(), "maximum concurrent authentications reached") {
		t.Fatalf("expected concurrency admission failure for user3, got: %v", err3)
	}

	// Cancel user1 to free a slot
	_ = mgr.Cancel(context.Background(), user1)

	// User 3 must now succeed
	_, err3Retry := mgr.SelectProviderFast(user3, "test_slot_provider")
	if err3Retry != nil {
		t.Fatalf("expected user3 to succeed after slot released, got: %v", err3Retry)
	}
}

func TestInvalidPhoneValidationNoSlotLeak(t *testing.T) {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		MaxConcurrentAuth:     1,
		SlotAcquireTimeout:    50 * time.Millisecond,
		APIID:                 12345,
		APIHash:               "test_api_hash",
		PythonPath:            "python",
	}
	mgr := NewManager(cfg)

	userID := int64(2001)
	_ = mgr.StartSession(userID)
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)

	// Invalid phone submissions
	_, errInvalid := mgr.StartPhoneAuth(context.Background(), userID, "invalid-phone")
	if errInvalid == nil || !strings.Contains(errInvalid.Error(), "invalid phone number format") {
		t.Fatalf("expected phone validation error, got: %v", errInvalid)
	}

	// Held slots map MUST remain empty for this user
	mgr.mu.Lock()
	held := mgr.heldSlots[userID]
	mgr.mu.Unlock()
	if held {
		t.Fatalf("expected slot to NOT be held after failed phone validation")
	}
}

func TestPhoneNumberRateLimiting(t *testing.T) {
	cfg := &config.Config{
		SessionExpiryDuration: 5 * time.Minute,
		WorkerTimeout:         2 * time.Second,
		MaxConcurrentAuth:     5,
		SlotAcquireTimeout:    50 * time.Millisecond,
		PhoneMaxAttempts:      2,
		APIID:                 12345,
		APIHash:               "test_api_hash",
		PythonPath:            "python",
	}
	mgr := NewManager(cfg)

	providers.Register("test_rate_prov", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return &mockProvider{name: "TestRate"}, nil
	})

	userID := int64(3001)
	_ = mgr.StartSession(userID)
	_, _ = mgr.SelectProviderFast(userID, "test_rate_prov")

	// 1. Submit first phone
	_, err1 := mgr.StartPhoneAuth(context.Background(), userID, "+12345678901")
	if err1 != nil {
		t.Fatalf("first phone attempt expected success, got: %v", err1)
	}

	// 2. Immediate second submission must trigger cooldown
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_, err2 := mgr.StartPhoneAuth(context.Background(), userID, "+12345678902")
	if err2 == nil || !strings.Contains(err2.Error(), "please wait a few seconds") {
		t.Fatalf("expected cooldown error, got: %v", err2)
	}

	// Bypass cooldown to test max attempts
	mgr.mu.Lock()
	delete(mgr.lastPhoneTime, userID)
	mgr.mu.Unlock()

	// 3. Second attempt
	_, err3 := mgr.StartPhoneAuth(context.Background(), userID, "+12345678903")
	if err3 != nil {
		t.Fatalf("expected second attempt to succeed, got: %v", err3)
	}

	// Bypass cooldown
	mgr.mu.Lock()
	delete(mgr.lastPhoneTime, userID)
	mgr.mu.Unlock()

	// 4. Third attempt exceeds PhoneMaxAttempts (2)
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)
	_, err4 := mgr.StartPhoneAuth(context.Background(), userID, "+12345678904")
	if err4 == nil || !strings.Contains(err4.Error(), "maximum phone submission attempts exceeded") {
		t.Fatalf("expected max attempts exceeded error, got: %v", err4)
	}
}

func TestCancellationCoordination(t *testing.T) {
	mgr := newTestManager()
	userID := int64(4001)

	_ = mgr.StartSession(userID)
	mock := &mockProvider{name: "TestCancelProv"}
	mgr.mu.Lock()
	mgr.activeProviders[userID] = mock
	mgr.mu.Unlock()

	// Start simulated operation
	opCtx, cleanup, err := mgr.beginUserOpWithCancel(context.Background(), userID)
	if err != nil {
		t.Fatalf("failed to begin user op: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer cleanup()
		select {
		case <-opCtx.Done():
		case <-time.After(2 * time.Second):
		}
		close(done)
	}()

	// Call Cancel while operation is running
	cancelErr := mgr.Cancel(context.Background(), userID)
	if cancelErr != nil {
		t.Fatalf("Cancel returned error: %v", cancelErr)
	}

	// Wait for op goroutine to terminate
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("operation did not terminate on cancel")
	}

	if !mock.cleanupCalled {
		t.Fatalf("expected provider Cleanup to be called")
	}
}

func TestDeadProviderEvictionAndReinitialization(t *testing.T) {
	mgr := newTestManager()
	userID := int64(7001)

	// Set session provider to a registered mock provider
	providers.Register("test_revive", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return &mockProvider{name: "TestRevive"}, nil
	})

	_ = mgr.store.SetState(userID, StateSelectingProvider)
	_ = mgr.store.Update(userID, func(s *UserSession) {
		s.ProviderName = "test_revive"
	})
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)

	// Inject a dead provider into activeProviders
	deadMock := &mockProvider{name: "DeadOldProv", cleanupCalled: true} // IsAlive() returns false
	mgr.mu.Lock()
	mgr.activeProviders[userID] = deadMock
	mgr.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// GetOrInitProvider should detect dead provider, evict it, and instantiate fresh one
	p, err := mgr.GetOrInitProvider(ctx, userID)
	if err != nil {
		t.Fatalf("unexpected error from GetOrInitProvider: %v", err)
	}

	if p == deadMock {
		t.Errorf("expected dead provider to be replaced, but got same dead instance")
	}
	if !p.IsAlive() {
		t.Errorf("expected new provider to be alive")
	}
	if p.Name() != "TestRevive" {
		t.Errorf("expected revived provider TestRevive, got %s", p.Name())
	}
}

func TestGlobalPhoneSubmissionThrottle(t *testing.T) {
	mgr := newTestManager()
	userID := int64(8001)

	_ = mgr.store.SetState(userID, StateSelectingProvider)
	_ = mgr.store.Update(userID, func(s *UserSession) {
		s.ProviderName = "test_instant"
	})
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)

	// Artificially saturate the global phone submission throttle semaphore (capacity 15)
	for i := 0; i < cap(mgr.globalPhoneSem); i++ {
		mgr.globalPhoneSem <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := mgr.StartPhoneAuth(ctx, userID, "+1234567890")
	if err == nil {
		t.Fatalf("expected throttle error when semaphore is saturated, got nil")
	}

	if !strings.Contains(err.Error(), "processing high volume") {
		t.Errorf("expected high volume throttle message, got: %v", err)
	}

	// Drain semaphore
	for i := 0; i < cap(mgr.globalPhoneSem); i++ {
		<-mgr.globalPhoneSem
	}
}

func TestStartQRAuthStateValidationBeforeSlot(t *testing.T) {
	mgr := newTestManager()
	userID := int64(9001)

	// User is in StateIdle
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := mgr.StartQRAuth(ctx, userID)
	if err == nil {
		t.Fatalf("expected error starting QR in Idle state, got nil")
	}

	// Verify no slot was leaked
	mgr.mu.Lock()
	held := mgr.heldSlots[userID]
	mgr.mu.Unlock()
	if held {
		t.Errorf("admission slot was leaked after invalid state in StartQRAuth")
	}
}

func TestConcurrentGetOrInitProviderSingleFlight(t *testing.T) {
	mgr := newTestManager()
	userID := int64(9501)

	var createCount atomic.Int32
	providers.Register("test_single_flight", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		createCount.Add(1)
		time.Sleep(50 * time.Millisecond) // Simulate process spawn latency
		return &mockProvider{name: "TestSingleFlight"}, nil
	})

	_ = mgr.store.SetState(userID, StateSelectingProvider)
	_ = mgr.store.Update(userID, func(s *UserSession) {
		s.ProviderName = "test_single_flight"
	})
	_ = mgr.store.SetState(userID, StateAwaitingPhoneNumber)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const concurrency = 10
	results := make([]providers.Provider, concurrency)
	errs := make([]error, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p, err := mgr.GetOrInitProvider(ctx, userID)
			results[idx] = p
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i := 0; i < concurrency; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d encountered error: %v", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("goroutine %d got nil provider", i)
		}
		if results[i] != results[0] {
			t.Errorf("goroutine %d got different provider instance than goroutine 0", i)
		}
	}

	if count := createCount.Load(); count != 1 {
		t.Errorf("expected exactly 1 provider creation during concurrent requests, got %d", count)
	}
}
