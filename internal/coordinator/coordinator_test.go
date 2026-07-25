package coordinator

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
)

func TestConversationDueNeverAutomaticallyFinalizesVoiceConversation(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Second)
	for _, turns := range []int{0, 3, 10} {
		session := &model.NightSession{
			ConversationTurns:             turns,
			ConversationSilenceDeadlineAt: &due,
			ConversationHardDeadlineAt:    &due,
		}
		if conversationDue(session, now) {
			t.Fatalf("conversationDue() = true for %d completed turns", turns)
		}
	}
}
