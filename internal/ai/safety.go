package ai

import (
	"context"
	"strings"

	"github.com/baomian/baomian-backend/internal/dto"
)

type SafetyAdapter struct {
	next Adapter
}

func NewSafetyAdapter(next Adapter) *SafetyAdapter { return &SafetyAdapter{next: next} }

func (a *SafetyAdapter) Generate(ctx context.Context, request Request) (dto.AIResult, error) {
	if highRisk(request.Text) {
		return dto.AIResult{
			Reply:             "我很在意你现在的安全。请立刻联系你信任的人陪在身边；如果你可能马上伤害自己，请联系当地急救或危机干预服务。抱眠不能替代专业帮助。",
			Emotion:           "高风险",
			Worry:             "用户表达了可能伤害自己的想法",
			TomorrowTask:      "现在联系可信任的人或专业支持",
			Comfort:           "你不需要独自承受，请现在寻求真人帮助。",
			GuidanceOptions:   guidanceOptions(),
			SuggestedGuidance: "silence",
			ShouldFinalize:    true,
			Fallback:          true,
			HighRisk:          true,
		}, nil
	}
	return a.next.Generate(ctx, request)
}

func highRisk(text string) bool {
	text = strings.ToLower(text)
	terms := []string{"自杀", "不想活", "结束生命", "伤害自己", "割腕", "跳楼", "suicide", "kill myself", "self harm"}
	return containsAny(text, terms...)
}
