package ai

import (
	"context"
	"strings"

	"github.com/baomian/baomian-backend/internal/dto"
)

type FallbackAdapter struct{}

func NewFallbackAdapter() *FallbackAdapter { return &FallbackAdapter{} }

func (a *FallbackAdapter) Generate(_ context.Context, request Request) (dto.AIResult, error) {
	text := strings.ToLower(request.Text)
	result := dto.AIResult{
		Reply:             "我听见了。今晚不用把所有事情解决，我们先把它留到明天。",
		Emotion:           "焦虑",
		Worry:             compact(request.Text),
		TomorrowTask:      "明早用十分钟确认最重要的一步",
		Comfort:           "现在可以先休息，这件事已经被记下了。",
		GuidanceOptions:   guidanceOptions(),
		SuggestedGuidance: "breathing_46",
		ShouldFinalize:    false,
		Fallback:          true,
	}

	switch {
	case containsAny(text, "汇报", "报告", "演讲", "presentation"):
		result.Emotion = "紧张"
		result.Worry = "担心明天的汇报表现"
		result.TomorrowTask = "明早先确认汇报的三页重点"
		result.Reply = "你在担心明天的汇报，我已经帮你把重点留到了明早。今晚不需要再彩排一遍。"
	case containsAny(text, "工作", "项目", "deadline", "截止", "老板"):
		result.Emotion = "压力"
		result.Worry = "工作任务仍压在心里"
		result.TomorrowTask = "明早先列出最紧急的一项工作"
		result.Reply = "工作还挂在心上，确实很难一下停下来。我先替你记住最紧急的那一步。"
	case containsAny(text, "关系", "朋友", "同事", "吵架", "家人", "对象"):
		result.Emotion = "难过"
		result.Worry = "一段关系或对话让你放不下"
		result.TomorrowTask = "明天情绪稳定后再决定是否沟通"
		result.SuggestedGuidance = "rain"
		result.Reply = "那段关系让你很不好受。今晚先不用得出结论，明天有力气时再处理。"
	case containsAny(text, "待办", "好多事", "来不及", "任务", "事情太多"):
		result.Emotion = "不堪重负"
		result.Worry = "任务太多，担心遗漏"
		result.TomorrowTask = "明早只选三件最重要的事"
		result.Reply = "事情太多时，大脑会一直提醒你别忘记。我已经记下了，明早只需要先选三件。"
	case containsAny(text, "睡不着", "焦虑", "害怕", "担心", "紧张"):
		result.Emotion = "焦虑"
		result.Worry = compact(request.Text)
		result.TomorrowTask = "明早再判断这件事是否需要立即处理"
		result.Reply = "这种担心现在很真实，但今晚不必解决它。我会把它留在这里，明早再看。"
	}
	if strings.TrimSpace(request.Text) == "" {
		result.Worry = "今晚没有想说的事"
		result.TomorrowTask = "无"
		result.Reply = "不想说也没关系，我会陪你安静下来。"
		result.SuggestedGuidance = "silence"
		result.ShouldFinalize = true
	}
	switch request.Persona {
	case "rational":
		result.Reply = "这件事已经记下了。今晚先停在这里，明早只处理最明确的一步。"
		result.Comfort = "事情已被放好，现在可以休息。"
	case "firm":
		result.Reply = "今晚到这里就够了。剩下的留给明天，现在先让自己休息。"
		result.Comfort = "你不需要在今晚完成所有事。"
	}
	return result, nil
}

func compact(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "今晚心里有些放不下"
	}
	runes := []rune(value)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return value
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func guidanceOptions() []string {
	return []string{"rain", "brown_noise", "breathing_46", "silence"}
}
