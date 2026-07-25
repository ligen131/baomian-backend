package service

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/ai"
	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/google/uuid"
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

func TestContinuousRunNeverExpiresAtLegacyHardDeadline(t *testing.T) {
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	session := &model.NightSession{ConversationHardDeadlineAt: &past}
	if conversationExpired(session, true, now) {
		t.Fatal("continuous run expired at legacy hard deadline")
	}
	if !conversationExpired(session, false, now) {
		t.Fatal("legacy HTTP conversation did not retain hard deadline")
	}
}

func TestConversationFinalizePolicyNeverFinalizesOrdinaryTurns(t *testing.T) {
	for _, turn := range []int{1, 2, 3, 4, 10} {
		finalize, reason := conversationFinalizePolicy(turn, dto.AIResult{ShouldFinalize: true})
		if finalize || reason != "" {
			t.Fatalf("turn %d finalize=%v reason=%q, want false and empty", turn, finalize, reason)
		}
	}
}

func TestConversationFinalizePolicyDoesNotAdvanceHighRiskBeforeThirdTurn(t *testing.T) {
	finalize, reason := conversationFinalizePolicy(1, dto.AIResult{ShouldFinalize: true, HighRisk: true})
	if finalize || reason != "" {
		t.Fatalf("high risk finalize=%v reason=%q, want false and empty", finalize, reason)
	}
}

func TestManualFinalizeAllowsAnyCompletedTurnCount(t *testing.T) {
	for _, turns := range []int{0, 1, 3, 10} {
		if err := validateConversationFinalization(&model.NightSession{Phase: "CONVERSATION", ConversationTurns: turns}); err != nil {
			t.Fatalf("turns=%d finalization rejected: %v", turns, err)
		}
	}
}

func TestRecoveryDistinguishesPendingAIFromPersistedReply(t *testing.T) {
	turnID := "turn-5"
	run := &model.ConversationRun{Status: model.ConversationRunActive, CompletedTurns: 4, ProcessingTurnID: &turnID}
	pending := recoveryState(run, []model.ConversationTurn{{Role: "user", TurnIndex: 5, ClientRequestID: &turnID}})
	if pending.ResumeAction != "wait_turn" {
		t.Fatalf("pending recovery = %#v", pending)
	}
	persisted := recoveryState(run, []model.ConversationTurn{{Role: "assistant", TurnIndex: 5, ClientRequestID: &turnID}})
	if persisted.ResumeAction != "replay_reply" {
		t.Fatalf("persisted recovery = %#v", persisted)
	}
}

func TestJournalRequestIncludesEveryCompleteTurnInRun(t *testing.T) {
	turns := []model.ConversationTurn{
		{Role: "user", Text: "第一件心事", TurnIndex: 1},
		{Role: "assistant", Text: "第一轮陪伴", TurnIndex: 1},
		{Role: "user", Text: "第二件心事", TurnIndex: 2},
		{Role: "assistant", Text: "第二轮陪伴", TurnIndex: 2},
		{Role: "user", Text: "未完成半轮", TurnIndex: 3},
	}
	request := journalRequest(turns)
	if request.Mode != ai.ModeJournal || len(request.Turns) != 4 {
		t.Fatalf("request = %#v", request)
	}
	if request.Turns[0].Text != "第一件心事" || request.Turns[3].Text != "第二轮陪伴" {
		t.Fatalf("turns = %#v", request.Turns)
	}
	if request.Text != "第一件心事\n第二件心事" {
		t.Fatalf("text = %q", request.Text)
	}
}

func TestSleepGuidanceMapsUnsupportedOptionsToRain(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "rain", want: "rain"},
		{input: "breathing_46", want: "breathing_46"},
		{input: "brown_noise", want: "rain"},
		{input: "silence", want: "rain"},
	} {
		if got := sleepGuidance(test.input); got != test.want {
			t.Fatalf("sleepGuidance(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestMemoryCardDTOCarriesRunIdentity(t *testing.T) {
	runID := uuid.New()
	value := dto.MemoryCardFromModel(&model.MemoryCard{RunID: runID})
	if value.RunID != runID {
		t.Fatalf("runId = %s, want %s", value.RunID, runID)
	}
}

func TestConversationTurnDTOCarriesRunIdentity(t *testing.T) {
	runID := uuid.New()
	value := dto.ConversationTurnFromModel(&model.ConversationTurn{RunID: runID})
	if value.RunID != runID {
		t.Fatalf("runId = %s, want %s", value.RunID, runID)
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
