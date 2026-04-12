package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

const reviewSystemPrompt = "You are a meticulous code reviewer. Return JSON only."

type anthropicMessagesAPI interface {
	New(ctx context.Context, body anthropic.MessageNewParams, opts ...anthropicoption.RequestOption) (*anthropic.Message, error)
}

type ClaudeReviewer struct {
	client  anthropicMessagesAPI
	model   string
	timeout time.Duration
}

func NewClaudeReviewer(model string, timeout time.Duration, opts ...anthropicoption.RequestOption) *ClaudeReviewer {
	if strings.TrimSpace(model) == "" {
		model = DefaultClaudeModel
	}

	client := anthropic.NewClient(opts...)
	return &ClaudeReviewer{
		client:  &client.Messages,
		model:   model,
		timeout: timeout,
	}
}

func (r *ClaudeReviewer) Review(ctx context.Context, input ReviewInput) (*ReviewResult, error) {
	opts := make([]anthropicoption.RequestOption, 0, 1)
	if r.timeout > 0 {
		opts = append(opts, anthropicoption.WithRequestTimeout(r.timeout))
	}

	message, err := r.client.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(r.model),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: reviewSystemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildReviewPrompt(input))),
		},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("anthropic review request failed: %w", err)
	}

	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("anthropic review response did not contain text content")
	}

	result, err := parseReviewResult(strings.Join(parts, "\n"))
	if err != nil {
		return nil, fmt.Errorf("anthropic review response invalid: %w", err)
	}
	return result, nil
}
