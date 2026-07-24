package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type Profile struct {
	ID                     uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID                 string    `gorm:"uniqueIndex;not null"`
	Bedtime                string    `gorm:"not null"`
	WakeTime               string    `gorm:"not null"`
	Persona                string    `gorm:"not null"`
	ReminderStyle          string    `gorm:"not null"`
	DefaultGuidance        string    `gorm:"not null"`
	WhiteNoiseDurationMin  int       `gorm:"not null"`
	TimeZone               string    `gorm:"not null"`
	BedtimeReminderEnabled bool      `gorm:"not null"`
	WakeAlarmEnabled       bool      `gorm:"not null"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type NightSession struct {
	ID                            uuid.UUID      `gorm:"type:uuid;primaryKey"`
	UserID                        string         `gorm:"uniqueIndex:idx_night_user_date;not null"`
	Date                          time.Time      `gorm:"type:date;uniqueIndex:idx_night_user_date;not null"`
	Phase                         string         `gorm:"not null"`
	ResumePhase                   string         `gorm:"not null;default:''"`
	BoxClosed                     bool           `gorm:"not null;default:false"`
	ConversationTurns             int            `gorm:"not null;default:0"`
	SelectedGuidance              string         `gorm:"not null;default:''"`
	AudioPlaying                  bool           `gorm:"not null;default:false"`
	SunriseProgress               int            `gorm:"not null;default:0"`
	PausedForTonight              bool           `gorm:"not null;default:false"`
	LatestAIDraft                 datatypes.JSON `gorm:"column:latest_ai_draft;type:jsonb;not null;default:'{}'"`
	RemindersSkipped              bool           `gorm:"not null;default:false"`
	FinalizeReason                string         `gorm:"not null;default:''"`
	ConversationStartedAt         *time.Time
	ConversationLastActivityAt    *time.Time
	ConversationSilenceDeadlineAt *time.Time
	ConversationHardDeadlineAt    *time.Time
	ConversationProcessingUntil   *time.Time
	PhoneRemovedAt                *time.Time
	ResumeDeadlineAt              *time.Time
	AudioEndsAt                   *time.Time
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
}

type ConversationTurn struct {
	ID              uuid.UUID      `gorm:"type:uuid;primaryKey"`
	SessionID       uuid.UUID      `gorm:"type:uuid;index;not null"`
	Role            string         `gorm:"not null"`
	Text            string         `gorm:"type:text;not null"`
	TurnIndex       int            `gorm:"not null"`
	Fallback        bool           `gorm:"not null;default:false"`
	InputMode       string         `gorm:"not null;default:'text'"`
	ClientRequestID *string        `gorm:"index"`
	Result          datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt       time.Time
}

type MemoryCard struct {
	ID                      uuid.UUID `gorm:"type:uuid;primaryKey"`
	SessionID               uuid.UUID `gorm:"type:uuid;uniqueIndex;not null"`
	UserID                  string    `gorm:"index;not null"`
	Date                    time.Time `gorm:"type:date;index;not null"`
	Emotion                 string    `gorm:"not null"`
	Worry                   string    `gorm:"type:text;not null"`
	TomorrowTask            string    `gorm:"type:text;not null"`
	Comfort                 string    `gorm:"type:text;not null"`
	SuggestedGuidance       string    `gorm:"not null"`
	Fallback                bool      `gorm:"not null;default:false"`
	TomorrowTaskCompleted   bool      `gorm:"not null;default:false"`
	TomorrowTaskCompletedAt *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type DeviceEvent struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey"`
	EventID     string         `gorm:"uniqueIndex;not null"`
	DeviceID    string         `gorm:"index;not null"`
	UserID      string         `gorm:"index;not null"`
	Type        string         `gorm:"not null"`
	Payload     datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	OccurredAt  time.Time      `gorm:"not null"`
	ProcessedAt time.Time      `gorm:"not null"`
	Result      datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt   time.Time
}

type DeviceCommand struct {
	ID               uuid.UUID      `gorm:"type:uuid;primaryKey"`
	DeviceID         string         `gorm:"index:idx_device_command_queue;not null"`
	UserID           string         `gorm:"index;not null"`
	Type             string         `gorm:"not null"`
	Payload          datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	Status           string         `gorm:"index:idx_device_command_queue;not null"`
	CreatedAt        time.Time      `gorm:"index:idx_device_command_queue"`
	DispatchedAt     *time.Time
	AckedAt          *time.Time
	AckPayload       datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	DispatchAttempts int            `gorm:"not null;default:0"`
	LeaseExpiresAt   *time.Time
}

type Device struct {
	DeviceID        string         `gorm:"primaryKey"`
	UserID          string         `gorm:"index;not null"`
	FirmwareVersion string         `gorm:"not null;default:''"`
	Capabilities    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	Status          datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	LocalTime       *time.Time
	LastSeenAt      time.Time `gorm:"index;not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func JSON(value any) datatypes.JSON {
	encoded, _ := json.Marshal(value)
	return datatypes.JSON(encoded)
}
