package ai

import (
	"context"
	"log/slog"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
)

type ResilientAdapter struct {
	primary  Adapter
	fallback Adapter
	timeout  time.Duration
	logger   *slog.Logger
}

func NewResilientAdapter(primary, fallback Adapter, timeout time.Duration, logger *slog.Logger) *ResilientAdapter {
	return &ResilientAdapter{primary: primary, fallback: fallback, timeout: timeout, logger: logger}
}

func (a *ResilientAdapter) Generate(ctx context.Context, request Request) (dto.AIResult, error) {
	primaryCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	result, err := a.primary.Generate(primaryCtx, request)
	if err == nil {
		return result, nil
	}
	a.logger.WarnContext(ctx, "AI primary adapter failed, using fallback", "error", err)
	result, fallbackErr := a.fallback.Generate(ctx, request)
	if fallbackErr != nil {
		return dto.AIResult{}, fallbackErr
	}
	result.Fallback = true
	return result, nil
}
