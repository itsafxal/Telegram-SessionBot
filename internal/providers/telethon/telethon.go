package telethon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"universal-session-generator/internal/providers"
	"universal-session-generator/internal/workers"
)

type Provider struct {
	apiID      int32
	apiHash    string
	pythonPath string
	timeout    time.Duration
	worker     *workers.WorkerProcess
	mu         sync.Mutex
}

// New creates a new Telethon provider instance.
func New(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
	scriptPath := filepath.Join("workers", "telethon", "worker.py")
	worker, err := workers.StartWorker(ctx, pythonPath, scriptPath, "telethon", timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to launch telethon worker: %w", err)
	}

	p := &Provider{
		apiID:      apiID,
		apiHash:    apiHash,
		pythonPath: pythonPath,
		timeout:    timeout,
		worker:     worker,
	}

	// Initialize worker with API credentials
	resp, err := worker.SendCommand(ctx, "init", map[string]interface{}{
		"api_id":   apiID,
		"api_hash": apiHash,
	})
	if err != nil {
		_ = worker.Close()
		return nil, fmt.Errorf("failed to initialize telethon client: %w", err)
	}
	if resp.Status != "ok" {
		_ = worker.Close()
		return nil, fmt.Errorf("telethon init error: %s", resp.Error)
	}

	return p, nil
}

func (p *Provider) Name() string {
	return providers.DisplayNameTelethon
}

func (p *Provider) SupportsQR() bool {
	return true
}

func (p *Provider) StartPhoneAuth(ctx context.Context, phoneNumber string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := p.worker.SendCommand(ctx, "send_code", map[string]interface{}{
		"phone_number": phoneNumber,
	})
	if err != nil {
		return nil, err
	}

	if resp.Status == "error" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: resp.Error,
		}, nil
	}

	var hash string
	if resp.Data != nil {
		if h, ok := resp.Data["phone_code_hash"].(string); ok {
			hash = h
		}
	}

	return &providers.AuthResult{
		Status:        providers.StatusNeedOTP,
		PhoneCodeHash: hash,
	}, nil
}

func (p *Provider) StartQRAuth(ctx context.Context) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := p.worker.SendCommand(ctx, "start_qr", nil)
	if err != nil {
		return nil, err
	}

	if resp.Status != "qr_code" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: resp.Error,
		}, nil
	}

	url, _ := resp.Data["token_url"].(string)
	expVal, _ := resp.Data["expires"].(float64)

	return &providers.AuthResult{
		Status:     providers.StatusQRCode,
		QRTokenURL: url,
		QRExpires:  int32(expVal),
	}, nil
}

func (p *Provider) WaitForQRLogin(ctx context.Context) (*providers.AuthResult, error) {
	p.mu.Lock()
	worker := p.worker
	p.mu.Unlock()

	if worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := worker.SendCommand(ctx, "wait_qr", nil)
	if err != nil {
		return nil, err
	}

	if resp.Status == "need_password" {
		return &providers.AuthResult{
			Status: providers.StatusNeedPassword,
		}, nil
	}

	if resp.Status == "error" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: resp.Error,
		}, nil
	}

	return &providers.AuthResult{
		Status: providers.StatusSuccess,
	}, nil
}

func (p *Provider) SubmitOTP(ctx context.Context, phoneCode string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := p.worker.SendCommand(ctx, "submit_otp", map[string]interface{}{
		"phone_code": phoneCode,
	})
	if err != nil {
		return nil, err
	}

	if resp.Status == "need_password" {
		return &providers.AuthResult{
			Status: providers.StatusNeedPassword,
		}, nil
	}

	if resp.Status == "error" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: resp.Error,
		}, nil
	}

	return &providers.AuthResult{
		Status: providers.StatusSuccess,
	}, nil
}

func (p *Provider) SubmitPassword(ctx context.Context, password string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := p.worker.SendCommand(ctx, "submit_password", map[string]interface{}{
		"password": password,
	})
	if err != nil {
		return nil, err
	}

	if resp.Status == "error" {
		return &providers.AuthResult{
			Status:       providers.StatusError,
			ErrorMessage: resp.Error,
		}, nil
	}

	return &providers.AuthResult{
		Status: providers.StatusSuccess,
	}, nil
}

func (p *Provider) ExportSession(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker == nil {
		return "", errors.New("provider worker is closed")
	}

	resp, err := p.worker.SendCommand(ctx, "export_session", nil)
	if err != nil {
		return "", err
	}

	if resp.Status != "ok" {
		return "", fmt.Errorf("failed to export telethon session: %s", resp.Error)
	}

	session, ok := resp.Data["session"].(string)
	if !ok || session == "" {
		return "", errors.New("empty session returned by telethon worker")
	}

	return session, nil
}

func (p *Provider) ValidateSession(ctx context.Context, sessionString string) (*providers.AccountInfo, error) {
	p.mu.Lock()
	worker := p.worker
	p.mu.Unlock()

	if worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := worker.SendCommand(ctx, "validate_session", map[string]interface{}{
		"session": sessionString,
	})
	if err != nil {
		return nil, err
	}

	if resp.Status != "ok" {
		return nil, fmt.Errorf("telethon session validation failed: %s", resp.Error)
	}

	idVal, _ := resp.Data["id"].(float64)
	firstName, _ := resp.Data["first_name"].(string)
	lastName, _ := resp.Data["last_name"].(string)
	username, _ := resp.Data["username"].(string)

	return &providers.AccountInfo{
		ID:        int64(idVal),
		FirstName: firstName,
		LastName:  lastName,
		Username:  username,
	}, nil
}

func (p *Provider) Cancel(ctx context.Context) error {
	p.Cleanup()
	return nil
}

func (p *Provider) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker != nil {
		_ = p.worker.Close()
		p.worker = nil
	}
}

func init() {
	providers.Register("telethon", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return New(ctx, apiID, apiHash, pythonPath, timeout)
	})
}
