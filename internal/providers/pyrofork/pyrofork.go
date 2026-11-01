package pyrofork

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

// New creates a new Pyrogram provider instance (powered internally by Pyrofork).
func New(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
	scriptPath := filepath.Join("workers", "pyrofork", "worker.py")
	worker, err := workers.StartWorker(ctx, pythonPath, scriptPath, "pyrofork", timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to launch pyrogram worker: %w", err)
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
		return nil, fmt.Errorf("failed to initialize pyrogram client: %w", err)
	}
	if resp.Status != "ok" {
		_ = worker.Close()
		return nil, fmt.Errorf("pyrogram init error: %s", resp.Error)
	}

	return p, nil
}

func (p *Provider) Name() string {
	return providers.DisplayNamePyrogram
}

func (p *Provider) SupportsQR() bool {
	return false
}

// IsAlive returns true if the underlying Python worker process is running and not marked closed.
func (p *Provider) IsAlive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.worker != nil && p.worker.IsAlive()
}

// ensureWorkerLocked verifies the worker is alive, or restarts and reinitializes it automatically.
func (p *Provider) ensureWorkerLocked(ctx context.Context) error {
	if p.worker != nil && p.worker.IsAlive() {
		return nil
	}

	if p.worker != nil {
		_ = p.worker.Close()
		p.worker = nil
	}

	scriptPath := filepath.Join("workers", "pyrofork", "worker.py")
	worker, err := workers.StartWorker(ctx, p.pythonPath, scriptPath, "pyrofork", p.timeout)
	if err != nil {
		return fmt.Errorf("failed to launch pyrogram worker: %w", err)
	}

	resp, err := worker.SendCommand(ctx, "init", map[string]interface{}{
		"api_id":   p.apiID,
		"api_hash": p.apiHash,
	})
	if err != nil {
		_ = worker.Close()
		return fmt.Errorf("failed to initialize pyrogram client: %w", err)
	}
	if resp.Status != "ok" {
		_ = worker.Close()
		return fmt.Errorf("pyrogram init error: %s", resp.Error)
	}

	p.worker = worker
	return nil
}

func (p *Provider) StartPhoneAuth(ctx context.Context, phoneNumber string) (*providers.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.ensureWorkerLocked(ctx); err != nil {
		return nil, err
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
	return nil, providers.ErrQRUnsupported
}

func (p *Provider) WaitForQRLogin(ctx context.Context) (*providers.AuthResult, error) {
	return nil, providers.ErrQRUnsupported
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
		return "", fmt.Errorf("failed to export pyrogram session: %s", resp.Error)
	}

	session, ok := resp.Data["session"].(string)
	if !ok || session == "" {
		return "", errors.New("empty session returned by pyrogram worker")
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
		return nil, fmt.Errorf("pyrogram session validation failed: %s", resp.Error)
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

func (p *Provider) GetAccountInfo(ctx context.Context) (*providers.AccountInfo, error) {
	p.mu.Lock()
	worker := p.worker
	p.mu.Unlock()

	if worker == nil {
		return nil, errors.New("provider worker is closed")
	}

	resp, err := worker.SendCommand(ctx, "get_me", nil)
	if err != nil {
		return nil, err
	}

	if resp.Status != "ok" {
		return nil, fmt.Errorf("login verification failed: %s", resp.Error)
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

func (p *Provider) SendToSavedMessages(ctx context.Context, sessionString string) (int64, error) {
	p.mu.Lock()
	worker := p.worker
	p.mu.Unlock()

	if worker == nil {
		return 0, errors.New("provider worker is closed")
	}

	resp, err := worker.SendCommand(ctx, "send_to_saved_messages", map[string]interface{}{
		"session":       sessionString,
		"provider_name": providers.DisplayNamePyrogram,
	})
	if err != nil {
		return 0, err
	}

	if resp.Status != "ok" {
		return 0, fmt.Errorf("saved messages delivery failed: %s", resp.Error)
	}

	msgIDVal, _ := resp.Data["message_id"].(float64)
	if msgIDVal <= 0 {
		return 0, errors.New("invalid message ID returned from Saved Messages delivery")
	}

	return int64(msgIDVal), nil
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
	factory := func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (providers.Provider, error) {
		return New(ctx, apiID, apiHash, pythonPath, timeout)
	}
	providers.Register("pyrofork", factory)
	providers.Register("pyrogram", factory)
}
