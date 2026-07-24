package voice

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSessionReadyAlwaysSerializesCompletedTurnsAsNumber(t *testing.T) {
	payload, err := json.Marshal(ServerEvent{
		Type: EventSessionReady, Phase: "LOCKED", CompletedTurns: 0, Audio: DefaultAudioFormat(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	value, ok := decoded["completedTurns"]
	if !ok {
		t.Fatalf("completedTurns missing from %s", payload)
	}
	if value != float64(0) {
		t.Fatalf("completedTurns = %#v, want JSON number 0", value)
	}
	if _, exists := decoded["completed_turns"]; exists {
		t.Fatalf("unexpected snake_case field in %s", payload)
	}
}

func TestOtherServerEventsStillOmitCompletedTurnsWhenUnused(t *testing.T) {
	payload, err := json.Marshal(ServerEvent{Type: EventThinking, TurnID: "turn-1"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["completedTurns"]; exists {
		t.Fatalf("unexpected completedTurns in %s", payload)
	}
}

func TestValidatePCMFrame(t *testing.T) {
	if err := ValidatePCMFrame(make([]byte, PCMFrameBytes)); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePCMFrame(make([]byte, PCMFrameBytes-1)); !errors.Is(err, ErrInvalidAudioFrame) {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeClientEvent(t *testing.T) {
	event, err := DecodeClientEvent([]byte(`{"type":"input.start","eventId":"e1","turnId":"t1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventInputStart || event.TurnID != "t1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestDecodeClientEventValidatesRequiredFields(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"type":"unknown","eventId":"e1"}`),
		[]byte(`{"type":"session.start"}`),
		[]byte(`{"type":"input.start","eventId":"e1"}`),
	}
	for _, input := range tests {
		if _, err := DecodeClientEvent(input); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("DecodeClientEvent(%s) error = %v", input, err)
		}
	}
}
