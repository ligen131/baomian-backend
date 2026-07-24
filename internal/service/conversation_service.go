package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	store           repository.Store
	ai              ai.Adapter
	hub             *realtime.Hub
	silenceTimeout  time.Duration
	maxDuration     time.Duration
	processingLease time.Duration
	logger          *slog.Logger
	now             func() time.Time
}

func NewConversationService(
	store repository.Store,
	adapter ai.Adapter,
	hub *realtime.Hub,
	silenceTimeout time.Duration,
	maxDuration time.Duration,
	processingLease time.Duration,
	logger ...*slog.Logger,
) *ConversationService {
	var serviceLogger *slog.Logger
	if len(logger) > 0 {
		serviceLogger = logger[0]
	}
	return &ConversationService{
		store: store, ai: adapter, hub: hub,
		silenceTimeout: silenceTimeout, maxDuration: maxDuration,
		processingLease: processingLease, logger: serviceLogger, now: time.Now,
	}
}

func (s *ConversationService) History(ctx context.Context, userID string) (dto.ConversationHistoryResponse, error) {
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取用户设置失败", err)
	}
	now := s.now().UTC()
	session, err := s.store.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), false)
	if err != nil {
		return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取今晚状态失败", err)
	}
	turns, err := s.store.ListConversationTurns(ctx, session.ID)
	if err != nil {
		return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取对话历史失败", err)
	}
	result := dto.ConversationHistoryResponse{
		Turns: make([]dto.ConversationTurn, 0, len(turns)), Tonight: dto.TonightFromModels(session, profile),
		RemainingTurns: max(0, 3-session.ConversationTurns),
		Processing:     session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil),
	}
	for i := range turns {
		result.Turns = append(result.Turns, dto.ConversationTurnFromModel(&turns[i]))
	}
	return result, nil
}

func (s *ConversationService) BeginPlayback(ctx context.Context, userID string) error {
	return s.updatePlaybackProtection(ctx, userID, true)
}

func (s *ConversationService) EndPlayback(ctx context.Context, userID string) error {
	return s.updatePlaybackProtection(ctx, userID, false)
}

func (s *ConversationService) updatePlaybackProtection(ctx context.Context, userID string, active bool) error {
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		profile, err := tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), true)
		if err != nil {
			return err
		}
		if session.Phase != string(state.Conversation) {
			return &Error{Code: "invalid_transition", Message: "当前不在倾诉阶段", Details: map[string]any{"phase": session.Phase}}
		}
		if active {
			leaseUntil := now.Add(s.processingLease)
			if session.ConversationHardDeadlineAt != nil && leaseUntil.Before(*session.ConversationHardDeadlineAt) {
				leaseUntil = *session.ConversationHardDeadlineAt
			}
			session.ConversationProcessingUntil = &leaseUntil
			return tx.UpdateNightSession(ctx, session)
		}
		session.ConversationProcessingUntil = nil
		session.ConversationLastActivityAt = &now
		deadline := now.Add(s.silenceTimeout)
		if session.ConversationHardDeadlineAt != nil && deadline.After(*session.ConversationHardDeadlineAt) {
			deadline = *session.ConversationHardDeadlineAt
		}
		session.ConversationSilenceDeadlineAt = &deadline
		return tx.UpdateNightSession(ctx, session)
	})
	if err != nil {
		return normalizeServiceError(err, "更新语音播放状态失败")
	}
	return nil
}

func (s *ConversationService) Activity(ctx context.Context, userID string, request dto.ConversationActivityRequest) (dto.TonightState, error) {
	if request.Activity != "" && !oneOf(request.Activity, "typing", "speaking", "listening") {
		return dto.TonightState{}, NewError("validation_error", "activity 无效", nil)
	}
	var response dto.TonightState
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		profile, err := tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), true)
		if err != nil {
			return err
		}
		if session.Phase != string(state.Conversation) {
			return &Error{Code: "invalid_transition", Message: "当前不在倾诉阶段", Details: map[string]any{"phase": session.Phase}}
		}
		if session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt) {
			return NewError("conversation_expired", "今晚的倾诉时间已结束", nil)
		}
		deadline := now.Add(s.silenceTimeout)
		if session.ConversationHardDeadlineAt != nil && deadline.After(*session.ConversationHardDeadlineAt) {
			deadline = *session.ConversationHardDeadlineAt
		}
		session.ConversationLastActivityAt = &now
		session.ConversationSilenceDeadlineAt = &deadline
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		response = dto.TonightFromModels(session, profile)
		return nil
	})
	if err != nil {
		return dto.TonightState{}, normalizeServiceError(err, "更新倾诉活动失败")
	}
	publish(s.hub, userID, "tonight.updated", response)
	return response, nil
}

func (s *ConversationService) Turn(ctx context.Context, userID string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error) {
	request.Text = strings.TrimSpace(request.Text)
	request.InputMode = strings.TrimSpace(request.InputMode)
	request.ClientRequestID = strings.TrimSpace(request.ClientRequestID)
	if request.Text == "" {
		return dto.ConversationTurnResponse{}, NewError("validation_error", "text 不能为空", nil)
	}
	if request.InputMode == "" {
		request.InputMode = "text"
	}
	if !oneOf(request.InputMode, "text", "voice") {
		return dto.ConversationTurnResponse{}, NewError("validation_error", "inputMode 必须是 text 或 voice", nil)
	}
	if len(request.ClientRequestID) > 128 {
		return dto.ConversationTurnResponse{}, NewError("validation_error", "clientRequestId 过长", nil)
	}

	var profile *model.Profile
	var sessionID uuid.UUID
	var turnIndex int
	var duplicateResponse *dto.ConversationTurnResponse
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		var err error
		profile, err = tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), true)
		if err != nil {
			return err
		}
		sessionID = session.ID
		if request.ClientRequestID != "" {
			assistant, err := tx.GetConversationTurnByClientRequestID(ctx, session.ID, request.ClientRequestID, "assistant")
			if err == nil {
				result, parseErr := resultForDuplicate(assistant)
				if parseErr != nil {
					return parseErr
				}
				value := dto.ConversationTurnResponse{Result: result, Tonight: dto.TonightFromModels(session, profile)}
				duplicateResponse = &value
				return nil
			}
			if !errors.Is(err, repository.ErrNotFound) {
				return err
			}
			if existing, err := tx.GetConversationTurnByClientRequestID(ctx, session.ID, request.ClientRequestID, "user"); err == nil {
				if session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil) {
					return NewError("request_in_progress", "该倾诉请求正在处理中", nil)
				}
				if session.Phase != string(state.Conversation) {
					return &Error{Code: "invalid_transition", Message: "该倾诉请求已无法继续处理", Details: map[string]any{"phase": session.Phase}}
				}
				turnIndex = existing.TurnIndex
				request.Text = existing.Text
				request.InputMode = existing.InputMode
				leaseUntil := now.Add(s.processingLease)
				session.ConversationProcessingUntil = &leaseUntil
				return tx.UpdateNightSession(ctx, session)
			} else if !errors.Is(err, repository.ErrNotFound) {
				return err
			}
		}
		if session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil) {
			return NewError("request_in_progress", "上一轮倾诉正在处理中", nil)
		}
		if session.Phase == string(state.Locked) {
			previousPhase := session.Phase
			next, err := state.Apply(snapshot(session), state.StartConversation)
			if err != nil {
				return err
			}
			applySnapshot(session, next)
			applySessionTiming(session, previousPhase, state.StartConversation, now, s.silenceTimeout, s.maxDuration, 0)
		}
		if session.Phase != string(state.Conversation) {
			return &Error{Code: "invalid_transition", Message: "当前不在倾诉阶段", Details: map[string]any{"phase": session.Phase}}
		}
		if session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt) {
			return NewError("conversation_expired", "今晚的倾诉时间已结束", nil)
		}
		if session.ConversationTurns >= 3 {
			return NewError("conversation_limit", "今晚的倾诉已达到 3 轮上限", nil)
		}
		session.ConversationTurns++
		turnIndex = session.ConversationTurns
		leaseUntil := now.Add(s.processingLease)
		session.ConversationProcessingUntil = &leaseUntil
		session.ConversationLastActivityAt = &now
		deadline := now.Add(s.silenceTimeout)
		if session.ConversationHardDeadlineAt != nil && deadline.After(*session.ConversationHardDeadlineAt) {
			deadline = *session.ConversationHardDeadlineAt
		}
		session.ConversationSilenceDeadlineAt = &deadline
		var clientRequestID *string
		if request.ClientRequestID != "" {
			clientRequestID = &request.ClientRequestID
		}
		if err := tx.CreateConversationTurn(ctx, &model.ConversationTurn{
			SessionID: session.ID, Role: "user", Text: request.Text, TurnIndex: turnIndex,
			InputMode: request.InputMode, ClientRequestID: clientRequestID,
		}); err != nil {
			return err
		}
		return tx.UpdateNightSession(ctx, session)
	})
	if err != nil {
		return dto.ConversationTurnResponse{}, normalizeServiceError(err, "提交倾诉失败")
	}
	if duplicateResponse != nil {
		return *duplicateResponse, nil
	}

	turns, err := s.store.ListConversationTurns(ctx, sessionID)
	if err != nil {
		s.releaseProcessingLease(ctx, sessionID)
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取对话历史失败", err)
	}
	cards, err := s.store.ListMemoryCards(ctx, userID, 7)
	if err != nil {
		s.releaseProcessingLease(ctx, sessionID)
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
		if s.logger != nil {
			s.logger.ErrorContext(ctx, "conversation AI generation failed", "sessionId", sessionID, "turnId", request.ClientRequestID, "turnIndex", turnIndex, "errorCategory", "ai_error", "error", err)
		}
		s.releaseProcessingLease(ctx, sessionID)
		return dto.ConversationTurnResponse{}, NewError("ai_error", "生成睡前回复失败", err)
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "conversation AI generation completed", "sessionId", sessionID, "turnId", request.ClientRequestID, "turnIndex", turnIndex, "fallback", result.Fallback, "highRisk", result.HighRisk)
	}
	shouldFinalize, finalizeReason := conversationFinalizePolicy(turnIndex, result)
	result.ShouldFinalize = shouldFinalize

	var response dto.ConversationTurnResponse
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetNightSessionByID(ctx, sessionID, true)
		if err != nil {
			return err
		}
		draft, err := json.Marshal(result)
		if err != nil {
			return err
		}
		session.LatestAIDraft = model.JSON(json.RawMessage(draft))
		session.ConversationProcessingUntil = nil
		now := s.now().UTC()
		session.ConversationLastActivityAt = &now
		deadline := now.Add(s.silenceTimeout)
		if session.ConversationHardDeadlineAt != nil && deadline.After(*session.ConversationHardDeadlineAt) {
			deadline = *session.ConversationHardDeadlineAt
		}
		session.ConversationSilenceDeadlineAt = &deadline
		var clientRequestID *string
		if request.ClientRequestID != "" {
			clientRequestID = &request.ClientRequestID
		}
		if err := tx.CreateConversationTurn(ctx, &model.ConversationTurn{
			SessionID: session.ID, Role: "assistant", Text: result.Reply, TurnIndex: turnIndex,
			Fallback: result.Fallback, InputMode: request.InputMode, ClientRequestID: clientRequestID,
			Result: model.JSON(json.RawMessage(draft)),
		}); err != nil {
			return err
		}
		var card *model.MemoryCard
		if result.ShouldFinalize {
			previousPhase := session.Phase
			card, err = finalizeSession(ctx, tx, session, userID, result, finalizeReason, now)
			if err != nil {
				return err
			}
			if s.logger != nil {
				s.logger.InfoContext(ctx, "night session phase changed", "sessionId", session.ID, "from", previousPhase, "to", session.Phase, "trigger", "conversation_turn", "finalizeReason", finalizeReason, "completedTurns", session.ConversationTurns)
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
	return s.FinalizeWithReason(ctx, userID, "manual")
}

func (s *ConversationService) FinalizeWithReason(ctx context.Context, userID, reason string) (dto.FinalizeResponse, error) {
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.FinalizeResponse{}, NewError("storage_error", "读取用户设置失败", err)
	}
	var response dto.FinalizeResponse
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		now := s.now().UTC()
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), true)
		if err != nil {
			return err
		}
		result, err := finalResult(ctx, tx, session)
		if err != nil {
			return err
		}
		card, err := finalizeSession(ctx, tx, session, userID, result, reason, now)
		if err != nil {
			return err
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

func (s *ConversationService) releaseProcessingLease(ctx context.Context, sessionID uuid.UUID) {
	_ = s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetNightSessionByID(ctx, sessionID, true)
		if err != nil {
			return err
		}
		session.ConversationProcessingUntil = nil
		return tx.UpdateNightSession(ctx, session)
	})
}

func finalResult(ctx context.Context, tx repository.Store, session *model.NightSession) (dto.AIResult, error) {
	var result dto.AIResult
	if len(session.LatestAIDraft) > 2 {
		if err := json.Unmarshal(session.LatestAIDraft, &result); err != nil {
			return dto.AIResult{}, err
		}
		return result, nil
	}
	latest, err := tx.GetLatestUserTurn(ctx, session.ID)
	text := ""
	if err == nil {
		text = latest.Text
	} else if !errors.Is(err, repository.ErrNotFound) {
		return dto.AIResult{}, err
	}
	fallback := ai.NewFallbackAdapter()
	return fallback.Generate(ctx, ai.Request{Text: text, TurnIndex: max(session.ConversationTurns, 1)})
}

func finalizeSession(ctx context.Context, tx repository.Store, session *model.NightSession, userID string, result dto.AIResult, reason string, now time.Time) (*model.MemoryCard, error) {
	result.ShouldFinalize = true
	card := memoryCard(session, userID, result, now)
	if err := tx.UpsertMemoryCard(ctx, card); err != nil {
		return nil, err
	}
	if session.Phase == string(state.Conversation) || session.Phase == string(state.Locked) {
		next, err := state.Apply(snapshot(session), state.Finalize)
		if err != nil {
			return nil, err
		}
		applySnapshot(session, next)
	}
	if session.Phase != string(state.ChoosingGuidance) {
		return nil, &Error{Code: "invalid_transition", Message: "当前状态无法结束倾诉", Details: map[string]any{"phase": session.Phase}}
	}
	session.FinalizeReason = reason
	clearConversationTiming(session)
	return card, nil
}

func conversationFinalizePolicy(turnIndex int, result dto.AIResult) (bool, string) {
	if result.HighRisk && result.ShouldFinalize {
		return true, "safety"
	}
	if turnIndex >= 3 {
		return true, "turn_limit"
	}
	return false, ""
}

func resultForDuplicate(assistant *model.ConversationTurn) (dto.AIResult, error) {
	var result dto.AIResult
	if len(assistant.Result) > 2 {
		if err := json.Unmarshal(assistant.Result, &result); err != nil {
			return dto.AIResult{}, err
		}
	}
	result.Reply = assistant.Text
	result.Fallback = assistant.Fallback
	return result, nil
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
