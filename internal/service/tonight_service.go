package service

import (
	"context"
	"errors"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/state"
)

type TonightService struct {
	store                      repository.Store
	hub                        *realtime.Hub
	defaultDeviceID            string
	conversationSilenceTimeout time.Duration
	conversationMaxDuration    time.Duration
	phoneRemovedResumeWindow   time.Duration
	now                        func() time.Time
}

func NewTonightService(
	store repository.Store,
	hub *realtime.Hub,
	defaultDeviceID string,
	conversationSilenceTimeout time.Duration,
	conversationMaxDuration time.Duration,
	phoneRemovedResumeWindow time.Duration,
) *TonightService {
	return &TonightService{
		store: store, hub: hub, defaultDeviceID: defaultDeviceID,
		conversationSilenceTimeout: conversationSilenceTimeout,
		conversationMaxDuration:    conversationMaxDuration,
		phoneRemovedResumeWindow:   phoneRemovedResumeWindow,
		now:                        time.Now,
	}
}

func (s *TonightService) Get(ctx context.Context, userID string) (dto.TonightState, error) {
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.TonightState{}, NewError("storage_error", "读取用户设置失败", err)
	}
	session, err := s.store.GetOrCreateTonight(ctx, userID, profileDate(s.now(), profile.TimeZone), false)
	if err != nil {
		return dto.TonightState{}, NewError("storage_error", "读取今晚状态失败", err)
	}
	return dto.TonightFromModels(session, profile), nil
}

func (s *TonightService) Action(ctx context.Context, userID string, request dto.TonightActionRequest) (dto.TonightState, error) {
	return s.applyAction(ctx, userID, request, true)
}

func (s *TonightService) StartVoiceConversation(ctx context.Context, userID string) (dto.TonightState, error) {
	return s.applyAction(ctx, userID, dto.TonightActionRequest{Action: "start_conversation"}, false)
}

func (s *TonightService) SelectVoiceGuidance(ctx context.Context, userID, guidance string) (dto.TonightState, error) {
	return s.applyAction(ctx, userID, dto.TonightActionRequest{Action: "select_guidance", Guidance: guidance}, false)
}

func (s *TonightService) applyAction(ctx context.Context, userID string, request dto.TonightActionRequest, enqueueCommands bool) (dto.TonightState, error) {
	var response dto.TonightState
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		profile, err := tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(now, profile.TimeZone), true)
		if err != nil {
			return err
		}

		if request.Action == "skip_tonight_reminders" {
			session.RemindersSkipped = true
			if err := tx.UpdateNightSession(ctx, session); err != nil {
				return err
			}
			response = dto.TonightFromModels(session, profile)
			return nil
		}
		if request.Action == "select_guidance" && !oneOf(request.Guidance, "rain", "brown_noise", "breathing_46", "silence") {
			return NewError("validation_error", "guidance 无效", nil)
		}
		trigger, err := actionTrigger(request.Action)
		if err != nil {
			return err
		}
		previousPhase := session.Phase
		resumeExpired := trigger == state.BoxClosed && session.Phase == string(state.PhoneRemoved) &&
			(session.ResumeDeadlineAt == nil || !now.Before(*session.ResumeDeadlineAt))
		if resumeExpired {
			session.ResumePhase = ""
		}
		next, err := state.Apply(snapshot(session), trigger)
		if err != nil {
			if errors.Is(err, state.ErrInvalidTransition) {
				return &Error{Code: "invalid_transition", Message: "当前状态不允许此操作", Details: map[string]any{"phase": session.Phase, "action": request.Action}, Cause: err}
			}
			return err
		}
		applySnapshot(session, next)
		applySessionTiming(session, previousPhase, trigger, now, s.conversationSilenceTimeout, s.conversationMaxDuration, s.phoneRemovedResumeWindow)
		if resumeExpired {
			session.PausedForTonight = true
		}

		if request.Action == "select_guidance" {
			session.SelectedGuidance = request.Guidance
			session.AudioPlaying = request.Guidance != "silence"
			if session.AudioPlaying && oneOf(request.Guidance, "rain", "brown_noise") {
				endsAt := now.Add(time.Duration(profile.WhiteNoiseDurationMin) * time.Minute)
				session.AudioEndsAt = &endsAt
			} else {
				session.AudioEndsAt = nil
			}
		}
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		if enqueueCommands {
			commands := actionCommands(userID, s.defaultDeviceID, request, profile.WhiteNoiseDurationMin)
			if err := tx.CreateDeviceCommands(ctx, commands); err != nil {
				return err
			}
		}
		response = dto.TonightFromModels(session, profile)
		return nil
	})
	if err != nil {
		var serviceErr *Error
		if errors.As(err, &serviceErr) {
			return dto.TonightState{}, serviceErr
		}
		return dto.TonightState{}, NewError("storage_error", "更新今晚状态失败", err)
	}
	publish(s.hub, userID, "tonight.updated", response)
	return response, nil
}

func actionTrigger(action string) (state.Trigger, error) {
	mapping := map[string]state.Trigger{
		"simulate_box_closed": state.BoxClosed,
		"simulate_box_opened": state.BoxOpened,
		"start_conversation":  state.StartConversation,
		"select_guidance":     state.SelectGuidance,
		"stop_audio":          state.StopAudio,
		"simulate_alarm":      state.AlarmStart,
		"snooze":              state.Snooze,
		"mark_awake":          state.MarkAwake,
	}
	trigger, ok := mapping[action]
	if !ok {
		return "", NewError("validation_error", "未知的 action", nil)
	}
	return trigger, nil
}

func actionCommands(userID, deviceID string, request dto.TonightActionRequest, whiteNoiseDurationMin int) []model.DeviceCommand {
	command := func(commandType string, payload any) model.DeviceCommand {
		return model.DeviceCommand{DeviceID: deviceID, UserID: userID, Type: commandType, Payload: model.JSON(payload), Status: "pending", AckPayload: model.JSON(map[string]any{})}
	}
	switch request.Action {
	case "simulate_box_closed":
		return []model.DeviceCommand{command("audio.confirm", map[string]any{"message": "手机已经安放好了"}), command("led.off", map[string]any{})}
	case "simulate_box_opened":
		return []model.DeviceCommand{command("audio.pause", map[string]any{})}
	case "select_guidance":
		if request.Guidance == "silence" {
			return []model.DeviceCommand{command("audio.stop", map[string]any{}), command("led.off", map[string]any{})}
		}
		payload := map[string]any{"guidance": request.Guidance}
		if oneOf(request.Guidance, "rain", "brown_noise") {
			payload["durationMinutes"] = whiteNoiseDurationMin
		}
		return []model.DeviceCommand{command("audio.play", payload), command("led.off", map[string]any{})}
	case "stop_audio":
		return []model.DeviceCommand{command("audio.stop", map[string]any{}), command("led.off", map[string]any{})}
	case "simulate_alarm":
		return []model.DeviceCommand{command("sunrise.start", map[string]any{"durationMinutes": 25})}
	case "snooze":
		return []model.DeviceCommand{command("alarm.snooze", map[string]any{"minutes": 5}), command("led.off", map[string]any{})}
	case "mark_awake":
		return []model.DeviceCommand{command("alarm.stop", map[string]any{})}
	default:
		return nil
	}
}
