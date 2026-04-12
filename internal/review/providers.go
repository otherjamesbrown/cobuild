package review

import (
	"fmt"
	"strings"
	"time"
)

// ProviderConfig captures common provider settings resolved by the caller.
type ProviderConfig struct {
	Model             string
	Timeout           time.Duration
	ExternalReviewers []string
	Runner            commandRunner
}

// NewReviewer constructs the concrete reviewer for the requested provider.
func NewReviewer(provider string, cfg ProviderConfig) (Reviewer, error) {
	switch normalizeRequestedProvider(provider) {
	case ProviderClaude:
		return NewClaudeReviewer(cfg.Model, cfg.Timeout), nil
	case ProviderOpenAI:
		return NewOpenAIReviewer(cfg.Model, cfg.Timeout), nil
	case ProviderExternal:
		return NewExternalReviewer(ExternalReviewerConfig{
			Timeout:          cfg.Timeout,
			Reviewers:        cfg.ExternalReviewers,
			Runner:           cfg.Runner,
			CurrentTime:      time.Now,
			DefaultPRTimeout: cfg.Timeout,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported review provider %q", provider)
	}
}

func normalizeRequestedProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderClaude:
		return ProviderClaude
	case ProviderOpenAI:
		return ProviderOpenAI
	case ProviderExternal:
		return ProviderExternal
	default:
		return ""
	}
}
