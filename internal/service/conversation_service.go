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
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/google/uuid"
)

type ConversationService struct {
	store                      repository.Store
	ai                         ai.Adapter
	hub                        *realtime.Hub
	silenceTimeout             time.Duration
	maxDuration                time.Duration
	processingLease            time.Duration
	demoContinuousConversation bool
	demoUserID                 string
	demoDeviceID               string
	logger                     *slog.Logger
	now                        func() time.Time
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

func (s *ConversationService) ConfigureDemoContinuousConversation(enabled bool, userID, deviceID string) {
	s.demoContinuousConversation = enabled
	s.demoUserID = userID
	s.demoDeviceID = deviceID
}

func demoContinuousConversationEnabled(enabled bool, configuredUser, configuredDevice, userID, deviceID string) bool {
	return enabled && userID == configuredUser && deviceID == configuredDevice
}

func demoConversationRestartPolicy(session *model.NightSession, now time.Time) (string, bool) {
	if !session.BoxClosed {
		return "", false
	}
	if session.Phase == string(state.Conversation) &&
		session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt) {
		if session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil) {
			return "", true
		}
		return "expired", false
	}
	if session.Phase == string(state.ChoosingGuidance) || session.Phase == string(state.Sleeping) {
		return "completed", false
	}
	return "", false
}

func (s *ConversationService) PrepareVoiceSession(_ context.Context, _, _ string) error {
	// RESET 设备事件负责幂等创建 run；Voice 建连只恢复该 run，不能删除历史 turn 或日记。
	return nil
}

func resetDemoConversation(session *model.NightSession) {
	session.Phase = string(state.Locked)
	session.ResumePhase = ""
	session.ConversationTurns = 0
	session.SelectedGuidance = ""
	session.AudioPlaying = false
	session.SunriseProgress = 0
	session.PausedForTonight = false
	session.LatestAIDraft = model.JSON(map[string]any{})
	session.FinalizeReason = ""
	session.ConversationStartedAt = nil
	session.ConversationLastActivityAt = nil
	clearConversationTiming(session)
	session.PhoneRemovedAt = nil
	session.ResumeDeadlineAt = nil
	session.AudioEndsAt = nil
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
	if err := model.ValidateNightSessionConversationState(session); err != nil {
		if s.logger != nil {
			s.logger.ErrorContext(ctx, "inconsistent night session state", "sessionId", session.ID, "phase", session.Phase, "completedTurns", session.ConversationTurns)
		}
		return dto.ConversationHistoryResponse{}, &Error{Code: "invalid_transition", Message: "今晚状态异常，请重新开始会话", Details: map[string]any{"phase": session.Phase, "completedTurns": session.ConversationTurns}, Cause: err}
	}
	var run *model.ConversationRun
	if s.demoDeviceID != "" {
		run, err = s.store.GetActiveConversationRun(ctx, userID, s.demoDeviceID, false)
		if errors.Is(err, repository.ErrNotFound) {
			run, err = s.store.GetLatestConversationRun(ctx, userID, s.demoDeviceID)
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取当前对话 run 失败", err)
		}
	}
	var turns []model.ConversationTurn
	if run != nil {
		turns, err = s.store.ListConversationTurnsByRun(ctx, run.ID)
	} else {
		turns, err = s.store.ListConversationTurns(ctx, session.ID)
	}
	if err != nil {
		return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取对话历史失败", err)
	}
	result := dto.ConversationHistoryResponse{
		Turns: make([]dto.ConversationTurn, 0, len(turns)), Tonight: dto.TonightFromModels(session, profile),
		RemainingTurns: 0,
		Processing:     session.ConversationProcessingUntil != nil && now.Before(*session.ConversationProcessingUntil),
	}
	if run != nil {
		result.RunID = run.ID
		result.Recovery = recoveryState(run, turns)
		if run.Status == model.ConversationRunCompleted {
			if card, cardErr := s.store.GetMemoryCardByRun(ctx, run.ID); cardErr == nil {
				result.Recovery.JournalID = card.ID.String()
			} else if !errors.Is(cardErr, repository.ErrNotFound) {
				return dto.ConversationHistoryResponse{}, NewError("storage_error", "读取 run 日记失败", cardErr)
			}
		}
	}
	for i := range turns {
		result.Turns = append(result.Turns, dto.ConversationTurnFromModel(&turns[i]))
	}
	return result, nil
}

func recoveryState(run *model.ConversationRun, turns []model.ConversationTurn) voice.RecoveryState {
	state := voice.RecoveryState{RunStatus: run.Status, ResumeAction: "listen"}
	if run.ProcessingTurnID != nil {
		state.PendingTurnID = *run.ProcessingTurnID
		state.ResumeAction = "wait_turn"
		for _, turn := range turns {
			if turn.Role == "assistant" && turn.ClientRequestID != nil && *turn.ClientRequestID == *run.ProcessingTurnID {
				state.ResumeAction = "replay_reply"
				break
			}
		}
	}
	if run.FinishEventID != nil {
		state.FinishEventID = *run.FinishEventID
	}
	state.Guidance = run.Guidance
	state.GuidanceStatus = run.GuidanceStatus
	switch run.Status {
	case model.ConversationRunFinishing:
		state.ResumeAction = "wait_finish"
	case model.ConversationRunCompleted:
		if run.GuidanceStatus == model.GuidanceCompleted {
			state.ResumeAction = "done"
		} else {
			state.ResumeAction = "replay_guidance"
		}
	case model.ConversationRunAborted:
		state.ResumeAction = "done"
	}
	return state
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
	continuousRun := request.RunID != uuid.Nil
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
	var runID uuid.UUID
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
		var run *model.ConversationRun
		if request.RunID != uuid.Nil {
			run, err = tx.GetConversationRun(ctx, userID, request.RunID, true)
		} else if s.demoDeviceID != "" {
			run, err = tx.GetActiveConversationRun(ctx, userID, s.demoDeviceID, true)
		} else {
			return NewError("validation_error", "runId 不能为空", nil)
		}
		if errors.Is(err, repository.ErrNotFound) {
			return NewError("invalid_transition", "当前没有可用的 conversation run", nil)
		}
		if err != nil {
			return err
		}
		if run.NightSessionID != session.ID || run.Status != model.ConversationRunActive {
			return &Error{Code: "invalid_transition", Message: "conversation run 与今晚状态不匹配", Details: map[string]any{"runStatus": run.Status}}
		}
		runID = run.ID
		if request.ClientRequestID != "" {
			assistant, err := tx.GetConversationTurnByRunRequestID(ctx, run.ID, request.ClientRequestID, "assistant")
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
			if existing, err := tx.GetConversationTurnByRunRequestID(ctx, run.ID, request.ClientRequestID, "user"); err == nil {
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
				run.ProcessingTurnID = &request.ClientRequestID
				if err := tx.UpdateConversationRun(ctx, run); err != nil {
					return err
				}
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
		if conversationExpired(session, continuousRun, now) {
			return NewError("conversation_expired", "今晚的倾诉时间已结束", nil)
		}
		if continuousRun {
			session.ConversationSilenceDeadlineAt = nil
			session.ConversationHardDeadlineAt = nil
		}
		turnIndex = nextTurnIndex(run.CompletedTurns)
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
			run.ProcessingTurnID = clientRequestID
		}
		if err := tx.CreateConversationTurn(ctx, &model.ConversationTurn{
			SessionID: session.ID, RunID: run.ID, Role: "user", Text: request.Text, TurnIndex: turnIndex,
			InputMode: request.InputMode, ClientRequestID: clientRequestID,
		}); err != nil {
			return err
		}
		if err := tx.UpdateConversationRun(ctx, run); err != nil {
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

	turns, err := s.store.ListConversationTurnsByRun(ctx, runID)
	if err != nil {
		s.releaseProcessingLease(ctx, sessionID, runID)
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取对话历史失败", err)
	}
	cards, err := s.store.ListMemoryCards(ctx, userID, 7)
	if err != nil {
		s.releaseProcessingLease(ctx, sessionID, runID)
		return dto.ConversationTurnResponse{}, NewError("storage_error", "读取记忆卡失败", err)
	}
	aiRequest := ai.Request{Mode: ai.ModeReply, Persona: profile.Persona, TurnIndex: turnIndex, Text: request.Text}
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
		s.releaseProcessingLease(ctx, sessionID, runID)
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
		run, err := tx.GetConversationRun(ctx, userID, runID, true)
		if err != nil {
			return err
		}
		if run.Status != model.ConversationRunActive {
			return &Error{Code: "invalid_transition", Message: "conversation run 已结束", Details: map[string]any{"runStatus": run.Status}}
		}
		draft, err := json.Marshal(result)
		if err != nil {
			return err
		}
		session.LatestAIDraft = model.JSON(json.RawMessage(draft))
		session.ConversationTurns = completedTurnsAfterReply(session.ConversationTurns, turnIndex)
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
			SessionID: session.ID, RunID: run.ID, Role: "assistant", Text: result.Reply, TurnIndex: turnIndex,
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
		run.CompletedTurns = completedTurnsAfterReply(run.CompletedTurns, turnIndex)
		if err := tx.UpdateConversationRun(ctx, run); err != nil {
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

func (s *ConversationService) FinishRun(ctx context.Context, userID string, runID uuid.UUID, eventID string) (dto.FinalizeResponse, error) {
	eventID = strings.TrimSpace(eventID)
	if runID == uuid.Nil || eventID == "" {
		return dto.FinalizeResponse{}, NewError("validation_error", "runId 和 finish eventId 不能为空", nil)
	}
	if existing, err := s.store.GetMemoryCardByRun(ctx, runID); err == nil {
		profile, profileErr := s.store.GetOrCreateProfile(ctx, userID)
		if profileErr != nil {
			return dto.FinalizeResponse{}, NewError("storage_error", "读取用户设置失败", profileErr)
		}
		session, sessionErr := s.store.GetNightSessionByID(ctx, existing.SessionID, false)
		if sessionErr != nil {
			return dto.FinalizeResponse{}, NewError("storage_error", "读取今晚状态失败", sessionErr)
		}
		return dto.FinalizeResponse{Journal: dto.MemoryCardFromModel(existing), Tonight: dto.TonightFromModels(session, profile)}, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return dto.FinalizeResponse{}, NewError("storage_error", "读取 run 日记失败", err)
	}

	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.FinalizeResponse{}, NewError("storage_error", "读取用户设置失败", err)
	}
	var sessionID uuid.UUID
	var turns []model.ConversationTurn
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		run, err := tx.GetConversationRun(ctx, userID, runID, true)
		if err != nil {
			return err
		}
		if run.Status == model.ConversationRunCompleted {
			return repository.ErrNotFound
		}
		if run.FinishEventID != nil && *run.FinishEventID != eventID {
			return NewError("finish_in_progress", "该 run 已由另一个 finish 事件结束", nil)
		}
		run.FinishEventID = &eventID
		run.Status = model.ConversationRunFinishing
		run.ProcessingTurnID = nil
		if err := tx.UpdateConversationRun(ctx, run); err != nil {
			return err
		}
		if err := tx.DeleteIncompleteConversationTurnsByRun(ctx, runID); err != nil {
			return err
		}
		sessionID = run.NightSessionID
		turns, err = tx.ListConversationTurnsByRun(ctx, runID)
		return err
	})
	if err != nil {
		return dto.FinalizeResponse{}, normalizeServiceError(err, "开始结束 conversation run 失败")
	}

	request := journalRequest(turns)
	result := emptyJournalResult()
	if len(request.Turns) > 0 {
		result, err = s.ai.Generate(ctx, request)
		if err != nil {
			return dto.FinalizeResponse{}, NewError("ai_error", "生成晚安日记失败", err)
		}
		result.ShouldFinalize = true
	}
	result.SuggestedGuidance = sleepGuidance(result.SuggestedGuidance)
	now := s.now().UTC()
	var response dto.FinalizeResponse
	err = s.store.WithTx(ctx, func(tx repository.Store) error {
		run, err := tx.GetConversationRun(ctx, userID, runID, true)
		if err != nil {
			return err
		}
		if card, err := tx.GetMemoryCardByRun(ctx, runID); err == nil {
			session, sessionErr := tx.GetNightSessionByID(ctx, card.SessionID, true)
			if sessionErr != nil {
				return sessionErr
			}
			response = dto.FinalizeResponse{Journal: dto.MemoryCardFromModel(card), Tonight: dto.TonightFromModels(session, profile)}
			return nil
		} else if !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		session, err := tx.GetNightSessionByID(ctx, sessionID, true)
		if err != nil {
			return err
		}
		card := memoryCard(session, userID, result, now)
		card.RunID = runID
		if err := tx.CreateMemoryCard(ctx, card); err != nil {
			return err
		}
		if session.Phase == string(state.Conversation) || session.Phase == string(state.Locked) {
			next, err := state.Apply(snapshot(session), state.Finalize)
			if err != nil {
				return err
			}
			applySnapshot(session, next)
		}
		session.FinalizeReason = "device_key"
		clearConversationTiming(session)
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		run.Status = model.ConversationRunCompleted
		run.CompletedTurns = session.ConversationTurns
		run.Guidance = result.SuggestedGuidance
		run.GuidanceStatus = model.GuidancePending
		run.FinishedAt = &now
		if err := tx.UpdateConversationRun(ctx, run); err != nil {
			return err
		}
		response = dto.FinalizeResponse{Journal: dto.MemoryCardFromModel(card), Tonight: dto.TonightFromModels(session, profile)}
		return nil
	})
	if err != nil {
		return dto.FinalizeResponse{}, normalizeServiceError(err, "完成 conversation run 失败")
	}
	publish(s.hub, userID, "journal.created", response.Journal)
	publish(s.hub, userID, "tonight.updated", response.Tonight)
	return response, nil
}

func (s *ConversationService) CompleteReplyDelivery(ctx context.Context, userID string, runID uuid.UUID, turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if runID == uuid.Nil || turnID == "" {
		return NewError("validation_error", "runId 和 turnId 不能为空", nil)
	}
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		run, err := tx.GetConversationRun(ctx, userID, runID, true)
		if err != nil {
			return err
		}
		if run.ProcessingTurnID == nil || *run.ProcessingTurnID != turnID {
			return nil
		}
		run.ProcessingTurnID = nil
		return tx.UpdateConversationRun(ctx, run)
	})
	if err != nil {
		return normalizeServiceError(err, "确认回复播放失败")
	}
	return nil
}

func (s *ConversationService) UpdateGuidanceStatus(ctx context.Context, userID string, runID uuid.UUID, status string) error {
	if runID == uuid.Nil || !oneOf(status, model.GuidancePlaying, model.GuidanceInterrupted, model.GuidanceCompleted) {
		return NewError("validation_error", "runId 或 guidance status 无效", nil)
	}
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		run, err := tx.GetConversationRun(ctx, userID, runID, true)
		if err != nil {
			return err
		}
		if run.Status != model.ConversationRunCompleted {
			return &Error{Code: "invalid_transition", Message: "conversation run 尚未完成", Details: map[string]any{"status": run.Status}}
		}
		switch status {
		case model.GuidancePlaying:
			if run.GuidanceStatus == model.GuidanceCompleted {
				return &Error{Code: "invalid_transition", Message: "guidance 已完成", Details: map[string]any{"guidanceStatus": run.GuidanceStatus}}
			}
		case model.GuidanceCompleted, model.GuidanceInterrupted:
			if run.GuidanceStatus != model.GuidancePlaying {
				return &Error{Code: "invalid_transition", Message: "guidance 未在播放", Details: map[string]any{"guidanceStatus": run.GuidanceStatus}}
			}
		}
		run.GuidanceStatus = status
		return tx.UpdateConversationRun(ctx, run)
	})
	if err != nil {
		return normalizeServiceError(err, "更新 guidance 状态失败")
	}
	return nil
}

func journalRequest(turns []model.ConversationTurn) ai.Request {
	completeIndexes := make(map[int]bool)
	for _, turn := range turns {
		if turn.Role == "assistant" {
			completeIndexes[turn.TurnIndex] = true
		}
	}
	request := ai.Request{Mode: ai.ModeJournal}
	var userTexts []string
	for _, turn := range turns {
		if !completeIndexes[turn.TurnIndex] {
			continue
		}
		request.Turns = append(request.Turns, ai.Turn{Role: turn.Role, Text: turn.Text})
		if turn.Role == "user" {
			userTexts = append(userTexts, turn.Text)
		}
	}
	request.Text = strings.Join(userTexts, "\n")
	return request
}

func sleepGuidance(value string) string {
	if value == "breathing_46" {
		return value
	}
	return "rain"
}

func emptyJournalResult() dto.AIResult {
	return dto.AIResult{
		Reply:             "今晚没有想说的也没关系，安心休息吧。",
		Emotion:           "平静",
		Worry:             "今晚没有留下具体的心事",
		TomorrowTask:      "明天醒来后照顾好自己",
		Comfort:           "今晚没有想说的也没关系，安心休息吧。",
		GuidanceOptions:   []string{"rain", "brown_noise", "breathing_46", "silence"},
		SuggestedGuidance: "rain",
		ShouldFinalize:    true,
		Fallback:          true,
	}
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

func (s *ConversationService) releaseProcessingLease(ctx context.Context, sessionID, runID uuid.UUID) {
	_ = s.store.WithTx(ctx, func(tx repository.Store) error {
		session, err := tx.GetNightSessionByID(ctx, sessionID, true)
		if err != nil {
			return err
		}
		session.ConversationProcessingUntil = nil
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		run, err := tx.GetConversationRun(ctx, session.UserID, runID, true)
		if err != nil {
			return err
		}
		run.ProcessingTurnID = nil
		return tx.UpdateConversationRun(ctx, run)
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

func validateConversationFinalization(session *model.NightSession) error {
	if session.ConversationTurns < 0 {
		return &Error{Code: "invalid_transition", Message: "倾诉轮数无效", Details: map[string]any{"phase": session.Phase, "completedTurns": session.ConversationTurns}}
	}
	return nil
}

func finalizeSession(ctx context.Context, tx repository.Store, session *model.NightSession, userID string, result dto.AIResult, reason string, now time.Time) (*model.MemoryCard, error) {
	if err := validateConversationFinalization(session); err != nil {
		return nil, err
	}
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

func conversationExpired(session *model.NightSession, continuousRun bool, now time.Time) bool {
	return !continuousRun && session.ConversationHardDeadlineAt != nil && !now.Before(*session.ConversationHardDeadlineAt)
}

func nextTurnIndex(completedTurns int) int {
	return completedTurns + 1
}

func completedTurnsAfterReply(current, turnIndex int) int {
	if turnIndex > current {
		return turnIndex
	}
	return current
}

func conversationFinalizePolicy(_ int, _ dto.AIResult) (bool, string) {
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
