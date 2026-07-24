package state

import (
	"errors"
	"fmt"
)

type Phase string

type Trigger string

const (
	WaitingToLock    Phase = "WAITING_TO_LOCK"
	Locked           Phase = "LOCKED"
	Conversation     Phase = "CONVERSATION"
	ChoosingGuidance Phase = "CHOOSING_GUIDANCE"
	Sleeping         Phase = "SLEEPING"
	Sunrise          Phase = "SUNRISE"
	Awake            Phase = "AWAKE"
	PhoneRemoved     Phase = "PHONE_REMOVED"
)

const (
	BoxClosed         Trigger = "box_closed"
	BoxOpened         Trigger = "box_opened"
	StartConversation Trigger = "start_conversation"
	Finalize          Trigger = "finalize"
	SelectGuidance    Trigger = "select_guidance"
	StopAudio         Trigger = "stop_audio"
	AlarmStart        Trigger = "alarm_start"
	Snooze            Trigger = "snooze"
	MarkAwake         Trigger = "mark_awake"
	SoftButtonLong    Trigger = "soft_button_long"
	PauseForTonight   Trigger = "pause_for_tonight"
)

var ErrInvalidTransition = errors.New("invalid state transition")

type Snapshot struct {
	Phase            Phase
	ResumePhase      Phase
	BoxClosed        bool
	AudioPlaying     bool
	SunriseProgress  int
	PausedForTonight bool
}

func Apply(current Snapshot, trigger Trigger) (Snapshot, error) {
	next := current

	switch trigger {
	case BoxClosed:
		next.BoxClosed = true
		next.PausedForTonight = false
		if current.Phase == PhoneRemoved && validResume(current.ResumePhase) {
			next.Phase = current.ResumePhase
			next.ResumePhase = ""
			return next, nil
		}
		if current.Phase == WaitingToLock || current.Phase == PhoneRemoved {
			next.Phase = Locked
			next.ResumePhase = ""
			return next, nil
		}
	case BoxOpened:
		if current.Phase != Awake && current.Phase != WaitingToLock && current.Phase != PhoneRemoved {
			next.ResumePhase = current.Phase
			next.Phase = PhoneRemoved
			next.BoxClosed = false
			next.AudioPlaying = false
			return next, nil
		}
		if current.Phase == PhoneRemoved {
			next.BoxClosed = false
			return next, nil
		}
	case StartConversation:
		if current.Phase == Locked {
			next.Phase = Conversation
			return next, nil
		}
	case Finalize:
		if current.Phase == Conversation || current.Phase == Locked {
			next.Phase = ChoosingGuidance
			next.AudioPlaying = false
			return next, nil
		}
	case SelectGuidance:
		if current.Phase == ChoosingGuidance {
			next.Phase = Sleeping
			next.AudioPlaying = true
			return next, nil
		}
	case StopAudio:
		if current.Phase == Sleeping || current.Phase == Conversation || current.Phase == ChoosingGuidance || current.Phase == Locked {
			next.AudioPlaying = false
			return next, nil
		}
	case AlarmStart:
		if current.Phase != Awake {
			next.Phase = Sunrise
			next.AudioPlaying = false
			next.SunriseProgress = 0
			return next, nil
		}
	case Snooze:
		if current.Phase == Sunrise {
			next.AudioPlaying = false
			next.SunriseProgress = 0
			return next, nil
		}
	case MarkAwake:
		if current.Phase == Sunrise {
			next.Phase = Awake
			next.AudioPlaying = false
			next.SunriseProgress = 100
			return next, nil
		}
	case SoftButtonLong:
		if current.Phase == Sunrise {
			next.Phase = Awake
			next.AudioPlaying = false
			next.SunriseProgress = 100
		}
		return next, nil
	case PauseForTonight:
		next.PausedForTonight = true
		next.AudioPlaying = false
		return next, nil
	}

	return current, fmt.Errorf("%w: %s from %s", ErrInvalidTransition, trigger, current.Phase)
}

func validResume(phase Phase) bool {
	switch phase {
	case Locked, Conversation, ChoosingGuidance, Sleeping:
		return true
	default:
		return false
	}
}
