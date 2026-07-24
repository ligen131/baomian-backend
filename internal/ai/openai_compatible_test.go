package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleAdapterGenerate(t *testing.T) {
	server := newOpenAICompatibleTestServer(t, func(t *testing.T, request map[string]any) {
		if request["model"] != "claude-opus-4-8" {
			t.Fatalf("model = %v", request["model"])
		}
		if _, exists := request["temperature"]; exists {
			t.Fatal("temperature must not be sent")
		}
		messages, ok := request["messages"].([]any)
		if !ok || len(messages) != 2 {
			t.Fatalf("messages = %#v", request["messages"])
		}
		if messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["role"] != "user" {
			t.Fatalf("messages = %#v", messages)
		}
		responseFormat, ok := request["response_format"].(map[string]any)
		if !ok || responseFormat["type"] != "json_schema" {
			t.Fatalf("response_format = %#v", request["response_format"])
		}
		jsonSchema := responseFormat["json_schema"].(map[string]any)
		if jsonSchema["strict"] != true {
			t.Fatalf("json_schema = %#v", jsonSchema)
		}
		schema := jsonSchema["schema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("schema = %#v", schema)
		}
	})
	defer server.Close()

	adapter := NewOpenAICompatibleAdapter("", "test-token", server.URL, "claude-opus-4-8", server.Client())
	result, err := adapter.Generate(context.Background(), Request{Persona: "gentle", TurnIndex: 1, Text: "明天要汇报，有点紧张"})
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

func TestOpenAICompatibleAdapterBaseURLWithV1(t *testing.T) {
	server := newOpenAICompatibleTestServer(t, nil)
	defer server.Close()
	adapter := NewOpenAICompatibleAdapter("test-key", "", server.URL+"/v1", "claude-opus-4-8", server.Client())
	if _, err := adapter.Generate(context.Background(), Request{TurnIndex: 1, Text: "测试"}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestOpenAICompatibleAdapterForcesThirdTurnFinalize(t *testing.T) {
	server := newOpenAICompatibleTestServer(t, nil)
	defer server.Close()
	adapter := NewOpenAICompatibleAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
	result, err := adapter.Generate(context.Background(), Request{TurnIndex: 3, Text: "还是有点担心"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !result.ShouldFinalize {
		t.Fatal("third turn must finalize even if the model returns false")
	}
}

func TestOpenAICompatibleAdapterRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed json", content: "not-json"},
		{name: "wrong guidance order", content: `{"reply":"好","emotion":"焦虑","worry":"工作","tomorrowTask":"列清单","comfort":"先休息","guidanceOptions":["silence","rain","brown_noise","breathing_46"],"suggestedGuidance":"silence","shouldFinalize":false,"fallback":false,"highRisk":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := openAICompatibleResponseServer(t, http.StatusOK, chatCompletionEnvelope(test.content))
			defer server.Close()
			adapter := NewOpenAICompatibleAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
			_, err := adapter.Generate(context.Background(), Request{TurnIndex: 1, Text: "测试"})
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestOpenAICompatibleAdapterRejectsHTTPErrorRefusalAndMissingCredential(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		server := openAICompatibleResponseServer(t, http.StatusNotFound, `{"error":{"message":"not found"}}`)
		defer server.Close()
		adapter := NewOpenAICompatibleAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
		if _, err := adapter.Generate(context.Background(), Request{Text: "测试"}); err == nil {
			t.Fatal("Generate() error = nil, want HTTP error")
		}
	})

	t.Run("refusal", func(t *testing.T) {
		server := openAICompatibleResponseServer(t, http.StatusOK, `{"choices":[{"message":{"content":"","refusal":"declined"},"finish_reason":"stop"}]}`)
		defer server.Close()
		adapter := NewOpenAICompatibleAdapter("test-key", "", server.URL, "claude-opus-4-8", server.Client())
		if _, err := adapter.Generate(context.Background(), Request{Text: "测试"}); err == nil {
			t.Fatal("Generate() error = nil, want refusal error")
		}
	})

	t.Run("missing credential", func(t *testing.T) {
		adapter := NewOpenAICompatibleAdapter("", "", "https://example.invalid", "claude-opus-4-8", nil)
		if _, err := adapter.Generate(context.Background(), Request{Text: "测试"}); err == nil {
			t.Fatal("Generate() error = nil, want missing credential error")
		}
	})
}

func newOpenAICompatibleTestServer(t *testing.T, inspect func(*testing.T, map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("authorization") != "Bearer test-token" && request.Header.Get("authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", request.Header.Get("authorization"))
		}
		if request.Header.Get("x-api-key") != "" {
			t.Fatalf("x-api-key must be absent: %q", request.Header.Get("x-api-key"))
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
		_, _ = writer.Write([]byte(chatCompletionEnvelope(validAIResultJSON())))
	}))
}

func openAICompatibleResponseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
}

func chatCompletionEnvelope(content string) string {
	encoded, _ := json.Marshal(content)
	return `{"id":"chatcmpl-test","object":"chat.completion","model":"claude-opus-4-8","choices":[{"index":0,"message":{"role":"assistant","content":` + string(encoded) + `},"finish_reason":"stop"}]}`
}
