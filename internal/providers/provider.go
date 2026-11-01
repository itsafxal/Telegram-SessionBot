package providers

import (
	"context"
	"errors"
)

// AuthStatus indicates the outcome of an authentication step.
type AuthStatus string

const (
	StatusSuccess      AuthStatus = "success"
	StatusNeedOTP      AuthStatus = "need_otp"
	StatusNeedPassword AuthStatus = "need_password"
	StatusQRCode       AuthStatus = "qr_code"
	StatusError        AuthStatus = "error"
)

// AccountInfo holds public metadata of an authenticated user for session validation confirmation.
type AccountInfo struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// AuthResult is the standard response returned by provider auth operations.
type AuthResult struct {
	Status        AuthStatus   `json:"status"`
	PhoneCodeHash string       `json:"phone_code_hash,omitempty"`
	QRTokenURL    string       `json:"qr_token_url,omitempty"`
	QRExpires     int32        `json:"qr_expires,omitempty"`
	SessionString string       `json:"session_string,omitempty"`
	Account       *AccountInfo `json:"account,omitempty"`
	ErrorMessage  string       `json:"error_message,omitempty"`
}

var (
	ErrSessionPasswordNeeded = errors.New("two-factor authentication (2FA) password required")
	ErrInvalidPhone          = errors.New("invalid phone number provided")
	ErrInvalidCode           = errors.New("invalid or expired verification code")
	ErrInvalidPassword       = errors.New("invalid 2FA password")
	ErrFloodWait             = errors.New("telegram flood wait: please wait before retrying")
	ErrQRUnsupported         = errors.New("qr code authentication is not supported by this provider")
)

// Provider defines the unified interface that all MTProto session generators must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "Pyrofork", "Telethon", "Gogram")
	Name() string

	// SupportsQR indicates whether the provider natively supports QR-based login
	SupportsQR() bool

	// StartPhoneAuth requests a verification code for the provided phone number
	StartPhoneAuth(ctx context.Context, phoneNumber string) (*AuthResult, error)

	// StartQRAuth begins QR code authentication if supported
	StartQRAuth(ctx context.Context) (*AuthResult, error)

	// WaitForQRLogin blocks until QR code is scanned or expires
	WaitForQRLogin(ctx context.Context) (*AuthResult, error)

	// SubmitOTP verifies the login code received by the user
	SubmitOTP(ctx context.Context, phoneCode string) (*AuthResult, error)

	// SubmitPassword verifies the 2FA password
	SubmitPassword(ctx context.Context, password string) (*AuthResult, error)

	// ExportSession exports the library's native String Session
	ExportSession(ctx context.Context) (string, error)

	// ValidateSession creates a fresh isolated client to verify account authorization
	ValidateSession(ctx context.Context, sessionString string) (*AccountInfo, error)

	// GetAccountInfo verifies that MTProto authorization succeeded and returns authenticated account identity
	GetAccountInfo(ctx context.Context) (*AccountInfo, error)

	// SendToSavedMessages sends the generated session string to the account's own Saved Messages and verifies delivery
	SendToSavedMessages(ctx context.Context, sessionString string) (int64, error)

	// Cancel stops any in-progress authentication
	Cancel(ctx context.Context) error

	// IsAlive returns true if the underlying client or worker process is operational
	IsAlive() bool

	// Cleanup frees all resources, worker processes, and in-memory caches
	Cleanup()
}
