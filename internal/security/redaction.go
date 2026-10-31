package security

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

var (
	// Regex patterns for detecting potential sensitive credentials in logs
	sessionPattern  = regexp.MustCompile(`(1[A-Za-z0-9+/=_-]{50,})`)
	phonePattern    = regexp.MustCompile(`(\+?[0-9]{1,4})[0-9]{5,10}([0-9]{2})`)
	botTokenPattern = regexp.MustCompile(`([0-9]{8,11}:[A-Za-z0-9_-]{30,})`)
)

// RedactPhone masks the middle digits of a phone number.
func RedactPhone(phone string) string {
	cleaned := strings.TrimSpace(phone)
	if len(cleaned) <= 5 {
		return "[REDACTED_PHONE]"
	}
	prefix := cleaned[:4]
	suffix := cleaned[len(cleaned)-2:]
	return prefix + "****" + suffix
}

// RedactCode masks verification codes.
func RedactCode(code string) string {
	if code == "" {
		return ""
	}
	return "[REDACTED_CODE]"
}

// RedactPassword masks passwords.
func RedactPassword(password string) string {
	if password == "" {
		return ""
	}
	return "[REDACTED_PASSWORD]"
}

// RedactSession masks session strings.
func RedactSession(session string) string {
	if session == "" {
		return ""
	}
	if len(session) < 10 {
		return "[REDACTED_SESSION]"
	}
	return session[:4] + "...[REDACTED]..." + session[len(session)-4:]
}

// SanitizeLogText sanitizes any log message string.
func SanitizeLogText(text string) string {
	res := botTokenPattern.ReplaceAllString(text, "[REDACTED_BOT_TOKEN]")
	res = sessionPattern.ReplaceAllString(res, "[REDACTED_SESSION_STRING]")
	return res
}

// RedactingHandler is a slog.Handler wrapper that redacts sensitive values from log attributes.
type RedactingHandler struct {
	inner slog.Handler
}

// NewRedactingHandler wraps an existing slog.Handler with sensitive data redaction.
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Sanitize message
	r.Message = SanitizeLogText(r.Message)

	// Build new sanitized record with filtered attributes
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(sanitizeAttr(a))
		return true
	})

	return h.inner.Handle(ctx, newRecord)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = sanitizeAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(sanitized)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}

// sanitizeAttr scrubs known sensitive keys or values.
func sanitizeAttr(a slog.Attr) slog.Attr {
	keyLower := strings.ToLower(a.Key)
	switch {
	case strings.Contains(keyLower, "password"),
		strings.Contains(keyLower, "pwd"),
		strings.Contains(keyLower, "secret"),
		strings.Contains(keyLower, "2fa"):
		return slog.String(a.Key, "[REDACTED_PASSWORD]")

	case strings.Contains(keyLower, "code"),
		strings.Contains(keyLower, "otp"):
		return slog.String(a.Key, "[REDACTED_CODE]")

	case strings.Contains(keyLower, "session"),
		strings.Contains(keyLower, "string_session"):
		return slog.String(a.Key, "[REDACTED_SESSION]")

	case strings.Contains(keyLower, "phone"):
		if a.Value.Kind() == slog.KindString {
			return slog.String(a.Key, RedactPhone(a.Value.String()))
		}
		return slog.String(a.Key, "[REDACTED_PHONE]")

	case strings.Contains(keyLower, "token"),
		strings.Contains(keyLower, "hash"),
		strings.Contains(keyLower, "auth_key"):
		return slog.String(a.Key, "[REDACTED_TOKEN]")

	default:
		if a.Value.Kind() == slog.KindString {
			return slog.String(a.Key, SanitizeLogText(a.Value.String()))
		}
		return a
	}
}
