package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/google/uuid"
)

func TestVoiceSessionDoesNotCompleteAfterThreeTurns(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Locked), runID: uuid.New()}
	conversation.responses = []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "第一轮回复", "breathing_46", false),
		voiceTurnResponse(2, "第二轮回复", "breathing_46", false),
		voiceTurnResponse(3, "第三轮回复", "breathing_46", false),
		voiceTurnResponse(4, "第四轮回复", "breathing_46", false),
	}
	asr := &fakeASRClient{transcripts: []string{"第一轮", "第二轮", "第三轮", "第四轮"}}
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

	for turn := 1; turn <= 4; turn++ {
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

	assertNoEvent(t, output, voice.EventConversationComplete, 50*time.Millisecond)

	conversation.mu.Lock()
	requests := append([]dto.ConversationTurnRequest(nil), conversation.requests...)
	conversation.mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("turn calls = %d", len(requests))
	}
	for index, request := range requests {
		if request.RunID.String() != conversation.runID.String() || request.InputMode != "voice" || request.ClientRequestID != "turn-"+string(rune('1'+index)) {
			t.Fatalf("request[%d] = %#v", index, request)
		}
	}
	if tonight.selected != "" {
		t.Fatalf("ordinary turns selected guidance %q", tonight.selected)
	}
	if tts.countText("开场白") != 1 || tts.countText("呼吸脚本") != 0 {
		t.Fatalf("TTS texts = %#v", tts.texts)
	}
}

func TestVoiceSessionAcceptsBurstWhileASRAppendIsBlocked(t *testing.T) {
	blocking := newBlockedAppendASRSession()
	output := newFakeVoiceOutput()
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), responses: []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "回复", "rain", false),
	}}
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &singleASRClient{session: blocking}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-burst"}); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		for index := 0; index < 165; index++ {
			frame := make([]byte, voice.PCMFrameBytes)
			frame[0] = byte(index)
			if err := session.HandlePCM(context.Background(), frame); err != nil {
				accepted <- err
				return
			}
		}
		accepted <- nil
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("burst upload blocked on slow ASR AppendPCM")
	}
	close(blocking.release)
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-burst"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.completed:
	case <-time.After(2 * time.Second):
		t.Fatal("ASR final did not run")
	}
}

func TestVoiceSessionDrainsBurstPCMInOrderBeforeASRFinal(t *testing.T) {
	recording := newRecordingASRSession()
	output := newFakeVoiceOutput()
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), responses: []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "回复", "rain", false),
	}}
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &singleASRClient{session: recording}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-burst"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 165; index++ {
		frame := make([]byte, voice.PCMFrameBytes)
		frame[0] = byte(index)
		if err := session.HandlePCM(context.Background(), frame); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
	}
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-burst"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recording.completed:
	case <-time.After(2 * time.Second):
		t.Fatal("ASR final did not run after burst drain")
	}
	recording.mu.Lock()
	frames := append([]byte(nil), recording.firstBytes...)
	recording.mu.Unlock()
	if len(frames) != 165 {
		t.Fatalf("frames = %d", len(frames))
	}
	for index, first := range frames {
		if first != byte(index) {
			t.Fatalf("frame[%d] = %d", index, first)
		}
	}
}

func TestVoiceSessionInputEndReturnsWhileASRFinalIsPending(t *testing.T) {
	blocking := newBlockingASRSession()
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(&fakeVoiceConversation{phase: string(state.Conversation)}, &fakeVoiceTonight{}, &singleASRClient{session: blocking}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		ended <- session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-1"})
	}()
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("input.end blocked on ASR final")
	}

	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e3", TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorTurnInProgress || !event.Retryable {
		t.Fatalf("event = %#v", event)
	}
	blocking.finish("", &speech.UpstreamError{Service: "asr", Code: "timeout", RequestID: "request-1", Retryable: true})
	event = waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorASRUnavailable || !event.Retryable {
		t.Fatalf("event = %#v", event)
	}
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e4", TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	accepted := waitForEvent(t, output, voice.EventInputAccepted, time.Second)
	if accepted.TurnID != "turn-2" {
		t.Fatalf("accepted = %#v", accepted)
	}
}

func TestVoiceSessionCloseBeforeInputEndDoesNotCallConversation(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Conversation)}
	blocking := newBlockedAppendASRSession()
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &singleASRClient{session: blocking}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)

	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-abandoned"}); err != nil {
		t.Fatal(err)
	}
	if err := session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes)); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	conversation.mu.Lock()
	calls := len(conversation.requests)
	conversation.mu.Unlock()
	if calls != 0 {
		t.Fatalf("conversation calls = %d", calls)
	}
}

func TestVoiceSessionCloseAfterInputEndDoesNotCancelPersistence(t *testing.T) {
	blocking := newBlockingASRSession()
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), runID: uuid.New(), responses: []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "回复", "rain", false),
	}}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &singleASRClient{session: blocking}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	bindTestRun(session, conversation.runID)

	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-1"})
	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-1"})
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("ASR final did not start")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	blocking.finish("断线后仍要保存", nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conversation.mu.Lock()
		calls := len(conversation.requests)
		conversation.mu.Unlock()
		if calls == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("input.end accepted turn was not persisted after disconnect")
}

func TestVoiceSessionProtectsConversationDuringReplyPlayback(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), runID: uuid.New()}
	conversation.responses = []dto.ConversationTurnResponse{
		voiceTurnResponse(1, "第一轮回复", "breathing_46", false),
	}
	asr := &fakeASRClient{transcripts: []string{"第一轮"}}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, asr, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()
	bindTestRun(session, conversation.runID)

	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-1"})
	_ = session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes))
	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-1"})
	waitForReplyEnd(t, output, "turn-1", time.Second)

	deadline := time.Now().Add(time.Second)
	var lifecycle []string
	for time.Now().Before(deadline) {
		conversation.mu.Lock()
		lifecycle = append([]string(nil), conversation.playbackLifecycle...)
		conversation.mu.Unlock()
		if len(lifecycle) == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(lifecycle) != 2 || lifecycle[0] != "begin" || lifecycle[1] != "end" {
		t.Fatalf("playback lifecycle = %#v, want [begin end]", lifecycle)
	}
	conversation.mu.Lock()
	delivered := append([]string(nil), conversation.deliveredTurns...)
	conversation.mu.Unlock()
	if len(delivered) != 1 || delivered[0] != "turn-1" {
		t.Fatalf("delivered turns = %#v", delivered)
	}
}

func TestVoiceSessionConversationFinishStreamsGuidancePCM(t *testing.T) {
	for _, guidance := range []string{"rain", "breathing_46"} {
		t.Run(guidance, func(t *testing.T) {
			runID := uuid.New()
			journalID := uuid.New()
			conversation := &fakeVoiceConversation{
				phase: string(state.Conversation), runID: runID,
				finishResponse: dto.FinalizeResponse{
					Journal: dto.MemoryCard{ID: journalID, SuggestedGuidance: guidance},
					Tonight: dto.TonightState{Phase: string(state.ChoosingGuidance), ConversationTurns: 4, WhiteNoiseDurationMin: 20},
				},
			}
			sleepAudio := &fakeSleepAudio{frames: [][]byte{
				makePCMFrame(1),
				makePCMFrame(2),
			}}
			output := newFakeVoiceOutput()
			service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
			service.ConfigureSleepAudio(sleepAudio)
			session := service.NewSession("user", "device", output)
			defer session.Close()
			if err := session.Ready(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitForEvent(t, output, voice.EventSessionReady, time.Second)
			if err := session.HandleEvent(context.Background(), voice.ClientEvent{
				Type: voice.EventConversationFinish, RunID: runID.String(), EventID: "finish-1",
			}); err != nil {
				t.Fatal(err)
			}

			completed := waitForEvent(t, output, voice.EventConversationComplete, time.Second)
			if completed.RunID != runID.String() || completed.EventID != "finish-1" || completed.JournalID != journalID.String() || completed.Guidance != guidance || completed.OccurredAt == "" {
				t.Fatalf("completed = %#v", completed)
			}
			started := waitForEvent(t, output, voice.EventPlaybackStart, time.Second)
			if started.RunID != runID.String() || started.PlaybackID == "" || started.Kind != "guidance" || started.Guidance != guidance || started.Audio == nil || started.OccurredAt == "" {
				t.Fatalf("playback.start = %#v", started)
			}
			ended := waitForEvent(t, output, voice.EventPlaybackEnd, time.Second)
			if ended.RunID != runID.String() || ended.PlaybackID != started.PlaybackID || ended.Reason != "completed" || ended.OccurredAt == "" {
				t.Fatalf("playback.end = %#v", ended)
			}
			output.mu.Lock()
			pcm := cloneFrames(output.pcm)
			output.mu.Unlock()
			if len(pcm) != 2 || pcm[0][0] != 1 || pcm[1][0] != 2 {
				t.Fatalf("PCM frames = %#v", pcm)
			}
			if sleepAudio.guidance != guidance {
				t.Fatalf("streamed guidance = %q", sleepAudio.guidance)
			}
			assertNoGuidancePlaybackRestart(t, output, 20*time.Millisecond)
			conversation.mu.Lock()
			finishEvents := append([]string(nil), conversation.finishEvents...)
			conversation.mu.Unlock()
			if len(finishEvents) != 1 || finishEvents[0] != "finish-1" {
				t.Fatalf("finish events = %#v", finishEvents)
			}
		})
	}
}

func TestVoiceSessionGuidanceLifecycleUpdatesPersistedStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		streamErr  error
		wantReason string
		wantStatus []string
	}{
		{name: "completed", wantReason: "completed", wantStatus: []string{model.GuidancePlaying, model.GuidanceCompleted}},
		{name: "failed", streamErr: context.DeadlineExceeded, wantReason: "failed", wantStatus: []string{model.GuidancePlaying, model.GuidanceInterrupted}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runID := uuid.New()
			conversation := &fakeVoiceConversation{phase: string(state.Conversation), runID: runID}
			output := newFakeVoiceOutput()
			service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
			service.ConfigureSleepAudio(&fakeSleepAudio{frames: [][]byte{makePCMFrame(1)}, err: test.streamErr})
			session := service.NewSession("user", "device", output).(*voiceSession)
			session.runID = runID.String()
			defer session.Close()

			err := session.streamSleepPlayback(context.Background(), "guidance-1", "rain")
			if test.streamErr == nil && err != nil {
				t.Fatal(err)
			}
			if test.streamErr != nil && err == nil {
				t.Fatal("sleep audio failure was not returned")
			}
			events := drainEvents(output.events)
			if len(events) != 2 || events[1].Reason != test.wantReason {
				t.Fatalf("events = %#v", events)
			}
			conversation.mu.Lock()
			statuses := append([]string(nil), conversation.guidanceStatuses...)
			conversation.mu.Unlock()
			if len(statuses) != len(test.wantStatus) {
				t.Fatalf("statuses = %#v", statuses)
			}
			for index := range statuses {
				if statuses[index] != test.wantStatus[index] {
					t.Fatalf("statuses = %#v", statuses)
				}
			}
		})
	}
}

func TestStreamSleepPlaybackEndsFailedWhenSourceFails(t *testing.T) {
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(&fakeVoiceConversation{}, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	service.ConfigureSleepAudio(&fakeSleepAudio{err: context.DeadlineExceeded})
	session := service.NewSession("user", "device", output).(*voiceSession)
	session.runID = uuid.NewString()
	defer session.Close()

	if err := session.streamSleepPlayback(context.Background(), "guidance-1", "rain"); err == nil {
		t.Fatal("sleep audio failure was not returned")
	}
	events := drainEvents(output.events)
	if len(events) != 2 || events[0].Type != voice.EventPlaybackStart || events[1].Type != voice.EventPlaybackEnd || events[1].Reason != "failed" {
		t.Fatalf("events = %#v", events)
	}
}

func TestStreamSleepPlaybackRejectsInvalidFrame(t *testing.T) {
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(&fakeVoiceConversation{}, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	service.ConfigureSleepAudio(&fakeSleepAudio{frames: [][]byte{make([]byte, voice.PCMFrameBytes-1)}})
	session := service.NewSession("user", "device", output).(*voiceSession)
	session.runID = uuid.NewString()
	defer session.Close()

	if err := session.streamSleepPlayback(context.Background(), "guidance-1", "rain"); err == nil {
		t.Fatal("invalid sleep audio frame was accepted")
	}
	events := drainEvents(output.events)
	if len(events) != 2 || events[1].Type != voice.EventPlaybackEnd || events[1].Reason != "failed" {
		t.Fatalf("events = %#v", events)
	}
	output.mu.Lock()
	pcmCount := len(output.pcm)
	output.mu.Unlock()
	if pcmCount != 0 {
		t.Fatalf("sent %d invalid PCM frames", pcmCount)
	}
}

func TestVoiceSessionRejectsControlEventForDifferentRun(t *testing.T) {
	runID := uuid.New()
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), runID: runID}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()
	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, output, voice.EventSessionReady, time.Second)
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{
		Type: voice.EventSessionStart, RunID: uuid.NewString(), EventID: "wrong-run",
	}); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorInvalidEvent || event.RunID != runID.String() {
		t.Fatalf("event = %#v", event)
	}
}

func TestVoiceSessionReadyCarriesRunRecoveryContext(t *testing.T) {
	runID := uuid.New()
	conversation := &fakeVoiceConversation{
		phase: string(state.Conversation), runID: runID,
		recovery: voice.RecoveryState{RunStatus: model.ConversationRunActive, ResumeAction: "replay_reply", PendingTurnID: "turn-4"},
	}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, output, voice.EventSessionReady, time.Second)
	if event.Phase != string(state.Conversation) || event.RunID != runID.String() || event.Recovery == nil ||
		event.Recovery.ResumeAction != "replay_reply" || event.Recovery.PendingTurnID != "turn-4" {
		t.Fatalf("event = %#v", event)
	}
	conversation.mu.Lock()
	calls := append([]string(nil), conversation.readyLifecycle...)
	conversation.mu.Unlock()
	if len(calls) != 2 || calls[0] != "prepare:user:device" || calls[1] != "history" {
		t.Fatalf("ready lifecycle = %#v", calls)
	}
}

func TestVoiceSessionReplaysPersistedReplyOnSessionStart(t *testing.T) {
	runID := uuid.New()
	conversation := &fakeVoiceConversation{
		phase: string(state.Conversation), runID: runID,
		recovery:     voice.RecoveryState{RunStatus: model.ConversationRunActive, ResumeAction: "replay_reply", PendingTurnID: "turn-4"},
		historyTurns: []dto.ConversationTurn{{RunID: runID, Role: "assistant", Text: "从头重播的回复", TurnIndex: 4, ClientRequestID: "turn-4"}},
	}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()
	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, output, voice.EventSessionReady, time.Second)
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventSessionStart, RunID: runID.String(), EventID: "resume-1"}); err != nil {
		t.Fatal(err)
	}
	started := waitForEvent(t, output, voice.EventPlaybackStart, time.Second)
	if started.Kind != "reply" || started.TurnID != "turn-4" || started.RunID != runID.String() {
		t.Fatalf("playback.start = %#v", started)
	}
	waitForEvent(t, output, voice.EventPlaybackEnd, time.Second)
}

func TestVoiceSessionReplaysGuidanceFromStart(t *testing.T) {
	runID := uuid.New()
	journalID := uuid.NewString()
	conversation := &fakeVoiceConversation{
		phase: string(state.Sleeping), runID: runID,
		recovery: voice.RecoveryState{
			RunStatus: model.ConversationRunCompleted, ResumeAction: "replay_guidance",
			FinishEventID: "finish-1", JournalID: journalID, Guidance: "rain", GuidanceStatus: model.GuidanceInterrupted,
		},
	}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	service.ConfigureSleepAudio(&fakeSleepAudio{frames: [][]byte{makePCMFrame(7)}})
	session := service.NewSession("user", "device", output)
	defer session.Close()
	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, output, voice.EventSessionReady, time.Second)
	if err := session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventSessionStart, RunID: runID.String(), EventID: "resume-1"}); err != nil {
		t.Fatal(err)
	}
	completed := waitForEvent(t, output, voice.EventConversationComplete, time.Second)
	if completed.JournalID != journalID || completed.EventID != "finish-1" || completed.Guidance != "rain" {
		t.Fatalf("conversation.completed = %#v", completed)
	}
	waitForEvent(t, output, voice.EventPlaybackStart, time.Second)
	ended := waitForEvent(t, output, voice.EventPlaybackEnd, time.Second)
	if ended.Reason != "completed" {
		t.Fatalf("playback.end = %#v", ended)
	}
}

func TestVoiceSessionReadyMapsBlockedDemoRestart(t *testing.T) {
	conversation := &fakeVoiceConversation{prepareErr: NewError("request_in_progress", "processing", nil)}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorTurnInProgress || !event.Retryable {
		t.Fatalf("event = %#v", event)
	}
}

func TestVoiceSessionReadyReportsInconsistentPersistedState(t *testing.T) {
	conversation := &fakeVoiceConversation{
		historyErr: &Error{Code: "invalid_transition", Message: "今晚状态异常", Details: map[string]any{"phase": string(state.ChoosingGuidance), "completedTurns": 0}},
	}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, &fakeASRClient{}, &fakeTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()

	if err := session.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, output, voice.EventError, time.Second)
	if event.Code != voice.ErrorInvalidPhase || event.Retryable {
		t.Fatalf("event = %#v", event)
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

func TestMapConversationErrorUsesSpecificDeviceCodes(t *testing.T) {
	tests := []struct {
		internal  string
		device    string
		retryable bool
	}{
		{internal: "invalid_transition", device: voice.ErrorInvalidPhase, retryable: false},
		{internal: "conversation_limit", device: voice.ErrorConversationLimit, retryable: false},
		{internal: "request_in_progress", device: voice.ErrorTurnInProgress, retryable: true},
		{internal: "conversation_expired", device: voice.ErrorConversationExpired, retryable: false},
		{internal: "storage_error", device: voice.ErrorServiceUnavailable, retryable: true},
		{internal: "ai_error", device: voice.ErrorAIUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.internal, func(t *testing.T) {
			mapped := mapConversationError(NewError(test.internal, "internal", nil))
			if mapped.code != test.device || mapped.retryable != test.retryable {
				t.Fatalf("mapped = %#v, want code=%q retryable=%v", mapped, test.device, test.retryable)
			}
		})
	}
}

func TestStreamPlaybackEndsAfterPCMOutputFailure(t *testing.T) {
	output := &failingPCMVoiceOutput{fakeVoiceOutput: newFakeVoiceOutput()}
	service := NewVoiceSessionService(&fakeVoiceConversation{}, &fakeVoiceTonight{}, &fakeASRClient{}, &partialTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output).(*voiceSession)
	defer session.Close()

	err := session.streamPlayback(context.Background(), "playback-1", "reply", 1, "turn-1", "回复")
	if err == nil {
		t.Fatal("PCM output failure was not returned")
	}
	events := drainEvents(output.events)
	var starts, ends, ttsErrors int
	var endReason string
	for _, event := range events {
		switch event.Type {
		case voice.EventPlaybackStart:
			starts++
		case voice.EventPlaybackEnd:
			ends++
			endReason = event.Reason
		case voice.EventError:
			if event.Code == voice.ErrorTTSUnavailable {
				ttsErrors++
			}
		}
	}
	if starts != 1 || ends != 1 || endReason != "failed" || ttsErrors != 0 {
		t.Fatalf("events = %#v", events)
	}
}

func TestStreamPlaybackEndsBeforeTTSUnavailableError(t *testing.T) {
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(&fakeVoiceConversation{}, &fakeVoiceTonight{}, &fakeASRClient{}, errorTTSClient{}, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output).(*voiceSession)
	defer session.Close()

	if err := session.streamPlayback(context.Background(), "playback-1", "reply", 1, "turn-1", "回复"); err == nil {
		t.Fatal("TTS failure was not returned")
	}
	events := drainEvents(output.events)
	if len(events) != 3 || events[0].Type != voice.EventPlaybackStart || events[1].Type != voice.EventPlaybackEnd || events[1].Reason != "failed" || events[2].Type != voice.EventError || events[2].Code != voice.ErrorTTSUnavailable {
		t.Fatalf("events = %#v", events)
	}
}

func TestVoiceSessionOrdinaryTurnDoesNotStartRainGuidance(t *testing.T) {
	conversation := &fakeVoiceConversation{phase: string(state.Conversation), runID: uuid.New()}
	conversation.responses = []dto.ConversationTurnResponse{voiceTurnResponse(3, "晚安", "rain", false)}
	asr := &fakeASRClient{transcripts: []string{"晚安"}}
	tts := &fakeTTSClient{}
	output := newFakeVoiceOutput()
	service := NewVoiceSessionService(conversation, &fakeVoiceTonight{}, asr, tts, "开场", "呼吸", 60*time.Second)
	session := service.NewSession("user", "device", output)
	defer session.Close()
	bindTestRun(session, conversation.runID)

	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputStart, EventID: "e1", TurnID: "turn-rain"})
	_ = session.HandlePCM(context.Background(), make([]byte, voice.PCMFrameBytes))
	_ = session.HandleEvent(context.Background(), voice.ClientEvent{Type: voice.EventInputEnd, EventID: "e2", TurnID: "turn-rain"})
	waitForReplyEnd(t, output, "turn-rain", 2*time.Second)
	assertNoGuidancePlaybackRestart(t, output, 50*time.Millisecond)
	if tts.countText("呼吸") != 0 {
		t.Fatal("ordinary turn must not start guidance")
	}
}

func bindTestRun(session VoiceSession, runID uuid.UUID) {
	session.(*voiceSession).runID = runID.String()
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
	mu                sync.Mutex
	phase             string
	requests          []dto.ConversationTurnRequest
	responses         []dto.ConversationTurnResponse
	playbackLifecycle []string
	readyLifecycle    []string
	prepareErr        error
	historyErr        error
	runID             uuid.UUID
	recovery          voice.RecoveryState
	historyTurns      []dto.ConversationTurn
	finishResponse    dto.FinalizeResponse
	finishEvents      []string
	guidanceStatuses  []string
	deliveredTurns    []string
}

func (f *fakeVoiceConversation) PrepareVoiceSession(_ context.Context, userID, deviceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readyLifecycle = append(f.readyLifecycle, "prepare:"+userID+":"+deviceID)
	return f.prepareErr
}

func (f *fakeVoiceConversation) History(context.Context, string) (dto.ConversationHistoryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readyLifecycle = append(f.readyLifecycle, "history")
	if f.historyErr != nil {
		return dto.ConversationHistoryResponse{}, f.historyErr
	}
	recovery := f.recovery
	if recovery.RunStatus == "" && f.runID != uuid.Nil {
		recovery = voice.RecoveryState{RunStatus: model.ConversationRunActive, ResumeAction: "listen"}
	}
	return dto.ConversationHistoryResponse{
		RunID: f.runID, Recovery: recovery, Turns: append([]dto.ConversationTurn(nil), f.historyTurns...),
		Tonight: dto.TonightState{Phase: f.phase, ConversationTurns: len(f.requests)},
	}, nil
}

func (f *fakeVoiceConversation) Turn(_ context.Context, _ string, request dto.ConversationTurnRequest) (dto.ConversationTurnResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	return f.responses[len(f.requests)-1], nil
}

func (f *fakeVoiceConversation) FinishRun(_ context.Context, _ string, _ uuid.UUID, eventID string) (dto.FinalizeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishEvents = append(f.finishEvents, eventID)
	return f.finishResponse, nil
}

func (f *fakeVoiceConversation) CompleteReplyDelivery(_ context.Context, _ string, _ uuid.UUID, turnID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveredTurns = append(f.deliveredTurns, turnID)
	return nil
}

func (f *fakeVoiceConversation) UpdateGuidanceStatus(_ context.Context, _ string, _ uuid.UUID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.guidanceStatuses = append(f.guidanceStatuses, status)
	return nil
}

func (f *fakeVoiceConversation) BeginPlayback(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playbackLifecycle = append(f.playbackLifecycle, "begin")
	return nil
}

func (f *fakeVoiceConversation) EndPlayback(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playbackLifecycle = append(f.playbackLifecycle, "end")
	return nil
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

type blockedAppendASRSession struct {
	release   chan struct{}
	completed chan struct{}
}

func newBlockedAppendASRSession() *blockedAppendASRSession {
	return &blockedAppendASRSession{release: make(chan struct{}), completed: make(chan struct{})}
}

func (f *blockedAppendASRSession) AppendPCM(ctx context.Context, _ []byte) error {
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *blockedAppendASRSession) Complete(context.Context) (string, error) {
	close(f.completed)
	return "测试", nil
}

func (f *blockedAppendASRSession) Close() error { return nil }

type recordingASRSession struct {
	mu         sync.Mutex
	firstBytes []byte
	completed  chan struct{}
}

func newRecordingASRSession() *recordingASRSession {
	return &recordingASRSession{completed: make(chan struct{})}
}

func (f *recordingASRSession) AppendPCM(_ context.Context, frame []byte) error {
	f.mu.Lock()
	f.firstBytes = append(f.firstBytes, frame[0])
	f.mu.Unlock()
	return nil
}

func (f *recordingASRSession) Complete(context.Context) (string, error) {
	close(f.completed)
	return "测试", nil
}

func (f *recordingASRSession) Close() error { return nil }

type singleASRClient struct {
	session speech.ASRSession
}

func (f *singleASRClient) Open(context.Context) (speech.ASRSession, error) {
	return f.session, nil
}

type blockingASRSession struct {
	started  chan struct{}
	result   chan blockingASRResult
	canceled chan struct{}
}

type blockingASRResult struct {
	text string
	err  error
}

func newBlockingASRSession() *blockingASRSession {
	return &blockingASRSession{
		started: make(chan struct{}), result: make(chan blockingASRResult, 1), canceled: make(chan struct{}),
	}
}

func (f *blockingASRSession) AppendPCM(context.Context, []byte) error { return nil }
func (f *blockingASRSession) Complete(ctx context.Context) (string, error) {
	close(f.started)
	select {
	case result := <-f.result:
		return result.text, result.err
	case <-ctx.Done():
		close(f.canceled)
		return "", ctx.Err()
	}
}
func (f *blockingASRSession) Close() error { return nil }
func (f *blockingASRSession) finish(text string, err error) {
	f.result <- blockingASRResult{text: text, err: err}
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

type errorTTSClient struct{}

func (errorTTSClient) Stream(context.Context, string, func([]byte) error) error {
	return context.DeadlineExceeded
}

type partialTTSClient struct{}

func (partialTTSClient) Stream(_ context.Context, _ string, onPCM func([]byte) error) error {
	return onPCM(make([]byte, voice.PCMFrameBytes/2))
}

type fakeSleepAudio struct {
	mu       sync.Mutex
	guidance string
	frames   [][]byte
	err      error
}

func (f *fakeSleepAudio) Stream(ctx context.Context, guidance string, onFrame func([]byte) error) error {
	f.mu.Lock()
	f.guidance = guidance
	frames := cloneFrames(f.frames)
	err := f.err
	f.mu.Unlock()
	for _, frame := range frames {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if callbackErr := onFrame(frame); callbackErr != nil {
			return callbackErr
		}
	}
	return err
}

func makePCMFrame(first byte) []byte {
	frame := make([]byte, voice.PCMFrameBytes)
	frame[0] = first
	return frame
}

func cloneFrames(frames [][]byte) [][]byte {
	result := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		result = append(result, append([]byte(nil), frame...))
	}
	return result
}

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

type failingPCMVoiceOutput struct {
	*fakeVoiceOutput
}

func (f *failingPCMVoiceOutput) SendPCM(context.Context, []byte) error {
	return context.DeadlineExceeded
}

func drainEvents(events <-chan voice.ServerEvent) []voice.ServerEvent {
	var result []voice.ServerEvent
	for {
		select {
		case event := <-events:
			result = append(result, event)
		default:
			return result
		}
	}
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

func assertNoGuidancePlaybackRestart(t *testing.T, output *fakeVoiceOutput, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-output.events:
			if event.Type == voice.EventPlaybackStart && event.Kind == "guidance" {
				t.Fatalf("unexpected guidance playback: %#v", event)
			}
		case <-deadline:
			return
		}
	}
}

func assertNoEvent(t *testing.T, output *fakeVoiceOutput, eventType string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-output.events:
			if event.Type == eventType {
				t.Fatalf("unexpected %s event: %#v", eventType, event)
			}
		case <-deadline:
			return
		}
	}
}

func waitForGuidanceEnd(t *testing.T, output *fakeVoiceOutput, timeout time.Duration) {
	t.Helper()
	waitForEvent(t, output, voice.EventPlaybackEnd, timeout)
}
