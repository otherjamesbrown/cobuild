package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	openaioption "github.com/openai/openai-go/option"
	openaishared "github.com/openai/openai-go/shared"
)

type openAIChatCompletionsAPI interface {
	New(ctx context.Context, body openai.ChatCompletionNewParams, opts ...openaioption.RequestOption) (*openai.ChatCompletion, error)
}

type OpenAIReviewer struct {
	client  openAIChatCompletionsAPI
	model   string
	timeout time.Duration
}

func NewOpenAIReviewer(model string, timeout time.Duration, opts ...openaioption.RequestOption) *OpenAIReviewer {
	if strings.TrimSpace(model) == "" {
		model = DefaultOpenAIModel
	}

	client := openai.NewClient(opts...)
	return &OpenAIReviewer{
		client:  &client.Chat.Completions,
		model:   model,
		timeout: timeout,
	}
}

func (r *OpenAIReviewer) Review(ctx context.Context, input ReviewInput) (*ReviewResult, error) {
	opts := make([]openaioption.RequestOption, 0, 1)
	if r.timeout > 0 {
		opts = append(opts, openaioption.WithRequestTimeout(r.timeout))
	}

	completion, err := r.client.New(ctx, openai.ChatCompletionNewParams{
		Model: openaishared.ChatModel(r.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(reviewSystemPrompt),
			openai.UserMessage(buildReviewPrompt(input)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openaishared.ResponseFormatJSONObjectParam{Type: "json_object"},
		},
		MaxCompletionTokens: openai.Int(4096),
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("openai review request failed: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("openai review response contained no choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("openai review response did not contain text content")
	}

	result, err := parseReviewResult(content)
	if err != nil {
		return nil, fmt.Errorf("openai review response invalid: %w", err)
	}
	return result, nil
}
