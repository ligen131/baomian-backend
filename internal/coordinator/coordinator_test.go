package coordinator

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
)

func TestConversationDueRequiresThreeCompletedTurns(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Second)

	for turns := 0; turns < 3; turns++ {
		session := &model.NightSession{
			ConversationTurns:             turns,
			ConversationSilenceDeadlineAt: &due,
			ConversationHardDeadlineAt:    &due,
		}
		if conversationDue(session, now) {
			t.Fatalf("conversationDue() = true for %d completed turns", turns)
		}
	}

	session := &model.NightSession{
		ConversationTurns:             3,
		ConversationSilenceDeadlineAt: &due,
	}
	if !conversationDue(session, now) {
		t.Fatal("conversationDue() = false for three completed turns")
	}
}

func TestConversationDueHonorsActiveProcessingLease(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Second)
	lease := now.Add(time.Second)
	session := &model.NightSession{
		ConversationTurns:             3,
		ConversationSilenceDeadlineAt: &due,
		ConversationProcessingUntil:   &lease,
	}

	if conversationDue(session, now) {
		t.Fatal("conversationDue() = true while processing lease is active")
	}
}
