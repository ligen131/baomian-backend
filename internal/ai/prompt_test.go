package ai

import (
	"strings"
	"testing"
)

func TestReplySystemPromptUsesLowPressureBedsideCompanionRules(t *testing.T) {
	prompt := systemPromptFor(ModeReply)
	required := []string{
		"不是心理医生，也不是说教者",
		"温柔，但不幼稚",
		"亲近，但不黏人",
		"像坐在床边自然说话",
		"优先回应用户刚刚说的内容",
		"每次只说 1 至 2 句话",
		"尽量控制在 40 个汉字以内",
		"每次最多提出一个问题",
		"不要连续追问“为什么”",
		"用户说得很短时，不要逼问",
		"用户说“没事”“不想说”时",
		"如果用户只想倾诉，不要强行安排任务",
		"tomorrowTask 必须为“无”",
		"不使用套路化鸡汤",
		"不暗示自己拥有真实情感",
		"不承诺“我会一直陪着你”",
		"reply 模式不按轮数结束",
		"conversation.finish",
		"shouldFinalize 必须为 false",
	}
	for _, value := range required {
		if !strings.Contains(prompt, value) {
			t.Errorf("reply prompt 缺少规则 %q", value)
		}
	}
	for _, incompatible := range []string{"不超过 3 轮", "第 3 轮结束", `"stage"`, `"finished"`} {
		if strings.Contains(prompt, incompatible) {
			t.Errorf("reply prompt 不应包含不兼容规则 %q", incompatible)
		}
	}
}

func TestJournalSystemPromptUsesFaithfulDiaryRules(t *testing.T) {
	prompt := systemPromptFor(ModeJournal)
	required := []string{
		"晚安日记编辑",
		"全部已完成的 user 和 assistant 对话",
		"不是复述整段聊天",
		"不夸大、不诊断、不替用户下结论",
		"心里亮亮的",
		"还算平稳",
		"心事有点多",
		"comfort 必须优先逐字选取 assistant 真实说过",
		"不得事后编造",
		"只提取一个很小、具体、可执行的动作",
		"tomorrowTask 必须为“无”",
		"shouldFinalize 必须为 true",
	}
	for _, value := range required {
		if !strings.Contains(prompt, value) {
			t.Errorf("journal prompt 缺少规则 %q", value)
		}
	}
}

func TestSystemPromptForUsesModeSpecificInstructionsAndSharedContract(t *testing.T) {
	reply := systemPromptFor(ModeReply)
	journal := systemPromptFor(ModeJournal)
	if reply == journal {
		t.Fatal("reply 与 journal prompt 不应相同")
	}
	if systemPromptFor("") != reply {
		t.Fatal("空 mode 应兼容为 reply prompt")
	}
	for name, prompt := range map[string]string{"reply": reply, "journal": journal} {
		for _, value := range []string{
			`["rain","brown_noise","breathing_46","silence"]`,
			"reply, emotion, worry, tomorrowTask, comfort, guidanceOptions, suggestedGuidance, shouldFinalize, fallback, highRisk",
			"fallback 和 highRisk 必须为 false",
			"不进行心理或医疗诊断",
			"高风险内容由服务端 SafetyAdapter",
		} {
			if !strings.Contains(prompt, value) {
				t.Errorf("%s prompt 缺少共享约束 %q", name, value)
			}
		}
	}
}

func TestUserInstructionForDistinguishesReplyAndJournal(t *testing.T) {
	if !strings.Contains(userInstructionFor(ModeReply), "本轮口语回复") {
		t.Fatalf("reply instruction = %q", userInstructionFor(ModeReply))
	}
	if !strings.Contains(userInstructionFor(ModeJournal), "完整对话整理晚安日记") {
		t.Fatalf("journal instruction = %q", userInstructionFor(ModeJournal))
	}
}
