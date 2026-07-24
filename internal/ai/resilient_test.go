package ai

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
)

type adapterFunc func(context.Context, Request) (dto.AIResult, error)

func (fn adapterFunc) Generate(ctx context.Context, request Request) (dto.AIResult, error) {
	return fn(ctx, request)
}

func TestResilientAdapterUsesFallbackAfterPrimaryFailure(t *testing.T) {
	primaryErr := errors.New("primary failed")
	primary := adapterFunc(func(context.Context, Request) (dto.AIResult, error) {
		return dto.AIResult{}, primaryErr
	})
	fallbackCalled := false
	fallback := adapterFunc(func(context.Context, Request) (dto.AIResult, error) {
		fallbackCalled = true
		return dto.AIResult{Reply: "fallback reply"}, nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := NewResilientAdapter(primary, fallback, time.Second, logger)

	result, err := adapter.Generate(context.Background(), Request{Text: "测试"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !fallbackCalled || !result.Fallback || result.Reply != "fallback reply" {
		t.Fatalf("result = %#v, fallbackCalled = %v", result, fallbackCalled)
	}
}
