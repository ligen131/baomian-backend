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
