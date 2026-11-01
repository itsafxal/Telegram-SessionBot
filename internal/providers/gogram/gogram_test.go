package gogram

import (
	"context"
	"strings"
	"testing"

	"github.com/amarnathcjd/gogram/telegram"
	"universal-session-generator/internal/providers"
)

func TestAuthSendCodeNilMarshalingError(t *testing.T) {
	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         123456,
		AppHash:       "0123456789abcdef0123456789abcdef",
		NoPreconnect:  true,
		MemorySession: true,
		Timeout:       1,
		ReqTimeout:    1,
		LogLevel:      telegram.ErrorLevel,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Make MTProto TCP state active so makeRequest proceeds to sendPacket (tl.Marshal)
	client.MTProto.TcpState().SetActive(true)

	// 1. With nil settings: MUST fail during tl.Marshal with "marshaling request: value can't be nil"
	_, errNil := client.AuthSendCode("+1234567890", 123456, "0123456789abcdef0123456789abcdef", nil)
	if errNil == nil {
		t.Fatalf("expected error with nil settings, got nil")
	}
	if !strings.Contains(errNil.Error(), "marshaling request: value can't be nil") {
		t.Fatalf("expected 'marshaling request: value can't be nil', got: %v", errNil)
	}
	t.Logf("REPRODUCED CONFIRMED: %v", errNil)

	// 2. With properly initialized CodeSettings: MUST NOT fail with nil marshaling error
	settings := &telegram.CodeSettings{
		AllowAppHash:  true,
		CurrentNumber: true,
	}
	_, errValid := client.AuthSendCode("+1234567890", 123456, "0123456789abcdef0123456789abcdef", settings)
	if errValid != nil && strings.Contains(errValid.Error(), "value can't be nil") {
		t.Fatalf("valid CodeSettings should not produce nil marshaling error, got: %v", errValid)
	}
	t.Logf("FIX VERIFIED: With initialized CodeSettings, marshaling succeeded without nil error: %v", errValid)
}

func TestCleanPhoneNumber(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{input: "+1 234 567 8900", expected: "+12345678900"},
		{input: "+1-234-567-8900", expected: "+12345678900"},
		{input: "+1 (234) 567-8900", expected: "+12345678900"},
		{input: "  +44 7911 123456  ", expected: "+447911123456"},
		{input: "+91 98765 43210", expected: "+919876543210"},
		{input: "", expected: ""},
	}

	for _, tt := range tests {
		got := cleanPhoneNumber(tt.input)
		if got != tt.expected {
			t.Errorf("cleanPhoneNumber(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestGogramProviderStartPhoneAuthValidation(t *testing.T) {
	ctx := context.Background()

	// 1. Closed client
	pClosed := &Provider{
		apiID:   123456,
		apiHash: "abcdef",
		client:  nil,
	}
	_, err := pClosed.StartPhoneAuth(ctx, "+1234567890")
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed client error, got: %v", err)
	}

	// 2. Invalid API ID / Hash
	pInvalidConfig := &Provider{
		apiID:   0,
		apiHash: "",
		client:  &telegram.Client{},
	}
	res, err := pInvalidConfig.StartPhoneAuth(ctx, "+1234567890")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if res.Status != providers.StatusError || !strings.Contains(res.ErrorMessage, "Invalid API credentials") {
		t.Fatalf("expected Invalid API credentials error, got: %+v", res)
	}

	// 3. Empty phone number
	pEmptyPhone := &Provider{
		apiID:   123456,
		apiHash: "abcdef",
		client:  &telegram.Client{},
	}
	resPhone, err := pEmptyPhone.StartPhoneAuth(ctx, "   ")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if resPhone.Status != providers.StatusError || !strings.Contains(resPhone.ErrorMessage, "Phone number cannot be empty") {
		t.Fatalf("expected empty phone error, got: %+v", resPhone)
	}
}

func TestGogramProviderSubmitOTPValidation(t *testing.T) {
	ctx := context.Background()

	p := &Provider{
		apiID:         123456,
		apiHash:       "abcdef",
		client:        &telegram.Client{},
		phoneNumber:   "+1234567890",
		phoneCodeHash: "", // Empty hash
	}

	// Empty OTP
	resEmpty, _ := p.SubmitOTP(ctx, "  ")
	if resEmpty.Status != providers.StatusError || !strings.Contains(resEmpty.ErrorMessage, "empty") {
		t.Errorf("expected empty OTP error, got: %+v", resEmpty)
	}

	// Missing phone code hash
	resNoHash, _ := p.SubmitOTP(ctx, "12345")
	if resNoHash.Status != providers.StatusError || !strings.Contains(resNoHash.ErrorMessage, "Missing phone code hash") {
		t.Errorf("expected missing hash error, got: %+v", resNoHash)
	}
}

func TestGogramProviderCleanup(t *testing.T) {
	p := &Provider{
		apiID:         123456,
		apiHash:       "abcdef",
		phoneNumber:   "+1234567890",
		phoneCodeHash: "test_hash_123",
		qrToken:       []byte("test_token"),
		qrExpires:     100,
	}

	p.Cleanup()

	if p.phoneNumber != "" {
		t.Errorf("expected empty phoneNumber after cleanup, got %s", p.phoneNumber)
	}
	if p.phoneCodeHash != "" {
		t.Errorf("expected empty phoneCodeHash after cleanup, got %s", p.phoneCodeHash)
	}
	if p.qrToken != nil {
		t.Errorf("expected nil qrToken after cleanup, got %v", p.qrToken)
	}
	if p.qrExpires != 0 {
		t.Errorf("expected 0 qrExpires after cleanup, got %d", p.qrExpires)
	}
}

func TestGogramProviderMetadata(t *testing.T) {
	p := &Provider{}
	if p.Name() != providers.DisplayNameGogram {
		t.Errorf("expected display name %s, got %s", providers.DisplayNameGogram, p.Name())
	}
	if !p.SupportsQR() {
		t.Errorf("expected SupportsQR to be true for Gogram")
	}
}

func TestGogramStartQRAuthNoDeadlock(t *testing.T) {
	// Closed client must immediately return error without locking indefinitely
	pClosed := &Provider{
		apiID:   123456,
		apiHash: "abcdef",
		client:  nil,
	}
	ctx := context.Background()
	_, err := pClosed.StartQRAuth(ctx)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed client error, got: %v", err)
	}

	// Canceled context should abort without hanging
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	pCanceled := &Provider{
		apiID:   123456,
		apiHash: "abcdef",
		client:  &telegram.Client{},
	}
	_, errCanceled := pCanceled.StartQRAuth(canceledCtx)
	if errCanceled == nil {
		t.Fatalf("expected error on canceled context, got nil")
	}
}
