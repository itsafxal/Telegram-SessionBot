package logger

import (
	"log/slog"
	"os"
	"strings"

	"universal-session-generator/internal/security"
)

// InitLogger initializes and sets the default global slog logger.
func InitLogger(levelStr string) *slog.Logger {
	var level slog.Level
	switch strings.ToUpper(strings.TrimSpace(levelStr)) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	}

	baseHandler := slog.NewTextHandler(os.Stdout, opts)
	redactingHandler := security.NewRedactingHandler(baseHandler)
	l := slog.New(redactingHandler)

	slog.SetDefault(l)
	return l
}
