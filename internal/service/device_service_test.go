package service

import (
	"testing"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/state"
)

func TestIdempotentBoxClosedOnlyWhenPhysicalStateAlreadySatisfied(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		phase     state.Phase
		closed    bool
		want      bool
	}{
		{name: "locked", eventType: "box_closed", phase: state.Locked, closed: true, want: true},
		{name: "conversation", eventType: "box_closed", phase: state.Conversation, closed: true, want: true},
		{name: "choosing guidance", eventType: "box_closed", phase: state.ChoosingGuidance, closed: true, want: true},
		{name: "sleeping", eventType: "box_closed", phase: state.Sleeping, closed: true, want: true},
		{name: "waiting must transition", eventType: "box_closed", phase: state.WaitingToLock, closed: true, want: false},
		{name: "phone removed must resume", eventType: "box_closed", phase: state.PhoneRemoved, closed: true, want: false},
		{name: "open physical state", eventType: "box_closed", phase: state.Conversation, closed: false, want: false},
		{name: "different event", eventType: "box_opened", phase: state.Conversation, closed: true, want: false},
		{name: "sunrise remains conflict", eventType: "box_closed", phase: state.Sunrise, closed: true, want: false},
		{name: "awake remains conflict", eventType: "box_closed", phase: state.Awake, closed: true, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := &model.NightSession{Phase: string(test.phase), BoxClosed: test.closed}
			if got := idempotentBoxClosed(test.eventType, session); got != test.want {
				t.Fatalf("idempotentBoxClosed() = %v, want %v", got, test.want)
			}
		})
	}
}
