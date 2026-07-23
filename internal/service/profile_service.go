package service

import (
	"context"
	"fmt"
	"regexp"

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
	if err := validateProfile(request); err != nil {
		return dto.Profile{}, err
	}
	profile, err := s.store.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return dto.Profile{}, NewError("storage_error", "读取设置失败", err)
	}
	profile.Bedtime = request.Bedtime
	profile.WakeTime = request.WakeTime
	profile.Persona = request.Persona
	profile.ReminderStyle = request.ReminderStyle
	profile.DefaultGuidance = request.DefaultGuidance
	profile.WhiteNoiseDurationMin = request.WhiteNoiseDurationMin
	if err := s.store.UpdateProfile(ctx, profile); err != nil {
		return dto.Profile{}, NewError("storage_error", "保存设置失败", err)
	}
	return dto.ProfileFromModel(profile), nil
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
