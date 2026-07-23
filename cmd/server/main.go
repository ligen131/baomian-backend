package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/baomian/baomian-backend/internal/ai"
	"github.com/baomian/baomian-backend/internal/config"
	"github.com/baomian/baomian-backend/internal/controller"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/platform/database"
	"github.com/baomian/baomian-backend/internal/realtime"
	postgresrepo "github.com/baomian/baomian-backend/internal/repository/postgres"
	"github.com/baomian/baomian-backend/internal/router"
	"github.com/baomian/baomian-backend/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := newLogger(cfg.LogLevel)
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	store := postgresrepo.NewStore(db)
	hub := realtime.NewHub()

	fallback := ai.NewFallbackAdapter()
	primaryTimeout := cfg.AITimeout - 500*time.Millisecond
	if primaryTimeout <= 0 {
		primaryTimeout = cfg.AITimeout
	}
	anthropic := ai.NewAnthropicAdapter(cfg.AnthropicAPIKey, cfg.AnthropicAuthToken, cfg.AnthropicBaseURL, cfg.AnthropicModel, &http.Client{Timeout: primaryTimeout})
	adapter := ai.NewSafetyAdapter(ai.NewResilientAdapter(anthropic, fallback, primaryTimeout, logger))

	profileService := service.NewProfileService(store)
	tonightService := service.NewTonightService(store, hub, cfg.DefaultDeviceID)
	journalService := service.NewJournalService(store)
	conversationService := service.NewConversationService(store, adapter, hub)
	deviceService := service.NewDeviceService(store, hub, cfg.DemoUserID)

	profileController := controller.NewProfileController(profileService)
	tonightController := controller.NewTonightController(tonightService)
	journalController := controller.NewJournalController(journalService)
	conversationController := controller.NewConversationController(conversationService)
	deviceController := controller.NewDeviceController(deviceService, cfg.DeviceLongPollTimeout)
	webSocketController := controller.NewWebSocketController(hub, cfg.DemoUserID)

	engine := router.New(router.Dependencies{
		DB: db, DefaultUserID: cfg.DemoUserID, CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		ProfileController: profileController, TonightController: tonightController,
		ConversationController: conversationController, JournalController: journalController,
		DeviceController: deviceController, WebSocketController: webSocketController,
	}, middleware.AccessLog(logger))

	server := &http.Server{Addr: cfg.HTTPAddr, Handler: engine, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr, "aiModel", cfg.AnthropicModel, "aiConfigured", cfg.AnthropicAPIKey != "" || cfg.AnthropicAuthToken != "")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	logger.Info("server stopped")
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}
