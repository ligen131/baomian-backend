package ai

import (
	"strings"
	"testing"
)

func TestSystemPromptContainsProductConversationRules(t *testing.T) {
	required := []string{
		"情绪接纳者",
		"每轮只完成一个主要任务",
		"1 至 3 句话",
		"不超过 60 个汉字",
		"每轮最多提出一个问题",
		"turnIndex>=3",
		"shouldFinalize 必须为 true",
		"不需要继续回答",
		"不进行心理或医疗诊断",
		"不使用“你应该”“你必须”",
		"不承诺“一切都会好起来”",
	}
	for _, value := range required {
		if !strings.Contains(systemPrompt, value) {
			t.Errorf("systemPrompt 缺少产品规则 %q", value)
		}
	}
}

func TestSystemPromptContainsStructuredOutputContract(t *testing.T) {
	required := []string{
		`["rain","brown_noise","breathing_46","silence"]`,
		"reply, emotion, worry, tomorrowTask, comfort, guidanceOptions, suggestedGuidance, shouldFinalize, fallback, highRisk",
		"fallback 和 highRisk 必须为 false",
	}
	for _, value := range required {
		if !strings.Contains(systemPrompt, value) {
			t.Errorf("systemPrompt 缺少结构化输出约束 %q", value)
		}
	}
}
