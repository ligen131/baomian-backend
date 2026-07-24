package speech

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const volcASRBatchBytes = 9600

type VolcASRClient struct {
	config Config
	dialer *websocket.Dialer
}

func NewVolcASRClient(config Config, dialer *websocket.Dialer) *VolcASRClient {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	return &VolcASRClient{config: config, dialer: dialer}
}

func (c *VolcASRClient) Open(ctx context.Context) (ASRSession, error) {
	requestID := uuid.NewString()
	headers := http.Header{
		"X-Api-App-Key":     []string{c.config.AppID},
		"X-Api-Access-Key":  []string{c.config.AccessToken},
		"X-Api-Resource-Id": []string{c.config.ASRResourceID},
		"X-Api-Request-Id":  []string{requestID},
	}
	connection, response, err := c.dialer.DialContext(ctx, c.config.ASRURL, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	session := &volcASRSession{
		connection: connection,
		timeout:    c.config.ASRTimeout,
		requestID:  requestID,
		done:       make(chan struct{}),
	}
	if err := session.configure(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	_ = connection.SetReadDeadline(time.Time{})
	go session.readLoop()
	return session, nil
}

type volcASRSession struct {
	connection *websocket.Conn
	timeout    time.Duration
	requestID  string

	mu          sync.Mutex
	buffer      []byte
	transcript  string
	terminalErr error
	completed   bool
	closed      bool
	closeOnce   sync.Once
	done        chan struct{}
}

func (s *volcASRSession) configure(ctx context.Context) error {
	request := map[string]any{
		"user": map[string]any{"uid": "baomian-device"},
		"audio": map[string]any{
			"format": "pcm", "codec": "raw", "sample_rate": 24000, "channel": 1,
		},
		"request": map[string]any{
			"model_name": "bigmodel", "enable_itn": true, "enable_punc": true, "enable_ddc": false,
		},
	}
	frame, err := encodeVolcASRFullRequest(1, request)
	if err != nil {
		return err
	}
	if err := s.writeFrame(ctx, frame); err != nil {
		return err
	}
	if err := s.connection.SetReadDeadline(operationDeadline(ctx, s.timeout)); err != nil {
		return err
	}
	messageType, payload, err := s.connection.ReadMessage()
	if err != nil {
		return err
	}
	if messageType != websocket.BinaryMessage {
		return fmt.Errorf("unexpected Volcengine ASR message type: %d", messageType)
	}
	response, err := parseVolcASRResponse(payload)
	if err != nil {
		return err
	}
	if response.Code != 0 {
		return s.upstreamError(response.Code)
	}
	return nil
}

func (s *volcASRSession) AppendPCM(ctx context.Context, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.completed {
		return context.Canceled
	}
	s.buffer = append(s.buffer, frame...)
	for len(s.buffer) > volcASRBatchBytes {
		batch := append([]byte(nil), s.buffer[:volcASRBatchBytes]...)
		s.buffer = s.buffer[volcASRBatchBytes:]
		if err := s.sendAudioLocked(ctx, batch, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *volcASRSession) Complete(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", context.Canceled
	}
	if !s.completed {
		s.completed = true
		last := append([]byte(nil), s.buffer...)
		s.buffer = nil
		if err := s.sendAudioLocked(ctx, last, true); err != nil {
			s.mu.Unlock()
			return "", err
		}
	}
	s.mu.Unlock()

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-ctx.Done():
		_ = s.Close()
		return "", ctx.Err()
	case <-timer.C:
		_ = s.Close()
		return "", context.DeadlineExceeded
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return "", s.terminalErr
	}
	transcript := strings.TrimSpace(s.transcript)
	if transcript == "" {
		return "", ErrEmptyTranscript
	}
	return transcript, nil
}

func (s *volcASRSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		closeErr = s.connection.Close()
	})
	return closeErr
}

func (s *volcASRSession) sendAudioLocked(ctx context.Context, audio []byte, last bool) error {
	frame, err := encodeVolcASRAudio(audio, last)
	if err != nil {
		return err
	}
	return s.writeFrame(ctx, frame)
}

func (s *volcASRSession) writeFrame(ctx context.Context, frame []byte) error {
	if err := s.connection.SetWriteDeadline(operationDeadline(ctx, s.timeout)); err != nil {
		return err
	}
	return s.connection.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *volcASRSession) readLoop() {
	defer close(s.done)
	for {
		messageType, payload, err := s.connection.ReadMessage()
		if err != nil {
			s.mu.Lock()
			if !s.closed {
				s.terminalErr = err
			}
			s.mu.Unlock()
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		response, err := parseVolcASRResponse(payload)
		if err != nil {
			s.setTerminal(err)
			return
		}
		if response.Code != 0 {
			s.setTerminal(s.upstreamError(response.Code))
			return
		}
		if transcript := volcASRTranscript(response.Payload); transcript != "" {
			s.mu.Lock()
			s.transcript = transcript
			s.mu.Unlock()
		}
		if response.Last || response.Sequence < 0 {
			return
		}
	}
}

func (s *volcASRSession) setTerminal(err error) {
	s.mu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	s.mu.Unlock()
}

func (s *volcASRSession) upstreamError(code uint32) error {
	return &UpstreamError{Service: "asr", Code: fmt.Sprint(code), RequestID: s.requestID, Retryable: true}
}

func volcASRTranscript(payload map[string]any) string {
	result, ok := payload["result"].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := result["text"].(string)
	return strings.TrimSpace(text)
}

func operationDeadline(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}

var _ ASRClient = (*VolcASRClient)(nil)
var _ ASRSession = (*volcASRSession)(nil)

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
