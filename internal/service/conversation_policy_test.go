package service

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
)

func TestDemoContinuousConversationRequiresExactIdentity(t *testing.T) {
	tests := []struct {
		name                             string
		enabled                          bool
		configuredUser, configuredDevice string
		userID, deviceID                 string
		want                             bool
	}{
		{name: "exact demo identity", enabled: true, configuredUser: "demo-user", configuredDevice: "demo-device", userID: "demo-user", deviceID: "demo-device", want: true},
		{name: "disabled", configuredUser: "demo-user", configuredDevice: "demo-device", userID: "demo-user", deviceID: "demo-device"},
		{name: "different user", enabled: true, configuredUser: "demo-user", configuredDevice: "demo-device", userID: "other-user", deviceID: "demo-device"},
		{name: "different device", enabled: true, configuredUser: "demo-user", configuredDevice: "demo-device", userID: "demo-user", deviceID: "other-device"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := demoContinuousConversationEnabled(test.enabled, test.configuredUser, test.configuredDevice, test.userID, test.deviceID); got != test.want {
				t.Fatalf("enabled = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDemoConversationRestartPolicy(t *testing.T) {
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	future := now.Add(time.Second)
	tests := []struct {
		name    string
		session model.NightSession
		want    string
		blocked bool
	}{
		{name: "expired incomplete", session: model.NightSession{Phase: "CONVERSATION", BoxClosed: true, ConversationTurns: 1, ConversationHardDeadlineAt: &past}, want: "expired"},
		{name: "expired with active lease", session: model.NightSession{Phase: "CONVERSATION", BoxClosed: true, ConversationTurns: 1, ConversationHardDeadlineAt: &past, ConversationProcessingUntil: &future}, blocked: true},
		{name: "completed choosing", session: model.NightSession{Phase: "CHOOSING_GUIDANCE", BoxClosed: true, ConversationTurns: 3}, want: "completed"},
		{name: "completed sleeping", session: model.NightSession{Phase: "SLEEPING", BoxClosed: true, ConversationTurns: 3}, want: "completed"},
		{name: "active conversation", session: model.NightSession{Phase: "CONVERSATION", BoxClosed: true, ConversationTurns: 1, ConversationHardDeadlineAt: &future}},
		{name: "open box", session: model.NightSession{Phase: "CONVERSATION", BoxClosed: false, ConversationTurns: 1, ConversationHardDeadlineAt: &past}},
		{name: "sunrise", session: model.NightSession{Phase: "SUNRISE", BoxClosed: true, ConversationTurns: 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, blocked := demoConversationRestartPolicy(&test.session, now)
			if reason != test.want || blocked != test.blocked {
				t.Fatalf("policy = (%q, %v), want (%q, %v)", reason, blocked, test.want, test.blocked)
			}
		})
	}
}

func TestResetDemoConversationPreservesPhysicalAndReminderState(t *testing.T) {
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	session := &model.NightSession{
		Phase: "SLEEPING", ResumePhase: "CONVERSATION", BoxClosed: true,
		ConversationTurns: 3, SelectedGuidance: "rain", AudioPlaying: true,
		SunriseProgress: 20, PausedForTonight: true, RemindersSkipped: true,
		FinalizeReason: "turn_limit", ConversationStartedAt: &now,
		ConversationLastActivityAt: &now, ConversationSilenceDeadlineAt: &now,
		ConversationHardDeadlineAt: &now, ConversationProcessingUntil: &now,
		PhoneRemovedAt: &now, ResumeDeadlineAt: &now, AudioEndsAt: &now,
	}

	resetDemoConversation(session)

	if session.Phase != "LOCKED" || session.ConversationTurns != 0 || !session.BoxClosed || !session.RemindersSkipped {
		t.Fatalf("session = %#v", session)
	}
	if session.ResumePhase != "" || session.SelectedGuidance != "" || session.AudioPlaying || session.SunriseProgress != 0 || session.PausedForTonight || session.FinalizeReason != "" {
		t.Fatalf("session was not reset: %#v", session)
	}
	if session.ConversationStartedAt != nil || session.ConversationLastActivityAt != nil || session.ConversationSilenceDeadlineAt != nil || session.ConversationHardDeadlineAt != nil || session.ConversationProcessingUntil != nil || session.PhoneRemovedAt != nil || session.ResumeDeadlineAt != nil || session.AudioEndsAt != nil {
		t.Fatalf("session timing was not cleared: %#v", session)
	}
}

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
