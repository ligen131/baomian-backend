package speech

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/gorilla/websocket"
)

func TestVolcASRStreamsAggregatedPCMAndReturnsTranscript(t *testing.T) {
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()
		if r.Header.Get("X-Api-App-Key") != "app-1" || r.Header.Get("X-Api-Access-Key") != "test-token" ||
			r.Header.Get("X-Api-Resource-Id") != "asr-resource" || r.Header.Get("X-Api-Request-Id") == "" {
			serverErrors <- fmt.Errorf("unexpected ASR headers")
			return
		}

		_, initFrame, err := connection.ReadMessage()
		if err != nil {
			serverErrors <- err
			return
		}
		if initFrame[1] != 0x11 || int32(binary.BigEndian.Uint32(initFrame[4:8])) != 1 {
			serverErrors <- fmt.Errorf("unexpected init frame: %x", initFrame[:8])
			return
		}
		initPayload, err := sizedPayload(initFrame[8:])
		if err != nil {
			serverErrors <- err
			return
		}
		initPayload, err = gunzipBytes(initPayload)
		if err != nil {
			serverErrors <- err
			return
		}
		var initRequest map[string]any
		if err := json.Unmarshal(initPayload, &initRequest); err != nil {
			serverErrors <- err
			return
		}
		audio := initRequest["audio"].(map[string]any)
		if audio["format"] != "pcm" || audio["codec"] != "raw" || audio["sample_rate"] != float64(24000) {
			serverErrors <- fmt.Errorf("unexpected ASR audio config: %#v", audio)
			return
		}
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestASRResponse(t, 1, false, map[string]any{}))

		_, audioFrame, err := connection.ReadMessage()
		if err != nil {
			serverErrors <- err
			return
		}
		audioPayload, err := decodeTestASRAudioFrame(audioFrame)
		if err != nil || len(audioPayload) != volcASRBatchBytes || audioFrame[1]&0x0F != volcFlagNoSequence {
			serverErrors <- fmt.Errorf("aggregated audio bytes=%d flags=%x err=%v", len(audioPayload), audioFrame[1]&0x0F, err)
			return
		}
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestASRResponse(t, 2, false, map[string]any{
			"result": map[string]any{"text": "今天有点"},
		}))

		_, lastFrame, err := connection.ReadMessage()
		if err != nil {
			serverErrors <- err
			return
		}
		lastPayload, err := decodeTestASRAudioFrame(lastFrame)
		if err != nil || len(lastPayload) != voice.PCMFrameBytes || lastFrame[1]&0x0F != volcFlagLast {
			serverErrors <- fmt.Errorf("last audio bytes=%d flags=%x err=%v", len(lastPayload), lastFrame[1]&0x0F, err)
			return
		}
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestASRResponse(t, -3, true, map[string]any{
			"result": map[string]any{"text": "今天有点累"},
		}))
		serverErrors <- nil
	}))
	defer server.Close()

	client := NewVolcASRClient(volcTestConfig(websocketTestURL(server.URL), ""), websocket.DefaultDialer)
	session, err := client.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for index := 0; index < 11; index++ {
		if err := session.AppendPCM(context.Background(), make([]byte, voice.PCMFrameBytes)); err != nil {
			t.Fatal(err)
		}
	}
	text, err := session.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if text != "今天有点累" {
		t.Fatalf("text = %q", text)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestVolcASRFinalTimeoutReturnsUpstreamError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	lastReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, _, _ = connection.ReadMessage()
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestASRResponse(t, 1, false, map[string]any{}))
		_, _, _ = connection.ReadMessage()
		close(lastReceived)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	config := volcTestConfig(websocketTestURL(server.URL), "")
	config.ASRTimeout = 2 * time.Second
	config.ASRFinalTimeout = 30 * time.Millisecond
	client := NewVolcASRClient(config, websocket.DefaultDialer)
	session, err := client.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	_, err = session.Complete(context.Background())
	<-lastReceived
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Service != "asr" || upstream.Code != "timeout" || upstream.RequestID == "" {
		t.Fatalf("error = %#v", err)
	}
}

func TestVolcASRReturnsSanitizedUpstreamError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, _, _ = connection.ReadMessage()
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestASRError(t, 45000081, "sensitive upstream message"))
	}))
	defer server.Close()

	config := volcTestConfig(websocketTestURL(server.URL), "")
	config.AccessToken = "secret-token"
	client := NewVolcASRClient(config, websocket.DefaultDialer)
	_, err := client.Open(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error = %v", err)
	}
	if upstream.Code != "45000081" || upstream.RequestID == "" {
		t.Fatalf("upstream = %#v", upstream)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "sensitive upstream message") {
		t.Fatalf("error leaks sensitive data: %v", err)
	}
}

func decodeTestASRAudioFrame(frame []byte) ([]byte, error) {
	if len(frame) < 8 || frame[1]>>4 != volcMessageAudioClient {
		return nil, errInvalidVolcFrame
	}
	payload, err := sizedPayload(frame[4:])
	if err != nil {
		return nil, err
	}
	return gunzipBytes(payload)
}

func volcTestConfig(asrURL, ttsURL string) Config {
	return Config{
		AppID: "app-1", AccessToken: "test-token", TTSAPIKey: "api-key-1",
		ASRURL: asrURL, ASRResourceID: "asr-resource",
		TTSURL: ttsURL, TTSResourceID: "tts-resource", TTSSpeaker: "speaker-1",
		ASRTimeout: 2 * time.Second, ASRFinalTimeout: 2 * time.Second,
		TTSFirstFrameTimeout: 2 * time.Second, TTSTotalTimeout: 2 * time.Second,
	}
}

func websocketTestURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
