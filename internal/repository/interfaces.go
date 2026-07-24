package repository

import (
	"context"
	"errors"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	WithTx(ctx context.Context, fn func(Store) error) error
	GetOrCreateProfile(ctx context.Context, userID string) (*model.Profile, error)
	UpdateProfile(ctx context.Context, profile *model.Profile) error
	GetOrCreateTonight(ctx context.Context, userID string, date time.Time, forUpdate bool) (*model.NightSession, error)
	UpdateNightSession(ctx context.Context, session *model.NightSession) error
	CreateConversationTurn(ctx context.Context, turn *model.ConversationTurn) error
	ListConversationTurns(ctx context.Context, sessionID uuid.UUID) ([]model.ConversationTurn, error)
	GetLatestUserTurn(ctx context.Context, sessionID uuid.UUID) (*model.ConversationTurn, error)
	GetConversationTurnByClientRequestID(ctx context.Context, sessionID uuid.UUID, clientRequestID, role string) (*model.ConversationTurn, error)
	DeleteConversationTurns(ctx context.Context, sessionID uuid.UUID) error
	UpsertMemoryCard(ctx context.Context, card *model.MemoryCard) error
	ListMemoryCards(ctx context.Context, userID string, limit int) ([]model.MemoryCard, error)
	GetMemoryCard(ctx context.Context, userID string, cardID uuid.UUID, forUpdate bool) (*model.MemoryCard, error)
	UpdateMemoryCard(ctx context.Context, card *model.MemoryCard) error
	DeleteMemoryCard(ctx context.Context, userID string, cardID uuid.UUID) error
	GetNightSessionByID(ctx context.Context, sessionID uuid.UUID, forUpdate bool) (*model.NightSession, error)
	ListDueNightSessionIDs(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
	GetDeviceEventByEventID(ctx context.Context, eventID string) (*model.DeviceEvent, error)
	CreateDeviceEvent(ctx context.Context, event *model.DeviceEvent) error
	CreateDeviceCommands(ctx context.Context, commands []model.DeviceCommand) error
	TakeNextDeviceCommand(ctx context.Context, deviceID string, lease time.Duration, maxAttempts int) (*model.DeviceCommand, error)
	AckDeviceCommand(ctx context.Context, deviceID string, commandID uuid.UUID, success bool, payload []byte) (*model.DeviceCommand, error)
	UpsertDevice(ctx context.Context, device *model.Device) error
	GetDevice(ctx context.Context, userID, deviceID string) (*model.Device, error)
}
