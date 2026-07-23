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
	HTTPAddr              string
	DatabaseURL           string
	CORSAllowedOrigins    []string
	DemoUserID            string
	DefaultDeviceID       string
	AnthropicAPIKey       string
	AnthropicAuthToken    string
	AnthropicBaseURL      string
	AnthropicModel        string
	AITimeout             time.Duration
	DeviceLongPollTimeout time.Duration
	ExpoTimeScale         float64
	LogLevel              string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DatabaseURL:        databaseURL(),
		CORSAllowedOrigins: splitCSV(env("CORS_ALLOWED_ORIGINS", "*")),
		DemoUserID:         env("DEMO_USER_ID", "expo-user-001"),
		DefaultDeviceID:    env("DEFAULT_DEVICE_ID", "expo-device-001"),
		AnthropicAPIKey:    strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		AnthropicAuthToken: strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")),
		AnthropicBaseURL:   strings.TrimRight(env("ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/"),
		AnthropicModel:     env("ANTHROPIC_MODEL", "claude-opus-4-8"),
		LogLevel:           strings.ToLower(env("LOG_LEVEL", "info")),
	}

	var err error
	if cfg.AITimeout, err = time.ParseDuration(env("AI_TIMEOUT", "8s")); err != nil {
		return Config{}, fmt.Errorf("parse AI_TIMEOUT: %w", err)
	}
	if cfg.DeviceLongPollTimeout, err = time.ParseDuration(env("DEVICE_LONG_POLL_TIMEOUT", "20s")); err != nil {
		return Config{}, fmt.Errorf("parse DEVICE_LONG_POLL_TIMEOUT: %w", err)
	}
	if cfg.ExpoTimeScale, err = strconv.ParseFloat(env("EXPO_TIME_SCALE", "1"), 64); err != nil || cfg.ExpoTimeScale <= 0 {
		return Config{}, fmt.Errorf("EXPO_TIME_SCALE must be a positive number")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
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
