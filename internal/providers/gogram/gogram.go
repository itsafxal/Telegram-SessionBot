package gogram

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"universal-session-generator/internal/providers"
)

type Provider struct {
	apiID         int32
	apiHash       string
	timeout       time.Duration
	client        *telegram.Client
	phoneNumber   string
	phoneCodeHash string
	qrToken       []byte
	qrExpires     int32
	mu            sync.Mutex
}

// New creates a new native Gogram provider instance.
func New(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         apiID,
		AppHash:       apiHash,
		MemorySession: true,
		LogLevel:      telegram.WarnLevel,
		NoUpdates:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gogram client: %w", err)
	}

	_, err = client.Conn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect gogram client: %w", err)
	}

	return &Provider{
		apiID:   apiID,
		apiHash: apiHash,
		timeout: timeout,
		client:  client,
	}, nil
}

func (p *Provider) Name() string {
	return providers.DisplayNameGogram
}

func (p *Provider) SupportsQR() bool {
	return true
}

func (p *Provider) StartPhoneAuth(ctx context.Context, phoneNumber string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	p.phoneNumber = strings.TrimSpace(phoneNumber)
	sentCode, err := p.client.AuthSendCode(p.phoneNumber, p.apiID, p.apiHash, nil)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "PHONE_NUMBER_INVALID") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Invalid phone number provided.",
			}, nil
		}
		if strings.Contains(errStr, "FLOOD_WAIT") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Telegram flood wait: please wait before retrying.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Failed to send code: %s", errStr),
		}, nil
	}

	if obj, ok := sentCode.(*telegram.AuthSentCodeObj); ok {
		p.phoneCodeHash = obj.PhoneCodeHash
		return &providers.AuthResult{
			Status:        providers.StatusNeedOTP,
			PhoneCodeHash: obj.PhoneCodeHash,
		}, nil
	}

	return nil, errors.New("unexpected response from Telegram server during send code")
}

func (p *Provider) StartQRAuth(ctx context.Context) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	res, err := p.client.AuthExportLoginToken(p.apiID, p.apiHash, nil)
	if err != nil {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Failed to initialize QR login: %v", err),
		}, nil
	}

	switch tok := res.(type) {
	case *telegram.AuthLoginTokenObj:
		p.qrToken = tok.Token
		p.qrExpires = tok.Expires
		url := "tg://login?token=" + base64.RawURLEncoding.EncodeToString(tok.Token)
		return &providers.AuthResult{
			Status:     providers.StatusQRCode,
			QRTokenURL: url,
			QRExpires:  tok.Expires,
		}, nil

	case *telegram.AuthLoginTokenMigrateTo:
		if err := p.client.SwitchDc(int(tok.DcID)); err != nil {
			return nil, fmt.Errorf("failed to migrate DC: %w", err)
		}
		return p.StartQRAuth(ctx)

	case *telegram.AuthLoginTokenSuccess:
		return &providers.AuthResult{
			Status: providers.StatusSuccess,
		}, nil

	default:
		return nil, errors.New("unsupported QR login token response from Telegram")
	}
}

func (p *Provider) WaitForQRLogin(ctx context.Context) (*providers.AuthResult, error) {
	deadline := time.Now().Add(180 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return &providers.AuthResult{
					Status:       providers.StatusError,
					ErrorMessage: "QR login timed out. Please request a new QR code.",
				}, nil
			}

			p.mu.Lock()
			if p.client == nil {
				p.mu.Unlock()
				return nil, errors.New("gogram client is closed")
			}
			res, err := p.client.AuthExportLoginToken(p.apiID, p.apiHash, nil)
			p.mu.Unlock()

			if err != nil {
				if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") {
					return &providers.AuthResult{
						Status: providers.StatusNeedPassword,
					}, nil
				}
				continue
			}

			switch tok := res.(type) {
			case *telegram.AuthLoginTokenSuccess:
				return &providers.AuthResult{
					Status: providers.StatusSuccess,
				}, nil
			case *telegram.AuthLoginTokenMigrateTo:
				p.mu.Lock()
				if p.client != nil {
					_ = p.client.SwitchDc(int(tok.DcID))
				}
				p.mu.Unlock()
			}
		}
	}
}

func (p *Provider) SubmitOTP(ctx context.Context, phoneCode string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	phoneCode = strings.TrimSpace(phoneCode)
	_, err := p.client.AuthSignIn(p.phoneNumber, p.phoneCodeHash, phoneCode, nil)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "SESSION_PASSWORD_NEEDED") {
			return &providers.AuthResult{
				Status: providers.StatusNeedPassword,
			}, nil
		}
		if strings.Contains(errStr, "PHONE_CODE_INVALID") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Invalid verification code.",
			}, nil
		}
		if strings.Contains(errStr, "PHONE_CODE_EXPIRED") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Verification code has expired. Please request a new one.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Authentication failed: %s", errStr),
		}, nil
	}

	return &providers.AuthResult{
		Status: providers.StatusSuccess,
	}, nil
}

func (p *Provider) SubmitPassword(ctx context.Context, password string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	accPwd, err := p.client.AccountGetPassword()
	if err != nil {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Failed to get 2FA parameters: %s", err.Error()),
		}, nil
	}

	srpInput, err := telegram.GetInputCheckPassword(password, accPwd)
	if err != nil {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Failed to compute 2FA hash: %s", err.Error()),
		}, nil
	}

	_, err = p.client.AuthCheckPassword(srpInput)
	if err != nil {
		if strings.Contains(err.Error(), "PASSWORD_HASH_INVALID") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Incorrect two-step verification password.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("2FA verification failed: %s", err.Error()),
		}, nil
	}

	return &providers.AuthResult{
		Status: providers.StatusSuccess,
	}, nil
}

func (p *Provider) ExportSession(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return "", errors.New("gogram client is closed")
	}

	sessionStr := p.client.ExportSession()
	if sessionStr == "" {
		return "", errors.New("empty session string exported from gogram")
	}

	return sessionStr, nil
}

func (p *Provider) ValidateSession(ctx context.Context, sessionString string) (*providers.AccountInfo, error) {
	// Fresh isolated client to validate the exported session
	valClient, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         p.apiID,
		AppHash:       p.apiHash,
		StringSession: sessionString,
		MemorySession: true,
		LogLevel:      telegram.WarnLevel,
		NoUpdates:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create validation client: %w", err)
	}
	defer valClient.Terminate()

	_, err = valClient.Conn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect validation client: %w", err)
	}

	user, err := valClient.GetMe()
	if err != nil {
		return nil, fmt.Errorf("gogram session is not authorized: %w", err)
	}

	return &providers.AccountInfo{
		ID:        user.ID,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Username:  user.Username,
	}, nil
}

func (p *Provider) Cancel(ctx context.Context) error {
	p.Cleanup()
	return nil
}

func (p *Provider) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil {
		_ = p.client.Terminate()
		p.client = nil
	}
}

func init() {
	providers.Register("gogram", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return New(ctx, apiID, apiHash, pythonPath, timeout)
	})
}
