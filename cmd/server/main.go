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
	"github.com/baomian/baomian-backend/internal/audio"
	"github.com/baomian/baomian-backend/internal/config"
	"github.com/baomian/baomian-backend/internal/controller"
	"github.com/baomian/baomian-backend/internal/coordinator"
	"github.com/baomian/baomian-backend/internal/metrics"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/platform/database"
	"github.com/baomian/baomian-backend/internal/realtime"
	postgresrepo "github.com/baomian/baomian-backend/internal/repository/postgres"
	"github.com/baomian/baomian-backend/internal/router"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/gorilla/websocket"
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
	registry := metrics.New()

	fallback := ai.NewFallbackAdapter()
	primaryTimeout := cfg.AITimeout - 500*time.Millisecond
	if primaryTimeout <= 0 {
		primaryTimeout = cfg.AITimeout
	}
	primary := newAIAdapter(cfg, &http.Client{Timeout: primaryTimeout})
	adapter := ai.NewSafetyAdapter(ai.NewResilientAdapter(primary, fallback, primaryTimeout, logger))

	profileService := service.NewProfileService(store)
	tonightService := service.NewTonightService(
		store, hub, cfg.DefaultDeviceID,
		cfg.ConversationSilenceTimeout, cfg.ConversationMaxDuration, cfg.PhoneRemovedResumeWindow,
	)
	journalService := service.NewJournalService(store, hub)
	conversationService := service.NewConversationService(
		store, adapter, hub,
		cfg.ConversationSilenceTimeout, cfg.ConversationMaxDuration, cfg.AITimeout+time.Second, logger,
	)
	conversationService.ConfigureDemoContinuousConversation(
		cfg.DemoContinuousConversation, cfg.DemoUserID, cfg.DefaultDeviceID,
	)
	deviceService := service.NewDeviceService(
		store, hub, cfg.DemoUserID, cfg.DeviceCommandLease, cfg.DeviceCommandMaxAttempts,
		cfg.ConversationSilenceTimeout, cfg.ConversationMaxDuration, cfg.PhoneRemovedResumeWindow, logger,
	)
	deviceService.ConfigureDemoContinuousConversation(
		cfg.DemoContinuousConversation, cfg.DemoUserID, cfg.DefaultDeviceID,
	)
	speechConfig := speech.Config{
		AppID: cfg.VolcengineSpeechAppID, AccessToken: cfg.VolcengineSpeechAccessToken,
		TTSAPIKey: cfg.VolcengineTTSAPIKey,
		ASRURL:    cfg.VolcengineASRWSURL, ASRResourceID: cfg.VolcengineASRResourceID,
		TTSURL: cfg.VolcengineTTSWSURL, TTSResourceID: cfg.VolcengineTTSResourceID,
		TTSSpeaker: cfg.VolcengineTTSSpeaker, ASRTimeout: cfg.VolcengineASRTimeout,
		ASRFinalTimeout: cfg.VolcengineASRFinalTimeout, TTSFirstFrameTimeout: cfg.VolcengineTTSFirstFrameTimeout,
		TTSTotalTimeout: cfg.VolcengineTTSTotalTimeout,
	}
	asrClient := speech.NewVolcASRClient(speechConfig, websocket.DefaultDialer)
	ttsClient := speech.NewRetryingTTSClient(speech.NewVolcTTSClient(speechConfig, websocket.DefaultDialer), 2)
	voiceSessionService := service.NewVoiceSessionService(
		conversationService, tonightService, asrClient, ttsClient,
		cfg.VoiceOpeningText, cfg.VoiceBreathingScript, cfg.VoiceMaxUtteranceDuration, logger,
	)
	voiceSessionService.ConfigureSleepAudio(audio.NewSleepService(
		cfg.DemoRainAudioPath,
		cfg.DemoBreathingAudioPath,
	))

	profileController := controller.NewProfileController(profileService)
	tonightController := controller.NewTonightController(tonightService)
	journalController := controller.NewJournalController(journalService)
	conversationController := controller.NewConversationController(conversationService)
	deviceController := controller.NewDeviceController(deviceService, cfg.DeviceLongPollTimeout)
	deviceVoiceController := controller.NewDeviceVoiceController(voiceSessionService, cfg.VolcengineSpeechConfigured(), cfg.DemoUserID, logger)
	ttsController := controller.NewTTSController(ttsClient, cfg.VolcengineTTSConfigured())
	webSocketController := controller.NewWebSocketController(hub, registry, cfg.DemoUserID)

	coordinatorContext, stopCoordinator := context.WithCancel(context.Background())
	defer stopCoordinator()
	sessionCoordinator := coordinator.New(store, hub, cfg.DefaultDeviceID, cfg.CoordinatorInterval, logger)
	go sessionCoordinator.Run(coordinatorContext)

	engine := router.New(router.Dependencies{
		DB: db, DefaultUserID: cfg.DemoUserID, CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		ProfileController: profileController, TonightController: tonightController,
		ConversationController: conversationController, JournalController: journalController,
		DeviceController: deviceController, DeviceVoiceController: deviceVoiceController,
		TTSController: ttsController, WebSocketController: webSocketController, Metrics: registry,
	}, middleware.AccessLog(logger))

	server := &http.Server{Addr: cfg.HTTPAddr, Handler: engine, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr, "aiProvider", cfg.AIProvider, "aiModel", cfg.AnthropicModel, "aiConfigured", cfg.AnthropicAPIKey != "" || cfg.AnthropicAuthToken != "", "speechProvider", "volcengine", "speechConfigured", cfg.VolcengineSpeechConfigured(), "voiceCodec", "pcm_s16le", "voiceSampleRate", 24000)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopCoordinator()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	logger.Info("server stopped")
}

func newAIAdapter(cfg config.Config, httpClient *http.Client) ai.Adapter {
	if cfg.AIProvider == "openai_compatible" {
		return ai.NewOpenAICompatibleAdapter(cfg.AnthropicAPIKey, cfg.AnthropicAuthToken, cfg.AnthropicBaseURL, cfg.AnthropicModel, httpClient)
	}
	return ai.NewAnthropicAdapter(cfg.AnthropicAPIKey, cfg.AnthropicAuthToken, cfg.AnthropicBaseURL, cfg.AnthropicModel, httpClient)
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
