package ai

import (
	"context"
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

func TestThirdTurnFinalizes(t *testing.T) {
	result, err := NewFallbackAdapter().Generate(context.Background(), Request{Text: "还有一点担心", TurnIndex: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ShouldFinalize {
		t.Fatal("third turn must finalize")
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
