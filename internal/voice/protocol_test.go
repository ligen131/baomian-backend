package voice

import (
	"errors"
	"testing"
)

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
