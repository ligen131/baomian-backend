package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/baomian/baomian-backend/internal/ai"
)

func main() {
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "deepseek-v4-flash"
	}
	adapter := ai.NewOpenAICompatibleAdapter(
		os.Getenv("ANTHROPIC_API_KEY"),
		os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		baseURL,
		model,
		&http.Client{Timeout: 30 * time.Second},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := adapter.Generate(ctx, ai.Request{
		Persona:   "gentle",
		TurnIndex: 1,
		Text:      "明天要做一个简短汇报，我有点紧张。",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("reply_present=%t fallback=%t high_risk=%t guidance=%s\n", result.Reply != "", result.Fallback, result.HighRisk, result.SuggestedGuidance)
}
