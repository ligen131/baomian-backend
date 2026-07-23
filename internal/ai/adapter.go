package ai

import (
	"context"
	"errors"

	"github.com/baomian/baomian-backend/internal/dto"
)

var ErrInvalidResponse = errors.New("invalid ai response")

type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type Memory struct {
	Emotion      string `json:"emotion"`
	Worry        string `json:"worry"`
	TomorrowTask string `json:"tomorrowTask"`
	Comfort      string `json:"comfort"`
}

type Request struct {
	Persona   string   `json:"persona"`
	TurnIndex int      `json:"turnIndex"`
	Text      string   `json:"text"`
	Turns     []Turn   `json:"turns"`
	Memories  []Memory `json:"memories"`
}

type Adapter interface {
	Generate(ctx context.Context, request Request) (dto.AIResult, error)
}
