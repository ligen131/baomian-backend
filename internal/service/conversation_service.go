package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/baomian/baomian-backend/internal/ai"
	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/google/uuid"
)

type ConversationService struct {
	store repository.Store
	ai    ai.Adapter
	hub   *realtime.Hub
	now   func() time.Time
}

func NewConversationService(store repository.Store, adapter ai.Adapter, hub *realtime.Hub) *ConversationService {
	return &ConversationService{store: store, ai: adapter, hub: hub, now: time.Now}
}

func (s *ConversationService) Turn(ctx context.Context, userID string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error) {
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		return dto.ConversationTurnResponse{}, NewError("validation_error", "text 不能为空", nil)
	}

	var sessionID uuid.UUID
	var turnIndex int
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetOrCreateTonight(ctx, userID, s.now(), true)
		if err != nil {
			return err
		}
		if session.Phase == string(state.Locked) {
			next, err := state.Apply(snapshot(session), state.StartConversation)
			if err != nil {
				return err
			}
			applySnapshot(session, next)
		}
		if session.Phase != string(state.Conversation) {
			return &Error{Code: "invalid_transition", Message: "当前不在倾诉阶段", Details: map[string]any{"phase": session.Phase}}
		}
		if session.ConversationTurns >= 3 {
			return NewError("conversation_limit", "今晚的倾诉已达到 3 轮上限", nil)
		}
		session.ConversationTurns++
		turnIndex = session.ConversationTurns
		sessionID = session.ID
		if err := tx.CreateConversationTurn(ctx, &model.ConversationTurn{SessionID: session.ID, Role: "user", Text: request.Text, TurnIndex: turnIndex}); err != nil {
			return err
		}
		return tx.UpdateNightSession(ctx, session)
	})
	if err != nil {
		return dto.ConversationTurnResponse{}, normalizeServiceError(err, "提交倾诉失败")
	}

	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取人格设置失败", err)
	}
	turns, err := s.store.ListConversationTurns(ctx, sessionID)
	if err != nil {
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取对话历史失败", err)
	}
	cards, err := s.store.ListMemoryCards(ctx, userID, 7)
	if err != nil {
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取记忆卡失败", err)
	}
	aiRequest := ai.Request{Persona: profile.Persona, TurnIndex: turnIndex, Text: request.Text}
	for _, turn := range turns {
		aiRequest.Turns = append(aiRequest.Turns, ai.Turn{Role: turn.Role, Text: turn.Text})
	}
	for _, card := range cards {
		aiRequest.Memories = append(aiRequest.Memories, ai.Memory{Emotion: card.Emotion, Worry: card.Worry, TomorrowTask: card.TomorrowTask, Comfort: card.Comfort})
	}
	result, err := s.ai.Generate(ctx, aiRequest)
	if err != nil {
		return dto.ConversationTurnResponse{}, NewError("ai_error", "生成睡前回复失败", err)
	}
	if turnIndex >= 3 {
		result.ShouldFinalize = true
	}

	var response dto.ConversationTurnResponse
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetOrCreateTonight(ctx, userID, s.now(), true)
		if err != nil {
			return err
		}
		draft, err := json.Marshal(result)
		if err != nil {
			return err
		}
		session.LatestAIDraft = model.JSON(json.RawMessage(draft))
		if err := tx.CreateConversationTurn(ctx, &model.ConversationTurn{SessionID: session.ID, Role: "assistant", Text: result.Reply, TurnIndex: turnIndex, Fallback: result.Fallback}); err != nil {
			return err
		}
		var card *model.MemoryCard
		if result.ShouldFinalize {
			card = memoryCard(session, userID, result, s.now())
			if err := tx.UpsertMemoryCard(ctx, card); err != nil {
				return err
			}
			if session.Phase == string(state.Conversation) || session.Phase == string(state.Locked) {
				next, err := state.Apply(snapshot(session), state.Finalize)
				if err != nil {
					return err
				}
				applySnapshot(session, next)
			}
		}
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		response = dto.ConversationTurnResponse{Result: result, Tonight: dto.TonightFromModels(session, profile)}
		if card != nil {
			converted := dto.MemoryCardFromModel(card)
			response.Journal = &converted
		}
		return nil
	})
	if err != nil {
		return dto.ConversationTurnResponse{}, normalizeServiceError(err, "保存 AI 回复失败")
	}
	publish(s.hub, userID, "conversation.reply", response)
	publish(s.hub, userID, "tonight.updated", response.Tonight)
	if response.Journal != nil {
		publish(s.hub, userID, "journal.created", response.Journal)
	}
	return response, nil
}

func (s *ConversationService) Finalize(ctx context.Context, userID string) (dto.FinalizeResponse, error) {
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.FinalizeResponse{}, NewError("storage_error", "读取用户设置失败", err)
	}
	var response dto.FinalizeResponse
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetOrCreateTonight(ctx, userID, s.now(), true)
		if err != nil {
			return err
		}
		var result dto.AIResult
		if len(session.LatestAIDraft) > 2 {
			if err := json.Unmarshal(session.LatestAIDraft, &result); err != nil {
				return err
			}
		} else {
			latest, err := tx.GetLatestUserTurn(ctx, session.ID)
			text := ""
			if err == nil {
				text = latest.Text
			} else if !errors.Is(err, repository.ErrNotFound) {
				return err
			}
			fallback := ai.NewFallbackAdapter()
			result, err = fallback.Generate(ctx, ai.Request{Text: text, TurnIndex: max(session.ConversationTurns, 1)})
			if err != nil {
				return err
			}
		}
		result.ShouldFinalize = true
		card := memoryCard(session, userID, result, s.now())
		if err := tx.UpsertMemoryCard(ctx, card); err != nil {
			return err
		}
		if session.Phase == string(state.Conversation) || session.Phase == string(state.Locked) {
			next, err := state.Apply(snapshot(session), state.Finalize)
			if err != nil {
				return err
			}
			applySnapshot(session, next)
		}
		if session.Phase != string(state.ChoosingGuidance) {
			return &Error{Code: "invalid_transition", Message: "当前状态无法结束倾诉", Details: map[string]any{"phase": session.Phase}}
		}
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		response = dto.FinalizeResponse{Journal: dto.MemoryCardFromModel(card), Tonight: dto.TonightFromModels(session, profile)}
		return nil
	})
	if err != nil {
		return dto.FinalizeResponse{}, normalizeServiceError(err, "结束倾诉失败")
	}
	publish(s.hub, userID, "journal.created", response.Journal)
	publish(s.hub, userID, "tonight.updated", response.Tonight)
	return response, nil
}

func memoryCard(session *model.NightSession, userID string, result dto.AIResult, now time.Time) *model.MemoryCard {
	return &model.MemoryCard{
		ID: uuid.New(), SessionID: session.ID, UserID: userID, Date: session.Date,
		Emotion: result.Emotion, Worry: result.Worry, TomorrowTask: result.TomorrowTask,
		Comfort: result.Comfort, SuggestedGuidance: result.SuggestedGuidance,
		Fallback: result.Fallback, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
}

func normalizeServiceError(err error, message string) error {
	var serviceErr *Error
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	return NewError("storage_error", message, err)
}
