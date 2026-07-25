package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/google/uuid"
)

type VoiceConversation interface {
	PrepareVoiceSession(ctx context.Context, userID, deviceID string) error
	History(ctx context.Context, userID string) (dto.ConversationHistoryResponse, error)
	Turn(ctx context.Context, userID string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error)
	FinishRun(ctx context.Context, userID string, runID uuid.UUID, eventID string) (dto.FinalizeResponse, error)
	UpdateGuidanceStatus(ctx context.Context, userID string, runID uuid.UUID, status string) error
	CompleteReplyDelivery(ctx context.Context, userID string, runID uuid.UUID, turnID string) error
	BeginPlayback(ctx context.Context, userID string) error
	EndPlayback(ctx context.Context, userID string) error
}

type VoiceTonight interface {
	StartVoiceConversation(ctx context.Context, userID string) (dto.TonightState, error)
	SelectVoiceGuidance(ctx context.Context, userID, guidance string) (dto.TonightState, error)
}

type VoiceOutput interface {
	SendEvent(ctx context.Context, event voice.ServerEvent) error
	SendPCM(ctx context.Context, frame []byte) error
}

type VoiceSession interface {
	Ready(ctx context.Context) error
	HandleEvent(ctx context.Context, event voice.ClientEvent) error
	HandlePCM(ctx context.Context, frame []byte) error
	Close() error
}

type VoiceSessionFactory interface {
	NewSession(userID, deviceID string, output VoiceOutput) VoiceSession
}

type VoiceSessionService struct {
	conversation    VoiceConversation
	tonight         VoiceTonight
	asr             speech.ASRClient
	tts             speech.TTSClient
	sleepAudio      SleepAudio
	openingText     string
	breathingScript string
	maxUtterance    time.Duration
	logger          *slog.Logger
	now             func() time.Time
}

func NewVoiceSessionService(
	conversation VoiceConversation,
	tonight VoiceTonight,
	asr speech.ASRClient,
	tts speech.TTSClient,
	openingText string,
	breathingScript string,
	maxUtterance time.Duration,
	logger ...*slog.Logger,
) *VoiceSessionService {
	var serviceLogger *slog.Logger
	if len(logger) > 0 {
		serviceLogger = logger[0]
	}
	return &VoiceSessionService{
		conversation: conversation, tonight: tonight, asr: asr, tts: tts,
		openingText: openingText, breathingScript: breathingScript,
		maxUtterance: maxUtterance, logger: serviceLogger, now: time.Now,
	}
}

func (s *VoiceSessionService) ConfigureSleepAudio(value SleepAudio) {
	s.sleepAudio = value
}

func (s *VoiceSessionService) NewSession(userID, deviceID string, output VoiceOutput) VoiceSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &voiceSession{
		service: s, userID: userID, deviceID: deviceID, output: output,
		context: ctx, cancel: cancel, playbackDone: make(chan struct{}),
	}
}

type voiceSession struct {
	service        *VoiceSessionService
	userID         string
	deviceID       string
	output         VoiceOutput
	context        context.Context
	cancel         context.CancelFunc
	runID          string
	recovery       voice.RecoveryState
	recoveryTurns  []dto.ConversationTurn
	completedTurns int

	mu               sync.Mutex
	input            *asrInputPump
	currentTurnID    string
	processingTurnID string
	inputStartedAt   time.Time
	cancelPlayback   context.CancelFunc
	currentPlayback  string
	playbackDone     chan struct{}
	closed           bool
}

type asrInputItem struct {
	frame []byte
	end   bool
}

type asrInputPump struct {
	asr    speech.ASRSession
	items  chan asrInputItem
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *voiceSession) Ready(ctx context.Context) error {
	if err := s.service.conversation.PrepareVoiceSession(ctx, s.userID, s.deviceID); err != nil {
		return s.sendConversationError(ctx, err, "")
	}
	history, err := s.service.conversation.History(ctx, s.userID)
	if err != nil {
		var serviceErr *Error
		if errors.As(err, &serviceErr) && serviceErr.Code == "invalid_transition" {
			return s.sendConversationError(ctx, err, "")
		}
		return err
	}
	recovery := history.Recovery
	s.runID = history.RunID.String()
	s.recovery = recovery
	s.recoveryTurns = append([]dto.ConversationTurn(nil), history.Turns...)
	s.completedTurns = history.Tonight.ConversationTurns
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventSessionReady, RunID: history.RunID.String(), Phase: history.Tonight.Phase,
		CompletedTurns: history.Tonight.ConversationTurns, Audio: voice.DefaultAudioFormat(), Recovery: &recovery,
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *voiceSession) HandleEvent(ctx context.Context, event voice.ClientEvent) error {
	if s.runID != "" && event.RunID != "" && event.RunID != s.runID {
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "事件 runId 与当前会话不匹配", false, event.TurnID)
	}
	switch event.Type {
	case voice.EventSessionStart:
		return s.startSession(ctx)
	case voice.EventInputStart:
		return s.startInput(ctx, event.EventID, event.TurnID)
	case voice.EventInputEnd:
		return s.endInput(ctx, event.TurnID)
	case voice.EventPlaybackStop:
		s.stopPlayback("user_interrupt")
		return nil
	case voice.EventConversationFinish:
		return s.finishConversation(ctx, event.EventID)
	default:
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "不支持的语音事件", false, event.TurnID)
	}
}

func (s *voiceSession) HandlePCM(ctx context.Context, frame []byte) error {
	if err := voice.ValidatePCMFrame(frame); err != nil {
		return s.sendVoiceError(ctx, voice.ErrorInvalidAudioFrame, "音频帧必须为 960 bytes", true, s.turnID())
	}
	s.mu.Lock()
	input := s.input
	startedAt := s.inputStartedAt
	turnID := s.currentTurnID
	s.mu.Unlock()
	if input == nil || turnID == "" {
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "当前没有正在接收的语音", false, "")
	}
	if s.service.now().Sub(startedAt) > s.service.maxUtterance {
		s.clearInput()
		return s.sendVoiceError(ctx, voice.ErrorTurnTooLong, "单次说话不能超过 60 秒", true, turnID)
	}
	item := asrInputItem{frame: append([]byte(nil), frame...)}
	select {
	case input.items <- item:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.context.Done():
		return s.context.Err()
	}
}

func (s *voiceSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	input := s.input
	s.input = nil
	cancelPlayback := s.cancelPlayback
	s.cancelPlayback = nil
	s.mu.Unlock()
	s.cancel()
	if cancelPlayback != nil {
		cancelPlayback()
	}
	if input != nil {
		input.cancel()
		<-input.done
	}
	return nil
}

func (s *voiceSession) finishConversation(ctx context.Context, eventID string) error {
	s.stopPlayback("conversation_finish")
	s.clearInput()
	runID, err := uuid.Parse(s.runID)
	if err != nil {
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "当前会话缺少有效 runId", false, "")
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer finishCancel()
	response, err := s.service.conversation.FinishRun(finishCtx, s.userID, runID, eventID)
	if err != nil {
		return s.sendConversationError(ctx, err, "")
	}
	guidance := response.Journal.SuggestedGuidance
	if guidance == "" {
		guidance = response.Tonight.SelectedGuidance
	}
	if err := s.output.SendEvent(s.context, voice.ServerEvent{
		Type: voice.EventConversationComplete, RunID: s.runID, EventID: eventID,
		CompletedTurns: response.Tonight.ConversationTurns,
		JournalID:      response.Journal.ID.String(), Guidance: guidance,
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}
	if _, err := s.service.tonight.SelectVoiceGuidance(finishCtx, s.userID, guidance); err != nil {
		return s.sendVoiceError(s.context, voice.ErrorInvalidPhase, "无法开始今晚的引导", false, "")
	}
	s.recovery = voice.RecoveryState{
		RunStatus: model.ConversationRunCompleted, ResumeAction: "replay_guidance",
		FinishEventID: eventID, JournalID: response.Journal.ID.String(), Guidance: guidance,
		GuidanceStatus: model.GuidancePending,
	}
	s.completedTurns = response.Tonight.ConversationTurns
	if s.service.sleepAudio != nil && (guidance == "rain" || guidance == "breathing_46") {
		s.startSleepPlayback(guidance)
	}
	return nil
}

func (s *voiceSession) startSession(ctx context.Context) error {
	switch s.recovery.ResumeAction {
	case "wait_turn":
		go s.waitForRecovery("wait_turn")
		return nil
	case "wait_finish":
		go func() {
			finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.finishConversation(finishCtx, s.recovery.FinishEventID)
		}()
		return nil
	case "replay_reply":
		return s.replayReply(ctx, s.recovery.PendingTurnID, s.recoveryTurns)
	case "replay_guidance":
		return s.replayGuidance(ctx, s.recovery)
	case "done":
		return nil
	}
	history, err := s.service.conversation.History(ctx, s.userID)
	if err != nil {
		return err
	}
	if history.RunID.String() != s.runID || history.Recovery.RunStatus != model.ConversationRunActive {
		return s.sendVoiceError(ctx, voice.ErrorInvalidPhase, "当前 conversation run 不可启动", false, "")
	}
	switch history.Tonight.Phase {
	case string(state.Locked):
		if _, err := s.service.tonight.StartVoiceConversation(ctx, s.userID); err != nil {
			return s.sendVoiceError(ctx, voice.ErrorInvalidPhase, "当前状态无法开始语音会话", false, "")
		}
		s.startPlayback("opening", 0, "", s.service.openingText, nil)
		return nil
	case string(state.Conversation):
		if history.Tonight.ConversationTurns == 0 {
			s.startPlayback("opening", 0, "", s.service.openingText, nil)
		}
		return nil
	default:
		return s.sendVoiceError(ctx, voice.ErrorInvalidPhase, "当前状态无法开始语音会话", false, "")
	}
}

func (s *voiceSession) waitForRecovery(action string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-s.context.Done():
			return
		case <-timeout.C:
			_ = s.sendVoiceError(s.context, voice.ErrorServiceUnavailable, "恢复等待超时，请重新连接", true, "")
			return
		case <-ticker.C:
			history, err := s.service.conversation.History(s.context, s.userID)
			if err != nil {
				continue
			}
			recovery := history.Recovery
			if recovery.ResumeAction == action {
				continue
			}
			switch recovery.ResumeAction {
			case "replay_reply":
				_ = s.replayReply(s.context, recovery.PendingTurnID, history.Turns)
			case "replay_guidance":
				_ = s.replayGuidance(s.context, recovery)
			case "listen", "done":
			}
			return
		}
	}
}

func (s *voiceSession) replayReply(ctx context.Context, turnID string, turns []dto.ConversationTurn) error {
	var reply string
	var turnIndex int
	for _, turn := range turns {
		if turn.Role == "assistant" && turn.ClientRequestID == turnID {
			reply = turn.Text
			turnIndex = turn.TurnIndex
			break
		}
	}
	if reply == "" {
		return s.sendVoiceError(ctx, voice.ErrorServiceUnavailable, "无法恢复上一轮回复", true, turnID)
	}
	runID, err := uuid.Parse(s.runID)
	if err != nil {
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "当前会话缺少有效 runId", false, turnID)
	}
	s.startPlayback("reply", turnIndex, turnID, reply, func(playbackErr error) {
		if playbackErr != nil {
			return
		}
		completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.service.conversation.CompleteReplyDelivery(completeCtx, s.userID, runID, turnID)
	})
	return nil
}

func (s *voiceSession) replayGuidance(ctx context.Context, recovery voice.RecoveryState) error {
	if recovery.JournalID == "" || recovery.FinishEventID == "" || recovery.Guidance == "" {
		return s.sendVoiceError(ctx, voice.ErrorServiceUnavailable, "无法恢复今晚的引导", true, "")
	}
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventConversationComplete, RunID: s.runID, EventID: recovery.FinishEventID,
		CompletedTurns: s.completedTurns, JournalID: recovery.JournalID, Guidance: recovery.Guidance,
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}
	if s.service.sleepAudio != nil && (recovery.Guidance == "rain" || recovery.Guidance == "breathing_46") {
		s.startSleepPlayback(recovery.Guidance)
	}
	return nil
}

func (s *voiceSession) startInput(ctx context.Context, eventID, turnID string) error {
	if s.recovery.ResumeAction == "done" || s.recovery.ResumeAction == "replay_guidance" || s.recovery.ResumeAction == "wait_finish" {
		return s.sendVoiceError(ctx, voice.ErrorInvalidPhase, "当前 run 已结束，不再接收语音", false, turnID)
	}
	s.stopPlayback("user_interrupt")
	s.mu.Lock()
	if s.input != nil || s.currentTurnID != "" || s.processingTurnID != "" {
		s.mu.Unlock()
		return s.sendVoiceError(ctx, voice.ErrorTurnInProgress, "上一轮语音仍在处理中", true, turnID)
	}
	s.mu.Unlock()

	asr, err := s.service.asr.Open(ctx)
	if err != nil {
		return s.sendVoiceError(ctx, voice.ErrorASRUnavailable, "语音识别暂时不可用，请重新说一次", true, turnID)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = asr.Close()
		return context.Canceled
	}
	pumpCtx, cancel := context.WithTimeout(context.Background(), s.service.maxUtterance+2*time.Minute)
	input := &asrInputPump{
		asr: asr, items: make(chan asrInputItem, 256), cancel: cancel, done: make(chan struct{}),
	}
	s.input = input
	s.currentTurnID = turnID
	s.inputStartedAt = s.service.now()
	s.mu.Unlock()
	go s.runInputPump(pumpCtx, input, turnID)
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventInputAccepted, RunID: s.runID, EventID: eventID, TurnID: turnID,
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *voiceSession) endInput(ctx context.Context, turnID string) error {
	s.mu.Lock()
	if s.input == nil || s.currentTurnID == "" || s.currentTurnID != turnID {
		s.mu.Unlock()
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "input.end 与当前 turnId 不匹配", false, turnID)
	}
	input := s.input
	s.input = nil
	s.currentTurnID = ""
	s.processingTurnID = turnID
	s.mu.Unlock()

	select {
	case input.items <- asrInputItem{end: true}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.context.Done():
		return s.context.Err()
	}
}

func (s *voiceSession) runInputPump(ctx context.Context, input *asrInputPump, turnID string) {
	defer close(input.done)
	defer input.asr.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-input.items:
			if item.end {
				s.processInput(ctx, input.asr, turnID)
				return
			}
			if err := input.asr.AppendPCM(ctx, item.frame); err != nil {
				s.clearInputIfCurrent(input)
				_ = s.sendVoiceError(ctx, voice.ErrorASRUnavailable, "语音识别暂时不可用，请重新说一次", true, turnID)
				return
			}
		}
	}
}

func (s *voiceSession) processInput(processingCtx context.Context, asr speech.ASRSession, turnID string) {
	startedAt := s.service.now()
	defer s.finishProcessing(turnID)

	transcript, err := asr.Complete(processingCtx)
	if errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, speech.ErrEmptyTranscript) {
		s.logVoiceStage("asr_final", turnID, startedAt, "empty_transcript")
		_ = s.sendVoiceError(processingCtx, voice.ErrorEmptyTranscript, "没有识别到有效内容，请重新说一次", true, turnID)
		return
	}
	if err != nil {
		s.logVoiceStage("asr_final", turnID, startedAt, "asr_unavailable")
		_ = s.sendVoiceError(processingCtx, voice.ErrorASRUnavailable, "语音识别暂时不可用，请重新说一次", true, turnID)
		return
	}
	s.logVoiceStage("asr_final", turnID, startedAt, "completed")
	now := s.service.now().UTC().Format(time.RFC3339Nano)
	_ = s.output.SendEvent(s.context, voice.ServerEvent{
		Type: voice.EventTranscriptFinal, RunID: s.runID, TurnID: turnID, Text: transcript, OccurredAt: now,
	})
	_ = s.output.SendEvent(s.context, voice.ServerEvent{
		Type: voice.EventThinking, RunID: s.runID, TurnID: turnID, OccurredAt: now,
	})

	runID, err := uuid.Parse(s.runID)
	if err != nil {
		_ = s.sendVoiceError(s.context, voice.ErrorInvalidEvent, "当前会话缺少有效 runId", false, turnID)
		return
	}
	response, err := s.service.conversation.Turn(processingCtx, s.userID, dto.ConversationTurnRequest{
		RunID: runID, Text: transcript, InputMode: "voice", ClientRequestID: turnID,
	})
	if err != nil {
		_ = s.sendConversationError(s.context, err, turnID)
		return
	}
	if err := s.service.conversation.BeginPlayback(s.context, s.userID); err != nil {
		_ = s.sendConversationError(s.context, err, turnID)
		return
	}
	s.finishProcessing(turnID)
	s.startPlayback("reply", response.Tonight.ConversationTurns, turnID, response.Result.Reply, func(playbackErr error) {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.service.conversation.EndPlayback(endCtx, s.userID)
		if playbackErr == nil {
			_ = s.service.conversation.CompleteReplyDelivery(endCtx, s.userID, runID, turnID)
		}
	})
}

func (s *voiceSession) finishProcessing(turnID string) {
	s.mu.Lock()
	if s.processingTurnID == turnID {
		s.processingTurnID = ""
	}
	s.mu.Unlock()
}

func (s *voiceSession) logVoiceStage(stage, turnID string, startedAt time.Time, result string) {
	if s.service.logger != nil {
		s.service.logger.Info("voice stage completed", "deviceId", s.deviceID, "turnId", turnID, "stage", stage, "result", result, "durationMs", s.service.now().Sub(startedAt).Milliseconds())
	}
}

func (s *voiceSession) startSleepPlayback(guidance string) {
	s.stopPlayback("interrupted")
	ctx, cancel := context.WithCancel(s.context)
	playbackID := uuid.NewString()
	doneChannel := make(chan struct{})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return
	}
	s.cancelPlayback = cancel
	s.currentPlayback = playbackID
	s.playbackDone = doneChannel
	s.mu.Unlock()

	go func() {
		defer close(doneChannel)
		_ = s.streamSleepPlayback(ctx, playbackID, guidance)
		s.mu.Lock()
		if s.currentPlayback == playbackID {
			s.cancelPlayback = nil
			s.currentPlayback = ""
		}
		s.mu.Unlock()
	}()
}

func (s *voiceSession) streamSleepPlayback(ctx context.Context, playbackID, guidance string) (resultErr error) {
	runID, err := uuid.Parse(s.runID)
	if err != nil {
		return err
	}
	statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := s.service.conversation.UpdateGuidanceStatus(statusCtx, s.userID, runID, model.GuidancePlaying); err != nil {
		statusCancel()
		return err
	}
	statusCancel()
	s.recovery.GuidanceStatus = model.GuidancePlaying
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackStart, RunID: s.runID, PlaybackID: playbackID,
		Kind: "guidance", Guidance: guidance, Audio: voice.DefaultAudioFormat(),
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		s.logPlaybackStage(playbackID, "guidance", "", "start_failed", err)
		return err
	}
	s.logPlaybackStage(playbackID, "guidance", "", "started", nil)
	endReason := "failed"
	defer func() {
		finalStatus := model.GuidanceInterrupted
		if endReason == "completed" {
			finalStatus = model.GuidanceCompleted
		}
		statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
		statusErr := s.service.conversation.UpdateGuidanceStatus(statusCtx, s.userID, runID, finalStatus)
		statusCancel()
		if statusErr == nil {
			s.recovery.GuidanceStatus = finalStatus
			if finalStatus == model.GuidanceCompleted {
				s.recovery.ResumeAction = "done"
			} else {
				s.recovery.ResumeAction = "replay_guidance"
			}
		}
		if statusErr != nil && resultErr == nil {
			resultErr = statusErr
			endReason = "failed"
		}
		endErr := s.output.SendEvent(s.context, voice.ServerEvent{
			Type: voice.EventPlaybackEnd, RunID: s.runID, PlaybackID: playbackID,
			Reason: endReason, OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
		})
		s.logPlaybackStage(playbackID, "guidance", "", "ended:"+endReason, endErr)
	}()

	err = s.service.sleepAudio.Stream(ctx, guidance, func(frame []byte) error {
		if err := voice.ValidatePCMFrame(frame); err != nil {
			return err
		}
		return s.output.SendPCM(ctx, frame)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			endReason = "interrupted"
		}
		return err
	}
	endReason = "completed"
	return nil
}

func (s *voiceSession) startPlayback(kind string, turn int, turnID, text string, done func(error)) {
	s.stopPlayback("interrupted")
	ctx, cancel := context.WithCancel(s.context)
	playbackID := uuid.NewString()
	doneChannel := make(chan struct{})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return
	}
	s.cancelPlayback = cancel
	s.currentPlayback = playbackID
	s.playbackDone = doneChannel
	s.mu.Unlock()

	go func() {
		defer close(doneChannel)
		err := s.streamPlayback(ctx, playbackID, kind, turn, turnID, text)
		s.mu.Lock()
		if s.currentPlayback == playbackID {
			s.cancelPlayback = nil
			s.currentPlayback = ""
		}
		s.mu.Unlock()
		if done != nil {
			done(err)
		}
	}()
}

func (s *voiceSession) streamPlayback(ctx context.Context, playbackID, kind string, turn int, turnID, text string) (resultErr error) {
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackStart, RunID: s.runID, PlaybackID: playbackID, Kind: kind,
		TurnID: turnID, Audio: voice.DefaultAudioFormat(),
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		s.logPlaybackStage(playbackID, kind, turnID, "start_failed", err)
		return err
	}
	s.logPlaybackStage(playbackID, kind, turnID, "started", nil)
	endReason := "failed"
	var ttsUnavailable bool
	defer func() {
		endErr := s.output.SendEvent(s.context, voice.ServerEvent{
			Type: voice.EventPlaybackEnd, RunID: s.runID, PlaybackID: playbackID,
			TurnID: turnID, Reason: endReason, OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
		})
		s.logPlaybackStage(playbackID, kind, turnID, "ended:"+endReason, endErr)
		if ttsUnavailable {
			_ = s.sendVoiceError(s.context, voice.ErrorTTSUnavailable, "语音合成暂时不可用", true, turnID)
		}
	}()

	buffer := make([]byte, 0, voice.PCMFrameBytes*2)
	var outputErr error
	err := s.service.tts.Stream(ctx, text, func(chunk []byte) error {
		buffer = append(buffer, chunk...)
		for len(buffer) >= voice.PCMFrameBytes {
			frame := append([]byte(nil), buffer[:voice.PCMFrameBytes]...)
			if err := s.output.SendPCM(ctx, frame); err != nil {
				outputErr = err
				return err
			}
			buffer = buffer[voice.PCMFrameBytes:]
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			endReason = "interrupted"
		} else if outputErr == nil {
			ttsUnavailable = true
		}
		return err
	}
	if len(buffer) > 0 {
		frame := make([]byte, voice.PCMFrameBytes)
		copy(frame, buffer)
		if err := s.output.SendPCM(ctx, frame); err != nil {
			return err
		}
	}
	endReason = "completed"
	return nil
}

func (s *voiceSession) logPlaybackStage(playbackID, kind, turnID, result string, err error) {
	if s.service.logger == nil {
		return
	}
	errorCategory := ""
	if err != nil {
		errorCategory = "output_unavailable"
	}
	s.service.logger.Info("voice playback stage", "deviceId", s.deviceID, "turnId", turnID, "playbackId", playbackID, "kind", kind, "result", result, "errorCategory", errorCategory)
}

func (s *voiceSession) stopPlayback(reason string) {
	s.mu.Lock()
	cancel := s.cancelPlayback
	playbackID := s.currentPlayback
	s.cancelPlayback = nil
	s.currentPlayback = ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		_ = s.output.SendEvent(s.context, voice.ServerEvent{
			Type: voice.EventPlaybackStop, RunID: s.runID, PlaybackID: playbackID, Reason: reason,
			OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
		})
	}
}

func (s *voiceSession) clearInput() {
	s.mu.Lock()
	input := s.input
	s.input = nil
	s.currentTurnID = ""
	s.mu.Unlock()
	if input != nil {
		input.cancel()
	}
}

func (s *voiceSession) clearInputIfCurrent(input *asrInputPump) {
	s.mu.Lock()
	if s.input == input {
		s.input = nil
		s.currentTurnID = ""
	}
	s.mu.Unlock()
}

func (s *voiceSession) turnID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTurnID
}

type mappedConversationError struct {
	code      string
	message   string
	retryable bool
	internal  string
}

func mapConversationError(err error) mappedConversationError {
	mapped := mappedConversationError{
		code: voice.ErrorServiceUnavailable, message: "服务暂时不可用，请稍后再试", retryable: true,
		internal: "internal_error",
	}
	var serviceErr *Error
	if !errors.As(err, &serviceErr) {
		return mapped
	}
	mapped.internal = serviceErr.Code
	switch serviceErr.Code {
	case "invalid_transition":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorInvalidPhase, "当前状态无法继续语音会话", false
	case "conversation_limit":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorConversationLimit, "今晚的对话已经完成", false
	case "request_in_progress":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorTurnInProgress, "上一轮语音仍在处理中", true
	case "conversation_expired":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorConversationExpired, "今晚的倾诉时间已结束", false
	case "ai_error":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorAIUnavailable, "回复暂时生成失败，请稍后再试", true
	case "storage_error":
		mapped.code, mapped.message, mapped.retryable = voice.ErrorServiceUnavailable, "服务暂时不可用，请稍后再试", true
	}
	return mapped
}

func (s *voiceSession) sendConversationError(ctx context.Context, err error, turnID string) error {
	mapped := mapConversationError(err)
	if s.service.logger != nil {
		phase := ""
		var serviceErr *Error
		if errors.As(err, &serviceErr) && serviceErr.Details != nil {
			if value, ok := serviceErr.Details["phase"].(string); ok {
				phase = value
			}
		}
		s.service.logger.WarnContext(ctx, "voice turn failed", "turnId", turnID, "internalCode", mapped.internal, "deviceCode", mapped.code, "phase", phase, "retryable", mapped.retryable)
	}
	return s.sendVoiceError(ctx, mapped.code, mapped.message, mapped.retryable, turnID)
}

func (s *voiceSession) sendVoiceError(ctx context.Context, code, message string, retryable bool, turnID string) error {
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventError, RunID: s.runID, Code: code, Message: message,
		Retryable: retryable, TurnID: turnID, TerminalFor: "event",
		OccurredAt: s.service.now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *voiceSession) String() string {
	return fmt.Sprintf("voice session device=%s", s.deviceID)
}
