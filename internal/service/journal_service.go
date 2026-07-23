package service

import (
	"context"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/repository"
)

type JournalService struct {
	store repository.Store
}

func NewJournalService(store repository.Store) *JournalService { return &JournalService{store: store} }

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
