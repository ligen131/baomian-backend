package speech

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEmptyTranscript = errors.New("empty transcript")
	ErrInvalidText     = errors.New("invalid speech text")
)

type ASRSession interface {
	AppendPCM(ctx context.Context, frame []byte) error
	Complete(ctx context.Context) (string, error)
	Close() error
}

type ASRClient interface {
	Open(ctx context.Context) (ASRSession, error)
}

type TTSClient interface {
	Stream(ctx context.Context, text string, onPCM func([]byte) error) error
}

type Config struct {
	AppID                string
	AccessToken          string
	TTSAPIKey            string
	ASRURL               string
	ASRResourceID        string
	TTSURL               string
	TTSResourceID        string
	TTSSpeaker           string
	ASRTimeout           time.Duration
	ASRFinalTimeout      time.Duration
	TTSFirstFrameTimeout time.Duration
	TTSTotalTimeout      time.Duration
}

type UpstreamError struct {
	Service   string
	Code      string
	RequestID string
	Retryable bool
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("%s upstream error (code=%s, requestId=%s)", e.Service, e.Code, e.RequestID)
}

type retryingTTSClient struct {
	inner    TTSClient
	attempts int
}

func NewRetryingTTSClient(inner TTSClient, attempts int) TTSClient {
	if attempts < 1 {
		attempts = 1
	}
	return &retryingTTSClient{inner: inner, attempts: attempts}
}

func (c *retryingTTSClient) Stream(ctx context.Context, text string, onPCM func([]byte) error) error {
	var lastErr error
	for attempt := 0; attempt < c.attempts; attempt++ {
		delivered := false
		err := c.inner.Stream(ctx, text, func(chunk []byte) error {
			delivered = true
			return onPCM(chunk)
		})
		if err == nil {
			return nil
		}
		lastErr = err
		if delivered || ctx.Err() != nil || errors.Is(err, ErrInvalidText) {
			return err
		}
	}
	return lastErr
}
