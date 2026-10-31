package providers

import (
	"context"
	"testing"
	"time"
)

type dummyProvider struct {
	name string
}

func (d *dummyProvider) Name() string     { return d.name }
func (d *dummyProvider) SupportsQR() bool { return false }
func (d *dummyProvider) StartPhoneAuth(ctx context.Context, p string) (*AuthResult, error) {
	return nil, nil
}
func (d *dummyProvider) StartQRAuth(ctx context.Context) (*AuthResult, error)    { return nil, nil }
func (d *dummyProvider) WaitForQRLogin(ctx context.Context) (*AuthResult, error) { return nil, nil }
func (d *dummyProvider) SubmitOTP(ctx context.Context, code string) (*AuthResult, error) {
	return nil, nil
}
func (d *dummyProvider) SubmitPassword(ctx context.Context, pwd string) (*AuthResult, error) {
	return nil, nil
}
func (d *dummyProvider) ExportSession(ctx context.Context) (string, error) { return "test_sess", nil }
func (d *dummyProvider) ValidateSession(ctx context.Context, s string) (*AccountInfo, error) {
	return nil, nil
}
func (d *dummyProvider) Cancel(ctx context.Context) error { return nil }
func (d *dummyProvider) Cleanup()                         {}

func TestRegistry(t *testing.T) {
	reg := &Registry{factories: make(map[string]Factory)}

	reg.Register("custom_mock", func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (Provider, error) {
		return &dummyProvider{name: "CustomMock"}, nil
	})

	p, err := reg.Get(context.Background(), "custom_mock", 1234, "hash", "python", time.Second)
	if err != nil {
		t.Fatalf("unexpected error getting provider: %v", err)
	}

	if p.Name() != "CustomMock" {
		t.Errorf("expected provider name CustomMock, got %s", p.Name())
	}

	// Case-insensitive / normalized lookup
	p2, err := reg.Get(context.Background(), "CUSTOM_MOCK", 1234, "hash", "python", time.Second)
	if err != nil {
		t.Fatalf("unexpected error on case-insensitive lookup: %v", err)
	}
	if p2 == nil {
		t.Errorf("expected provider instance, got nil")
	}

	// Missing provider should fail
	_, err = reg.Get(context.Background(), "non_existent", 1234, "hash", "python", time.Second)
	if err == nil {
		t.Errorf("expected error for non-existent provider, got nil")
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"🔥 Pyrogram", "pyrofork"},
		{"Pyrogram", "pyrofork"},
		{"🔥 Pyrofork", "pyrofork"},
		{"Pyrofork", "pyrofork"},
		{"⚡ Telethon", "telethon"},
		{"Telethon", "telethon"},
		{"🔷 Gogram", "gogram"},
		{"Gogram", "gogram"},
	}

	for _, c := range cases {
		out := NormalizeName(c.input)
		if out != c.expected {
			t.Errorf("NormalizeName(%q) = %q; expected %q", c.input, out, c.expected)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"pyrofork", "Pyrogram"},
		{"pyrogram", "Pyrogram"},
		{"🔥 Pyrogram", "Pyrogram"},
		{"🔥 Pyrofork", "Pyrogram"},
		{"telethon", "Telethon"},
		{"⚡ Telethon", "Telethon"},
		{"gogram", "Gogram"},
		{"🔷 Gogram", "Gogram"},
	}

	for _, c := range cases {
		out := DisplayName(c.input)
		if out != c.expected {
			t.Errorf("DisplayName(%q) = %q; expected %q", c.input, out, c.expected)
		}
	}
}
