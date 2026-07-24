package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/google/uuid"
)

type VoiceConversation interface {
	History(ctx context.Context, userID string) (dto.ConversationHistoryResponse, error)
	Turn(ctx context.Context, userID string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error)
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
	openingText     string
	breathingScript string
	maxUtterance    time.Duration
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
) *VoiceSessionService {
	return &VoiceSessionService{
		conversation: conversation, tonight: tonight, asr: asr, tts: tts,
		openingText: openingText, breathingScript: breathingScript,
		maxUtterance: maxUtterance, now: time.Now,
	}
}

func (s *VoiceSessionService) NewSession(userID, deviceID string, output VoiceOutput) VoiceSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &voiceSession{
		service: s, userID: userID, deviceID: deviceID, output: output,
		context: ctx, cancel: cancel, playbackDone: make(chan struct{}),
	}
}

type voiceSession struct {
	service  *VoiceSessionService
	userID   string
	deviceID string
	output   VoiceOutput
	context  context.Context
	cancel   context.CancelFunc

	mu              sync.Mutex
	asr             speech.ASRSession
	currentTurnID   string
	inputStartedAt  time.Time
	cancelPlayback  context.CancelFunc
	currentPlayback string
	playbackDone    chan struct{}
	closed          bool
}

func (s *voiceSession) Ready(ctx context.Context) error {
	history, err := s.service.conversation.History(ctx, s.userID)
	if err != nil {
		return err
	}
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventSessionReady, Phase: history.Tonight.Phase,
		CompletedTurns: history.Tonight.ConversationTurns, Audio: voice.DefaultAudioFormat(),
	})
}

func (s *voiceSession) HandleEvent(ctx context.Context, event voice.ClientEvent) error {
	switch event.Type {
	case voice.EventSessionStart:
		return s.startSession(ctx)
	case voice.EventInputStart:
		return s.startInput(ctx, event.TurnID)
	case voice.EventInputEnd:
		return s.endInput(ctx, event.TurnID)
	case voice.EventPlaybackStop:
		s.stopPlayback("user_interrupt")
		return nil
	default:
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "不支持的语音事件", false, event.TurnID)
	}
}

func (s *voiceSession) HandlePCM(ctx context.Context, frame []byte) error {
	if err := voice.ValidatePCMFrame(frame); err != nil {
		return s.sendVoiceError(ctx, voice.ErrorInvalidAudioFrame, "音频帧必须为 960 bytes", true, s.turnID())
	}
	s.mu.Lock()
	asr := s.asr
	startedAt := s.inputStartedAt
	turnID := s.currentTurnID
	s.mu.Unlock()
	if asr == nil || turnID == "" {
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "当前没有正在接收的语音", false, "")
	}
	if s.service.now().Sub(startedAt) > s.service.maxUtterance {
		s.clearInput()
		return s.sendVoiceError(ctx, voice.ErrorTurnTooLong, "单次说话不能超过 60 秒", true, turnID)
	}
	if err := asr.AppendPCM(ctx, frame); err != nil {
		s.clearInput()
		return s.sendVoiceError(ctx, voice.ErrorASRUnavailable, "语音识别暂时不可用，请重新说一次", true, turnID)
	}
	return nil
}

func (s *voiceSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	asr := s.asr
	s.asr = nil
	cancelPlayback := s.cancelPlayback
	s.cancelPlayback = nil
	s.mu.Unlock()
	s.cancel()
	if cancelPlayback != nil {
		cancelPlayback()
	}
	if asr != nil {
		return asr.Close()
	}
	return nil
}

func (s *voiceSession) startSession(ctx context.Context) error {
	history, err := s.service.conversation.History(ctx, s.userID)
	if err != nil {
		return err
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

func (s *voiceSession) startInput(ctx context.Context, turnID string) error {
	s.stopPlayback("user_interrupt")
	s.mu.Lock()
	if s.asr != nil || s.currentTurnID != "" {
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
	s.asr = asr
	s.currentTurnID = turnID
	s.inputStartedAt = s.service.now()
	s.mu.Unlock()
	return s.output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventInputAccepted, TurnID: turnID})
}

func (s *voiceSession) endInput(ctx context.Context, turnID string) error {
	s.mu.Lock()
	if s.asr == nil || s.currentTurnID == "" || s.currentTurnID != turnID {
		s.mu.Unlock()
		return s.sendVoiceError(ctx, voice.ErrorInvalidEvent, "input.end 与当前 turnId 不匹配", false, turnID)
	}
	asr := s.asr
	s.asr = nil
	s.currentTurnID = ""
	s.mu.Unlock()
	defer asr.Close()

	transcript, err := asr.Complete(ctx)
	if errors.Is(err, speech.ErrEmptyTranscript) {
		return s.sendVoiceError(ctx, voice.ErrorEmptyTranscript, "没有识别到有效内容，请重新说一次", true, turnID)
	}
	if err != nil {
		return s.sendVoiceError(ctx, voice.ErrorASRUnavailable, "语音识别暂时不可用，请重新说一次", true, turnID)
	}
	if err := s.output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventTranscriptFinal, TurnID: turnID, Text: transcript}); err != nil {
		return err
	}
	if err := s.output.SendEvent(ctx, voice.ServerEvent{Type: voice.EventThinking, TurnID: turnID}); err != nil {
		return err
	}

	response, err := s.service.conversation.Turn(ctx, s.userID, dto.ConversationTurnRequest{
		Text: transcript, InputMode: "voice", ClientRequestID: turnID,
	})
	if err != nil {
		var serviceErr *Error
		if errors.As(err, &serviceErr) && serviceErr.Code == "conversation_limit" {
			return s.sendVoiceError(ctx, voice.ErrorConversationLimit, "今晚的对话已经完成", false, turnID)
		}
		return s.sendVoiceError(ctx, voice.ErrorAIUnavailable, "回复暂时生成失败，请稍后再试", true, turnID)
	}
	completed := response.Journal != nil || response.Tonight.ConversationTurns >= 3
	s.startPlayback("reply", response.Tonight.ConversationTurns, turnID, response.Result.Reply, func(playbackErr error) {
		if playbackErr == nil && completed {
			s.completeConversation(response)
		}
	})
	return nil
}

func (s *voiceSession) completeConversation(response dto.ConversationTurnResponse) {
	guidance := response.Result.SuggestedGuidance
	ctx, cancel := context.WithTimeout(s.context, 10*time.Second)
	defer cancel()
	if _, err := s.service.tonight.SelectVoiceGuidance(ctx, s.userID, guidance); err != nil {
		_ = s.sendVoiceError(ctx, voice.ErrorInvalidPhase, "无法开始今晚的引导", false, "")
		return
	}
	journalID := ""
	if response.Journal != nil {
		journalID = response.Journal.ID.String()
	}
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventConversationComplete, CompletedTurns: response.Tonight.ConversationTurns,
		JournalID: journalID, Guidance: guidance,
	}); err != nil {
		return
	}
	switch guidance {
	case "rain", "brown_noise":
		_ = s.output.SendEvent(ctx, voice.ServerEvent{
			Type: voice.EventGuidanceStart, Guidance: guidance, Source: "device",
			DurationMinutes: response.Tonight.WhiteNoiseDurationMin,
		})
	case "breathing_46":
		s.startPlayback("guidance", 0, "", s.service.breathingScript, nil)
	case "silence":
	}
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

func (s *voiceSession) streamPlayback(ctx context.Context, playbackID, kind string, turn int, turnID, text string) error {
	if err := s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackStart, PlaybackID: playbackID, Kind: kind,
		Turn: turn, TurnID: turnID, Text: text,
	}); err != nil {
		return err
	}
	buffer := make([]byte, 0, voice.PCMFrameBytes*2)
	err := s.service.tts.Stream(ctx, text, func(chunk []byte) error {
		buffer = append(buffer, chunk...)
		for len(buffer) >= voice.PCMFrameBytes {
			frame := append([]byte(nil), buffer[:voice.PCMFrameBytes]...)
			if err := s.output.SendPCM(ctx, frame); err != nil {
				return err
			}
			buffer = buffer[voice.PCMFrameBytes:]
		}
		return nil
	})
	if err != nil {
		reason := "upstream_error"
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			reason = "interrupted"
		}
		_ = s.output.SendEvent(context.Background(), voice.ServerEvent{
			Type: voice.EventPlaybackEnd, PlaybackID: playbackID, Reason: reason,
		})
		if reason == "upstream_error" {
			_ = s.sendVoiceError(context.Background(), voice.ErrorTTSUnavailable, "语音合成暂时不可用", true, turnID)
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
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventPlaybackEnd, PlaybackID: playbackID, Reason: "completed",
	})
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
		_ = s.output.SendEvent(context.Background(), voice.ServerEvent{
			Type: voice.EventPlaybackStop, PlaybackID: playbackID, Reason: reason,
		})
	}
}

func (s *voiceSession) clearInput() {
	s.mu.Lock()
	asr := s.asr
	s.asr = nil
	s.currentTurnID = ""
	s.mu.Unlock()
	if asr != nil {
		_ = asr.Close()
	}
}

func (s *voiceSession) turnID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTurnID
}

func (s *voiceSession) sendVoiceError(ctx context.Context, code, message string, retryable bool, turnID string) error {
	return s.output.SendEvent(ctx, voice.ServerEvent{
		Type: voice.EventError, Code: code, Message: message, Retryable: retryable, TurnID: turnID,
	})
}

func (s *voiceSession) String() string {
	return fmt.Sprintf("voice session device=%s", s.deviceID)
}
