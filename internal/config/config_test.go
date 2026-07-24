package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadBuildsDatabaseURLFromFugueBinding(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres.internal")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_NAME", "sleep data")
	t.Setenv("DB_USER", "sleep user")
	t.Setenv("DB_PASSWORD", "p@ss/word")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := "postgres://sleep%20user:p%40ss%2Fword@postgres.internal:5433/sleep%20data?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestLoadPrefersExplicitDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://explicit.example/app")
	t.Setenv("DB_HOST", "postgres.internal")
	t.Setenv("DB_NAME", "baomian")
	t.Setenv("DB_USER", "baomian")
	t.Setenv("DB_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseURL != "postgres://explicit.example/app" {
		t.Fatalf("DatabaseURL = %q, want explicit DATABASE_URL", cfg.DatabaseURL)
	}
}

func TestLoadVolcengineSpeechDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/app")
	t.Setenv("VOLCENGINE_SPEECH_APP_ID", "")
	t.Setenv("VOLCENGINE_SPEECH_ACCESS_TOKEN", "")
	t.Setenv("VOLCENGINE_TTS_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VolcengineASRWSURL != "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async" {
		t.Fatal(cfg.VolcengineASRWSURL)
	}
	if cfg.VolcengineASRResourceID != "volc.bigasr.sauc.duration" {
		t.Fatal(cfg.VolcengineASRResourceID)
	}
	if cfg.VolcengineTTSWSURL != "wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream" {
		t.Fatal(cfg.VolcengineTTSWSURL)
	}
	if cfg.VolcengineTTSResourceID != "seed-tts-2.0" {
		t.Fatal(cfg.VolcengineTTSResourceID)
	}
	if cfg.VolcengineTTSSpeaker != "zh_female_gaolengyujie_uranus_bigtts" {
		t.Fatal(cfg.VolcengineTTSSpeaker)
	}
	if cfg.VolcengineASRTimeout != 20*time.Second || cfg.VoiceMaxUtteranceDuration != 60*time.Second {
		t.Fatalf("timeouts = %s %s", cfg.VolcengineASRTimeout, cfg.VoiceMaxUtteranceDuration)
	}
	if cfg.VolcengineSpeechConfigured() {
		t.Fatal("empty ASR credentials and TTS API Key must not be configured")
	}
}

func TestVolcengineSpeechConfiguredRequiresASRAndTTSKeys(t *testing.T) {
	cfg := Config{
		VolcengineSpeechAppID:       "app",
		VolcengineSpeechAccessToken: "token",
		VolcengineTTSAPIKey:         "key",
	}
	if !cfg.VolcengineSpeechConfigured() {
		t.Fatal("complete speech credentials must be configured")
	}
	cfg.VolcengineTTSAPIKey = ""
	if cfg.VolcengineSpeechConfigured() {
		t.Fatal("missing TTS API Key must not be configured")
	}
}

func TestLoadRejectsInvalidVolcengineURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/app")
	t.Setenv("VOLCENGINE_ASR_WS_URL", "https://example.com/asr")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "VOLCENGINE_ASR_WS_URL") {
		t.Fatal(err)
	}
}

func TestLoadAIProvider(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/app")

	t.Run("default anthropic", func(t *testing.T) {
		t.Setenv("AI_PROVIDER", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AIProvider != "anthropic" {
			t.Fatalf("AIProvider = %q", cfg.AIProvider)
		}
	})

	t.Run("openai compatible", func(t *testing.T) {
		t.Setenv("AI_PROVIDER", "OPENAI_COMPATIBLE")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AIProvider != "openai_compatible" {
			t.Fatalf("AIProvider = %q", cfg.AIProvider)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("AI_PROVIDER", "unknown")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "AI_PROVIDER") {
			t.Fatalf("Load() error = %v", err)
		}
	})
}
