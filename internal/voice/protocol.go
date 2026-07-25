package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	PCMSampleRate = 24000
	PCMBitDepth   = 16
	PCMChannels   = 1
	PCMFrameMS    = 20
	PCMFrameBytes = 960
)

const (
	EventSessionStart         = "session.start"
	EventInputStart           = "input.start"
	EventInputEnd             = "input.end"
	EventPlaybackStop         = "playback.stop"
	EventSessionReady         = "session.ready"
	EventInputAccepted        = "input.accepted"
	EventTranscriptFinal      = "transcript.final"
	EventThinking             = "thinking"
	EventPlaybackStart        = "playback.start"
	EventPlaybackEnd          = "playback.end"
	EventConversationFinish   = "conversation.finish"
	EventConversationComplete = "conversation.completed"
	EventError                = "error"
)

const (
	ErrorSpeechNotConfigured = "speech_not_configured"
	ErrorInvalidPhase        = "invalid_phase"
	ErrorInvalidEvent        = "invalid_event"
	ErrorInvalidAudioFrame   = "invalid_audio_frame"
	ErrorTurnInProgress      = "turn_in_progress"
	ErrorTurnTooLong         = "turn_too_long"
	ErrorConversationLimit   = "conversation_limit"
	ErrorConversationExpired = "conversation_expired"
	ErrorServiceUnavailable  = "service_unavailable"
	ErrorASRUnavailable      = "asr_unavailable"
	ErrorEmptyTranscript     = "empty_transcript"
	ErrorAIUnavailable       = "ai_unavailable"
	ErrorTTSUnavailable      = "tts_unavailable"
	ErrorDeviceTooSlow       = "device_too_slow"
)

var (
	ErrInvalidEvent      = errors.New("invalid voice event")
	ErrInvalidAudioFrame = errors.New("invalid PCM audio frame")
)

type AudioFormat struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sampleRate"`
	BitDepth   int    `json:"bitDepth"`
	Channels   int    `json:"channels"`
	FrameMS    int    `json:"frameMs"`
	FrameBytes int    `json:"frameBytes"`
}

type ClientEvent struct {
	Type       string `json:"type"`
	RunID      string `json:"runId"`
	EventID    string `json:"eventId"`
	TurnID     string `json:"turnId,omitempty"`
	PlaybackID string `json:"playbackId,omitempty"`
}

type RecoveryState struct {
	RunStatus           string `json:"runStatus"`
	ResumeAction        string `json:"resumeAction"`
	PendingTurnID       string `json:"pendingTurnId,omitempty"`
	LastCompletedTurnID string `json:"lastCompletedTurnId,omitempty"`
	FinishEventID       string `json:"finishEventId,omitempty"`
	JournalID           string `json:"journalId,omitempty"`
	Guidance            string `json:"guidance,omitempty"`
	GuidanceStatus      string `json:"guidanceStatus,omitempty"`
}

type ServerEvent struct {
	Type            string         `json:"type"`
	RunID           string         `json:"runId,omitempty"`
	EventID         string         `json:"eventId,omitempty"`
	Phase           string         `json:"phase,omitempty"`
	CompletedTurns  int            `json:"completedTurns,omitempty"`
	Audio           *AudioFormat   `json:"audio,omitempty"`
	Recovery        *RecoveryState `json:"recovery,omitempty"`
	TurnID          string         `json:"turnId,omitempty"`
	PlaybackID      string         `json:"playbackId,omitempty"`
	Kind            string         `json:"kind,omitempty"`
	Turn            int            `json:"turn,omitempty"`
	Text            string         `json:"text,omitempty"`
	Reason          string         `json:"reason,omitempty"`
	JournalID       string         `json:"journalId,omitempty"`
	Guidance        string         `json:"guidance,omitempty"`
	Source          string         `json:"source,omitempty"`
	DurationMinutes int            `json:"durationMinutes,omitempty"`
	Code            string         `json:"code,omitempty"`
	Message         string         `json:"message,omitempty"`
	Retryable       bool           `json:"retryable,omitempty"`
	RetryAfterMS    int            `json:"retryAfterMs,omitempty"`
	TerminalFor     string         `json:"terminalFor,omitempty"`
	OccurredAt      string         `json:"occurredAt,omitempty"`
}

type serverEventJSON ServerEvent

func (e ServerEvent) MarshalJSON() ([]byte, error) {
	switch e.Type {
	case EventSessionReady, EventConversationComplete:
		return json.Marshal(struct {
			serverEventJSON
			CompletedTurns int `json:"completedTurns"`
		}{
			serverEventJSON: serverEventJSON(e),
			CompletedTurns:  e.CompletedTurns,
		})
	case EventError:
		return json.Marshal(struct {
			serverEventJSON
			Retryable bool `json:"retryable"`
		}{
			serverEventJSON: serverEventJSON(e),
			Retryable:       e.Retryable,
		})
	default:
		return json.Marshal(serverEventJSON(e))
	}
}

func DefaultAudioFormat() *AudioFormat {
	return &AudioFormat{
		Codec: "pcm", SampleRate: PCMSampleRate, BitDepth: PCMBitDepth,
		Channels: PCMChannels, FrameMS: PCMFrameMS, FrameBytes: PCMFrameBytes,
	}
}

func DecodeClientEvent(payload []byte) (ClientEvent, error) {
	var event ClientEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return ClientEvent{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEvent)
	}
	event.Type = strings.TrimSpace(event.Type)
	event.RunID = strings.TrimSpace(event.RunID)
	event.EventID = strings.TrimSpace(event.EventID)
	event.TurnID = strings.TrimSpace(event.TurnID)
	event.PlaybackID = strings.TrimSpace(event.PlaybackID)
	if event.RunID == "" {
		return ClientEvent{}, fmt.Errorf("%w: runId is required", ErrInvalidEvent)
	}
	if event.EventID == "" {
		return ClientEvent{}, fmt.Errorf("%w: eventId is required", ErrInvalidEvent)
	}
	switch event.Type {
	case EventSessionStart, EventConversationFinish:
	case EventInputStart, EventInputEnd:
		if event.TurnID == "" {
			return ClientEvent{}, fmt.Errorf("%w: turnId is required for %s", ErrInvalidEvent, event.Type)
		}
	case EventPlaybackStop:
		if event.PlaybackID == "" {
			return ClientEvent{}, fmt.Errorf("%w: playbackId is required for %s", ErrInvalidEvent, event.Type)
		}
	default:
		return ClientEvent{}, fmt.Errorf("%w: unsupported type", ErrInvalidEvent)
	}
	return event, nil
}

func ValidatePCMFrame(frame []byte) error {
	if len(frame) != PCMFrameBytes {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidAudioFrame, len(frame), PCMFrameBytes)
	}
	return nil
}
