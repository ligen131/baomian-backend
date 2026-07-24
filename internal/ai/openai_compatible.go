package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/baomian/baomian-backend/internal/dto"
)

const maxErrorBodyBytes = 8 * 1024

type OpenAICompatibleAdapter struct {
	credential string
	endpoint   string
	model      string
	httpClient *http.Client
}

type chatCompletionRequest struct {
	Model          string                  `json:"model"`
	MaxTokens      int                     `json:"max_tokens"`
	Messages       []chatCompletionMessage `json:"messages"`
	ResponseFormat chatResponseFormat      `json:"response_format"`
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponseFormat struct {
	Type       string             `json:"type"`
	JSONSchema chatResponseSchema `json:"json_schema"`
}

type chatResponseSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatCompletionResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string  `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
	} `json:"choices"`
}

func NewOpenAICompatibleAdapter(apiKey, authToken, baseURL, model string, httpClient *http.Client) *OpenAICompatibleAdapter {
	credential := strings.TrimSpace(authToken)
	if credential == "" {
		credential = strings.TrimSpace(apiKey)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAICompatibleAdapter{
		credential: credential,
		endpoint:   chatCompletionsEndpoint(baseURL),
		model:      model,
		httpClient: httpClient,
	}
}

func (a *OpenAICompatibleAdapter) Generate(ctx context.Context, request Request) (dto.AIResult, error) {
	if a.credential == "" {
		return dto.AIResult{}, fmt.Errorf("openai-compatible credential is not configured")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("marshal ai request: %w", err)
	}
	payload, err := json.Marshal(chatCompletionRequest{
		Model:     a.model,
		MaxTokens: 4096,
		Messages: []chatCompletionMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "请根据以下结构化输入生成本轮回复：\n" + string(input)},
		},
		ResponseFormat: chatResponseFormat{
			Type: "json_schema",
			JSONSchema: chatResponseSchema{
				Name:   "baomian_ai_result",
				Strict: true,
				Schema: aiResultSchema,
			},
		},
	})
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("marshal chat completion request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(payload))
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("create chat completion request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+a.credential)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("call openai-compatible chat completions: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
		if readErr != nil {
			return dto.AIResult{}, fmt.Errorf("openai-compatible chat completions returned HTTP %d", response.StatusCode)
		}
		return dto.AIResult{}, fmt.Errorf("openai-compatible chat completions returned HTTP %d: %s", response.StatusCode, summarizeAPIError(body))
	}

	var completion chatCompletionResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024))
	if err := decoder.Decode(&completion); err != nil {
		return dto.AIResult{}, fmt.Errorf("%w: decode chat completion response: %v", ErrInvalidResponse, err)
	}
	if len(completion.Choices) == 0 {
		return dto.AIResult{}, fmt.Errorf("%w: chat completion has no choices", ErrInvalidResponse)
	}
	choice := completion.Choices[0]
	if choice.Message.Refusal != nil && strings.TrimSpace(*choice.Message.Refusal) != "" {
		return dto.AIResult{}, fmt.Errorf("openai-compatible chat completion refused request")
	}
	return parseResult(choice.Message.Content, request.TurnIndex)
}

func chatCompletionsEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

func summarizeAPIError(body []byte) string {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		parts := make([]string, 0, 3)
		for _, part := range []string{envelope.Error.Type, envelope.Error.Code} {
			if value := strings.TrimSpace(part); value != "" {
				parts = append(parts, value)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ": ")
		}
	}
	return "upstream request failed"
}
