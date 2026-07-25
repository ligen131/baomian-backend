package ai

import (
	"context"
	"strings"
	"testing"
)

func TestFallbackScenarios(t *testing.T) {
	adapter := NewFallbackAdapter()
	cases := []string{
		"我担心明天的汇报",
		"工作任务太多了",
		"今天和朋友吵架",
		"待办太多来不及",
		"我很焦虑睡不着",
	}
	for _, text := range cases {
		result, err := adapter.Generate(context.Background(), Request{Text: text, TurnIndex: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.Reply == "" || result.TomorrowTask == "" || len(result.GuidanceOptions) != 4 || !result.Fallback {
			t.Fatalf("incomplete result for %q: %+v", text, result)
		}
	}
}

func TestFallbackPersonaStyles(t *testing.T) {
	tests := []struct {
		persona  string
		contains string
	}{
		{persona: "gentle", contains: "今晚不必解决"},
		{persona: "rational", contains: "最明确的一步"},
		{persona: "firm", contains: "今晚到这里"},
	}
	adapter := NewFallbackAdapter()
	for _, test := range tests {
		t.Run(test.persona, func(t *testing.T) {
			result, err := adapter.Generate(context.Background(), Request{Persona: test.persona, Text: "有点担心", TurnIndex: 1})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Reply, test.contains) {
				t.Fatalf("reply = %q", result.Reply)
			}
		})
	}
}

func TestReplyModeNeverFinalizesByTurnCount(t *testing.T) {
	for _, turn := range []int{3, 4, 10} {
		result, err := NewFallbackAdapter().Generate(context.Background(), Request{Mode: ModeReply, Text: "还有一点担心", TurnIndex: turn})
		if err != nil {
			t.Fatal(err)
		}
		if result.ShouldFinalize {
			t.Fatalf("turn %d unexpectedly finalized", turn)
		}
	}
}

func TestSafetyGuard(t *testing.T) {
	adapter := NewSafetyAdapter(NewFallbackAdapter())
	result, err := adapter.Generate(context.Background(), Request{Text: "我不想活了", TurnIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HighRisk || !result.ShouldFinalize {
		t.Fatalf("expected high risk result: %+v", result)
	}
}
