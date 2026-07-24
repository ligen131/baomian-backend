package service

import (
	"encoding/json"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/state"
)

func snapshot(session *model.NightSession) state.Snapshot {
	return state.Snapshot{
		Phase:            state.Phase(session.Phase),
		ResumePhase:      state.Phase(session.ResumePhase),
		BoxClosed:        session.BoxClosed,
		AudioPlaying:     session.AudioPlaying,
		SunriseProgress:  session.SunriseProgress,
		PausedForTonight: session.PausedForTonight,
	}
}

func applySnapshot(session *model.NightSession, value state.Snapshot) {
	session.Phase = string(value.Phase)
	session.ResumePhase = string(value.ResumePhase)
	session.BoxClosed = value.BoxClosed
	session.AudioPlaying = value.AudioPlaying
	session.SunriseProgress = value.SunriseProgress
	session.PausedForTonight = value.PausedForTonight
}

func publish(hub *realtime.Hub, userID, eventType string, data any) {
	if hub == nil {
		return
	}
	hub.Publish(userID, dto.WSEvent{Type: eventType, OccurredAt: time.Now().UTC(), Data: data})
}

func decodeAI(raw []byte) (dto.AIResult, error) {
	var result dto.AIResult
	return result, json.Unmarshal(raw, &result)
}

func profileDate(now time.Time, timeZone string) time.Time {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.UTC
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func clearConversationTiming(session *model.NightSession) {
	session.ConversationSilenceDeadlineAt = nil
	session.ConversationHardDeadlineAt = nil
	session.ConversationProcessingUntil = nil
}

func applySessionTiming(
	session *model.NightSession,
	previousPhase string,
	trigger state.Trigger,
	now time.Time,
	silenceTimeout time.Duration,
	maxDuration time.Duration,
	resumeWindow time.Duration,
) {
	if session.Phase == string(state.Conversation) && previousPhase != string(state.Conversation) {
		hardDeadline := now.Add(maxDuration)
		silenceDeadline := now.Add(silenceTimeout)
		session.ConversationStartedAt = &now
		session.ConversationLastActivityAt = &now
		session.ConversationHardDeadlineAt = &hardDeadline
		session.ConversationSilenceDeadlineAt = &silenceDeadline
		session.FinalizeReason = ""
	}
	if trigger == state.BoxOpened && session.Phase == string(state.PhoneRemoved) {
		resumeDeadline := now.Add(resumeWindow)
		session.PhoneRemovedAt = &now
		session.ResumeDeadlineAt = &resumeDeadline
		session.AudioEndsAt = nil
	}
	if trigger == state.BoxClosed && previousPhase == string(state.PhoneRemoved) {
		session.PhoneRemovedAt = nil
		session.ResumeDeadlineAt = nil
	}
	if trigger == state.StopAudio || trigger == state.AlarmStart || trigger == state.MarkAwake {
		session.AudioEndsAt = nil
	}
	if session.Phase != string(state.Conversation) && previousPhase == string(state.Conversation) {
		clearConversationTiming(session)
	}
}
