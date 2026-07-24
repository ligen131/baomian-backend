package service

import (
	"testing"
	"time"

	"github.com/baomian/baomian-backend/internal/model"
	"github.com/baomian/baomian-backend/internal/state"
)

func TestProfileDateUsesIANATimeZone(t *testing.T) {
	now := time.Date(2026, 7, 23, 16, 30, 0, 0, time.UTC)
	got := profileDate(now, "Asia/Shanghai")
	want := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("profileDate() = %v, want %v", got, want)
	}
}

func TestActiveJournalSessionOnlyBlocksCurrentDate(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	current := &model.NightSession{Date: now, Phase: string(state.Sleeping)}
	if !activeJournalSession(current, now) {
		t.Fatal("当前日期进行中的 Session 应阻止删除")
	}
	old := &model.NightSession{Date: now.AddDate(0, 0, -1), Phase: string(state.Sleeping)}
	if activeJournalSession(old, now) {
		t.Fatal("历史未结束 Session 不应永久阻止删除")
	}
}

func TestApplySessionTimingConversationAndPhoneRemoval(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := &model.NightSession{Phase: string(state.Conversation)}
	applySessionTiming(session, string(state.Locked), state.StartConversation, now, 20*time.Second, 4*time.Minute, 10*time.Minute)
	if session.ConversationSilenceDeadlineAt == nil || !session.ConversationSilenceDeadlineAt.Equal(now.Add(20*time.Second)) {
		t.Fatal("静默截止未正确初始化")
	}
	if session.ConversationHardDeadlineAt == nil || !session.ConversationHardDeadlineAt.Equal(now.Add(4*time.Minute)) {
		t.Fatal("硬截止未正确初始化")
	}

	session.Phase = string(state.PhoneRemoved)
	applySessionTiming(session, string(state.Conversation), state.BoxOpened, now, 20*time.Second, 4*time.Minute, 10*time.Minute)
	if session.ResumeDeadlineAt == nil || !session.ResumeDeadlineAt.Equal(now.Add(10*time.Minute)) {
		t.Fatal("恢复截止未正确初始化")
	}
	if session.AudioEndsAt != nil {
		t.Fatal("开仓后必须清除音频截止")
	}
}
