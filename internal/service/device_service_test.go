package service

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/google/uuid"
)

func TestDemoResetStartsNewRunAndResetsNightSession(t *testing.T) {
	now := time.Date(2026, 7, 25, 22, 0, 0, 0, time.UTC)
	session := &model.NightSession{
		ID: uuid.New(), UserID: "demo-user", Date: now, Phase: string(state.Sleeping),
		BoxClosed: true, ConversationTurns: 8, SelectedGuidance: "rain", AudioPlaying: true,
	}
	request := dto.DeviceEventRequest{
		EventID: "reset-2", DeviceID: "demo-device", UserID: "demo-user",
		Type: "box_closed", Payload: map[string]any{"source": "reset_button"},
	}

	run, ok := newDemoConversationRun(request, "demo-user", true, "demo-user", "demo-device", session, now)
	if !ok {
		t.Fatal("expected demo reset to create run")
	}
	if run.ID == uuid.Nil || run.NightSessionID != session.ID || run.Status != model.ConversationRunActive {
		t.Fatalf("run = %#v", run)
	}
	if session.Phase != string(state.Locked) || session.ConversationTurns != 0 || !session.BoxClosed {
		t.Fatalf("session = %#v", session)
	}
	if session.SelectedGuidance != "" || session.AudioPlaying {
		t.Fatalf("stale guidance state retained: %#v", session)
	}
}

func TestDemoResetRequiresExactIdentityAndSource(t *testing.T) {
	session := &model.NightSession{ID: uuid.New(), Phase: string(state.Sleeping)}
	base := dto.DeviceEventRequest{
		EventID: "reset-1", DeviceID: "demo-device", UserID: "demo-user", Type: "box_closed",
		Payload: map[string]any{"source": "reset_button"},
	}
	for _, request := range []dto.DeviceEventRequest{
		{EventID: base.EventID, DeviceID: base.DeviceID, UserID: base.UserID, Type: base.Type, Payload: map[string]any{"source": "box_sensor"}},
		{EventID: base.EventID, DeviceID: "other-device", UserID: base.UserID, Type: base.Type, Payload: base.Payload},
		{EventID: base.EventID, DeviceID: base.DeviceID, UserID: "other-user", Type: base.Type, Payload: base.Payload},
		{EventID: base.EventID, DeviceID: base.DeviceID, UserID: base.UserID, Type: "box_opened", Payload: base.Payload},
	} {
		if _, ok := newDemoConversationRun(request, request.UserID, true, "demo-user", "demo-device", session, time.Now()); ok {
			t.Fatalf("unexpected demo reset for %#v", request)
		}
	}
}

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
