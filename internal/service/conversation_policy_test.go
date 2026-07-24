package service

import (
	"testing"

	"github.com/baomian/baomian-backend/internal/dto"
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

func TestConversationFinalizePolicyPreservesHighRiskSafetyExit(t *testing.T) {
	finalize, reason := conversationFinalizePolicy(1, dto.AIResult{ShouldFinalize: true, HighRisk: true})
	if !finalize || reason != "safety" {
		t.Fatalf("high risk finalize=%v reason=%q", finalize, reason)
	}
}
