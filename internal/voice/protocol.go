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
	EventConversationComplete = "conversation.completed"
	EventGuidanceStart        = "guidance.start"
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
}

type ClientEvent struct {
	Type    string `json:"type"`
	EventID string `json:"eventId"`
	TurnID  string `json:"turnId,omitempty"`
}

type ServerEvent struct {
	Type            string       `json:"type"`
	Phase           string       `json:"phase,omitempty"`
	CompletedTurns  int          `json:"completedTurns,omitempty"`
	Audio           *AudioFormat `json:"audio,omitempty"`
	TurnID          string       `json:"turnId,omitempty"`
	PlaybackID      string       `json:"playbackId,omitempty"`
	Kind            string       `json:"kind,omitempty"`
	Turn            int          `json:"turn,omitempty"`
	Text            string       `json:"text,omitempty"`
	Reason          string       `json:"reason,omitempty"`
	JournalID       string       `json:"journalId,omitempty"`
	Guidance        string       `json:"guidance,omitempty"`
	Source          string       `json:"source,omitempty"`
	DurationMinutes int          `json:"durationMinutes,omitempty"`
	Code            string       `json:"code,omitempty"`
	Message         string       `json:"message,omitempty"`
	Retryable       bool         `json:"retryable,omitempty"`
}

func DefaultAudioFormat() *AudioFormat {
	return &AudioFormat{
		Codec: "pcm", SampleRate: PCMSampleRate, BitDepth: PCMBitDepth,
		Channels: PCMChannels, FrameMS: PCMFrameMS,
	}
}

func DecodeClientEvent(payload []byte) (ClientEvent, error) {
	var event ClientEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return ClientEvent{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEvent)
	}
	event.Type = strings.TrimSpace(event.Type)
	event.EventID = strings.TrimSpace(event.EventID)
	event.TurnID = strings.TrimSpace(event.TurnID)
	if event.EventID == "" {
		return ClientEvent{}, fmt.Errorf("%w: eventId is required", ErrInvalidEvent)
	}
	switch event.Type {
	case EventSessionStart, EventPlaybackStop:
	case EventInputStart, EventInputEnd:
		if event.TurnID == "" {
			return ClientEvent{}, fmt.Errorf("%w: turnId is required for %s", ErrInvalidEvent, event.Type)
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
