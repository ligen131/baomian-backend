package state

import (
	"errors"
	"testing"
)

func TestMainFlow(t *testing.T) {
	current := Snapshot{Phase: WaitingToLock}
	steps := []struct {
		trigger Trigger
		phase   Phase
	}{
		{BoxClosed, Locked},
		{StartConversation, Conversation},
		{Finalize, ChoosingGuidance},
		{SelectGuidance, Sleeping},
		{AlarmStart, Sunrise},
		{MarkAwake, Awake},
	}
	for _, step := range steps {
		next, err := Apply(current, step.trigger)
		if err != nil {
			t.Fatalf("apply %s: %v", step.trigger, err)
		}
		if next.Phase != step.phase {
			t.Fatalf("apply %s: got %s want %s", step.trigger, next.Phase, step.phase)
		}
		current = next
	}
}

func TestPhoneRemovedResume(t *testing.T) {
	removed, err := Apply(Snapshot{Phase: Sleeping, BoxClosed: true, AudioPlaying: true}, BoxOpened)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Phase != PhoneRemoved || removed.ResumePhase != Sleeping || removed.AudioPlaying {
		t.Fatalf("unexpected removed snapshot: %+v", removed)
	}
	resumed, err := Apply(removed, BoxClosed)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Phase != Sleeping || resumed.ResumePhase != "" || !resumed.BoxClosed {
		t.Fatalf("unexpected resumed snapshot: %+v", resumed)
	}
}

func TestLongPressIgnoredOutsideSunrise(t *testing.T) {
	current := Snapshot{Phase: Sleeping}
	next, err := Apply(current, SoftButtonLong)
	if err != nil {
		t.Fatal(err)
	}
	if next != current {
		t.Fatalf("long press should be ignored: got %+v", next)
	}
}

func TestInvalidTransition(t *testing.T) {
	_, err := Apply(Snapshot{Phase: WaitingToLock}, StartConversation)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
}
