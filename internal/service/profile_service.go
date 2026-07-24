package service

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/repository"
)

var timePattern = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)

type ProfileService struct {
	store repository.Store
}

func NewProfileService(store repository.Store) *ProfileService { return &ProfileService{store: store} }

func (s *ProfileService) Get(ctx context.Context, userID string) (dto.Profile, error) {
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.Profile{}, NewError("storage_error", "读取设置失败", err)
	}
	return dto.ProfileFromModel(profile), nil
}

func (s *ProfileService) Update(ctx context.Context, userID string, request dto.UpdateProfileRequest) (dto.Profile, error) {
	var response dto.Profile
	err := s.store.WithTx(ctx, func(tx repository.Store) error {
		profile, err := tx.GetOrCreateProfile(ctx, userID)
		if err != nil {
			return err
		}
		if request.Bedtime != nil {
			profile.Bedtime = *request.Bedtime
		}
		if request.WakeTime != nil {
			profile.WakeTime = *request.WakeTime
		}
		if request.Persona != nil {
			profile.Persona = *request.Persona
		}
		if request.ReminderStyle != nil {
			profile.ReminderStyle = *request.ReminderStyle
		}
		if request.DefaultGuidance != nil {
			profile.DefaultGuidance = *request.DefaultGuidance
		}
		if request.WhiteNoiseDurationMin != nil {
			profile.WhiteNoiseDurationMin = *request.WhiteNoiseDurationMin
		}
		if request.TimeZone != nil {
			profile.TimeZone = *request.TimeZone
		}
		if request.BedtimeReminderEnabled != nil {
			profile.BedtimeReminderEnabled = *request.BedtimeReminderEnabled
		}
		if request.WakeAlarmEnabled != nil {
			profile.WakeAlarmEnabled = *request.WakeAlarmEnabled
		}
		value := dto.ProfileFromModel(profile)
		if err := validateProfile(value); err != nil {
			return err
		}
		if err := tx.UpdateProfile(ctx, profile); err != nil {
			return err
		}
		response = value
		return nil
	})
	if err != nil {
		return dto.Profile{}, normalizeServiceError(err, "保存设置失败")
	}
	return response, nil
}

func validateProfile(value dto.Profile) error {
	if !timePattern.MatchString(value.Bedtime) || !timePattern.MatchString(value.WakeTime) {
		return NewError("validation_error", "bedtime 和 wakeTime 必须使用 HH:mm 格式", nil)
	}
	if !oneOf(value.Persona, "gentle", "rational", "firm") {
		return NewError("validation_error", "persona 必须为 gentle、rational 或 firm", nil)
	}
	if !oneOf(value.ReminderStyle, "gentle", "firm") {
		return NewError("validation_error", "reminderStyle 必须为 gentle 或 firm", nil)
	}
	if !oneOf(value.DefaultGuidance, "rain", "brown_noise", "breathing_46", "silence") {
		return NewError("validation_error", "defaultGuidance 无效", nil)
	}
	if value.WhiteNoiseDurationMin != 10 && value.WhiteNoiseDurationMin != 20 && value.WhiteNoiseDurationMin != 30 {
		return NewError("validation_error", fmt.Sprintf("whiteNoiseDurationMin 不支持 %d", value.WhiteNoiseDurationMin), nil)
	}
	if value.TimeZone == "" {
		return NewError("validation_error", "timeZone 不能为空", nil)
	}
	if _, err := time.LoadLocation(value.TimeZone); err != nil {
		return NewError("validation_error", "timeZone 必须为有效的 IANA 时区", err)
	}
	return nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
