package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baomian/baomian-backend/internal/dto"
)

type AnthropicAdapter struct {
	credentialConfigured bool
	model                string
	client               anthropic.Client
}

func NewAnthropicAdapter(apiKey, authToken, baseURL, model string, httpClient *http.Client) *AnthropicAdapter {
	options := []option.RequestOption{
		option.WithBaseURL(strings.TrimRight(baseURL, "/")),
		option.WithMaxRetries(0),
	}
	apiKey = strings.TrimSpace(apiKey)
	authToken = strings.TrimSpace(authToken)
	if authToken != "" {
		options = append(options, option.WithAuthToken(authToken))
	} else if apiKey != "" {
		options = append(options, option.WithAPIKey(apiKey))
	}
	if httpClient != nil {
		options = append(options, option.WithHTTPClient(httpClient))
	}
	return &AnthropicAdapter{
		credentialConfigured: apiKey != "" || authToken != "",
		model:                model,
		client:               anthropic.NewClient(options...),
	}
}

func (a *AnthropicAdapter) Generate(ctx context.Context, request Request) (dto.AIResult, error) {
	if !a.credentialConfigured {
		return dto.AIResult{}, fmt.Errorf("anthropic credential is not configured")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("marshal ai request: %w", err)
	}

	response, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		},
		System: []anthropic.TextBlockParam{{
			Text: systemPrompt,
		}},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{
				Schema: aiResultSchema,
			},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("请根据以下结构化输入生成本轮回复：\n" + string(input))),
		},
	})
	if err != nil {
		return dto.AIResult{}, fmt.Errorf("call anthropic: %w", err)
	}
	if response.StopReason == anthropic.StopReasonRefusal {
		return dto.AIResult{}, fmt.Errorf("anthropic refused request: %s", response.StopDetails.Explanation)
	}

	var text strings.Builder
	for _, content := range response.Content {
		if content.Type == "text" {
			text.WriteString(content.Text)
		}
	}
	value := stripCodeFence(strings.TrimSpace(text.String()))
	if value == "" {
		return dto.AIResult{}, fmt.Errorf("%w: empty text content", ErrInvalidResponse)
	}

	var result dto.AIResult
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return dto.AIResult{}, fmt.Errorf("%w: decode model json: %v", ErrInvalidResponse, err)
	}
	if err := validateResult(result); err != nil {
		return dto.AIResult{}, err
	}
	if request.TurnIndex >= 3 {
		result.ShouldFinalize = true
	}
	result.Fallback = false
	result.HighRisk = false
	return result, nil
}

var aiResultSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"reply":             map[string]any{"type": "string"},
		"emotion":           map[string]any{"type": "string"},
		"worry":             map[string]any{"type": "string"},
		"tomorrowTask":      map[string]any{"type": "string"},
		"comfort":           map[string]any{"type": "string"},
		"guidanceOptions":   map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": guidanceOptions()}},
		"suggestedGuidance": map[string]any{"type": "string", "enum": guidanceOptions()},
		"shouldFinalize":    map[string]any{"type": "boolean"},
		"fallback":          map[string]any{"type": "boolean"},
		"highRisk":          map[string]any{"type": "boolean"},
	},
	"required": []string{
		"reply", "emotion", "worry", "tomorrowTask", "comfort", "guidanceOptions", "suggestedGuidance", "shouldFinalize", "fallback", "highRisk",
	},
}

func validateResult(result dto.AIResult) error {
	required := []string{
		result.Reply,
		result.Emotion,
		result.Worry,
		result.TomorrowTask,
		result.Comfort,
		result.SuggestedGuidance,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: required field is empty", ErrInvalidResponse)
		}
	}
	options := guidanceOptions()
	if len(result.GuidanceOptions) != len(options) {
		return fmt.Errorf("%w: invalid guidance options", ErrInvalidResponse)
	}
	for index, option := range options {
		if result.GuidanceOptions[index] != option {
			return fmt.Errorf("%w: guidance options must be %v", ErrInvalidResponse, options)
		}
	}
	for _, option := range options {
		if result.SuggestedGuidance == option {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid suggested guidance", ErrInvalidResponse)
}

func stripCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		value = strings.TrimPrefix(value, "```json")
		value = strings.TrimPrefix(value, "```")
		value = strings.TrimSuffix(value, "```")
	}
	return strings.TrimSpace(value)
}
