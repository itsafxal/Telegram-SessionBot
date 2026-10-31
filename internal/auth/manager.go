package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

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
	mu              sync.Mutex
}

// NewManager creates a new authentication manager.
func NewManager(cfg *config.Config) *Manager {
	mgr := &Manager{
		cfg:             cfg,
		activeProviders: make(map[int64]providers.Provider),
		pendingInits:    make(map[int64]*pendingInit),
	}

	mgr.store = NewSessionStore(cfg.SessionExpiryDuration, func(userID int64) {
		slog.Info("Cleaning up expired user session", "user_id", userID)
		mgr.Cleanup(userID)
	})

	return mgr
}

// GetSession retrieves the in-memory session for a user.
func (m *Manager) GetSession(userID int64) *UserSession {
	return m.store.Get(userID)
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

	supportsQR := providers.SupportsQR(canonicalName)
	sess.ProviderName = providers.DisplayName(canonicalName)

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
			}
			pending.provider = p
			pending.err = err
		} else {
			// Session was cancelled or changed while initializing
			if p != nil {
				go p.Cleanup()
			}
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
		sess.AuthMethod = "qr"
		if err := m.store.SetState(userID, StateAwaitingQRAuthentication); err != nil {
			return StateIdle, err
		}
		return StateAwaitingQRAuthentication, nil
	}

	sess.AuthMethod = "phone"
	if err := m.store.SetState(userID, StateAwaitingPhoneNumber); err != nil {
		return StateIdle, err
	}
	return StateAwaitingPhoneNumber, nil
}

// StartPhoneAuth validates the phone number and requests an OTP code from Telegram.
func (m *Manager) StartPhoneAuth(ctx context.Context, userID int64, rawPhone string) (*providers.AuthResult, error) {
	cleanedPhone := strings.ReplaceAll(strings.TrimSpace(rawPhone), " ", "")
	if !strings.HasPrefix(cleanedPhone, "+") {
		cleanedPhone = "+" + cleanedPhone
	}

	if !phoneRegex.MatchString(cleanedPhone) {
		return nil, errors.New("invalid phone number format. Please provide international format with country code (e.g. +1234567890)")
	}

	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingPhoneNumber {
		return nil, errors.New("not awaiting a phone number")
	}

	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	_ = m.store.SetState(userID, StateRequestingCode)

	slog.Info("Requesting login code", "provider", p.Name(), "phone", security.RedactPhone(cleanedPhone))
	res, err := p.StartPhoneAuth(ctx, cleanedPhone)
	if err != nil {
		_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
		return nil, err
	}

	if res.Status == providers.StatusError {
		_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
		return res, nil
	}

	sess.PhoneNumber = cleanedPhone
	sess.PhoneCodeHash = res.PhoneCodeHash
	_ = m.store.SetState(userID, StateAwaitingOTP)

	return res, nil
}

// StartQRAuth begins QR code authentication and generates PNG bytes of the QR code.
func (m *Manager) StartQRAuth(ctx context.Context, userID int64) (*providers.AuthResult, []byte, error) {
	sess := m.store.Get(userID)
	if sess == nil {
		return nil, nil, errors.New("session not found")
	}

	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	if !p.SupportsQR() {
		return nil, nil, providers.ErrQRUnsupported
	}

	res, err := p.StartQRAuth(ctx)
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

// SubmitOTP verifies the user-entered login code.
func (m *Manager) SubmitOTP(ctx context.Context, userID int64, otpCode string) (*providers.AuthResult, error) {
	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingOTP {
		return nil, errors.New("not awaiting a verification code")
	}

	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	slog.Info("Verifying OTP code", "provider", p.Name())
	res, err := p.SubmitOTP(ctx, strings.TrimSpace(otpCode))
	if err != nil {
		return nil, err
	}

	if res.Status == providers.StatusNeedPassword {
		_ = m.store.SetState(userID, StateAwaitingTwoFactorPassword)
		return res, nil
	}

	return res, nil
}

// SubmitPassword verifies the two-step verification password.
func (m *Manager) SubmitPassword(ctx context.Context, userID int64, password string) (*providers.AuthResult, error) {
	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateAwaitingTwoFactorPassword {
		return nil, errors.New("not awaiting a two-step verification password")
	}

	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("provider initialization failed: %w", err)
	}

	slog.Info("Verifying 2FA password", "provider", p.Name())
	res, err := p.SubmitPassword(ctx, password)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CompleteAndDeliverToSavedMessages verifies login, generates native session, validates fresh client,
// delivers to Saved Messages, and verifies delivery.
func (m *Manager) CompleteAndDeliverToSavedMessages(ctx context.Context, userID int64) (*providers.AccountInfo, int64, error) {
	sess := m.store.Get(userID)
	if sess == nil {
		return nil, 0, errors.New("no active session found")
	}

	p, err := m.GetOrInitProvider(ctx, userID)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("provider initialization failed: %w", err)
	}

	// 1. Login Verification on the authenticated client
	_ = m.store.SetState(userID, StateValidatingSession)
	slog.Info("Verifying authenticated account login", "provider", p.Name())
	authAccount, err := p.GetAccountInfo(ctx)
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
	sessionString, err := p.ExportSession(ctx)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return nil, 0, fmt.Errorf("session export failed: %w", err)
	}

	// 3. Fresh client validation
	_ = m.store.SetState(userID, StateValidatingSession)
	slog.Info("Validating session with fresh client instance", "provider", p.Name())
	valAccount, err := p.ValidateSession(ctx, sessionString)
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
	msgID, err := p.SendToSavedMessages(ctx, sessionString)
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
	p, ok := m.activeProviders[userID]
	if ok {
		delete(m.activeProviders, userID)
	}
	delete(m.pendingInits, userID)
	m.mu.Unlock()

	if ok && p != nil {
		_ = p.Cancel(ctx)
		p.Cleanup()
	}

	_ = m.store.SetState(userID, StateCancelled)
	m.store.Clear(userID)
	slog.Info("User authentication cancelled and state cleared", "user_id", userID)
	return nil
}

// Cleanup releases all provider instances and removes memory traces.
func (m *Manager) Cleanup(userID int64) {
	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	if ok {
		delete(m.activeProviders, userID)
	}
	delete(m.pendingInits, userID)
	m.mu.Unlock()

	if ok && p != nil {
		p.Cleanup()
	}

	m.store.Clear(userID)
}
