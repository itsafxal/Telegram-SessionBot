package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"universal-session-generator/internal/config"
	"universal-session-generator/internal/providers"
	"universal-session-generator/internal/security"
)

var phoneRegex = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// pendingInit tracks an asynchronous background provider pre-warming operation.
type pendingInit struct {
	provider providers.Provider
	err      error
	done     chan struct{}
}

// Manager coordinates user sessions, provider lifecycles, and authentication states.
type Manager struct {
	cfg             *config.Config
	store           *SessionStore
	activeProviders map[int64]providers.Provider
	pendingInits    map[int64]*pendingInit
	heldSlots       map[int64]bool
	userOps         map[int64]chan struct{}
	userCancels     map[int64]context.CancelFunc
	cancelling      map[int64]bool
	authSem         chan struct{}
	phoneAttempts   map[int64]int
	lastPhoneTime   map[int64]time.Time
	otpAttempts     map[int64]int
	pwdAttempts     map[int64]int
	lastAttempt     map[int64]time.Time
	mu              sync.Mutex
}

// NewManager creates a new authentication manager with concurrency bounding and brute-force protection.
func NewManager(cfg *config.Config) *Manager {
	maxAuth := cfg.MaxConcurrentAuth
	if maxAuth <= 0 {
		maxAuth = 50
	}

	mgr := &Manager{
		cfg:             cfg,
		activeProviders: make(map[int64]providers.Provider),
		pendingInits:    make(map[int64]*pendingInit),
		heldSlots:       make(map[int64]bool),
		userOps:         make(map[int64]chan struct{}),
		userCancels:     make(map[int64]context.CancelFunc),
		cancelling:      make(map[int64]bool),
		authSem:         make(chan struct{}, maxAuth),
		phoneAttempts:   make(map[int64]int),
		lastPhoneTime:   make(map[int64]time.Time),
		otpAttempts:     make(map[int64]int),
		pwdAttempts:     make(map[int64]int),
		lastAttempt:     make(map[int64]time.Time),
	}

	mgr.store = NewSessionStore(cfg.SessionExpiryDuration, func(userID int64) {
		slog.Info("Cleaning up expired user session", "user_id", userID)
		mgr.Cleanup(userID)
	})

	return mgr
}

// GetSession retrieves the in-memory session snapshot for a user.
func (m *Manager) GetSession(userID int64) *UserSession {
	return m.store.Get(userID)
}

// ActiveSessionsCount returns the number of currently active or initializing authentication sessions.
func (m *Manager) ActiveSessionsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.activeProviders) + len(m.pendingInits)
}

// acquireSlot allocates a global concurrency slot for the user's authentication process.
func (m *Manager) acquireSlot(ctx context.Context, userID int64) error {
	m.mu.Lock()
	if m.heldSlots[userID] {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	timeout := m.cfg.SlotAcquireTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case m.authSem <- struct{}{}:
		m.mu.Lock()
		if m.heldSlots[userID] {
			select {
			case <-m.authSem:
			default:
			}
			m.mu.Unlock()
			return nil
		}
		m.heldSlots[userID] = true
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("concurrency wait cancelled: %w", ctx.Err())
	case <-timer.C:
		return errors.New("maximum concurrent authentications reached; please try again in a few moments")
	}
}

// releaseSlotLocked safely returns an authentication slot when m.mu is already held.
func (m *Manager) releaseSlotLocked(userID int64) {
	if m.heldSlots[userID] {
		delete(m.heldSlots, userID)
		select {
		case <-m.authSem:
		default:
		}
	}
}

// releaseSlot safely returns an authentication slot to the global semaphore.
func (m *Manager) releaseSlot(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseSlotLocked(userID)
}

// beginUserOpWithCancel creates an operation context and coordinates cancelation safety.
func (m *Manager) beginUserOpWithCancel(parentCtx context.Context, userID int64) (context.Context, func(), error) {
	m.mu.Lock()
	if m.cancelling[userID] {
		m.mu.Unlock()
		return nil, nil, errors.New("operation is being cancelled")
	}
	if _, busy := m.userOps[userID]; busy {
		m.mu.Unlock()
		return nil, nil, errors.New("another operation is currently processing for this session. Please wait.")
	}
	done := make(chan struct{})
	m.userOps[userID] = done

	opCtx, cancelFn := context.WithCancel(parentCtx)
	m.userCancels[userID] = cancelFn
	m.mu.Unlock()

	cleanup := func() {
		cancelFn()
		m.mu.Lock()
		delete(m.userOps, userID)
		delete(m.userCancels, userID)
		close(done)
		m.mu.Unlock()
	}

	return opCtx, cleanup, nil
}

// beginUserOp guarantees that only one authentication operation runs at a time per user.
func (m *Manager) beginUserOp(userID int64) (func(), error) {
	_, cleanup, err := m.beginUserOpWithCancel(context.Background(), userID)
	return cleanup, err
}

// checkPhoneRateLimit enforces cooldown and max attempts ceiling for phone submissions.
func (m *Manager) checkPhoneRateLimit(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	last, ok := m.lastPhoneTime[userID]
	if ok && time.Since(last) < 3*time.Second {
		return errors.New("please wait a few seconds before submitting another phone number")
	}

	maxAttempts := m.cfg.PhoneMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	m.phoneAttempts[userID]++
	if m.phoneAttempts[userID] > maxAttempts {
		return errors.New("maximum phone submission attempts exceeded; session aborted for security")
	}

	m.lastPhoneTime[userID] = time.Now()
	return nil
}

// checkCooldown enforces a minimum interval between verification submissions.
func (m *Manager) checkCooldown(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastAttempt[userID]
	if ok && time.Since(last) < 2*time.Second {
		return errors.New("please wait a moment before trying again")
	}
	m.lastAttempt[userID] = time.Now()
	return nil
}

// recordOTPAttempt tracks failed OTP submissions and enforces a 3-attempt ceiling.
func (m *Manager) recordOTPAttempt(userID int64, success bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.otpAttempts[userID] = 0
		return 0
	}
	m.otpAttempts[userID]++
	return m.otpAttempts[userID]
}

// recordPasswordAttempt tracks failed 2FA password submissions and enforces a 3-attempt ceiling.
func (m *Manager) recordPasswordAttempt(userID int64, success bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.pwdAttempts[userID] = 0
		return 0
	}
	m.pwdAttempts[userID]++
	return m.pwdAttempts[userID]
}

// resetAttempts clears attempt counters and cooldown timers for a user.
func (m *Manager) resetAttempts(userID int64) {
	m.mu.Lock()
	delete(m.phoneAttempts, userID)
	delete(m.otpAttempts, userID)
	delete(m.pwdAttempts, userID)
	delete(m.lastAttempt, userID)
	m.mu.Unlock()
}

// StartSession initiates provider selection for a user.
func (m *Manager) StartSession(userID int64) error {
	m.Cleanup(userID)
	return m.store.SetState(userID, StateSelectingProvider)
}

// SelectProviderFast validates the provider selection immediately and transitions state in <1ms,
// while asynchronously pre-warming the provider instance in the background.
func (m *Manager) SelectProviderFast(userID int64, providerInput string) (bool, error) {
	canonicalName := providers.NormalizeName(providerInput)
	if canonicalName == "" {
		return false, errors.New("invalid provider selected")
	}

	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateSelectingProvider {
		return false, errors.New("please start a new session before selecting a provider")
	}

	// Priority 2: Enforce concurrency admission before pre-warming heavy worker processes
	slotTimeout := m.cfg.SlotAcquireTimeout
	if slotTimeout <= 0 {
		slotTimeout = 5 * time.Second
	}
	acquireCtx, cancel := context.WithTimeout(context.Background(), slotTimeout)
	defer cancel()
	if err := m.acquireSlot(acquireCtx, userID); err != nil {
		return false, err
	}

	supportsQR := providers.SupportsQR(canonicalName)
	_ = m.store.Update(userID, func(s *UserSession) {
		s.ProviderName = providers.DisplayName(canonicalName)
	})

	if supportsQR {
		_ = m.store.SetState(userID, StateSelectingAuthMethod)
	} else {
		_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
	}

	// Pre-warm the provider asynchronously in the background so expensive worker startup
	// occurs while the user is typing their phone number or selecting QR mode.
	m.mu.Lock()
	if existing, ok := m.activeProviders[userID]; ok && existing != nil {
		go existing.Cleanup()
		delete(m.activeProviders, userID)
	}

	initObj := &pendingInit{
		done: make(chan struct{}),
	}
	m.pendingInits[userID] = initObj
	m.mu.Unlock()

	go func(uid int64, cName string, pending *pendingInit) {
		initCtx, cancel := context.WithTimeout(context.Background(), m.cfg.WorkerTimeout)
		defer cancel()

		p, err := providers.Get(initCtx, cName, m.cfg.APIID, m.cfg.APIHash, m.cfg.PythonPath, m.cfg.WorkerTimeout)

		m.mu.Lock()
		cur, ok := m.pendingInits[uid]
		if ok && cur == pending {
			delete(m.pendingInits, uid)
			if err == nil {
				m.activeProviders[uid] = p
			} else {
				m.releaseSlotLocked(uid)
			}
			pending.provider = p
			pending.err = err
		} else {
			// Session was cancelled or changed while initializing
			if p != nil {
				go p.Cleanup()
			}
			m.releaseSlotLocked(uid)
		}
		close(pending.done)
		m.mu.Unlock()
	}(userID, canonicalName, initObj)

	return supportsQR, nil
}

// SelectProvider associates a provider with the user's session (blocking compatibility wrapper).
func (m *Manager) SelectProvider(ctx context.Context, userID int64, providerInput string) (bool, error) {
	supportsQR, err := m.SelectProviderFast(userID, providerInput)
	if err != nil {
		return false, err
	}
	// Await the provider to confirm readiness
	_, err = m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return false, err
	}
	return supportsQR, nil
}

// GetOrInitProvider returns the active provider for the user, waiting for any pending background initialization.
func (m *Manager) GetOrInitProvider(ctx context.Context, userID int64) (providers.Provider, error) {
	m.mu.Lock()
	if p, ok := m.activeProviders[userID]; ok && p != nil {
		m.mu.Unlock()
		return p, nil
	}

	pending, hasPending := m.pendingInits[userID]
	m.mu.Unlock()

	if hasPending && pending != nil {
		select {
		case <-pending.done:
			if pending.err != nil {
				return nil, pending.err
			}
			return pending.provider, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// If no provider is active or pending, instantiate synchronously
	sess := m.store.Get(userID)
	if sess == nil || sess.ProviderName == "" {
		return nil, errors.New("no provider selected for session")
	}

	canonical := providers.NormalizeName(sess.ProviderName)
	p, err := providers.Get(ctx, canonical, m.cfg.APIID, m.cfg.APIHash, m.cfg.PythonPath, m.cfg.WorkerTimeout)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.activeProviders[userID] = p
	m.mu.Unlock()

	return p, nil
}

// ChooseAuthMethod handles the choice between Phone and QR code when supported.
func (m *Manager) ChooseAuthMethod(userID int64, method string) (State, error) {
	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateSelectingAuthMethod {
		return StateIdle, errors.New("not currently selecting an authentication method")
	}

	lower := strings.ToLower(method)
	if strings.Contains(lower, "qr") {
		if !providers.SupportsQR(sess.ProviderName) {
			return StateIdle, providers.ErrQRUnsupported
		}
		_ = m.store.Update(userID, func(s *UserSession) {
			s.AuthMethod = "qr"
		})
		if err := m.store.SetState(userID, StateAwaitingQRAuthentication); err != nil {
			return StateIdle, err
		}
		return StateAwaitingQRAuthentication, nil
	}

	_ = m.store.Update(userID, func(s *UserSession) {
		s.AuthMethod = "phone"
	})
	if err := m.store.SetState(userID, StateAwaitingPhoneNumber); err != nil {
		return StateIdle, err
	}
	return StateAwaitingPhoneNumber, nil
}

// StartPhoneAuth validates the phone number and requests an OTP code from Telegram.
func (m *Manager) StartPhoneAuth(ctx context.Context, userID int64, rawPhone string) (*providers.AuthResult, error) {
	// 1. Phone number format validation BEFORE acquiring slot or mutating state (Priority 3)
	cleanedPhone := strings.ReplaceAll(strings.TrimSpace(rawPhone), " ", "")
	if !strings.HasPrefix(cleanedPhone, "+") {
		cleanedPhone = "+" + cleanedPhone
	}

	if !phoneRegex.MatchString(cleanedPhone) {
		return nil, errors.New("invalid phone number format. Please provide international format with country code (e.g. +1234567890)")
	}

	// 2. Validate current state
	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingPhoneNumber {
		return nil, errors.New("not awaiting a phone number")
	}

	// 3. Priority 4: Enforce phone submission rate limiting (3s cooldown and max attempts ceiling)
	if err := m.checkPhoneRateLimit(userID); err != nil {
		return nil, err
	}

	// 4. Begin single-flight user operation with cancellation support (Priority 9)
	opCtx, unlock, err := m.beginUserOpWithCancel(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// 5. Ensure admission slot is held
	if err := m.acquireSlot(opCtx, userID); err != nil {
		return nil, err
	}

	p, err := m.GetOrInitProvider(opCtx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	_ = m.store.SetState(userID, StateRequestingCode)

	slog.Info("Requesting login code", "provider", p.Name(), "phone", security.RedactPhone(cleanedPhone))
	res, err := p.StartPhoneAuth(opCtx, cleanedPhone)
	if err != nil {
		// Priority 5 bug fix: allow recovery on code dispatch error
		_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
		return nil, err
	}

	if res.Status == providers.StatusError {
		_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
		return res, nil
	}

	_ = m.store.Update(userID, func(s *UserSession) {
		s.PhoneNumber = cleanedPhone
		s.PhoneCodeHash = res.PhoneCodeHash
	})
	_ = m.store.SetState(userID, StateAwaitingOTP)

	return res, nil
}

// StartQRAuth begins QR code authentication and generates PNG bytes of the QR code.
func (m *Manager) StartQRAuth(ctx context.Context, userID int64) (*providers.AuthResult, []byte, error) {
	opCtx, unlock, err := m.beginUserOpWithCancel(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()

	if err := m.acquireSlot(opCtx, userID); err != nil {
		return nil, nil, err
	}

	sess := m.store.Get(userID)
	if sess == nil {
		return nil, nil, errors.New("session not found")
	}

	p, err := m.GetOrInitProvider(opCtx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	if !p.SupportsQR() {
		return nil, nil, providers.ErrQRUnsupported
	}

	res, err := p.StartQRAuth(opCtx)
	if err != nil {
		return nil, nil, err
	}

	if res.Status != providers.StatusQRCode || res.QRTokenURL == "" {
		return res, nil, nil
	}

	// Generate QR Code PNG
	pngBytes, err := qrcode.Encode(res.QRTokenURL, qrcode.Medium, 300)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode QR code image: %w", err)
	}

	_ = m.store.SetState(userID, StateAwaitingQRAuthentication)
	return res, pngBytes, nil
}

// WaitForQRLogin blocks until QR code is scanned or expires.
func (m *Manager) WaitForQRLogin(ctx context.Context, userID int64) (*providers.AuthResult, error) {
	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	res, err := p.WaitForQRLogin(ctx)
	if err != nil {
		return nil, err
	}

	if res.Status == providers.StatusNeedPassword {
		_ = m.store.SetState(userID, StateAwaitingTwoFactorPassword)
	}

	return res, nil
}

// SubmitOTP verifies the user-entered login code with rate limit and attempt tracking.
func (m *Manager) SubmitOTP(ctx context.Context, userID int64, otpCode string) (*providers.AuthResult, error) {
	opCtx, unlock, err := m.beginUserOpWithCancel(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := m.checkCooldown(userID); err != nil {
		return nil, err
	}

	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingOTP {
		return nil, errors.New("not awaiting a verification code")
	}

	p, err := m.GetOrInitProvider(opCtx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	slog.Info("Verifying OTP code", "provider", p.Name())
	res, err := p.SubmitOTP(opCtx, strings.TrimSpace(otpCode))
	if err != nil {
		return nil, err
	}

	if res.Status == providers.StatusError {
		attempts := m.recordOTPAttempt(userID, false)
		if attempts >= 3 {
			_ = m.Cancel(opCtx, userID)
			return nil, errors.New("too many failed verification code attempts. Your authentication session has been terminated for security. Please restart with /start.")
		}
		return res, nil
	}

	m.recordOTPAttempt(userID, true)
	if res.Status == providers.StatusNeedPassword {
		_ = m.store.SetState(userID, StateAwaitingTwoFactorPassword)
		return res, nil
	}

	return res, nil
}

// SubmitPassword verifies the two-step verification password with attempt tracking.
func (m *Manager) SubmitPassword(ctx context.Context, userID int64, password string) (*providers.AuthResult, error) {
	opCtx, unlock, err := m.beginUserOpWithCancel(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := m.checkCooldown(userID); err != nil {
		return nil, err
	}

	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingTwoFactorPassword {
		return nil, errors.New("not awaiting a two-step verification password")
	}

	p, err := m.GetOrInitProvider(opCtx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	slog.Info("Verifying 2FA password", "provider", p.Name())
	res, err := p.SubmitPassword(opCtx, password)
	if err != nil {
		return nil, err
	}

	if res.Status == providers.StatusError {
		attempts := m.recordPasswordAttempt(userID, false)
		if attempts >= 3 {
			_ = m.Cancel(opCtx, userID)
			return nil, errors.New("too many failed two-step verification attempts. Your authentication session has been terminated for security. Please restart with /start.")
		}
		return res, nil
	}

	m.recordPasswordAttempt(userID, true)
	return res, nil
}

// CompleteAndDeliverToSavedMessages verifies login, generates native session, validates fresh client,
// delivers to Saved Messages, and verifies delivery.
func (m *Manager) CompleteAndDeliverToSavedMessages(ctx context.Context, userID int64) (*providers.AccountInfo, int64, error) {
	opCtx, unlock, err := m.beginUserOpWithCancel(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()

	sess := m.store.Get(userID)
	if sess == nil {
		return nil, 0, errors.New("no active session found")
	}

	p, err := m.GetOrInitProvider(opCtx, userID)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("provider initialization failed: %w", err)
	}

	// 1. Login Verification on the authenticated client
	_ = m.store.SetState(userID, StateValidatingSession)
	slog.Info("Verifying authenticated account login", "provider", p.Name())
	authAccount, err := p.GetAccountInfo(opCtx)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("login verification failed: %w", err)
	}
	if authAccount == nil || authAccount.ID <= 0 {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, errors.New("login verification failed: invalid account identity")
	}

	// 2. Export native string session
	_ = m.store.SetState(userID, StateGeneratingSession)
	slog.Info("Generating native string session", "provider", p.Name(), "account_id", authAccount.ID)
	sessionString, err := p.ExportSession(opCtx)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("session export failed: %w", err)
	}

	// 3. Fresh client validation
	_ = m.store.SetState(userID, StateValidatingSession)
	slog.Info("Validating session with fresh client instance", "provider", p.Name())
	valAccount, err := p.ValidateSession(opCtx, sessionString)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("fresh client session validation failed: %w", err)
	}

	// 4. Identity Match Verification
	if valAccount.ID != authAccount.ID {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("account identity mismatch: logged in as %d but fresh session validated as %d", authAccount.ID, valAccount.ID)
	}

	// 5. Send string session to Telegram Saved Messages & verify delivery
	_ = m.store.SetState(userID, StateDeliveringSession)
	slog.Info("Delivering session to Telegram Saved Messages", "provider", p.Name(), "account_id", authAccount.ID)
	msgID, err := p.SendToSavedMessages(opCtx, sessionString)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("delivery to Saved Messages failed: %w", err)
	}

	// 6. Complete and safe cleanup
	m.Cleanup(userID)
	_ = m.store.SetState(userID, StateCompleted)
	slog.Info("Session successfully verified and delivered to Saved Messages", "provider", p.Name(), "account_id", authAccount.ID, "saved_msg_id", msgID)

	return authAccount, msgID, nil
}

// CompleteAndValidate exports the native session, validates it via a fresh client, and cleans up.
func (m *Manager) CompleteAndValidate(ctx context.Context, userID int64) (string, *providers.AccountInfo, error) {
	account, _, err := m.CompleteAndDeliverToSavedMessages(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	return "", account, nil
}

// Cancel terminates active authentication and cleans up all temporary state.
func (m *Manager) Cancel(ctx context.Context, userID int64) error {
	m.mu.Lock()
	m.cancelling[userID] = true
	cancelFn := m.userCancels[userID]
	opDone := m.userOps[userID]
	p, hasProvider := m.activeProviders[userID]
	delete(m.activeProviders, userID)
	delete(m.pendingInits, userID)
	m.mu.Unlock()

	// Trigger cancellation of active in-flight worker operation (Priority 9)
	if cancelFn != nil {
		cancelFn()
	}

	// Wait up to 1.5 seconds for active operation goroutine to exit gracefully
	if opDone != nil {
		select {
		case <-opDone:
		case <-time.After(1500 * time.Millisecond):
			slog.Warn("Timed out waiting for in-flight user op during cancellation", "user_id", userID)
		}
	}

	m.releaseSlot(userID)
	m.resetAttempts(userID)

	if hasProvider && p != nil {
		_ = p.Cancel(ctx)
		p.Cleanup()
	}

	m.mu.Lock()
	delete(m.cancelling, userID)
	delete(m.userCancels, userID)
	m.mu.Unlock()

	_ = m.store.SetState(userID, StateCancelled)
	m.store.Clear(userID)
	slog.Info("User authentication cancelled and state cleared", "user_id", userID)
	return nil
}

// Cleanup releases all provider instances, releases concurrency slots, and removes memory traces.
func (m *Manager) Cleanup(userID int64) {
	m.mu.Lock()
	cancelFn := m.userCancels[userID]
	opDone := m.userOps[userID]
	p, ok := m.activeProviders[userID]
	if ok {
		delete(m.activeProviders, userID)
	}
	delete(m.pendingInits, userID)
	m.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if opDone != nil {
		select {
		case <-opDone:
		case <-time.After(500 * time.Millisecond):
		}
	}

	m.releaseSlot(userID)
	m.resetAttempts(userID)

	if ok && p != nil {
		p.Cleanup()
	}

	m.mu.Lock()
	delete(m.cancelling, userID)
	delete(m.userCancels, userID)
	m.mu.Unlock()

	m.store.Clear(userID)
}
