package service

import (
	"context"
	"errors"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/google/uuid"
)

type JournalService struct {
	store repository.Store
	hub   *realtime.Hub
	now   func() time.Time
}

func NewJournalService(store repository.Store, hub *realtime.Hub) *JournalService {
	return &JournalService{store: store, hub: hub, now: time.Now}
}

func (s *JournalService) List(ctx context.Context, userID string, limit int) ([]dto.MemoryCard, error) {
	if limit <= 0 {
		limit = 7
	}
	if limit > 30 {
		limit = 30
	}
	cards, err := s.store.ListMemoryCards(ctx, userID, limit)
	if err != nil {
		return nil, NewError("storage_error", "读取晚安日记失败", err)
	}
	result := make([]dto.MemoryCard, 0, len(cards))
	for i := range cards {
		result = append(result, dto.MemoryCardFromModel(&cards[i]))
	}
	return result, nil
}

func (s *JournalService) Get(ctx context.Context, userID string, cardID uuid.UUID) (dto.MemoryCard, error) {
	card, err := s.store.GetMemoryCard(ctx, userID, cardID, false)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.MemoryCard{}, NewError("not_found", "晚安卡不存在", err)
	}
	if err != nil {
		return dto.MemoryCard{}, NewError("storage_error", "读取晚安卡失败", err)
	}
	return dto.MemoryCardFromModel(card), nil
}

func (s *JournalService) Update(ctx context.Context, userID string, cardID uuid.UUID, request dto.UpdateMemoryCardRequest) (dto.MemoryCard, error) {
	if request.TomorrowTaskCompleted == nil {
		return dto.MemoryCard{}, NewError("validation_error", "tomorrowTaskCompleted 不能为空", nil)
	}
	var result dto.MemoryCard
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		card, err := tx.GetMemoryCard(ctx, userID, cardID, true)
		if err != nil {
			return err
		}
		card.TomorrowTaskCompleted = *request.TomorrowTaskCompleted
		if card.TomorrowTaskCompleted {
			now := s.now().UTC()
			card.TomorrowTaskCompletedAt = &now
		} else {
			card.TomorrowTaskCompletedAt = nil
		}
		if err := tx.UpdateMemoryCard(ctx, card); err != nil {
			return err
		}
		result = dto.MemoryCardFromModel(card)
		return nil
	})
	if errors.Is(err, repository.ErrNotFound) {
		return dto.MemoryCard{}, NewError("not_found", "晚安卡不存在", err)
	}
	if err != nil {
		return dto.MemoryCard{}, normalizeServiceError(err, "更新晚安卡失败")
	}
	publish(s.hub, userID, "journal.updated", result)
	return result, nil
}

func (s *JournalService) Delete(ctx context.Context, userID string, cardID uuid.UUID) error {
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		card, err := tx.GetMemoryCard(ctx, userID, cardID, true)
		if err != nil {
			return err
		}
		session, err := tx.GetNightSessionByID(ctx, card.SessionID, true)
		if err != nil {
			return err
		}
		if activeJournalSession(session, s.now()) {
			return NewError("journal_not_deletable", "今晚流程进行中，暂时不能删除这张晚安卡", nil)
		}
		if err := tx.DeleteConversationTurns(ctx, session.ID); err != nil {
			return err
		}
		session.LatestAIDraft = model.JSON(map[string]any{})
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		return tx.DeleteMemoryCard(ctx, userID, cardID)
	})
	if errors.Is(err, repository.ErrNotFound) {
		return NewError("not_found", "晚安卡不存在", err)
	}
	if err != nil {
		return normalizeServiceError(err, "删除晚安卡失败")
	}
	publish(s.hub, userID, "journal.deleted", map[string]any{"id": cardID})
	return nil
}

func activeJournalSession(session *model.NightSession, now time.Time) bool {
	return state.Phase(session.Phase) != state.Awake && sameUTCDate(session.Date, now)
}

func sameUTCDate(left, right time.Time) bool {
	leftYear, leftMonth, leftDay := left.UTC().Date()
	rightYear, rightMonth, rightDay := right.UTC().Date()
	return leftYear == rightYear && leftMonth == rightMonth && leftDay == rightDay
}
