package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"universal-session-generator/internal/auth"
	botHandlers "universal-session-generator/internal/bot/handlers"
	"universal-session-generator/internal/bot/router"
	"universal-session-generator/internal/broadcast"
	"universal-session-generator/internal/config"
	"universal-session-generator/internal/database"
	"universal-session-generator/internal/logger"
	"universal-session-generator/internal/providers"
	"universal-session-generator/internal/security"

	// Self-registering MTProto providers
	_ "universal-session-generator/internal/providers/gogram"
	_ "universal-session-generator/internal/providers/pyrofork"
	_ "universal-session-generator/internal/providers/telethon"
)

func main() {
	// 1. Load application configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Configuration error: %v\n", err)
		os.Exit(1)
	}

	// 2. Initialize structured logger with credential redaction
	_ = logger.InitLogger(cfg.LogLevel)

	slog.Info("Starting Universal Telegram MTProto Session Generator",
		"config", cfg.SafeString(),
	)

	// 3. Connect to MongoDB (strictly for numeric user_id storage)
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	db, err := database.NewDatabase(initCtx, cfg.MongoDBURI, cfg.MongoDBDatabase)
	if err != nil {
		slog.Error("Fatal database connection failure", "error", err)
		os.Exit(1)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = db.Close(closeCtx)
	}()

	// 4. Start HTTP health check server (required for Render Web Service compatibility)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"Universal Telegram MTProto Session Generator"}`))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	go func() {
		slog.Info("Health check HTTP server listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP health check server error", "error", err)
		}
	}()

	// 5. Log registered MTProto providers
	registeredProviders := providers.List()
	slog.Info("Registered MTProto providers", "providers", registeredProviders)

	// 6. Initialize managers and security modules
	rateLimiter := security.NewRateLimiter(20, time.Minute)
	authMgr := auth.NewManager(cfg)
	broadcastMgr := broadcast.NewManager(cfg.OwnerID, db)

	handlerCtx := &botHandlers.HandlerContext{
		AuthMgr:      authMgr,
		BroadcastMgr: broadcastMgr,
		DB:           db,
		RateLimiter:  rateLimiter,
	}

	// 7. Initialize Telegram Bot
	bot, err := gotgbot.NewBot(cfg.BotToken, nil)
	if err != nil {
		slog.Error("Failed to initialize Telegram bot instance", "error", err)
		os.Exit(1)
	}

	slog.Info("Telegram bot initialized successfully", "username", bot.Username, "id", bot.Id)

	// Explicitly clear existing webhook and pending updates to prevent duplicate polling conflicts
	delWebhookOpts := &gotgbot.DeleteWebhookOpts{DropPendingUpdates: true}
	if _, delErr := bot.DeleteWebhook(delWebhookOpts); delErr != nil {
		slog.Warn("Failed to explicitly delete webhook before polling (proceeding anyway)", "error", delErr)
	}

	// 8. Setup router and dispatcher
	_, updater := router.SetupRouter(handlerCtx)

	// Custom error handler for polling issues to gracefully log warnings without fatal crashing
	updater.UnhandledErrFunc = func(err error) {
		slog.Warn("Telegram updater warning", "error", err)
	}

	// 9. Start long-polling for updates
	pollOpts := &ext.PollingOpts{
		DropPendingUpdates:    true,
		EnableWebhookDeletion: true,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			Timeout: 30,
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 35 * time.Second,
			},
		},
	}

	err = updater.StartPolling(bot, pollOpts)
	if err != nil {
		slog.Error("Failed to start long polling", "error", err)
		os.Exit(1)
	}

	slog.Info("Bot long-polling started. Listening for updates...")

	// 10. Graceful shutdown handler for SIGINT and SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	slog.Info("Shutdown signal received, starting graceful termination...", "signal", sig.String())

	// Stop polling updates
	stopErr := updater.Stop()
	if stopErr != nil {
		slog.Warn("Error while stopping updater", "error", stopErr)
	} else {
		slog.Info("Updater stopped successfully")
	}

	// Graceful shutdown for HTTP health server
	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer httpCancel()
	if err := httpServer.Shutdown(httpShutdownCtx); err != nil {
		slog.Warn("Error shutting down HTTP server", "error", err)
	} else {
		slog.Info("HTTP server stopped successfully")
	}

	slog.Info("Antigravity Session Generator terminated cleanly")
}
