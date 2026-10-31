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

// Manager coordinates user sessions, provider lifecycles, and authentication states.
type Manager struct {
	cfg             *config.Config
	store           *SessionStore
	activeProviders map[int64]providers.Provider
	mu              sync.Mutex
}

// NewManager creates a new authentication manager.
func NewManager(cfg *config.Config) *Manager {
	mgr := &Manager{
		cfg:             cfg,
		activeProviders: make(map[int64]providers.Provider),
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

// SelectProvider associates a provider with the user's session.
func (m *Manager) SelectProvider(ctx context.Context, userID int64, providerInput string) (bool, error) {
	canonicalName := providers.NormalizeName(providerInput)
	if canonicalName == "" {
		return false, errors.New("invalid provider selected")
	}

	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateSelectingProvider {
		return false, errors.New("please start a new session before selecting a provider")
	}

	// Instantiate the provider
	p, err := providers.Get(ctx, canonicalName, m.cfg.APIID, m.cfg.APIHash, m.cfg.PythonPath, m.cfg.WorkerTimeout)
	if err != nil {
		slog.Error("Failed to instantiate provider", "provider", canonicalName, "error", err)
		return false, fmt.Errorf("unable to initialize %s: %w", canonicalName, err)
	}

	m.mu.Lock()
	if existing, ok := m.activeProviders[userID]; ok {
		existing.Cleanup()
	}
	m.activeProviders[userID] = p
	m.mu.Unlock()

	sess.ProviderName = providers.DisplayName(p.Name())

	if p.SupportsQR() {
		_ = m.store.SetState(userID, StateSelectingAuthMethod)
		return true, nil
	}

	_ = m.store.SetState(userID, StateAwaitingPhoneNumber)
	return false, nil
}

// ChooseAuthMethod handles the choice between Phone and QR code when supported.
func (m *Manager) ChooseAuthMethod(userID int64, method string) (State, error) {
	sess := m.store.Get(userID)
	if sess == nil || sess.State != StateSelectingAuthMethod {
		return StateIdle, errors.New("not currently selecting an authentication method")
	}

	m.mu.Lock()
	p, hasProvider := m.activeProviders[userID]
	m.mu.Unlock()

	if !hasProvider {
		return StateIdle, errors.New("provider instance missing for session")
	}

	lower := strings.ToLower(method)
	if strings.Contains(lower, "qr") {
		if !p.SupportsQR() {
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

	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return nil, errors.New("provider not initialized")
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

	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return nil, nil, errors.New("provider not initialized")
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
	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return nil, errors.New("provider not initialized")
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

	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return nil, errors.New("provider not initialized")
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

	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return nil, errors.New("provider not initialized")
	}

	slog.Info("Verifying 2FA password", "provider", p.Name())
	res, err := p.SubmitPassword(ctx, password)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CompleteAndValidate exports the native session, validates it via a fresh client, and cleans up.
func (m *Manager) CompleteAndValidate(ctx context.Context, userID int64) (string, *providers.AccountInfo, error) {
	sess := m.store.Get(userID)
	if sess == nil {
		return "", nil, errors.New("no active session found")
	}

	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	m.mu.Unlock()

	if !ok {
		return "", nil, errors.New("provider not initialized")
	}

	_ = m.store.SetState(userID, StateGeneratingSession)
	slog.Info("Exporting native session string", "provider", p.Name())

	sessionString, err := p.ExportSession(ctx)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return "", nil, fmt.Errorf("session export failed: %w", err)
	}

	_ = m.store.SetState(userID, StateValidatingSession)
	slog.Info("Validating session with fresh client", "provider", p.Name())

	account, err := p.ValidateSession(ctx, sessionString)
	if err != nil {
		_ = m.store.SetState(userID, StateFailed)
		m.Cleanup(userID)
		return "", nil, fmt.Errorf("session validation failed: %w", err)
	}

	_ = m.store.SetState(userID, StateDeliveringSession)
	slog.Info("Session validated successfully", "provider", p.Name(), "account_id", account.ID)

	// Clean up ephemeral provider and state immediately
	m.Cleanup(userID)

	return sessionString, account, nil
}

// Cancel terminates active authentication and cleans up all temporary state.
func (m *Manager) Cancel(ctx context.Context, userID int64) error {
	m.mu.Lock()
	p, ok := m.activeProviders[userID]
	if ok {
		delete(m.activeProviders, userID)
	}
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
	m.mu.Unlock()

	if ok && p != nil {
		p.Cleanup()
	}

	m.store.Clear(userID)
}
