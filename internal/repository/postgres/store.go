package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) WithTx(ctx context.Context, fn func(repository.Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Store{db: tx})
	})
}

func (s *Store) GetOrCreateProfile(ctx context.Context, userID string) (*model.Profile, error) {
	var profile model.Profile
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error
	if err == nil {
		return &profile, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("get profile: %w", err)
	}

	profile = model.Profile{
		ID:                     uuid.New(),
		UserID:                 userID,
		Bedtime:                "23:00",
		WakeTime:               "07:30",
		Persona:                "gentle",
		ReminderStyle:          "gentle",
		DefaultGuidance:        "rain",
		WhiteNoiseDurationMin:  20,
		TimeZone:               "Asia/Shanghai",
		BedtimeReminderEnabled: true,
		WakeAlarmEnabled:       true,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&profile).Error; err != nil {
		return nil, fmt.Errorf("create profile: %w", err)
	}
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error; err != nil {
		return nil, fmt.Errorf("reload profile: %w", err)
	}
	return &profile, nil
}

func (s *Store) UpdateProfile(ctx context.Context, profile *model.Profile) error {
	if err := s.db.WithContext(ctx).Save(profile).Error; err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

func (s *Store) GetOrCreateTonight(ctx context.Context, userID string, date time.Time, forUpdate bool) (*model.NightSession, error) {
	date = day(date)
	query := s.db.WithContext(ctx).Where("user_id = ? AND date = ?", userID, date)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var session model.NightSession
	err := query.First(&session).Error
	if err == nil {
		return &session, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("get tonight session: %w", err)
	}

	session = model.NightSession{
		ID:            uuid.New(),
		UserID:        userID,
		Date:          date,
		Phase:         "WAITING_TO_LOCK",
		LatestAIDraft: datatypes.JSON([]byte("{}")),
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&session).Error; err != nil {
		return nil, fmt.Errorf("create tonight session: %w", err)
	}
	query = s.db.WithContext(ctx).Where("user_id = ? AND date = ?", userID, date)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.First(&session).Error; err != nil {
		return nil, fmt.Errorf("reload tonight session: %w", err)
	}
	return &session, nil
}

func (s *Store) UpdateNightSession(ctx context.Context, session *model.NightSession) error {
	if err := s.db.WithContext(ctx).Save(session).Error; err != nil {
		return fmt.Errorf("update night session: %w", err)
	}
	return nil
}

func (s *Store) CreateConversationTurn(ctx context.Context, turn *model.ConversationTurn) error {
	if turn.ID == uuid.Nil {
		turn.ID = uuid.New()
	}
	if err := s.db.WithContext(ctx).Create(turn).Error; err != nil {
		return fmt.Errorf("create conversation turn: %w", err)
	}
	return nil
}

func (s *Store) ListConversationTurns(ctx context.Context, sessionID uuid.UUID) ([]model.ConversationTurn, error) {
	var turns []model.ConversationTurn
	if err := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").Find(&turns).Error; err != nil {
		return nil, fmt.Errorf("list conversation turns: %w", err)
	}
	return turns, nil
}

func (s *Store) GetLatestUserTurn(ctx context.Context, sessionID uuid.UUID) (*model.ConversationTurn, error) {
	var turn model.ConversationTurn
	if err := s.db.WithContext(ctx).Where("session_id = ? AND role = ?", sessionID, "user").Order("created_at DESC").First(&turn).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("get latest user turn: %w", err)
	}
	return &turn, nil
}

func (s *Store) GetConversationTurnByClientRequestID(ctx context.Context, sessionID uuid.UUID, clientRequestID, role string) (*model.ConversationTurn, error) {
	var turn model.ConversationTurn
	err := s.db.WithContext(ctx).Where("session_id = ? AND client_request_id = ? AND role = ?", sessionID, clientRequestID, role).First(&turn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation turn by client request id: %w", err)
	}
	return &turn, nil
}

func (s *Store) DeleteConversationTurns(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&model.ConversationTurn{}).Error; err != nil {
		return fmt.Errorf("delete conversation turns: %w", err)
	}
	return nil
}

func (s *Store) UpsertMemoryCard(ctx context.Context, card *model.MemoryCard) error {
	if card.ID == uuid.Nil {
		card.ID = uuid.New()
	}
	columns := []string{"user_id", "date", "emotion", "worry", "tomorrow_task", "comfort", "suggested_guidance", "fallback", "updated_at"}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns(columns),
	}).Create(card).Error; err != nil {
		return fmt.Errorf("upsert memory card: %w", err)
	}
	return nil
}

func (s *Store) ListMemoryCards(ctx context.Context, userID string, limit int) ([]model.MemoryCard, error) {
	var cards []model.MemoryCard
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("date DESC, created_at DESC").Limit(limit).Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list memory cards: %w", err)
	}
	return cards, nil
}

func (s *Store) GetMemoryCard(ctx context.Context, userID string, cardID uuid.UUID, forUpdate bool) (*model.MemoryCard, error) {
	query := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", cardID, userID)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var card model.MemoryCard
	if err := query.First(&card).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("get memory card: %w", err)
	}
	return &card, nil
}

func (s *Store) UpdateMemoryCard(ctx context.Context, card *model.MemoryCard) error {
	if err := s.db.WithContext(ctx).Save(card).Error; err != nil {
		return fmt.Errorf("update memory card: %w", err)
	}
	return nil
}

func (s *Store) DeleteMemoryCard(ctx context.Context, userID string, cardID uuid.UUID) error {
	result := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", cardID, userID).Delete(&model.MemoryCard{})
	if result.Error != nil {
		return fmt.Errorf("delete memory card: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) GetNightSessionByID(ctx context.Context, sessionID uuid.UUID, forUpdate bool) (*model.NightSession, error) {
	query := s.db.WithContext(ctx).Where("id = ?", sessionID)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var session model.NightSession
	if err := query.First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("get night session by id: %w", err)
	}
	return &session, nil
}

func (s *Store) ListDueNightSessionIDs(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	due := `(phase = 'CONVERSATION' AND conversation_turns >= 3 AND (conversation_silence_deadline_at <= ? OR conversation_hard_deadline_at <= ?) AND (conversation_processing_until IS NULL OR conversation_processing_until <= ?)) OR (phase = 'PHONE_REMOVED' AND resume_deadline_at <= ?) OR (audio_playing = TRUE AND audio_ends_at <= ?)`
	if err := s.db.WithContext(ctx).Model(&model.NightSession{}).Where(due, now, now, now, now, now).
		Order("updated_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list due night sessions: %w", err)
	}
	return ids, nil
}

func (s *Store) GetDeviceEventByEventID(ctx context.Context, eventID string) (*model.DeviceEvent, error) {
	var event model.DeviceEvent
	if err := s.db.WithContext(ctx).Where("event_id = ?", eventID).First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("get device event: %w", err)
	}
	return &event, nil
}

func (s *Store) CreateDeviceEvent(ctx context.Context, event *model.DeviceEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if err := s.db.WithContext(ctx).Create(event).Error; err != nil {
		return fmt.Errorf("create device event: %w", err)
	}
	return nil
}

func (s *Store) CreateDeviceCommands(ctx context.Context, commands []model.DeviceCommand) error {
	if len(commands) == 0 {
		return nil
	}
	for i := range commands {
		if commands[i].ID == uuid.Nil {
			commands[i].ID = uuid.New()
		}
		if commands[i].Status == "" {
			commands[i].Status = "pending"
		}
		if len(commands[i].Payload) == 0 {
			commands[i].Payload = datatypes.JSON([]byte("{}"))
		}
		if len(commands[i].AckPayload) == 0 {
			commands[i].AckPayload = datatypes.JSON([]byte("{}"))
		}
	}
	if err := s.db.WithContext(ctx).Create(&commands).Error; err != nil {
		return fmt.Errorf("create device commands: %w", err)
	}
	return nil
}

func (s *Store) TakeNextDeviceCommand(ctx context.Context, deviceID string, lease time.Duration, maxAttempts int) (*model.DeviceCommand, error) {
	var command model.DeviceCommand
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&model.DeviceCommand{}).
			Where("device_id = ? AND status = ? AND lease_expires_at <= ? AND dispatch_attempts >= ?", deviceID, "dispatched", now, maxAttempts).
			Updates(map[string]any{"status": "failed", "lease_expires_at": nil}).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("device_id = ? AND dispatch_attempts < ? AND (status = ? OR (status = ? AND lease_expires_at <= ?))", deviceID, maxAttempts, "pending", "dispatched", now).
			Order("created_at ASC").First(&command).Error; err != nil {
			return err
		}
		leaseExpiresAt := now.Add(lease)
		command.Status = "dispatched"
		command.DispatchedAt = &now
		command.DispatchAttempts++
		command.LeaseExpiresAt = &leaseExpiresAt
		return tx.Save(&command).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("take device command: %w", err)
	}
	return &command, nil
}

func (s *Store) UpsertDevice(ctx context.Context, device *model.Device) error {
	if len(device.Capabilities) == 0 {
		device.Capabilities = datatypes.JSON([]byte("{}"))
	}
	if len(device.Status) == 0 {
		device.Status = datatypes.JSON([]byte("{}"))
	}
	columns := []string{"user_id", "firmware_version", "capabilities", "status", "local_time", "last_seen_at", "updated_at"}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns(columns),
	}).Create(device).Error; err != nil {
		return fmt.Errorf("upsert device: %w", err)
	}
	return nil
}

func (s *Store) GetDevice(ctx context.Context, userID, deviceID string) (*model.Device, error) {
	var device model.Device
	if err := s.db.WithContext(ctx).Where("device_id = ? AND user_id = ?", deviceID, userID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("get device: %w", err)
	}
	return &device, nil
}

func (s *Store) AckDeviceCommand(ctx context.Context, deviceID string, commandID uuid.UUID, success bool, payload []byte) (*model.DeviceCommand, error) {
	var command model.DeviceCommand
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND device_id = ?", commandID, deviceID).First(&command).Error; err != nil {
			return err
		}
		if command.Status == "acked" || command.Status == "failed" {
			return nil
		}
		now := time.Now().UTC()
		if success {
			command.Status = "acked"
		} else {
			command.Status = "failed"
		}
		command.AckedAt = &now
		if len(payload) > 0 {
			command.AckPayload = datatypes.JSON(payload)
		}
		return tx.Save(&command).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ack device command: %w", err)
	}
	return &command, nil
}

func day(value time.Time) time.Time {
	year, month, date := value.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}
