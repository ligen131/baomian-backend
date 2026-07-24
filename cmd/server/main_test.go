package main

import (
	"net/http"
	"testing"

	"github.com/baomian/baomian-backend/internal/ai"
	"github.com/baomian/baomian-backend/internal/config"
)

func TestNewAIAdapterSelectsProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     any
	}{
		{provider: "anthropic", want: &ai.AnthropicAdapter{}},
		{provider: "openai_compatible", want: &ai.OpenAICompatibleAdapter{}},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			adapter := newAIAdapter(config.Config{AIProvider: test.provider}, http.DefaultClient)
			switch test.want.(type) {
			case *ai.AnthropicAdapter:
				if _, ok := adapter.(*ai.AnthropicAdapter); !ok {
					t.Fatalf("adapter = %T", adapter)
				}
			case *ai.OpenAICompatibleAdapter:
				if _, ok := adapter.(*ai.OpenAICompatibleAdapter); !ok {
					t.Fatalf("adapter = %T", adapter)
				}
			}
		})
	}
}
