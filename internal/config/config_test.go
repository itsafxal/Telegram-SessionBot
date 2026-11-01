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
	os.Setenv("PORT", "9000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.Port != "9000" {
		t.Errorf("expected Port 9000, got %s", cfg.Port)
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

func TestConfigInsecureMongoURI(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("MONGODB_URI", "mongodb://admin:session_secure_pwd_replace_me@localhost:27017")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "insecure placeholder") {
		t.Fatalf("expected error detecting placeholder password in MONGODB_URI, got: %v", err)
	}
}

func TestConfigInvalidMongoURIScheme(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("MONGODB_URI", "http://localhost:27017")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "invalid MONGODB_URI scheme") {
		t.Fatalf("expected scheme validation error, got: %v", err)
	}
}

func TestConfigEmptyPasswordMongoURI(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("MONGODB_URI", "mongodb://app_user:@localhost:27017/telegram_session_bot")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "empty password is prohibited") {
		t.Fatalf("expected empty password error, got: %v", err)
	}
}

func TestConfigRootUserUsageRejected(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("MONGO_ROOT_USERNAME", "cluster_admin")
	os.Setenv("MONGODB_URI", "mongodb://cluster_admin:StrongRootPass123@localhost:27017/telegram_session_bot")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "cannot use MongoDB root/admin credentials") {
		t.Fatalf("expected root user rejection error, got: %v", err)
	}
}

func TestConfigLeastPrivilegeAppUserSuccess(t *testing.T) {
	os.Setenv("BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ123456789")
	os.Setenv("API_ID", "123456")
	os.Setenv("API_HASH", "abcdef0123456789abcdef0123456789")
	os.Setenv("OWNER_ID", "99887766")
	os.Setenv("MONGODB_DATABASE", "telegram_session_bot")
	os.Setenv("MONGO_ROOT_USERNAME", "cluster_admin")
	os.Setenv("MONGODB_URI", "mongodb://session_app_user:UniqueAppSecurePass456@localhost:27017/telegram_session_bot?authSource=telegram_session_bot")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected least-privilege app config to succeed, got: %v", err)
	}
	if cfg.MongoDBDatabase != "telegram_session_bot" {
		t.Errorf("expected DB telegram_session_bot, got %s", cfg.MongoDBDatabase)
	}
}
