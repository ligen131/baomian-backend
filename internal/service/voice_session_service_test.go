package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/google/uuid"
)

func TestVoiceSessionCompletesThreeTurnsAndBreathingGuidance(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Locked)}
	conversation.responses = []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "第一轮回复", "breathing_46", false),
		voiceTurnResponse(2, "第二轮回复", "breathing_46", false),
		voiceTurnResponse(3, "第三轮回复", "breathing_46", true),
	}
	asr := &fakeASRClient{transcripts: []string{"第一轮", "第二轮", "第三轮"}}
	tts := &fakeTTSClient{}
	tonight := &fakeVoiceTonight{}
	output := newFakeVoiceOutput()
	factory := NewVoiceSessionService(conversation, tonight, asr, tts, "开场白", "呼吸脚本", 60*time.Second)
	session := factory.NewSession("user-1", "device-1", output)
	defer session.Close()

	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventSessionStart, EventID: "start"}); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, output, voice.EventPlaybackEnd, 2*time.Second)

	for turn := 1; turn <= 3; turn++ {
		turnID := "turn-" + string(rune('0'+turn))
		if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "input-start", TurnID: turnID}); err != nil {
			t.Fatal(err)
		}
		if err := session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes)); err != nil {
			t.Fatal(err)
		}
		if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "input-end", TurnID: turnID}); err != nil {
			t.Fatal(err)
		}
		waitForReplyEnd(t, output, turnID, 2*time.Second)
	}

	completed := waitForEvent(t, output, voice.EventConversationComplete, 2*time.Second)
	if completed.JournalID == "" || completed.Guidance != "breathing_46" {
		t.Fatalf("completed = %#v", completed)
	}
	waitForGuidanceEnd(t, output, 2*time.Second)

	conversation.mu.Lock()
	requests := append([]dto.ConversationTurnRequest(nil), conversation.requests...)
	conversation.mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("turn calls = %d", len(requests))
	}
	for index, request := range requests {
		if request.InputMode != "voice" || request.ClientRequestID != "turn-"+string(rune('1'+index)) {
			t.Fatalf("request[%d] = %#v", index, request)
		}
	}
	if tonight.selected != "breathing_46" {
		t.Fatalf("selected guidance = %q", tonight.selected)
	}
	if tts.countText("开场白") != 1 || tts.countText("呼吸脚本") != 1 {
		t.Fatalf("TTS texts = %#v", tts.texts)
	}
}

func TestVoiceSessionEmptyTranscriptDoesNotCallConversation(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Conversation)}
	asr := &fakeASRClient{transcripts: []string{""}}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, asr, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-empty"})
	_ = session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes))
	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-empty"})
	event := waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorEmptyTranscript {
		t.Fatalf("event = %#v", event)
	}
	if len(conversation.requests) != 0 {
		t.Fatalf("conversation calls = %d", len(conversation.requests))
	}
}

func TestVoiceSessionRainGuidanceStaysOnDevice(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Conversation)}
	conversation.responses = []dto.ConversationTurnResponse{voiceTurnResponse(3, "晚安", "rain", true)}
	asr := &fakeASRClient{transcripts: []string{"晚安"}}
	tts := &fakeTTSClient{}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, asr, tts, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-rain"})
	_ = session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes))
	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-rain"})
	event := waitForEvent(t, output, voice.EventGuidanceStart, 2*time.Second)
	if event.Guidance != "rain" || event.Source != "device" || event.DurationMinutes != 20 {
		t.Fatalf("guidance = %#v", event)
	}
	if tts.countText("呼吸") != 0 {
		t.Fatal("rain guidance must not use TTS")
	}
}

func voiceTurnResponse(turn int, reply, guidance string, withJournal bool) dto.ConversationTurnResponse {
	response := dto.ConversationTurnResponse{
		Result: dto.AIResult{Reply: reply, SuggestedGuidance: guidance},
		Tonight: dto.TonightState{
			ConversationTurns: turn, Phase: string(state.Conversation), WhiteNoiseDurationMin: 20,
		},
	}
	if withJournal {
		response.Tonight.Phase = string(state.ChoosingGuidance)
		response.Journal = &dto.MemoryCard{ID: uuid.New()}
	}
	return response
}

type fakeVoiceConversation struct {
	mu        sync.Mutex
	phase     string
	requests  []dto.ConversationTurnRequest
	responses []dto.ConversationTurnResponse
}

func (f *fakeVoiceConversation) History(context.Context, string) (dto.ConversationHistoryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return dto.ConversationHistoryResponse{Tonight: dto.TonightState{Phase: f.phase, ConversationTurns: len(f.requests)}}, nil
}

func (f *fakeVoiceConversation) Turn(_ context.Context, _ string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	return f.responses[len(f.requests)-1], nil
}

type fakeVoiceTonight struct {
	selected string
}

func (f *fakeVoiceTonight) StartVoiceConversation(context.Context, string) (dto.TonightState, error) {
	return dto.TonightState{Phase: string(state.Conversation)}, nil
}

func (f *fakeVoiceTonight) SelectVoiceGuidance(_ context.Context, _ string, guidance string) (dto.TonightState, error) {
	f.selected = guidance
	return dto.TonightState{Phase: string(state.Sleeping), SelectedGuidance: guidance}, nil
}

type fakeASRClient struct {
	mu          sync.Mutex
	transcripts []string
	next        int
}

func (f *fakeASRClient) Open(context.Context) (speech.ASRSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := f.transcripts[f.next]
	f.next++
	return &fakeASRSession{text: text}, nil
}

type fakeASRSession struct {
	text string
}

func (f *fakeASRSession) AppendPCM(context.Context, []byte) error { return nil }
func (f *fakeASRSession) Complete(context.Context) (string, error) {
	if f.text == "" {
		return "", speech.ErrEmptyTranscript
	}
	return f.text, nil
}
func (f *fakeASRSession) Close() error { return nil }

type fakeTTSClient struct {
	mu    sync.Mutex
	texts []string
}

func (f *fakeTTSClient) Stream(ctx context.Context, text string, onPCM func([]byte) error) error {
	f.mu.Lock()
	f.texts = append(f.texts, text)
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return onPCM(make([]byte, voice.PCMFrameBytes))
	}
}

func (f *fakeTTSClient) countText(text string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, value := range f.texts {
		if value == text {
			count++
		}
	}
	return count
}

type fakeVoiceOutput struct {
	events chan voice.ServerEvent
	mu     sync.Mutex
	pcm    [][]byte
}

func newFakeVoiceOutput() *fakeVoiceOutput {
	return &fakeVoiceOutput{events: make(chan voice.ServerEvent, 128)}
}

func (f *fakeVoiceOutput) SendEvent(_ context.Context, event voice.ServerEvent) error {
	f.events <- event
	return nil
}

func (f *fakeVoiceOutput) SendPCM(_ context.Context, frame []byte) error {
	f.mu.Lock()
	f.pcm = append(f.pcm, append([]byte(nil), frame...))
	f.mu.Unlock()
	return nil
}

func waitForEvent(t *testing.T, output *fakeVoiceOutput, eventType string, timeout time.Duration) voice.ServerEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-output.events:
			if event.Type == eventType {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func waitForReplyEnd(t *testing.T, output *fakeVoiceOutput, turnID string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-output.events:
			if event.Type == voice.EventPlaybackEnd && event.Reason == "completed" {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for reply end: %s", turnID)
		}
	}
}

func waitForGuidanceEnd(t *testing.T, output *fakeVoiceOutput, timeout time.Duration) {
	t.Helper()
	waitForEvent(t, output, voice.EventPlaybackEnd, timeout)
}
