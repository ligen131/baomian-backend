package dto

import (
	"encoding/json"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/google/uuid"
)

type Profile struct {
	Bedtime               string `json:"bedtime"`
	WakeTime              string `json:"wakeTime"`
	Persona               string `json:"persona"`
	ReminderStyle         string `json:"reminderStyle"`
	DefaultGuidance       string `json:"defaultGuidance"`
	WhiteNoiseDurationMin int    `json:"whiteNoiseDurationMin"`
}

type UpdateProfileRequest = Profile

type TonightState struct {
	ID                uuid.UUID       `json:"id"`
	Date              string          `json:"date"`
	Phase             string          `json:"phase"`
	Bedtime           string          `json:"bedtime"`
	WakeTime          string          `json:"wakeTime"`
	ConversationTurns int             `json:"conversationTurns"`
	SelectedGuidance  string          `json:"selectedGuidance,omitempty"`
	AudioPlaying      bool            `json:"audioPlaying"`
	PausedForTonight  bool            `json:"pausedForTonight"`
	Device            DeviceState     `json:"device"`
	Sunrise           SunriseState    `json:"sunrise"`
	LatestAIDraft     json.RawMessage `json:"latestAIDraft,omitempty"`
}

type DeviceState struct {
	BoxClosed bool `json:"boxClosed"`
}

type SunriseState struct {
	Progress int `json:"progress"`
}

type TonightActionRequest struct {
	Action   string         `json:"action" binding:"required"`
	Guidance string         `json:"guidance,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type ConversationTurnRequest struct {
	Text      string `json:"text" binding:"required"`
	InputMode string `json:"inputMode"`
}

type AIResult struct {
	Reply             string   `json:"reply"`
	Emotion           string   `json:"emotion"`
	Worry             string   `json:"worry"`
	TomorrowTask      string   `json:"tomorrowTask"`
	Comfort           string   `json:"comfort"`
	GuidanceOptions   []string `json:"guidanceOptions"`
	SuggestedGuidance string   `json:"suggestedGuidance"`
	ShouldFinalize    bool     `json:"shouldFinalize"`
	Fallback          bool     `json:"fallback"`
	HighRisk          bool     `json:"highRisk,omitempty"`
}

type ConversationTurnResponse struct {
	Result  AIResult     `json:"result"`
	Tonight TonightState `json:"tonight"`
	Journal *MemoryCard  `json:"journal,omitempty"`
}

type FinalizeResponse struct {
	Journal MemoryCard   `json:"journal"`
	Tonight TonightState `json:"tonight"`
}

type MemoryCard struct {
	ID                uuid.UUID `json:"id"`
	Date              string    `json:"date"`
	Emotion           string    `json:"emotion"`
	Worry             string    `json:"worry"`
	TomorrowTask      string    `json:"tomorrowTask"`
	Comfort           string    `json:"comfort"`
	SuggestedGuidance string    `json:"suggestedGuidance"`
	Fallback          bool      `json:"fallback"`
	CreatedAt         time.Time `json:"createdAt"`
}

type DeviceEventRequest struct {
	EventID    string         `json:"eventId" binding:"required"`
	DeviceID   string         `json:"deviceId" binding:"required"`
	UserID     string         `json:"userId,omitempty"`
	Type       string         `json:"type" binding:"required"`
	Payload    map[string]any `json:"payload,omitempty"`
	OccurredAt *time.Time     `json:"occurredAt,omitempty"`
}

type DeviceEventResponse struct {
	Duplicate bool         `json:"duplicate"`
	Tonight   TonightState `json:"tonight"`
	Commands  []Command    `json:"commands"`
}

type Command struct {
	ID        uuid.UUID       `json:"id"`
	DeviceID  string          `json:"deviceId"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"createdAt"`
}

type CommandAckRequest struct {
	DeviceID  string         `json:"deviceId" binding:"required"`
	CommandID uuid.UUID      `json:"commandId" binding:"required"`
	Success   bool           `json:"success"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type WSEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	Data       any       `json:"data"`
}

func ProfileFromModel(value *model.Profile) Profile {
	return Profile{value.Bedtime, value.WakeTime, value.Persona, value.ReminderStyle, value.DefaultGuidance, value.WhiteNoiseDurationMin}
}

func TonightFromModels(session *model.NightSession, profile *model.Profile) TonightState {
	return TonightState{
		ID:                session.ID,
		Date:              session.Date.Format("2006-01-02"),
		Phase:             session.Phase,
		Bedtime:           profile.Bedtime,
		WakeTime:          profile.WakeTime,
		ConversationTurns: session.ConversationTurns,
		SelectedGuidance:  session.SelectedGuidance,
		AudioPlaying:      session.AudioPlaying,
		PausedForTonight:  session.PausedForTonight,
		Device:            DeviceState{BoxClosed: session.BoxClosed},
		Sunrise:           SunriseState{Progress: session.SunriseProgress},
		LatestAIDraft:     json.RawMessage(session.LatestAIDraft),
	}
}

func MemoryCardFromModel(value *model.MemoryCard) MemoryCard {
	return MemoryCard{
		ID: value.ID, Date: value.Date.Format("2006-01-02"), Emotion: value.Emotion,
		Worry: value.Worry, TomorrowTask: value.TomorrowTask, Comfort: value.Comfort,
		SuggestedGuidance: value.SuggestedGuidance, Fallback: value.Fallback, CreatedAt: value.CreatedAt,
	}
}

func CommandFromModel(value *model.DeviceCommand) Command {
	return Command{value.ID, value.DeviceID, value.Type, json.RawMessage(value.Payload), value.Status, value.CreatedAt}
}
