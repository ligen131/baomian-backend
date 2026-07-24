package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/state"
	"github.com/google/uuid"
)

type DeviceService struct {
	store                      repository.Store
	hub                        *realtime.Hub
	defaultUser                string
	commandLease               time.Duration
	commandMaxAttempts         int
	conversationSilenceTimeout time.Duration
	conversationMaxDuration    time.Duration
	phoneRemovedResumeWindow   time.Duration
	now                        func() time.Time
}

func NewDeviceService(
	store repository.Store,
	hub *realtime.Hub,
	defaultUser string,
	commandLease time.Duration,
	commandMaxAttempts int,
	conversationSilenceTimeout time.Duration,
	conversationMaxDuration time.Duration,
	phoneRemovedResumeWindow time.Duration,
) *DeviceService {
	return &DeviceService{
		store: store, hub: hub, defaultUser: defaultUser,
		commandLease: commandLease, commandMaxAttempts: commandMaxAttempts,
		conversationSilenceTimeout: conversationSilenceTimeout,
		conversationMaxDuration:    conversationMaxDuration,
		phoneRemovedResumeWindow:   phoneRemovedResumeWindow,
		now:                        time.Now,
	}
}

func (s *DeviceService) HandleEvent(ctx context.Context, request dto.DeviceEventRequest) (dto.DeviceEventResponse, error) {
	if existing, err := s.store.GetDeviceEventByEventID(ctx, request.EventID); err == nil {
		var response dto.DeviceEventResponse
		if json.Unmarshal(existing.Result, &response) == nil {
			response.Duplicate = true
			return response, nil
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return dto.DeviceEventResponse{}, NewError("storage_error", "查询设备事件失败", err)
	}

	userID := request.UserID
	if userID == "" {
		userID = s.defaultUser
	}
	occurredAt := s.now().UTC()
	if request.OccurredAt != nil {
		occurredAt = request.OccurredAt.UTC()
	}
	var response dto.DeviceEventResponse
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		profile, err := tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		device, err := tx.GetDevice(ctx, userID, request.DeviceID)
		if errors.Is(err, repository.ErrNotFound) {
			device = &model.Device{
				DeviceID: request.DeviceID, UserID: userID,
				Capabilities: model.JSON(map[string]any{}), Status: model.JSON(map[string]any{}),
			}
		} else if err != nil {
			return err
		}
		device.LastSeenAt = s.now().UTC()
		if err := tx.UpsertDevice(ctx, device); err != nil {
			return err
		}
		session, err := tx.GetOrCreateTonight(ctx, userID, profileDate(occurredAt, profile.TimeZone), true)
		if err != nil {
			return err
		}
		trigger, err := deviceTrigger(request.Type, session.Phase)
		if err != nil {
			return err
		}
		previousPhase := session.Phase
		resumeExpired := trigger == state.BoxClosed && session.Phase == string(state.PhoneRemoved) &&
			(session.ResumeDeadlineAt == nil || !s.now().UTC().Before(*session.ResumeDeadlineAt))
		if resumeExpired {
			session.ResumePhase = ""
		}
		next, err := state.Apply(snapshot(session), trigger)
		if err != nil {
			if errors.Is(err, state.ErrInvalidTransition) {
				return &Error{Code: "invalid_transition", Message: "当前状态不接受此设备事件", Details: map[string]any{"phase": session.Phase, "eventType": request.Type}, Cause: err}
			}
			return err
		}
		applySnapshot(session, next)
		applySessionTiming(
			session, previousPhase, trigger, s.now().UTC(),
			s.conversationSilenceTimeout, s.conversationMaxDuration, s.phoneRemovedResumeWindow,
		)
		if resumeExpired {
			session.PausedForTonight = true
		}
		commands := deviceCommands(userID, request.DeviceID, request.Type, state.Phase(session.Phase))
		if err := tx.UpdateNightSession(ctx, session); err != nil {
			return err
		}
		if err := tx.CreateDeviceCommands(ctx, commands); err != nil {
			return err
		}
		converted := make([]dto.Command, 0, len(commands))
		for i := range commands {
			converted = append(converted, dto.CommandFromModel(&commands[i]))
		}
		response = dto.DeviceEventResponse{Tonight: dto.TonightFromModels(session, profile), Commands: converted}
		result, err := json.Marshal(response)
		if err != nil {
			return err
		}
		event := &model.DeviceEvent{
			EventID: request.EventID, DeviceID: request.DeviceID, UserID: userID, Type: request.Type,
			Payload: model.JSON(request.Payload), OccurredAt: occurredAt, ProcessedAt: s.now().UTC(), Result: model.JSON(json.RawMessage(result)),
		}
		return tx.CreateDeviceEvent(ctx, event)
	})
	if err != nil {
		// 并发重复上报可能在唯一约束处失败；事务回滚后再读取首次处理结果。
		if existing, readErr := s.store.GetDeviceEventByEventID(ctx, request.EventID); readErr == nil {
			if json.Unmarshal(existing.Result, &response) == nil {
				response.Duplicate = true
				return response, nil
			}
		}
		return dto.DeviceEventResponse{}, normalizeServiceError(err, "处理设备事件失败")
	}
	publish(s.hub, userID, "device.event", map[string]any{"eventId": request.EventID, "type": request.Type, "deviceId": request.DeviceID})
	publish(s.hub, userID, "tonight.updated", response.Tonight)
	return response, nil
}

func (s *DeviceService) Heartbeat(ctx context.Context, request dto.DeviceHeartbeatRequest) (dto.DeviceStatus, error) {
	userID := request.UserID
	if userID == "" {
		userID = s.defaultUser
	}
	now := s.now().UTC()
	device := &model.Device{
		DeviceID: request.DeviceID, UserID: userID, FirmwareVersion: request.FirmwareVersion,
		Capabilities: model.JSON(request.Capabilities), Status: model.JSON(request.Status),
		LocalTime: request.LocalTime, LastSeenAt: now,
	}
	if err := s.store.UpsertDevice(ctx, device); err != nil {
		return dto.DeviceStatus{}, NewError("storage_error", "更新设备在线状态失败", err)
	}
	result := dto.DeviceStatusFromModel(device, 90*time.Second, now)
	publish(s.hub, userID, "device.status", result)
	return result, nil
}

func (s *DeviceService) Status(ctx context.Context, userID, deviceID string) (dto.DeviceStatus, error) {
	device, err := s.store.GetDevice(ctx, userID, deviceID)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.DeviceStatus{}, NewError("not_found", "设备不存在", err)
	}
	if err != nil {
		return dto.DeviceStatus{}, NewError("storage_error", "读取设备状态失败", err)
	}
	return dto.DeviceStatusFromModel(device, 90*time.Second, s.now().UTC()), nil
}

func (s *DeviceService) NextCommand(ctx context.Context, deviceID string, timeout time.Duration) (*dto.Command, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		command, err := s.store.TakeNextDeviceCommand(ctx, deviceID, s.commandLease, s.commandMaxAttempts)
		if err == nil {
			converted := dto.CommandFromModel(command)
			return &converted, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, NewError("storage_error", "读取设备命令失败", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, repository.ErrNotFound
		case <-ticker.C:
		}
	}
}

func (s *DeviceService) Ack(ctx context.Context, request dto.CommandAckRequest) (dto.Command, error) {
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return dto.Command{}, NewError("validation_error", "ack payload 无法编码", err)
	}
	command, err := s.store.AckDeviceCommand(ctx, request.DeviceID, request.CommandID, request.Success, payload)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.Command{}, NewError("not_found", "设备命令不存在", err)
	}
	if err != nil {
		return dto.Command{}, NewError("storage_error", "确认设备命令失败", err)
	}
	return dto.CommandFromModel(command), nil
}

func deviceTrigger(eventType, currentPhase string) (state.Trigger, error) {
	switch eventType {
	case "box_closed":
		return state.BoxClosed, nil
	case "box_opened":
		return state.BoxOpened, nil
	case "soft_button/short_press":
		if currentPhase == string(state.Sunrise) {
			return state.Snooze, nil
		}
		return state.StopAudio, nil
	case "soft_button/long_press":
		return state.SoftButtonLong, nil
	case "alarm_start":
		return state.AlarmStart, nil
	default:
		return "", NewError("validation_error", "未知的设备事件类型", nil)
	}
}

func deviceCommands(userID, deviceID, eventType string, resultingPhase state.Phase) []model.DeviceCommand {
	command := func(commandType string, payload any) model.DeviceCommand {
		return model.DeviceCommand{ID: uuid.New(), DeviceID: deviceID, UserID: userID, Type: commandType, Payload: model.JSON(payload), Status: "pending", AckPayload: model.JSON(map[string]any{})}
	}
	switch eventType {
	case "box_closed":
		return []model.DeviceCommand{command("audio.confirm", map[string]any{"message": "手机已经安放好了"}), command("led.off", map[string]any{})}
	case "box_opened":
		return []model.DeviceCommand{command("audio.pause", map[string]any{})}
	case "soft_button/short_press":
		if resultingPhase == state.Sunrise {
			return []model.DeviceCommand{command("alarm.snooze", map[string]any{"minutes": 5}), command("led.off", map[string]any{})}
		}
		return []model.DeviceCommand{command("audio.stop", map[string]any{}), command("led.off", map[string]any{})}
	case "soft_button/long_press":
		if resultingPhase == state.Awake {
			return []model.DeviceCommand{command("alarm.stop", map[string]any{})}
		}
	case "alarm_start":
		return []model.DeviceCommand{command("sunrise.start", map[string]any{"durationMinutes": 25})}
	}
	return nil
}
