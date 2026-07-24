package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baomian/baomian-backend/internal/service"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestDeviceVoiceControllerRejectsMissingConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/voice", NewDeviceVoiceController(fakeVoiceSessionFactory{}, false, "user").Connect)
	request := httptest.NewRequest(http.MethodGet, "/voice?deviceId=device-1", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), voice.ErrorSpeechNotConfigured) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestDeviceVoiceControllerRequiresDeviceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/voice", NewDeviceVoiceController(fakeVoiceSessionFactory{}, true, "user").Connect)
	request := httptest.NewRequest(http.MethodGet, "/voice", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestDeviceVoiceControllerWebSocketSmoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/voice", NewDeviceVoiceController(fakeVoiceSessionFactory{}, true, "user-1").Connect)
	server := httptest.NewServer(engine)
	defer server.Close()

	connection, _, err := websocket.DefaultDialer.Dial(websocketTestURLForController(server.URL)+"/voice?deviceId=device-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	var ready voice.ServerEvent
	if err := connection.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != voice.EventSessionReady || ready.CompletedTurns != 1 {
		t.Fatalf("ready = %#v", ready)
	}
	if err := connection.WriteJSON(voice.ClientEvent{Type: voice.EventSessionStart, EventID: "start-1"}); err != nil {
		t.Fatal(err)
	}
	var started voice.ServerEvent
	if err := connection.ReadJSON(&started); err != nil {
		t.Fatal(err)
	}
	if started.Type != voice.EventPlaybackStart {
		t.Fatalf("started = %#v", started)
	}
	messageType, pcm, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage || len(pcm) != voice.PCMFrameBytes {
		t.Fatalf("PCM type=%d bytes=%d", messageType, len(pcm))
	}
	var ended voice.ServerEvent
	if err := connection.ReadJSON(&ended); err != nil {
		t.Fatal(err)
	}
	if ended.Type != voice.EventPlaybackEnd || ended.Reason != "completed" {
		t.Fatalf("ended = %#v", ended)
	}
}

type fakeVoiceSessionFactory struct{}

func (fakeVoiceSessionFactory) NewSession(_ string, _ string, output service.VoiceOutput) service.VoiceSession {
	return &fakeControllerVoiceSession{output: output}
}

type fakeControllerVoiceSession struct {
	output service.VoiceOutput
}

func (s *fakeControllerVoiceSession) Ready(ctx context.Context) error {
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventSessionReady, Phase: "CONVERSATION", CompletedTurns: 1,
		Audio: voice.DefaultAudioFormat(),
	})
}

func (s *fakeControllerVoiceSession) HandleEvent(ctx context.Context, event voice.ClientEvent) error {
	if event.Type != voice.EventSessionStart {
		return nil
	}
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackStart, PlaybackID: "playback-1", Kind: "opening",
	}); err != nil {
		return err
	}
	if err := s.output.SendPCM(ctx, make([]byte, voice.PCMFrameBytes)); err != nil {
		return err
	}
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackEnd, PlaybackID: "playback-1", Reason: "completed",
	})
}

func (s *fakeControllerVoiceSession) HandlePCM(context.Context, []byte) error { return nil }
func (s *fakeControllerVoiceSession) Close() error                            { return nil }

func websocketTestURLForController(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
