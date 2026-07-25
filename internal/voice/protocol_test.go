package voice

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestConversationCompletedSerializesZeroCompletedTurns(t *testing.T) {
	payload, err := json.Marshal(ServerEvent{Type: EventConversationComplete, CompletedTurns: 0})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if value, exists := decoded["completedTurns"]; !exists || value != float64(0) {
		t.Fatalf("completedTurns = %#v, exists=%v, payload=%s", value, exists, payload)
	}
}

func TestVoiceErrorSerializesRequiredFalseRetryable(t *testing.T) {
	payload, err := json.Marshal(ServerEvent{
		Type: EventError, RunID: "run-1", Code: ErrorInvalidEvent,
		Message: "invalid", Retryable: false, TerminalFor: "event",
		OccurredAt: "2026-07-25T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	value, exists := decoded["retryable"]
	if !exists || value != false {
		t.Fatalf("retryable = %#v, exists=%v, payload=%s", value, exists, payload)
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
	event, err := DecodeClientEvent([]byte(`{"type":"input.start","runId":"r1","eventId":"e1","turnId":"t1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventInputStart || event.RunID != "r1" || event.TurnID != "t1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestDecodeClientEventSupportsConversationFinish(t *testing.T) {
	event, err := DecodeClientEvent([]byte(`{"type":"conversation.finish","runId":"r1","eventId":"finish-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventConversationFinish || event.RunID != "r1" || event.EventID != "finish-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestDecodeClientEventValidatesRequiredFields(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"type":"unknown","runId":"r1","eventId":"e1"}`),
		[]byte(`{"type":"session.start","runId":"r1"}`),
		[]byte(`{"type":"input.start","runId":"r1","eventId":"e1"}`),
		[]byte(`{"type":"input.start","eventId":"e1","turnId":"t1"}`),
		[]byte(`{"type":"conversation.finish","runId":"r1"}`),
	}
	for _, input := range tests {
		if _, err := DecodeClientEvent(input); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("DecodeClientEvent(%s) error = %v", input, err)
		}
	}
}

func TestVoiceContractSchemaDefinesEveryControlEvent(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "device-voice.schema.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(payload, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("$schema = %#v", schema["$schema"])
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("$defs = %#v", schema["$defs"])
	}
	for _, name := range []string{
		"sessionReady", "sessionStart", "inputStart", "inputAccepted", "inputEnd",
		"transcriptFinal", "thinking", "playbackStart", "playbackEnd", "playbackStop",
		"conversationFinish", "conversationCompleted", "voiceError",
	} {
		if _, exists := definitions[name]; !exists {
			t.Errorf("missing schema definition %q", name)
		}
	}
}

func TestOpenAPIReferencesAuthoritativeVoiceContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "openapi.yaml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contract := string(payload)
	for _, expected := range []string{
		"x-websocket:",
		"schema: ./device-voice.schema.json",
		"frameBytes: 960",
	} {
		if !strings.Contains(contract, expected) {
			t.Errorf("OpenAPI missing %q", expected)
		}
	}
}

func TestVoiceServerEventsCarryRunAndRecoveryContext(t *testing.T) {
	event := ServerEvent{
		Type: EventSessionReady, RunID: "run-1", Phase: "CONVERSATION", CompletedTurns: 4,
		Audio: DefaultAudioFormat(), Recovery: &RecoveryState{
			RunStatus: "active", ResumeAction: "replay_reply", PendingTurnID: "turn-4",
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["runId"] != "run-1" || decoded["recovery"].(map[string]any)["resumeAction"] != "replay_reply" {
		t.Fatalf("event = %s", payload)
	}
}
