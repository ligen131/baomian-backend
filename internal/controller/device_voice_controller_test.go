package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestWebsocketVoiceOutputAddsRunContextToControllerErrors(t *testing.T) {
	messages := make(chan voiceOutboundMessage, 2)
	output := &websocketVoiceOutput{messages: messages}
	if err := output.SendEvent(context.Background(), voice.ServerEvent{Type: voice.EventSessionReady, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if err := output.SendEvent(context.Background(), voice.ServerEvent{
		Type: voice.EventError, Code: voice.ErrorInvalidEvent, Message: "invalid",
	}); err != nil {
		t.Fatal(err)
	}
	<-messages
	message := <-messages
	event := message.payload.(voice.ServerEvent)
	if event.RunID != "run-1" || event.TerminalFor != "event" || event.OccurredAt == "" {
		t.Fatalf("event = %#v", event)
	}
}

func TestWebsocketVoiceOutputBackpressuresUntilQueueHasSpace(t *testing.T) {
	messages := make(chan voiceOutboundMessage, 1)
	output := &websocketVoiceOutput{messages: messages}
	if err := output.SendPCM(context.Background(), make([]byte, voice.PCMFrameBytes)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventPlaybackEnd, PlaybackID: "playback-1", Reason: "completed"})
	}()
	select {
	case err := <-result:
		t.Fatalf("enqueue returned before space was available: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	first := <-messages
	if first.messageType != websocket.BinaryMessage {
		t.Fatalf("first message type = %d", first.messageType)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	second := <-messages
	event, ok := second.payload.(voice.ServerEvent)
	if !ok || event.Type != voice.EventPlaybackEnd {
		t.Fatalf("second message = %#v", second)
	}
}

func TestWebsocketVoiceOutputStopsBackpressureWhenContextEnds(t *testing.T) {
	messages := make(chan voiceOutboundMessage, 1)
	output := &websocketVoiceOutput{messages: messages}
	messages <- voiceOutboundMessage{messageType: websocket.BinaryMessage, payload: []byte{0}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventPlaybackEnd}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestDeviceVoiceControllerDeliversPlaybackEndAfterBurstPCM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/voice", NewDeviceVoiceController(burstVoiceSessionFactory{frames: 169}, true, "user-1").Connect)
	server := httptest.NewServer(engine)
	defer server.Close()

	connection, _, err := websocket.DefaultDialer.Dial(websocketTestURLForController(server.URL)+"/voice?deviceId=device-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, _, err := connection.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteJSON(voice.ClientEvent{Type: voice.EventSessionStart, RunID: "run-1", EventID: "start-1"}); err != nil {
		t.Fatal(err)
	}
	var started voice.ServerEvent
	if err := connection.ReadJSON(&started); err != nil || started.Type != voice.EventPlaybackStart {
		t.Fatalf("started=%#v err=%v", started, err)
	}
	for index := 0; index < 169; index++ {
		messageType, pcm, err := connection.ReadMessage()
		if err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		if messageType != websocket.BinaryMessage || len(pcm) != voice.PCMFrameBytes {
			t.Fatalf("frame %d: type=%d bytes=%d", index, messageType, len(pcm))
		}
	}
	var ended voice.ServerEvent
	if err := connection.ReadJSON(&ended); err != nil {
		t.Fatal(err)
	}
	if ended.Type != voice.EventPlaybackEnd || ended.PlaybackID != started.PlaybackID || ended.Reason != "completed" {
		t.Fatalf("ended = %#v", ended)
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

	_, readyPayload, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var readyJSON map[string]any
	if err := json.Unmarshal(readyPayload, &readyJSON); err != nil {
		t.Fatal(err)
	}
	if readyJSON["completedTurns"] != float64(0) {
		t.Fatalf("raw ready = %s", readyPayload)
	}
	var ready voice.ServerEvent
	if err := json.Unmarshal(readyPayload, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != voice.EventSessionReady || ready.CompletedTurns != 0 {
		t.Fatalf("ready = %#v", ready)
	}
	if err := connection.WriteJSON(voice.ClientEvent{Type: voice.EventSessionStart, RunID: "run-1", EventID: "start-1"}); err != nil {
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

type burstVoiceSessionFactory struct {
	frames int
}

func (f burstVoiceSessionFactory) NewSession(_ string, _ string, output service.VoiceOutput) service.VoiceSession {
	return &burstControllerVoiceSession{fakeControllerVoiceSession: fakeControllerVoiceSession{output: output}, frames: f.frames}
}

type burstControllerVoiceSession struct {
	fakeControllerVoiceSession
	frames int
}

func (s *burstControllerVoiceSession) HandleEvent(ctx context.Context, event voice.ClientEvent) error {
	if event.Type != voice.EventSessionStart {
		return nil
	}
	playbackID := "burst-playback"
	if err := s.output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventPlaybackStart, PlaybackID: playbackID, Kind: "opening"}); err != nil {
		return err
	}
	for index := 0; index < s.frames; index++ {
		if err := s.output.SendPCM(ctx, make([]byte, voice.PCMFrameBytes)); err != nil {
			return err
		}
	}
	return s.output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventPlaybackEnd, PlaybackID: playbackID, Reason: "completed"})
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
		Type: voice.EventSessionReady, Phase: "LOCKED", CompletedTurns: 0,
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
