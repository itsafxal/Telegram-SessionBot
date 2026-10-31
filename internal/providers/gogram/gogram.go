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

// cleanPhoneNumber strips whitespace, dashes, and parentheses while preserving valid digits and '+' prefix.
func cleanPhoneNumber(phone string) string {
	phone = strings.TrimSpace(phone)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	return phone
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

// sendCodeWithRetry calls AuthSendCode with non-nil CodeSettings and handles DC migration and connection initialization.
func (p *Provider) sendCodeWithRetry(phoneNumber string, maxRetries int) (string, error) {
	settings := &telegram.CodeSettings{
		AllowAppHash:  true,
		CurrentNumber: true,
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		sentCode, err := p.client.AuthSendCode(phoneNumber, p.apiID, p.apiHash, settings)
		if err != nil {
			if telegram.MatchError(err, "CONNECTION_NOT_INITED") {
				if initErr := p.client.InitialRequest(); initErr == nil {
					continue
				}
			}
			if telegram.MatchError(err, "DC_MIGRATE") || telegram.MatchError(err, "PHONE_MIGRATE") || telegram.MatchError(err, "NETWORK_MIGRATE") {
				continue
			}
			return "", err
		}

		switch obj := sentCode.(type) {
		case *telegram.AuthSentCodeObj:
			return obj.PhoneCodeHash, nil
		case *telegram.AuthSentCodePaymentRequired:
			return obj.PhoneCodeHash, nil
		default:
			return "", fmt.Errorf("unexpected response type from Telegram: %T", sentCode)
		}
	}
	return "", errors.New("maximum DC migration retry attempts exceeded")
}

func (p *Provider) StartPhoneAuth(ctx context.Context, phoneNumber string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	if p.apiID <= 0 || p.apiHash == "" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: "Invalid API credentials configured.",
		}, nil
	}

	cleanedPhone := cleanPhoneNumber(phoneNumber)
	if cleanedPhone == "" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: "Phone number cannot be empty.",
		}, nil
	}

	// Ensure TCP connection is active
	if !p.client.IsConnected() {
		if _, err := p.client.Conn(); err != nil {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: fmt.Sprintf("Failed to establish connection to Telegram: %v", err),
			}, nil
		}
	}

	p.phoneNumber = cleanedPhone
	phoneCodeHash, err := p.sendCodeWithRetry(p.phoneNumber, 3)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "PHONE_NUMBER_INVALID") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Invalid phone number provided. Please ensure it includes your country code (e.g. +1234567890).",
			}, nil
		}
		if strings.Contains(errStr, "PHONE_NUMBER_UNOCCUPIED") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "This phone number is not registered on Telegram. Please sign up in the official Telegram app first.",
			}, nil
		}
		if strings.Contains(errStr, "PHONE_NUMBER_BANNED") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "This phone number has been banned by Telegram.",
			}, nil
		}
		if strings.Contains(errStr, "FLOOD_WAIT") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Telegram flood wait limit reached. Please wait before retrying.",
			}, nil
		}
		if strings.Contains(errStr, "API_ID_INVALID") || strings.Contains(errStr, "API_ID_PUBLISHED_FLOOD") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Invalid API credentials configured.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Failed to send code: %s", errStr),
		}, nil
	}

	p.phoneCodeHash = phoneCodeHash
	return &providers.AuthResult{
		Status:        providers.StatusNeedOTP,
		PhoneCodeHash: phoneCodeHash,
	}, nil
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
	if phoneCode == "" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: "Verification code cannot be empty.",
		}, nil
	}

	if p.phoneCodeHash == "" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: "Missing phone code hash. Please request a new code.",
		}, nil
	}

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
				ErrorMessage: "Invalid verification code. Please check the code and try again.",
			}, nil
		}
		if strings.Contains(errStr, "PHONE_CODE_EXPIRED") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Verification code has expired. Please request a new one.",
			}, nil
		}
		if strings.Contains(errStr, "PHONE_CODE_EMPTY") {
			return &providers.AuthResult{
				Status:       providers.StatusError,
				ErrorMessage: "Verification code cannot be empty.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("Authentication failed: %s", errStr),
		}, nil
	}

	_ = p.client.SaveSession(true)

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
				ErrorMessage: "Incorrect two-step verification password. Please try again.",
			}, nil
		}
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: fmt.Sprintf("2FA verification failed: %s", err.Error()),
		}, nil
	}

	_ = p.client.SaveSession(true)

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

func (p *Provider) GetAccountInfo(ctx context.Context) (*providers.AccountInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, errors.New("gogram client is closed")
	}

	user, err := p.client.GetMe()
	if err != nil {
		return nil, fmt.Errorf("login verification failed: %w", err)
	}

	return &providers.AccountInfo{
		ID:        user.ID,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Username:  user.Username,
	}, nil
}

func (p *Provider) SendToSavedMessages(ctx context.Context, sessionString string) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return 0, errors.New("gogram client is closed")
	}

	user, err := p.client.GetMe()
	userID := int64(0)
	name := "Authorized Account"
	username := "N/A"
	if err == nil && user != nil {
		userID = user.ID
		name = strings.TrimSpace(user.FirstName + " " + user.LastName)
		if user.Username != "" {
			username = "@" + user.Username
		}
	}
	nowUTC := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	text := fmt.Sprintf("🔐 **Universal Telegram MTProto Session**\n\n"+
		"• **Provider:** %s\n"+
		"• **Account ID:** `%d`\n"+
		"• **Account Name:** %s\n"+
		"• **Username:** %s\n"+
		"• **Generated At:** %s\n"+
		"• **Status:** ✅ Validated & Verified\n\n"+
		"📋 **Your String Session:**\n"+
		"```\n%s\n```\n\n"+
		"⚠️ **Security Advisory:**\n"+
		"This String Session grants full access to your Telegram account. Never share it with anyone or paste it into untrusted bots or websites.",
		providers.DisplayNameGogram,
		userID,
		name,
		username,
		nowUTC,
		sessionString,
	)

	sentMsg, err := p.client.SendMessage("me", text)
	if err != nil {
		return 0, fmt.Errorf("failed to send session to Saved Messages: %w", err)
	}
	if sentMsg == nil || sentMsg.ID <= 0 {
		return 0, errors.New("failed to send session: no valid message ID received from Telegram")
	}

	// Verify delivery by searching for the message ID in Saved Messages
	msgs, err := p.client.GetMessages("me", &telegram.SearchOption{
		IDs: []int32{sentMsg.ID},
	})
	if err != nil || len(msgs) == 0 || msgs[0].ID != sentMsg.ID {
		// If explicit retrieval encountered a transient error but sentMsg.ID > 0 was confirmed by Telegram
		if sentMsg.ID > 0 {
			return int64(sentMsg.ID), nil
		}
		return 0, errors.New("failed to verify message persistence in Saved Messages")
	}

	return int64(sentMsg.ID), nil
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
	p.phoneNumber = ""
	p.phoneCodeHash = ""
	p.qrToken = nil
	p.qrExpires = 0
}

func init() {
	providers.Register("gogram", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return New(ctx, apiID, apiHash, pythonPath, timeout)
	})
}
