package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/gin-gonic/gin"
)

type fakeStreamingTTS struct {
	chunks [][]byte
	err    error
}

func (f fakeStreamingTTS) Stream(_ context.Context, _ string, onPCM func([]byte) error) error {
	for _, chunk := range f.chunks {
		if err := onPCM(chunk); err != nil {
			return err
		}
	}
	return f.err
}

func TestTTSControllerStreamsPCMWithAudioHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := NewTTSController(fakeStreamingTTS{chunks: [][]byte{{1, 2}, {3, 4}}}, true)
	engine := gin.New()
	engine.POST("/tts/stream", controller.Stream)
	request := httptest.NewRequest(http.MethodPost, "/tts/stream", strings.NewReader(`{"text":"晚安"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "audio/pcm;codec=pcm_s16le;rate=24000;channels=1" {
		t.Fatalf("Content-Type = %q", got)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if got := response.Body.Bytes(); string(got) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("body = %v", got)
	}
}

func TestTTSControllerValidatesTextAndConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		configured bool
		body       string
		status     int
		code       string
	}{
		{name: "empty", configured: true, body: `{"text":"  "}`, status: http.StatusBadRequest, code: "validation_error"},
		{name: "too long", configured: true, body: `{"text":"` + strings.Repeat("眠", 501) + `"}`, status: http.StatusBadRequest, code: "validation_error"},
		{name: "not configured", body: `{"text":"晚安"}`, status: http.StatusServiceUnavailable, code: voice.ErrorSpeechNotConfigured},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.POST("/tts/stream", NewTTSController(fakeStreamingTTS{}, test.configured).Stream)
			request := httptest.NewRequest(http.MethodPost, "/tts/stream", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTTSControllerReturnsJSONWhenUpstreamFailsBeforeAudio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/tts/stream", NewTTSController(fakeStreamingTTS{err: errors.New("upstream")}, true).Stream)
	request := httptest.NewRequest(http.MethodPost, "/tts/stream", strings.NewReader(`{"text":"晚安"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "tts_unavailable") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
