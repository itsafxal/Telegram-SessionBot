package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration options for the application.
type Config struct {
	BotToken              string
	APIID                 int32
	APIHash               string
	MongoDBURI            string
	MongoDBDatabase       string
	OwnerID               int64
	PythonPath            string
	Port                  string
	WorkerTimeout         time.Duration
	MaxConcurrentAuth     int
	SlotAcquireTimeout    time.Duration
	PhoneMaxAttempts      int
	LogLevel              string
	SessionExpiryDuration time.Duration
}

// Load reads configuration from environment variables, optionally loading a .env file.
func Load() (*Config, error) {
	// Try loading .env file if it exists, but don't fail if it's missing (e.g., in production/Render)
	_ = godotenv.Load()

	botToken := strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if botToken == "" {
		return nil, errors.New("missing required environment variable: BOT_TOKEN")
	}

	apiIDStr := strings.TrimSpace(os.Getenv("API_ID"))
	if apiIDStr == "" {
		return nil, errors.New("missing required environment variable: API_ID")
	}
	apiIDInt, err := strconv.ParseInt(apiIDStr, 10, 32)
	if err != nil || apiIDInt <= 0 {
		return nil, fmt.Errorf("invalid API_ID '%s': must be a positive integer", apiIDStr)
	}

	apiHash := strings.TrimSpace(os.Getenv("API_HASH"))
	if apiHash == "" {
		return nil, errors.New("missing required environment variable: API_HASH")
	}

	mongoURI := strings.TrimSpace(os.Getenv("MONGODB_URI"))
	if mongoURI == "" {
		return nil, errors.New("missing required environment variable: MONGODB_URI")
	}

	insecurePlaceholders := []string{
		"session_secure_pwd_replace_me",
		"change_me",
		":password@",
	}
	for _, placeholder := range insecurePlaceholders {
		if strings.Contains(strings.ToLower(mongoURI), placeholder) {
			return nil, fmt.Errorf("insecure placeholder credential '%s' detected in MONGODB_URI; please provide a secure unique production password", placeholder)
		}
	}

	mongoDB := strings.TrimSpace(os.Getenv("MONGODB_DATABASE"))
	if mongoDB == "" {
		mongoDB = "telegram_session_bot"
	}

	ownerIDStr := strings.TrimSpace(os.Getenv("OWNER_ID"))
	if ownerIDStr == "" {
		return nil, errors.New("missing required environment variable: OWNER_ID")
	}
	ownerID, err := strconv.ParseInt(ownerIDStr, 10, 64)
	if err != nil || ownerID == 0 {
		return nil, fmt.Errorf("invalid OWNER_ID '%s': must be a valid Telegram numeric ID", ownerIDStr)
	}

	pythonPath := strings.TrimSpace(os.Getenv("PYTHON_PATH"))
	if pythonPath == "" {
		pythonPath = detectPythonPath()
	}

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	workerTimeoutSec := 300
	if secStr := strings.TrimSpace(os.Getenv("WORKER_TIMEOUT_SECONDS")); secStr != "" {
		if sec, err := strconv.Atoi(secStr); err == nil && sec > 0 {
			workerTimeoutSec = sec
		}
	}

	maxAuth := 50
	if maxStr := strings.TrimSpace(os.Getenv("MAX_CONCURRENT_AUTH")); maxStr != "" {
		if m, err := strconv.Atoi(maxStr); err == nil && m > 0 {
			maxAuth = m
		}
	}

	slotTimeoutSec := 5
	if slotStr := strings.TrimSpace(os.Getenv("SLOT_ACQUIRE_TIMEOUT_SECONDS")); slotStr != "" {
		if s, err := strconv.Atoi(slotStr); err == nil && s > 0 {
			slotTimeoutSec = s
		}
	}

	phoneMaxAttempts := 5
	if pStr := strings.TrimSpace(os.Getenv("PHONE_MAX_ATTEMPTS")); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			phoneMaxAttempts = p
		}
	}

	logLevel := strings.ToUpper(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	if logLevel == "" {
		logLevel = "INFO"
	}

	return &Config{
		BotToken:              botToken,
		APIID:                 int32(apiIDInt),
		APIHash:               apiHash,
		MongoDBURI:            mongoURI,
		MongoDBDatabase:       mongoDB,
		OwnerID:               ownerID,
		PythonPath:            pythonPath,
		Port:                  port,
		WorkerTimeout:         time.Duration(workerTimeoutSec) * time.Second,
		MaxConcurrentAuth:     maxAuth,
		SlotAcquireTimeout:    time.Duration(slotTimeoutSec) * time.Second,
		PhoneMaxAttempts:      phoneMaxAttempts,
		LogLevel:              logLevel,
		SessionExpiryDuration: 10 * time.Minute,
	}, nil
}

// detectPythonPath looks for python3 or python in PATH.
func detectPythonPath() string {
	if _, err := os.Stat("/usr/bin/python3"); err == nil {
		return "/usr/bin/python3"
	}
	if p := os.Getenv("PYTHON"); p != "" {
		return p
	}
	return "python"
}

// SafeString prints the config without revealing secrets.
func (c *Config) SafeString() string {
	maskedToken := "REDACTED"
	if len(c.BotToken) > 8 {
		maskedToken = c.BotToken[:4] + "..." + c.BotToken[len(c.BotToken)-4:]
	}
	return fmt.Sprintf("Config{BotToken: %s, APIID: %d, MongoDBDatabase: %s, OwnerID: %d, PythonPath: %s, Port: %s, WorkerTimeout: %v, MaxConcurrentAuth: %d, LogLevel: %s}",
		maskedToken, c.APIID, c.MongoDBDatabase, c.OwnerID, c.PythonPath, c.Port, c.WorkerTimeout, c.MaxConcurrentAuth, c.LogLevel)
}
