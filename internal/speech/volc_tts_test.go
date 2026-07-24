package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestVolcTTSStreamsPCM(t *testing.T) {
	wantPCM := []byte{1, 2, 3, 4, 5, 6}
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, http.Header{"X-Tt-Logid": []string{"log-1"}})
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()
		if r.Header.Get("X-Api-Key") != "api-key-1" || r.Header.Get("X-Api-Resource-Id") != "tts-resource" ||
			r.Header.Get("X-Api-Connect-Id") == "" || r.Header.Get("X-Control-Require-Usage-Tokens-Return") != "*" {
			serverErrors <- fmt.Errorf("unexpected TTS headers")
			return
		}
		if r.Header.Get("X-Api-App-Key") != "" || r.Header.Get("X-Api-Access-Key") != "" || r.Header.Get("X-Tt-Logid") != "" {
			serverErrors <- fmt.Errorf("legacy TTS headers present")
			return
		}

		request, err := readTestTTSRequest(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		params := request["req_params"].(map[string]any)
		audio := params["audio_params"].(map[string]any)
		if params["text"] != "今晚先休息一下。" || params["speaker"] != "speaker-1" ||
			audio["format"] != "pcm" || audio["sample_rate"] != float64(24000) {
			serverErrors <- fmt.Errorf("params = %#v", params)
			return
		}
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestTTSAudio(1, wantPCM[:3]))
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestTTSAudio(2, wantPCM[3:]))
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestTTSEvent(volcTTSEventSessionFinished, "session-1", nil))
		serverErrors <- nil
	}))
	defer server.Close()

	client := NewVolcTTSClient(volcTestConfig("", websocketTestURL(server.URL)), websocket.DefaultDialer)
	var got []byte
	if err := client.Stream(context.Background(), "今晚先休息一下。", func(chunk []byte) error {
		got = append(got, chunk...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantPCM) {
		t.Fatalf("PCM = %v, want %v", got, wantPCM)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestVolcTTSStopsWhenCallbackFails(t *testing.T) {
	callbackErr := errors.New("stop playback")
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = readTestTTSRequest(connection)
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestTTSAudio(1, []byte{1, 2}))
	}))
	defer server.Close()

	client := NewVolcTTSClient(volcTestConfig("", websocketTestURL(server.URL)), websocket.DefaultDialer)
	err := client.Stream(context.Background(), "停止测试", func([]byte) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestVolcTTSSanitizesUpstreamError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, http.Header{"X-Tt-Logid": []string{"safe-log-id"}})
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = readTestTTSRequest(connection)
		_ = connection.WriteMessage(websocket.BinaryMessage, encodeTestTTSError(55000000, "sensitive message"))
	}))
	defer server.Close()

	config := volcTestConfig("", websocketTestURL(server.URL))
	config.TTSAPIKey = "secret-api-key"
	err := NewVolcTTSClient(config, websocket.DefaultDialer).Stream(context.Background(), "测试", func([]byte) error { return nil })
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error = %v", err)
	}
	if upstream.Code != "55000000" || upstream.RequestID != "safe-log-id" {
		t.Fatalf("upstream = %#v", upstream)
	}
	if strings.Contains(err.Error(), "secret-api-key") || strings.Contains(err.Error(), "sensitive message") {
		t.Fatalf("error leaks sensitive data: %v", err)
	}
}

func TestRetryingTTSRetriesOnlyBeforeDelivery(t *testing.T) {
	inner := &fakeRetryTTS{errors: []error{errors.New("dial failed"), nil}}
	client := NewRetryingTTSClient(inner, 2)
	if err := client.Stream(context.Background(), "test", func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("calls = %d", inner.calls)
	}

	inner = &fakeRetryTTS{deliverBeforeError: true, errors: []error{errors.New("stream failed"), nil}}
	client = NewRetryingTTSClient(inner, 2)
	if err := client.Stream(context.Background(), "test", func([]byte) error { return nil }); err == nil {
		t.Fatal("expected stream failure")
	}
	if inner.calls != 1 {
		t.Fatalf("calls after delivery = %d", inner.calls)
	}
}

func readTestTTSRequest(connection *websocket.Conn) (map[string]any, error) {
	messageType, frame, err := connection.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.BinaryMessage || len(frame) < 8 || frame[0] != 0x11 || frame[1] != 0x10 || frame[2] != 0x10 {
		return nil, errInvalidVolcFrame
	}
	payload, err := sizedPayload(frame[4:])
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	return request, nil
}

func encodeTestTTSAudio(sequence int32, payload []byte) []byte {
	frame := append([]byte(nil), volcHeader(volcTTSMessageAudioServer, volcFlagSequence, volcSerializationNone, volcCompressionNone)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(sequence))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	return append(frame, payload...)
}

func encodeTestTTSEvent(event int32, sessionID string, payload map[string]any) []byte {
	value, _ := json.Marshal(payload)
	frame := append([]byte(nil), volcHeader(volcTTSMessageFullServer, volcTTSFlagWithEvent, volcSerializationJSON, volcCompressionNone)...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(event))
	if !volcTTSConnectionEvent(event) {
		frame = appendVolcString(frame, sessionID)
	}
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(value)))
	return append(frame, value...)
}

func encodeTestTTSError(code uint32, message string) []byte {
	value, _ := json.Marshal(map[string]any{"message": message})
	frame := append([]byte(nil), volcHeader(volcTTSMessageError, volcFlagNoSequence, volcSerializationJSON, volcCompressionNone)...)
	frame = binary.BigEndian.AppendUint32(frame, code)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(value)))
	return append(frame, value...)
}

type fakeRetryTTS struct {
	calls              int
	errors             []error
	deliverBeforeError bool
}

func (f *fakeRetryTTS) Stream(_ context.Context, _ string, onPCM func([]byte) error) error {
	index := f.calls
	f.calls++
	if f.deliverBeforeError {
		if err := onPCM([]byte{1}); err != nil {
			return err
		}
	}
	return f.errors[index]
}
