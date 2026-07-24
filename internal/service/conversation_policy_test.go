package service

import (
	"testing"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
)

func TestConversationFinalizePolicyRequiresThreeOrdinaryTurns(t *testing.T) {
	for turn := 1; turn <= 2; turn++ {
		finalize, reason := conversationFinalizePolicy(turn, dto.AIResult{ShouldFinalize: true})
		if finalize || reason != "" {
			t.Fatalf("turn %d finalize=%v reason=%q, want false and empty", turn, finalize, reason)
		}
	}

	finalize, reason := conversationFinalizePolicy(3, dto.AIResult{})
	if !finalize || reason != "turn_limit" {
		t.Fatalf("third turn finalize=%v reason=%q", finalize, reason)
	}
}

func TestConversationFinalizePolicyDoesNotAdvanceHighRiskBeforeThirdTurn(t *testing.T) {
	finalize, reason := conversationFinalizePolicy(1, dto.AIResult{ShouldFinalize: true, HighRisk: true})
	if finalize || reason != "" {
		t.Fatalf("high risk finalize=%v reason=%q, want false and empty", finalize, reason)
	}
}

func TestFinalizeRequiresThreeCompletedTurns(t *testing.T) {
	for turns := 0; turns < 3; turns++ {
		if err := validateConversationFinalization(&model.NightSession{Phase: "CONVERSATION", ConversationTurns: turns}); err == nil {
			t.Fatalf("turns=%d finalization unexpectedly allowed", turns)
		}
	}
	if err := validateConversationFinalization(&model.NightSession{Phase: "CONVERSATION", ConversationTurns: 3}); err != nil {
		t.Fatalf("three completed turns rejected: %v", err)
	}
}

func TestNextCompletedTurnsOnlyAdvancesAfterAssistantReply(t *testing.T) {
	if got := nextTurnIndex(0); got != 1 {
		t.Fatalf("nextTurnIndex(0) = %d", got)
	}
	if got := completedTurnsAfterReply(0, 1); got != 1 {
		t.Fatalf("completedTurnsAfterReply(0, 1) = %d", got)
	}
}
