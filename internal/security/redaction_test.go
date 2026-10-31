package security

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactPhone(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"+1234567890", "+123****90"},
		{"+447911123456", "+447****56"},
		{"123", "[REDACTED_PHONE]"},
	}

	for _, c := range cases {
		out := RedactPhone(c.input)
		if out != c.expected {
			t.Errorf("RedactPhone(%q) = %q; expected %q", c.input, out, c.expected)
		}
	}
}

func TestRedactCode(t *testing.T) {
	if RedactCode("12345") != "[REDACTED_CODE]" {
		t.Errorf("expected [REDACTED_CODE]")
	}
	if RedactCode("") != "" {
		t.Errorf("expected empty string")
	}
}

func TestRedactPassword(t *testing.T) {
	if RedactPassword("supersecret2FA") != "[REDACTED_PASSWORD]" {
		t.Errorf("expected [REDACTED_PASSWORD]")
	}
	if RedactPassword("") != "" {
		t.Errorf("expected empty string")
	}
}

func TestRedactSession(t *testing.T) {
	longSession := "1BQANMT0AUpX4Z..." + strings.Repeat("A", 60) + "XYZ1"
	redacted := RedactSession(longSession)
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Errorf("expected session to be masked, got: %s", redacted)
	}
}

func TestRedactingLogger(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRedactingHandler(baseHandler)
	testLogger := slog.New(handler)

	testLogger.InfoContext(context.Background(), "Processing user auth",
		slog.String("phone", "+1234567890"),
		slog.String("password", "MySecretPassword123"),
		slog.String("code", "98765"),
		slog.String("session", "1BQANMT0AUpX4ZAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="),
	)

	logOutput := buf.String()

	if strings.Contains(logOutput, "MySecretPassword123") {
		t.Errorf("log contains plain password: %s", logOutput)
	}
	if strings.Contains(logOutput, "98765") {
		t.Errorf("log contains plain OTP code: %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED_PASSWORD]") {
		t.Errorf("log missing [REDACTED_PASSWORD]: %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED_CODE]") {
		t.Errorf("log missing [REDACTED_CODE]: %s", logOutput)
	}
}
