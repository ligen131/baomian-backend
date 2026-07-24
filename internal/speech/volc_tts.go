package speech

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type VolcTTSClient struct {
	config Config
	dialer *websocket.Dialer
}

func NewVolcTTSClient(config Config, dialer *websocket.Dialer) *VolcTTSClient {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	return &VolcTTSClient{config: config, dialer: dialer}
}

func (c *VolcTTSClient) Stream(ctx context.Context, text string, onPCM func([]byte) error) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrInvalidText
	}
	totalContext, cancel := context.WithTimeout(ctx, c.config.TTSTotalTimeout)
	defer cancel()

	connectID := uuid.NewString()
	headers := http.Header{
		"X-Api-Key":                             []string{c.config.TTSAPIKey},
		"X-Api-Resource-Id":                     []string{c.config.TTSResourceID},
		"X-Api-Connect-Id":                      []string{connectID},
		"X-Control-Require-Usage-Tokens-Return": []string{"*"},
	}
	connection, response, err := c.dialer.DialContext(totalContext, c.config.TTSURL, headers)
	requestID := connectID
	if response != nil {
		if logID := strings.TrimSpace(response.Header.Get("X-Tt-Logid")); logID != "" {
			requestID = logID
		}
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}
	if err != nil {
		if response != nil {
			return &UpstreamError{
				Service: "tts", Code: fmt.Sprintf("http_%d", response.StatusCode),
				RequestID: requestID, Retryable: response.StatusCode >= http.StatusInternalServerError,
			}
		}
		return err
	}
	defer connection.Close()

	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-totalContext.Done():
			_ = connection.Close()
		case <-closed:
		}
	}()

	frame, err := encodeVolcTTSRequest(map[string]any{
		"req_params": map[string]any{
			"speaker": c.config.TTSSpeaker,
			"text":    text,
			"audio_params": map[string]any{
				"format": "pcm", "sample_rate": 24000,
			},
		},
	})
	if err != nil {
		return err
	}
	if err := connection.SetWriteDeadline(operationDeadline(totalContext, c.config.TTSTotalTimeout)); err != nil {
		return err
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return err
	}

	delivered := false
	for {
		timeout := c.config.TTSTotalTimeout
		if !delivered {
			timeout = c.config.TTSFirstFrameTimeout
		}
		result, err := c.readResponse(totalContext, connection, timeout)
		if err != nil {
			return err
		}
		if result.Code != "" {
			return &UpstreamError{Service: "tts", Code: result.Code, RequestID: requestID, Retryable: true}
		}
		if result.Event == volcTTSEventConnectionFailed || result.Event == volcTTSEventSessionFailed {
			code := "event_" + fmt.Sprint(result.Event)
			if value, ok := result.Payload["code"]; ok {
				code = fmt.Sprint(value)
			}
			return &UpstreamError{Service: "tts", Code: code, RequestID: requestID, Retryable: true}
		}
		if len(result.Audio) > 0 {
			delivered = true
			if err := onPCM(result.Audio); err != nil {
				return err
			}
		}
		if result.Event == volcTTSEventSessionFinished {
			if !delivered {
				return &UpstreamError{Service: "tts", Code: "empty_audio", RequestID: requestID, Retryable: true}
			}
			return nil
		}
	}
}

func (c *VolcTTSClient) readResponse(ctx context.Context, connection *websocket.Conn, timeout time.Duration) (volcTTSResponse, error) {
	if err := connection.SetReadDeadline(operationDeadline(ctx, timeout)); err != nil {
		return volcTTSResponse{}, err
	}
	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		if ctx.Err() != nil {
			return volcTTSResponse{}, ctx.Err()
		}
		return volcTTSResponse{}, err
	}
	if messageType != websocket.BinaryMessage {
		return volcTTSResponse{}, fmt.Errorf("unexpected Volcengine TTS message type: %d", messageType)
	}
	return parseVolcTTSResponse(payload)
}

var _ TTSClient = (*VolcTTSClient)(nil)
