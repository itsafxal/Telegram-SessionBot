package config

import (
	"os"
	"strings"
	"testing"
)

func TestConfigMissingVariables(t *testing.T) {
	// Clear environment for test
	os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error when BOT_TOKEN is missing, got nil")
	}
}

func TestConfigValid(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	os.Setenv("MONGODB_DATABASE", "test_db")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("LOG_LEVEL", "DEBUG")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.APIID != 123456 {
		t.Errorf("expected APIID 123456, got %d", cfg.APIID)
	}
	if cfg.OwnerID != 99887766 {
		t.Errorf("expected OwnerID 99887766, got %d", cfg.OwnerID)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("expected LogLevel DEBUG, got %s", cfg.LogLevel)
	}

	safe := cfg.SafeString()
	if strings.Contains(safe, "abcdef0123456789abcdef0123456789") {
		t.Errorf("SafeString leaked API_HASH: %s", safe)
	}
	if strings.Contains(safe, "ABCdefGHIjklMNOpqrSTUvwxYZ123456789") {
		t.Errorf("SafeString leaked full BOT_TOKEN: %s", safe)
	}
}
