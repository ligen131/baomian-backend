package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr                       string
	DatabaseURL                    string
	CORSAllowedOrigins             []string
	DemoUserID                     string
	DefaultDeviceID                string
	DemoContinuousConversation     bool
	AIProvider                     string
	AnthropicAPIKey                string
	AnthropicAuthToken             string
	AnthropicBaseURL               string
	AnthropicModel                 string
	AITimeout                      time.Duration
	VolcengineSpeechAppID          string
	VolcengineSpeechAccessToken    string
	VolcengineTTSAPIKey            string
	VolcengineASRWSURL             string
	VolcengineASRResourceID        string
	VolcengineTTSWSURL             string
	VolcengineTTSResourceID        string
	VolcengineTTSSpeaker           string
	VolcengineASRTimeout           time.Duration
	VolcengineASRFinalTimeout      time.Duration
	VolcengineTTSFirstFrameTimeout time.Duration
	VolcengineTTSTotalTimeout      time.Duration
	VoiceMaxUtteranceDuration      time.Duration
	VoiceOpeningText               string
	VoiceBreathingScript           string
	DemoRainAudioPath              string
	DemoBreathingAudioPath         string
	DeviceLongPollTimeout          time.Duration
	ConversationSilenceTimeout     time.Duration
	ConversationMaxDuration        time.Duration
	PhoneRemovedResumeWindow       time.Duration
	CoordinatorInterval            time.Duration
	DeviceCommandLease             time.Duration
	DeviceCommandMaxAttempts       int
	ExpoTimeScale                  float64
	LogLevel                       string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		HTTPAddr:                    env("HTTP_ADDR", ":8080"),
		DatabaseURL:                 databaseURL(),
		CORSAllowedOrigins:          splitCSV(env("CORS_ALLOWED_ORIGINS", "*")),
		DemoUserID:                  env("DEMO_USER_ID", "expo-user-001"),
		DefaultDeviceID:             env("DEFAULT_DEVICE_ID", "expo-device-001"),
		AIProvider:                  strings.ToLower(env("AI_PROVIDER", "anthropic")),
		AnthropicAPIKey:             strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		AnthropicAuthToken:          strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")),
		AnthropicBaseURL:            strings.TrimRight(env("ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/"),
		AnthropicModel:              env("ANTHROPIC_MODEL", "claude-opus-4-8"),
		VolcengineSpeechAppID:       strings.TrimSpace(os.Getenv("VOLCENGINE_SPEECH_APP_ID")),
		VolcengineSpeechAccessToken: strings.TrimSpace(os.Getenv("VOLCENGINE_SPEECH_ACCESS_TOKEN")),
		VolcengineTTSAPIKey:         strings.TrimSpace(os.Getenv("VOLCENGINE_TTS_API_KEY")),
		VolcengineASRWSURL:          env("VOLCENGINE_ASR_WS_URL", "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"),
		VolcengineASRResourceID:     env("VOLCENGINE_ASR_RESOURCE_ID", "volc.bigasr.sauc.duration"),
		VolcengineTTSWSURL:          env("VOLCENGINE_TTS_WS_URL", "wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream"),
		VolcengineTTSResourceID:     env("VOLCENGINE_TTS_RESOURCE_ID", "seed-tts-2.0"),
		VolcengineTTSSpeaker:        env("VOLCENGINE_TTS_SPEAKER", "ICL_uranus_zh_female_wenrouwenya_tob"),
		VoiceOpeningText:            env("VOICE_OPENING_TEXT", "手机已经安放好了。今晚有什么想和眠眠说的吗？"),
		VoiceBreathingScript:        env("VOICE_BREATHING_SCRIPT", "跟着眠眠，慢慢吸气四秒，再轻轻呼气六秒。"),
		DemoRainAudioPath:           strings.TrimSpace(os.Getenv("DEMO_RAIN_AUDIO_PATH")),
		DemoBreathingAudioPath:      strings.TrimSpace(os.Getenv("DEMO_BREATHING_AUDIO_PATH")),
		LogLevel:                    strings.ToLower(env("LOG_LEVEL", "info")),
	}

	var err error
	if cfg.DemoContinuousConversation, err = strconv.ParseBool(env("DEMO_CONTINUOUS_CONVERSATION", "false")); err != nil {
		return Config{}, fmt.Errorf("parse DEMO_CONTINUOUS_CONVERSATION: %w", err)
	}
	if cfg.AITimeout, err = time.ParseDuration(env("AI_TIMEOUT", "8s")); err != nil {
		return Config{}, fmt.Errorf("parse AI_TIMEOUT: %w", err)
	}
	if cfg.VolcengineASRTimeout, err = parsePositiveDuration("VOLCENGINE_ASR_TIMEOUT", "20s"); err != nil {
		return Config{}, err
	}
	if cfg.VolcengineASRFinalTimeout, err = parsePositiveDuration("VOLCENGINE_ASR_FINAL_TIMEOUT", "8s"); err != nil {
		return Config{}, err
	}
	if cfg.VolcengineTTSFirstFrameTimeout, err = parsePositiveDuration("VOLCENGINE_TTS_FIRST_FRAME_TIMEOUT", "10s"); err != nil {
		return Config{}, err
	}
	if cfg.VolcengineTTSTotalTimeout, err = parsePositiveDuration("VOLCENGINE_TTS_TOTAL_TIMEOUT", "45s"); err != nil {
		return Config{}, err
	}
	if cfg.VoiceMaxUtteranceDuration, err = parsePositiveDuration("VOICE_MAX_UTTERANCE_DURATION", "60s"); err != nil {
		return Config{}, err
	}
	if err := validateWebSocketURL("VOLCENGINE_ASR_WS_URL", cfg.VolcengineASRWSURL); err != nil {
		return Config{}, err
	}
	if err := validateWebSocketURL("VOLCENGINE_TTS_WS_URL", cfg.VolcengineTTSWSURL); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.VolcengineASRResourceID) == "" || strings.TrimSpace(cfg.VolcengineTTSResourceID) == "" || strings.TrimSpace(cfg.VolcengineTTSSpeaker) == "" {
		return Config{}, fmt.Errorf("Volcengine speech resource IDs and speaker must not be empty")
	}
	if cfg.DeviceLongPollTimeout, err = parsePositiveDuration("DEVICE_LONG_POLL_TIMEOUT", "20s"); err != nil {
		return Config{}, err
	}
	if cfg.ConversationSilenceTimeout, err = parsePositiveDuration("CONVERSATION_SILENCE_TIMEOUT", "20s"); err != nil {
		return Config{}, err
	}
	if cfg.ConversationMaxDuration, err = parsePositiveDuration("CONVERSATION_MAX_DURATION", "4m"); err != nil {
		return Config{}, err
	}
	if cfg.PhoneRemovedResumeWindow, err = parsePositiveDuration("PHONE_REMOVED_RESUME_WINDOW", "10m"); err != nil {
		return Config{}, err
	}
	if cfg.CoordinatorInterval, err = parsePositiveDuration("SESSION_COORDINATOR_INTERVAL", "1s"); err != nil {
		return Config{}, err
	}
	if cfg.DeviceCommandLease, err = parsePositiveDuration("DEVICE_COMMAND_LEASE", "30s"); err != nil {
		return Config{}, err
	}
	if cfg.DeviceCommandMaxAttempts, err = strconv.Atoi(env("DEVICE_COMMAND_MAX_ATTEMPTS", "5")); err != nil || cfg.DeviceCommandMaxAttempts <= 0 {
		return Config{}, fmt.Errorf("DEVICE_COMMAND_MAX_ATTEMPTS must be a positive integer")
	}
	if cfg.ExpoTimeScale, err = strconv.ParseFloat(env("EXPO_TIME_SCALE", "1"), 64); err != nil || cfg.ExpoTimeScale <= 0 {
		return Config{}, fmt.Errorf("EXPO_TIME_SCALE must be a positive number")
	}
	cfg.ConversationSilenceTimeout = scaleDuration(cfg.ConversationSilenceTimeout, cfg.ExpoTimeScale)
	cfg.ConversationMaxDuration = scaleDuration(cfg.ConversationMaxDuration, cfg.ExpoTimeScale)
	cfg.PhoneRemovedResumeWindow = scaleDuration(cfg.PhoneRemovedResumeWindow, cfg.ExpoTimeScale)
	cfg.DeviceCommandLease = scaleDuration(cfg.DeviceCommandLease, cfg.ExpoTimeScale)
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.AIProvider != "anthropic" && cfg.AIProvider != "openai_compatible" {
		return Config{}, fmt.Errorf("AI_PROVIDER must be anthropic or openai_compatible")
	}
	return cfg, nil
}

func (c Config) VolcengineTTSConfigured() bool {
	return strings.TrimSpace(c.VolcengineTTSAPIKey) != ""
}

func (c Config) VolcengineSpeechConfigured() bool {
	return strings.TrimSpace(c.VolcengineSpeechAppID) != "" &&
		strings.TrimSpace(c.VolcengineSpeechAccessToken) != "" &&
		strings.TrimSpace(c.VolcengineTTSAPIKey) != ""
}

func validateWebSocketURL(key, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return fmt.Errorf("%s must be a ws or wss URL", key)
	}
	return nil
}

func parsePositiveDuration(key, fallback string) (time.Duration, error) {
	value, err := time.ParseDuration(env(key, fallback))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func scaleDuration(value time.Duration, scale float64) time.Duration {
	scaled := time.Duration(float64(value) / scale)
	if scaled < time.Millisecond {
		return time.Millisecond
	}
	return scaled
}

func databaseURL() string {
	if value := strings.TrimSpace(os.Getenv("DATABASE_URL")); value != "" {
		return value
	}
	if host := strings.TrimSpace(os.Getenv("DB_HOST")); host != "" {
		port := env("DB_PORT", "5432")
		dsn := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(env("DB_USER", "postgres"), os.Getenv("DB_PASSWORD")),
			Host:   net.JoinHostPort(host, port),
			Path:   env("DB_NAME", "postgres"),
		}
		dsn.RawQuery = "sslmode=disable"
		return dsn.String()
	}
	return "postgres://baomian:baomian@localhost:5432/baomian?sslmode=disable"
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
