package dto

import (
	"encoding/json"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/google/uuid"
)

type Profile struct {
	Bedtime                string `json:"bedtime"`
	WakeTime               string `json:"wakeTime"`
	Persona                string `json:"persona"`
	ReminderStyle          string `json:"reminderStyle"`
	DefaultGuidance        string `json:"defaultGuidance"`
	WhiteNoiseDurationMin  int    `json:"whiteNoiseDurationMin"`
	TimeZone               string `json:"timeZone"`
	BedtimeReminderEnabled bool   `json:"bedtimeReminderEnabled"`
	WakeAlarmEnabled       bool   `json:"wakeAlarmEnabled"`
}

type UpdateProfileRequest struct {
	Bedtime                *string `json:"bedtime"`
	WakeTime               *string `json:"wakeTime"`
	Persona                *string `json:"persona"`
	ReminderStyle          *string `json:"reminderStyle"`
	DefaultGuidance        *string `json:"defaultGuidance"`
	WhiteNoiseDurationMin  *int    `json:"whiteNoiseDurationMin"`
	TimeZone               *string `json:"timeZone"`
	BedtimeReminderEnabled *bool   `json:"bedtimeReminderEnabled"`
	WakeAlarmEnabled       *bool   `json:"wakeAlarmEnabled"`
}

type TonightState struct {
	ID                            uuid.UUID       `json:"id"`
	Date                          string          `json:"date"`
	Phase                         string          `json:"phase"`
	Bedtime                       string          `json:"bedtime"`
	WakeTime                      string          `json:"wakeTime"`
	WhiteNoiseDurationMin         int             `json:"whiteNoiseDurationMin"`
	ConversationTurns             int             `json:"conversationTurns"`
	SelectedGuidance              string          `json:"selectedGuidance,omitempty"`
	AudioPlaying                  bool            `json:"audioPlaying"`
	PausedForTonight              bool            `json:"pausedForTonight"`
	RemindersSkipped              bool            `json:"remindersSkipped"`
	FinalizeReason                string          `json:"finalizeReason,omitempty"`
	ConversationStartedAt         *time.Time      `json:"conversationStartedAt,omitempty"`
	ConversationSilenceDeadlineAt *time.Time      `json:"conversationSilenceDeadlineAt,omitempty"`
	ConversationHardDeadlineAt    *time.Time      `json:"conversationHardDeadlineAt,omitempty"`
	ConversationProcessingUntil   *time.Time      `json:"conversationProcessingUntil,omitempty"`
	PhoneRemovedAt                *time.Time      `json:"phoneRemovedAt,omitempty"`
	ResumeDeadlineAt              *time.Time      `json:"resumeDeadlineAt,omitempty"`
	AudioEndsAt                   *time.Time      `json:"audioEndsAt,omitempty"`
	Device                        DeviceState     `json:"device"`
	Sunrise                       SunriseState    `json:"sunrise"`
	LatestAIDraft                 json.RawMessage `json:"latestAIDraft,omitempty"`
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
	Text            string `json:"text" binding:"required"`
	InputMode       string `json:"inputMode"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
}

type ConversationActivityRequest struct {
	Activity string `json:"activity"`
}

type ConversationTurn struct {
	ID              uuid.UUID `json:"id"`
	Role            string    `json:"role"`
	Text            string    `json:"text"`
	TurnIndex       int       `json:"turnIndex"`
	Fallback        bool      `json:"fallback"`
	InputMode       string    `json:"inputMode"`
	ClientRequestID string    `json:"clientRequestId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

type ConversationHistoryResponse struct {
	Turns          []ConversationTurn `json:"turns"`
	Tonight        TonightState       `json:"tonight"`
	RemainingTurns int                `json:"remainingTurns"`
	Processing     bool               `json:"processing"`
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
	ID                      uuid.UUID  `json:"id"`
	Date                    string     `json:"date"`
	Emotion                 string     `json:"emotion"`
	Worry                   string     `json:"worry"`
	TomorrowTask            string     `json:"tomorrowTask"`
	Comfort                 string     `json:"comfort"`
	SuggestedGuidance       string     `json:"suggestedGuidance"`
	Fallback                bool       `json:"fallback"`
	TomorrowTaskCompleted   bool       `json:"tomorrowTaskCompleted"`
	TomorrowTaskCompletedAt *time.Time `json:"tomorrowTaskCompletedAt,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
}

type UpdateMemoryCardRequest struct {
	TomorrowTaskCompleted *bool `json:"tomorrowTaskCompleted" binding:"required"`
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

type DeviceHeartbeatRequest struct {
	DeviceID        string         `json:"deviceId" binding:"required"`
	UserID          string         `json:"userId,omitempty"`
	FirmwareVersion string         `json:"firmwareVersion,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	Status          map[string]any `json:"status,omitempty"`
	LocalTime       *time.Time     `json:"localTime,omitempty"`
}

type DeviceStatus struct {
	DeviceID        string          `json:"deviceId"`
	UserID          string          `json:"userId"`
	FirmwareVersion string          `json:"firmwareVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	Status          json.RawMessage `json:"status"`
	LocalTime       *time.Time      `json:"localTime,omitempty"`
	LastSeenAt      time.Time       `json:"lastSeenAt"`
	Online          bool            `json:"online"`
}

type Command struct {
	ID             uuid.UUID       `json:"id"`
	DeviceID       string          `json:"deviceId"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	Attempt        int             `json:"attempt"`
	LeaseExpiresAt *time.Time      `json:"leaseExpiresAt,omitempty"`
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
	return Profile{
		Bedtime: value.Bedtime, WakeTime: value.WakeTime, Persona: value.Persona,
		ReminderStyle: value.ReminderStyle, DefaultGuidance: value.DefaultGuidance,
		WhiteNoiseDurationMin: value.WhiteNoiseDurationMin, TimeZone: value.TimeZone,
		BedtimeReminderEnabled: value.BedtimeReminderEnabled, WakeAlarmEnabled: value.WakeAlarmEnabled,
	}
}

func TonightFromModels(session *model.NightSession, profile *model.Profile) TonightState {
	return TonightState{
		ID:                    session.ID,
		Date:                  session.Date.Format("2006-01-02"),
		Phase:                 session.Phase,
		Bedtime:               profile.Bedtime,
		WakeTime:              profile.WakeTime,
		WhiteNoiseDurationMin: profile.WhiteNoiseDurationMin,
		ConversationTurns:     session.ConversationTurns,
		SelectedGuidance:      session.SelectedGuidance,
		AudioPlaying:          session.AudioPlaying,
		PausedForTonight:      session.PausedForTonight, RemindersSkipped: session.RemindersSkipped,
		FinalizeReason: session.FinalizeReason, ConversationStartedAt: session.ConversationStartedAt,
		ConversationSilenceDeadlineAt: session.ConversationSilenceDeadlineAt,
		ConversationHardDeadlineAt:    session.ConversationHardDeadlineAt,
		ConversationProcessingUntil:   session.ConversationProcessingUntil,
		PhoneRemovedAt:                session.PhoneRemovedAt, ResumeDeadlineAt: session.ResumeDeadlineAt,
		AudioEndsAt:   session.AudioEndsAt,
		Device:        DeviceState{BoxClosed: session.BoxClosed},
		Sunrise:       SunriseState{Progress: session.SunriseProgress},
		LatestAIDraft: json.RawMessage(session.LatestAIDraft),
	}
}

func MemoryCardFromModel(value *model.MemoryCard) MemoryCard {
	return MemoryCard{
		ID: value.ID, Date: value.Date.Format("2006-01-02"), Emotion: value.Emotion,
		Worry: value.Worry, TomorrowTask: value.TomorrowTask, Comfort: value.Comfort,
		SuggestedGuidance: value.SuggestedGuidance, Fallback: value.Fallback,
		TomorrowTaskCompleted:   value.TomorrowTaskCompleted,
		TomorrowTaskCompletedAt: value.TomorrowTaskCompletedAt, CreatedAt: value.CreatedAt,
	}
}

func CommandFromModel(value *model.DeviceCommand) Command {
	return Command{
		ID: value.ID, DeviceID: value.DeviceID, Type: value.Type,
		Payload: json.RawMessage(value.Payload), Status: value.Status, CreatedAt: value.CreatedAt,
		Attempt: value.DispatchAttempts, LeaseExpiresAt: value.LeaseExpiresAt,
	}
}

func ConversationTurnFromModel(value *model.ConversationTurn) ConversationTurn {
	clientRequestID := ""
	if value.ClientRequestID != nil {
		clientRequestID = *value.ClientRequestID
	}
	return ConversationTurn{
		ID: value.ID, Role: value.Role, Text: value.Text, TurnIndex: value.TurnIndex,
		Fallback: value.Fallback, InputMode: value.InputMode, ClientRequestID: clientRequestID,
		CreatedAt: value.CreatedAt,
	}
}

func DeviceStatusFromModel(value *model.Device, onlineWindow time.Duration, now time.Time) DeviceStatus {
	return DeviceStatus{
		DeviceID: value.DeviceID, UserID: value.UserID, FirmwareVersion: value.FirmwareVersion,
		Capabilities: json.RawMessage(value.Capabilities), Status: json.RawMessage(value.Status),
		LocalTime: value.LocalTime, LastSeenAt: value.LastSeenAt,
		Online: now.Sub(value.LastSeenAt) <= onlineWindow,
	}
}
