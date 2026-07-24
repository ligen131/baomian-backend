package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/baomian/baomian-backend/internal/ai"
	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/state"
)

type coordinatorFinalizeEvent struct {
	Journal dto.MemoryCard
	Tonight dto.TonightState
}

type Coordinator struct {
	store           repository.Store
	hub             *realtime.Hub
	defaultDeviceID string
	interval        time.Duration
	logger          *slog.Logger
	now             func() time.Time
}

func New(store repository.Store, hub *realtime.Hub, defaultDeviceID string, interval time.Duration, logger *slog.Logger) *Coordinator {
	return &Coordinator{
		store: store, hub: hub, defaultDeviceID: defaultDeviceID,
		interval: interval, logger: logger, now: time.Now,
	}
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Scan(ctx); err != nil && c.logger != nil {
				c.logger.ErrorContext(ctx, "session coordinator scan failed", "error", err)
			}
		}
	}
}

func (c *Coordinator) Scan(ctx context.Context) error {
	now := c.now().UTC()
	ids, err := c.store.ListDueNightSessionIDs(ctx, now, 100)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var eventType string
		var eventData any
		var userID string
		err := c.store.WithTx(ctx, func(tx repository.Store) error {
			session, err := tx.GetNightSessionByID(ctx, id, true)
			if err != nil {
				return err
			}
			userID = session.UserID
			profile, err := tx.GetOrCreateProfile(ctx, session.UserID)
			if err != nil {
				return err
			}
			if session.Phase == string(state.Conversation) && conversationDue(session, now) {
				result, err := coordinatorResult(ctx, tx, session)
				if err != nil {
					return err
				}
				card := memoryCard(session, result, now)
				if err := tx.UpsertMemoryCard(ctx, card); err != nil {
					return err
				}
				next, err := state.Apply(snapshot(session), state.Finalize)
				if err != nil {
					return err
				}
				applySnapshot(session, next)
				if session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt) {
					session.FinalizeReason = "max_duration"
				} else {
					session.FinalizeReason = "silence"
				}
				clearConversationTiming(session)
				if err := tx.UpdateNightSession(ctx, session); err != nil {
					return err
				}
				eventType = "journal.created"
				eventData = coordinatorFinalizeEvent{
					Journal: dto.MemoryCardFromModel(card), Tonight: dto.TonightFromModels(session, profile),
				}
				return nil
			}
			if session.Phase == string(state.PhoneRemoved) && session.ResumeDeadlineAt != nil && !now.Before(*session.ResumeDeadlineAt) {
				session.ResumePhase = ""
				session.ResumeDeadlineAt = nil
				session.PausedForTonight = true
				session.AudioPlaying = false
				session.AudioEndsAt = nil
				if err := tx.CreateDeviceCommands(ctx, []model.DeviceCommand{{
					DeviceID: c.defaultDeviceID, UserID: session.UserID, Type: "audio.stop", Payload: model.JSON(map[string]any{}), Status: "pending", AckPayload: model.JSON(map[string]any{}),
				}}); err != nil {
					return err
				}
				if err := tx.UpdateNightSession(ctx, session); err != nil {
					return err
				}
				eventType = "tonight.updated"
				eventData = dto.TonightFromModels(session, profile)
				return nil
			}
			if session.AudioPlaying && session.AudioEndsAt != nil && !now.Before(*session.AudioEndsAt) {
				session.AudioPlaying = false
				session.AudioEndsAt = nil
				if err := tx.CreateDeviceCommands(ctx, []model.DeviceCommand{{
					DeviceID: c.defaultDeviceID, UserID: session.UserID, Type: "audio.stop", Payload: model.JSON(map[string]any{}), Status: "pending", AckPayload: model.JSON(map[string]any{}),
				}}); err != nil {
					return err
				}
				if err := tx.UpdateNightSession(ctx, session); err != nil {
					return err
				}
				eventType = "tonight.updated"
				eventData = dto.TonightFromModels(session, profile)
			}
			return nil
		})
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			if c.logger != nil {
				c.logger.WarnContext(ctx, "session coordinator item failed", "sessionId", id, "error", err)
			}
			continue
		}
		if eventType != "" {
			if finalized, ok := eventData.(coordinatorFinalizeEvent); ok {
				publish(c.hub, userID, "journal.created", finalized.Journal, now)
				publish(c.hub, userID, "tonight.updated", finalized.Tonight, now)
			} else {
				publish(c.hub, userID, eventType, eventData, now)
			}
		}
	}
	return nil
}

func conversationDue(session *model.NightSession, now time.Time) bool {
	if session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil) {
		return false
	}
	return session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt) ||
		session.ConversationSilenceDeadlineAt != nil && !now.Before(*session.ConversationSilenceDeadlineAt)
}

func coordinatorResult(ctx context.Context, tx repository.Store, session *model.NightSession) (dto.AIResult, error) {
	if len(session.LatestAIDraft) > 2 {
		var result dto.AIResult
		if err := decodeAI(session.LatestAIDraft, &result); err == nil {
			result.ShouldFinalize = true
			return result, nil
		}
	}
	text := ""
	latest, err := tx.GetLatestUserTurn(ctx, session.ID)
	if err == nil {
		text = latest.Text
	} else if !errors.Is(err, repository.ErrNotFound) {
		return dto.AIResult{}, err
	}
	result, err := ai.NewFallbackAdapter().Generate(ctx, ai.Request{Text: text, TurnIndex: max(session.ConversationTurns, 1)})
	result.ShouldFinalize = true
	return result, err
}

func memoryCard(session *model.NightSession, result dto.AIResult, now time.Time) *model.MemoryCard {
	return &model.MemoryCard{
		SessionID: session.ID, UserID: session.UserID, Date: session.Date,
		Emotion: result.Emotion, Worry: result.Worry, TomorrowTask: result.TomorrowTask,
		Comfort: result.Comfort, SuggestedGuidance: result.SuggestedGuidance,
		Fallback: result.Fallback, CreatedAt: now, UpdatedAt: now,
	}
}

func snapshot(session *model.NightSession) state.Snapshot {
	return state.Snapshot{
		Phase: state.Phase(session.Phase), ResumePhase: state.Phase(session.ResumePhase),
		BoxClosed: session.BoxClosed, AudioPlaying: session.AudioPlaying,
		SunriseProgress: session.SunriseProgress, PausedForTonight: session.PausedForTonight,
	}
}

func applySnapshot(session *model.NightSession, value state.Snapshot) {
	session.Phase = string(value.Phase)
	session.ResumePhase = string(value.ResumePhase)
	session.BoxClosed = value.BoxClosed
	session.AudioPlaying = value.AudioPlaying
	session.SunriseProgress = value.SunriseProgress
	session.PausedForTonight = value.PausedForTonight
}

func clearConversationTiming(session *model.NightSession) {
	session.ConversationSilenceDeadlineAt = nil
	session.ConversationHardDeadlineAt = nil
	session.ConversationProcessingUntil = nil
}

func decodeAI(raw []byte, result *dto.AIResult) error {
	return json.Unmarshal(raw, result)
}

func publish(hub *realtime.Hub, userID, eventType string, data any, now time.Time) {
	if hub != nil {
		hub.Publish(userID, dto.WSEvent{Type: eventType, OccurredAt: now, Data: data})
	}
}
