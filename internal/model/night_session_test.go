package model

import "testing"

func TestValidateNightSessionConversationState(t *testing.T) {
	tests := []struct {
		phase string
		turns int
		ok    bool
	}{
		{phase: "LOCKED", turns: 0, ok: true},
		{phase: "LOCKED", turns: 1, ok: false},
		{phase: "CONVERSATION", turns: 0, ok: true},
		{phase: "CONVERSATION", turns: 1, ok: true},
		{phase: "CONVERSATION", turns: 2, ok: true},
		{phase: "CONVERSATION", turns: 3, ok: false},
		{phase: "CHOOSING_GUIDANCE", turns: 3, ok: true},
		{phase: "CHOOSING_GUIDANCE", turns: 0, ok: false},
		{phase: "SLEEPING", turns: 3, ok: true},
		{phase: "SLEEPING", turns: 2, ok: false},
		{phase: "PHONE_REMOVED", turns: 1, ok: true},
		{phase: "SUNRISE", turns: 0, ok: true},
	}

	for _, test := range tests {
		t.Run(test.phase, func(t *testing.T) {
			err := ValidateNightSessionConversationState(&NightSession{Phase: test.phase, ConversationTurns: test.turns})
			if (err == nil) != test.ok {
				t.Fatalf("phase=%s turns=%d error=%v", test.phase, test.turns, err)
			}
		})
	}
}
