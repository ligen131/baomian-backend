package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicAdapterGenerate(t *testing.T) {
	server := newAnthropicTestServer(t, func(t *testing.T, request map[string]any) {
		if request["model"] != "claude-opus-4-8" {
			t.Fatalf("model = %v", request["model"])
		}
		if request["max_tokens"] != float64(4096) {
			t.Fatalf("max_tokens = %v", request["max_tokens"])
		}
		if _, exists := request["temperature"]; exists {
			t.Fatal("temperature must not be sent to Claude Opus 4.8")
		}
		thinking, ok := request["thinking"].(map[string]any)
		if !ok || thinking["type"] != "adaptive" {
			t.Fatalf("thinking = %#v", request["thinking"])
		}
		outputConfig, ok := request["output_config"].(map[string]any)
		if !ok || outputConfig["effort"] != "low" {
			t.Fatalf("output_config = %#v", request["output_config"])
		}
		format, ok := outputConfig["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("output_config.format = %#v", outputConfig["format"])
		}
		schema, ok := format["schema"].(map[string]any)
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("schema = %#v", format["schema"])
		}
	})
	defer server.Close()

	adapter := NewAnthropicAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
	result, err := adapter.Generate(context.Background(), Request{
		Persona: "gentle", TurnIndex: 1, Text: "明天要汇报，有点紧张",
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if result.Reply == "" || result.SuggestedGuidance != "breathing_46" {
		t.Fatalf("result = %#v", result)
	}
	if result.Fallback || result.HighRisk {
		t.Fatalf("server-owned flags must be false: %#v", result)
	}
}

func TestAnthropicAdapterUsesJournalPromptForJournalMode(t *testing.T) {
	server := newAnthropicTestServer(t, func(t *testing.T, request map[string]any) {
		system, ok := request["system"].([]any)
		if !ok || len(system) != 1 {
			t.Fatalf("system = %#v", request["system"])
		}
		systemText, _ := system[0].(map[string]any)["text"].(string)
		if !strings.Contains(systemText, "晚安日记编辑") || strings.Contains(systemText, "本轮任务") {
			t.Fatalf("journal system prompt = %q", systemText)
		}
		messages, ok := request["messages"].([]any)
		if !ok || len(messages) != 1 {
			t.Fatalf("messages = %#v", request["messages"])
		}
		content := messages[0].(map[string]any)["content"].([]any)
		userText, _ := content[0].(map[string]any)["text"].(string)
		if !strings.Contains(userText, "完整对话整理晚安日记") {
			t.Fatalf("journal user instruction = %q", userText)
		}
	})
	defer server.Close()

	adapter := NewAnthropicAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
	if _, err := adapter.Generate(context.Background(), Request{Mode: ModeJournal, Turns: []Turn{{Role: "user", Text: "今天有点累"}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestAnthropicAdapterUsesAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", request.Header.Get("authorization"))
		}
		if request.Header.Get("x-api-key") != "" {
			t.Fatalf("x-api-key must be absent when auth token is configured: %q", request.Header.Get("x-api-key"))
		}
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(messageEnvelope(validAIResultJSON(), "end_turn")))
	}))
	defer server.Close()

	adapter := NewAnthropicAdapter("", "test-token", server.URL, "claude-opus-4-8", server.Client())
	if _, err := adapter.Generate(context.Background(), Request{TurnIndex: 1, Text: "测试"}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestAnthropicAdapterDoesNotForceThirdTurnFinalize(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()

	adapter := NewAnthropicAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
	result, err := adapter.Generate(context.Background(), Request{Mode: ModeReply, TurnIndex: 3, Text: "还是有点担心"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if result.ShouldFinalize {
		t.Fatal("third turn unexpectedly finalized")
	}
}

func TestAnthropicAdapterRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed json", content: "not-json"},
		{name: "wrong guidance order", content: `{"reply":"好","emotion":"焦虑","worry":"工作","tomorrowTask":"列清单","comfort":"先休息","guidanceOptions":["silence","rain","brown_noise","breathing_46"],"suggestedGuidance":"silence","shouldFinalize":false,"fallback":false,"highRisk":false}`},
		{name: "empty required field", content: `{"reply":"","emotion":"焦虑","worry":"工作","tomorrowTask":"列清单","comfort":"先休息","guidanceOptions":["rain","brown_noise","breathing_46","silence"],"suggestedGuidance":"silence","shouldFinalize":false,"fallback":false,"highRisk":false}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := anthropicResponseServer(t, http.StatusOK, messageEnvelope(test.content, "end_turn"))
			defer server.Close()
			adapter := NewAnthropicAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())

			_, err := adapter.Generate(context.Background(), Request{TurnIndex: 1, Text: "测试"})
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestAnthropicAdapterRejectsRefusalAndMissingKey(t *testing.T) {
	t.Run("refusal", func(t *testing.T) {
		server := anthropicResponseServer(t, http.StatusOK, `{
			"id":"msg_refusal","type":"message","role":"assistant","model":"claude-opus-4-8",
			"content":[],"stop_reason":"refusal","stop_sequence":null,
			"stop_details":{"type":"refusal","category":null,"explanation":"request declined"},
			"usage":{"input_tokens":0,"output_tokens":0}
		}`)
		defer server.Close()
		adapter := NewAnthropicAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
		if _, err := adapter.Generate(context.Background(), Request{Text: "测试"}); err == nil {
			t.Fatal("Generate() error = nil, want refusal error")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		adapter := NewAnthropicAdapter("", "", "https://api.anthropic.com", "claude-opus-4-8", &http.Client{Timeout: time.Second})
		if _, err := adapter.Generate(context.Background(), Request{Text: "测试"}); err == nil {
			t.Fatal("Generate() error = nil, want missing key error")
		}
	})
}

func newAnthropicTestServer(t *testing.T, inspect func(*testing.T, map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("x-api-key = %q", request.Header.Get("x-api-key"))
		}
		if request.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", request.Header.Get("anthropic-version"))
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if inspect != nil {
			inspect(t, body)
		}
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(messageEnvelope(validAIResultJSON(), "end_turn")))
	}))
}

func anthropicResponseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
}

func validAIResultJSON() string {
	return `{"reply":"我听见你在担心明天的汇报，重点已经记下了，今晚先休息。","emotion":"紧张","worry":"明天的汇报","tomorrowTask":"确认三页重点","comfort":"今晚不需要再彩排。","guidanceOptions":["rain","brown_noise","breathing_46","silence"],"suggestedGuidance":"breathing_46","shouldFinalize":false,"fallback":false,"highRisk":false}`
}

func messageEnvelope(content, stopReason string) string {
	encoded, _ := json.Marshal(content)
	return `{
		"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-4-8",
		"content":[{"type":"text","text":` + string(encoded) + `}],
		"stop_reason":"` + stopReason + `","stop_sequence":null,
		"usage":{"input_tokens":100,"output_tokens":80}
	}`
}
