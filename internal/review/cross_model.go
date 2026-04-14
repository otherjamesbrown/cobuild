package review

import (
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

const (
	ProviderAuto     = "auto"
	ProviderClaude   = "claude"
	ProviderOpenAI   = "openai"
	ProviderExternal = "external"

	FamilyClaude  = "claude"
	FamilyOpenAI  = "openai"
	FamilyUnknown = "unknown"

	DefaultClaudeModel = "sonnet"
	DefaultOpenAIModel = "gpt-5.4"
)

// ResolvedReviewer captures the selected review provider/model pair.
type ResolvedReviewer struct {
	Provider     string
	Model        string
	WriterFamily string
}

// DetectModelFamily classifies a writer runtime/model into a provider family.
func DetectModelFamily(runtime, model string) string {
	rt := strings.ToLower(strings.TrimSpace(runtime))
	mdl := strings.ToLower(strings.TrimSpace(model))

	switch {
	case strings.Contains(rt, "codex"), strings.Contains(rt, "openai"), strings.HasPrefix(mdl, "gpt-"):
		return FamilyOpenAI
	case strings.Contains(rt, "claude"), strings.HasPrefix(mdl, "claude"), mdl == "sonnet", mdl == "haiku", mdl == "opus":
		return FamilyClaude
	default:
		return FamilyUnknown
	}
}

// ResolveReviewer picks the effective built-in review provider/model, while
// preserving the external path for legacy repos.
func ResolveReviewer(cfg config.ReviewCfg, writerRuntime, writerModel string) ResolvedReviewer {
	writerFamily := DetectModelFamily(writerRuntime, writerModel)
	provider := cfg.EffectiveProvider()
	model := strings.TrimSpace(cfg.Model)

	if provider == ProviderExternal {
		return ResolvedReviewer{
			Provider:     ProviderExternal,
			Model:        model,
			WriterFamily: writerFamily,
		}
	}

	if model != "" {
		if provider == ProviderAuto {
			provider = providerForFamily(DetectModelFamily("", model))
		}
		return ResolvedReviewer{
			Provider:     normalizeProvider(provider),
			Model:        model,
			WriterFamily: writerFamily,
		}
	}

	if provider != ProviderAuto {
		return ResolvedReviewer{
			Provider:     normalizeProvider(provider),
			Model:        defaultModelForProvider(provider),
			WriterFamily: writerFamily,
		}
	}

	targetFamily := FamilyClaude
	if cfg.CrossModelEnabled() {
		targetFamily = oppositeFamily(writerFamily)
	}
	return ResolvedReviewer{
		Provider:     providerForFamily(targetFamily),
		Model:        defaultModelForFamily(targetFamily),
		WriterFamily: writerFamily,
	}
}

func oppositeFamily(family string) string {
	switch family {
	case FamilyOpenAI:
		return FamilyClaude
	case FamilyClaude:
		return FamilyOpenAI
	default:
		return FamilyClaude
	}
}

func providerForFamily(family string) string {
	switch family {
	case FamilyOpenAI:
		return ProviderOpenAI
	case FamilyClaude:
		return ProviderClaude
	default:
		return ProviderClaude
	}
}

func defaultModelForFamily(family string) string {
	switch family {
	case FamilyOpenAI:
		return DefaultOpenAIModel
	case FamilyClaude:
		return DefaultClaudeModel
	default:
		return DefaultClaudeModel
	}
}

func defaultModelForProvider(provider string) string {
	return defaultModelForFamily(normalizeProvider(provider))
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderOpenAI:
		return ProviderOpenAI
	case ProviderExternal:
		return ProviderExternal
	case ProviderClaude:
		return ProviderClaude
	default:
		// Unknown provider — route through external rather than silently
		// degrading to Claude. Callers setting review.provider to something
		// unrecognised (e.g. "gemini" before cb-efe119) expected external
		// PR-comment handling, not an authenticated Anthropic API call.
		return ProviderExternal
	}
}
