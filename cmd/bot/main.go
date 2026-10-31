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
	"universal-session-generator/internal/auth"
	botHandlers "universal-session-generator/internal/bot/handlers"
	"universal-session-generator/internal/bot/polling"
	"universal-session-generator/internal/bot/router"
	"universal-session-generator/internal/broadcast"
	"universal-session-generator/internal/config"
	"universal-session-generator/internal/database"
	"universal-session-generator/internal/health"
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

	// 4. Log registered MTProto providers
	registeredProviders := providers.List()
	slog.Info("Registered MTProto providers", "providers", registeredProviders)

	// 5. Initialize managers and security modules
	rateLimiter := security.NewRateLimiter(20, time.Minute)
	authMgr := auth.NewManager(cfg)
	broadcastMgr := broadcast.NewManager(cfg.OwnerID, db)

	handlerCtx := &botHandlers.HandlerContext{
		AuthMgr:      authMgr,
		BroadcastMgr: broadcastMgr,
		DB:           db,
		RateLimiter:  rateLimiter,
	}

	// 6. Initialize Telegram Bot instance
	bot, err := gotgbot.NewBot(cfg.BotToken, nil)
	if err != nil {
		slog.Error("Failed to initialize Telegram bot instance", "error", err)
		os.Exit(1)
	}

	slog.Info("Telegram bot initialized successfully", "username", bot.Username, "id", bot.Id)

	// 7. Setup router and dispatcher
	dispatcher, _ := router.SetupRouter(handlerCtx)

	// 8. Setup resilient Telegram long-polling supervisor
	pollConfig := polling.DefaultConfig()
	supervisor := polling.NewSupervisor(bot, dispatcher, pollConfig)

	if err := supervisor.Start(context.Background()); err != nil {
		slog.Error("Failed to start polling supervisor", "error", err)
		os.Exit(1)
	}

	// 9. Setup real application health monitoring and HTTP server
	healthMon := health.NewMonitor(supervisor, db, authMgr)
	mux := http.NewServeMux()
	healthMon.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	go func() {
		slog.Info("HTTP server listening", "port", cfg.Port, "endpoints", []string{"/healthz", "/readyz", "/health", "/"})
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	slog.Info("Universal Telegram Session Generator running and healthy. Listening for updates...")

	// 10. Graceful shutdown handler for SIGINT and SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	slog.Info("Shutdown signal received, starting graceful termination...", "signal", sig.String())

	// Stop polling supervisor and wait for in-flight updates to conclude
	if err := supervisor.Stop(); err != nil {
		slog.Warn("Error stopping polling supervisor", "error", err)
	}

	// Graceful shutdown for HTTP server
	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer httpCancel()
	if err := httpServer.Shutdown(httpShutdownCtx); err != nil {
		slog.Warn("Error shutting down HTTP server", "error", err)
	} else {
		slog.Info("HTTP server stopped successfully")
	}

	slog.Info("Universal Telegram Session Generator terminated cleanly")
}
