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
	UpsertMemoryCard(ctx context.Context, card *model.MemoryCard) error
	ListMemoryCards(ctx context.Context, userID string, limit int) ([]model.MemoryCard, error)
	GetDeviceEventByEventID(ctx context.Context, eventID string) (*model.DeviceEvent, error)
	CreateDeviceEvent(ctx context.Context, event *model.DeviceEvent) error
	CreateDeviceCommands(ctx context.Context, commands []model.DeviceCommand) error
	TakeNextDeviceCommand(ctx context.Context, deviceID string) (*model.DeviceCommand, error)
	AckDeviceCommand(ctx context.Context, deviceID string, commandID uuid.UUID, success bool, payload []byte) (*model.DeviceCommand, error)
}
