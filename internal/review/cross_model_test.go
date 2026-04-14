package review

import (
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestDetectModelFamily(t *testing.T) {
	tests := []struct {
		name    string
		runtime string
		model   string
		want    string
	}{
		{name: "codex runtime maps to openai", runtime: "codex", want: FamilyOpenAI},
		{name: "gpt model maps to openai", model: "gpt-5.4", want: FamilyOpenAI},
		{name: "claude runtime maps to claude", runtime: "claude-code", want: FamilyClaude},
		{name: "claude model maps to claude", model: "claude-sonnet-4-6", want: FamilyClaude},
		{name: "sonnet alias maps to claude", model: "sonnet", want: FamilyClaude},
		{name: "unknown stays unknown", runtime: "custom", model: "mystery", want: FamilyUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectModelFamily(tc.runtime, tc.model); got != tc.want {
				t.Fatalf("DetectModelFamily(%q, %q) = %q, want %q", tc.runtime, tc.model, got, tc.want)
			}
		})
	}
}

func TestResolveReviewer(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.ReviewCfg
		writerRuntime string
		writerModel   string
		wantProvider  string
		wantModel     string
	}{
		{
			name:          "codex writer resolves to claude reviewer",
			cfg:           config.ReviewCfg{Provider: ProviderAuto, CrossModel: boolPtr(true)},
			writerRuntime: "codex",
			wantProvider:  ProviderClaude,
			wantModel:     DefaultClaudeModel,
		},
		{
			name:         "gpt writer resolves to claude reviewer",
			cfg:          config.ReviewCfg{Provider: ProviderAuto, CrossModel: boolPtr(true)},
			writerModel:  "gpt-5.4",
			wantProvider: ProviderClaude,
			wantModel:    DefaultClaudeModel,
		},
		{
			name:          "claude writer resolves to openai reviewer",
			cfg:           config.ReviewCfg{Provider: ProviderAuto, CrossModel: boolPtr(true)},
			writerRuntime: "claude-code",
			wantProvider:  ProviderOpenAI,
			wantModel:     DefaultOpenAIModel,
		},
		{
			name:         "unknown writer defaults to claude reviewer",
			cfg:          config.ReviewCfg{Provider: ProviderAuto, CrossModel: boolPtr(true)},
			writerModel:  "mystery-model",
			wantProvider: ProviderClaude,
			wantModel:    DefaultClaudeModel,
		},
		{
			name:         "strategy external keeps legacy provider",
			cfg:          config.ReviewCfg{Strategy: "external"},
			wantProvider: ProviderExternal,
			wantModel:    "",
		},
		{
			name:         "explicit model infers provider when auto",
			cfg:          config.ReviewCfg{Provider: ProviderAuto, Model: "claude-sonnet-4-6"},
			wantProvider: ProviderClaude,
			wantModel:    "claude-sonnet-4-6",
		},
		{
			name:         "explicit provider bypasses cross-model selection",
			cfg:          config.ReviewCfg{Provider: ProviderOpenAI, CrossModel: boolPtr(true)},
			wantProvider: ProviderOpenAI,
			wantModel:    DefaultOpenAIModel,
		},
		{
			name:          "cross model disabled with auto falls back to claude default",
			cfg:           config.ReviewCfg{Provider: ProviderAuto, CrossModel: boolPtr(false)},
			writerRuntime: "claude-code",
			wantProvider:  ProviderClaude,
			wantModel:     DefaultClaudeModel,
		},
		{
			name:         "gemini provider routes to external (cb-efe119)",
			cfg:          config.ReviewCfg{Provider: "gemini"},
			wantProvider: ProviderExternal,
			wantModel:    "",
		},
		{
			name:         "unknown provider defaults to external not claude (cb-efe119)",
			cfg:          config.ReviewCfg{Provider: "copilot"},
			wantProvider: ProviderExternal,
			wantModel:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveReviewer(tc.cfg, tc.writerRuntime, tc.writerModel)
			if got.Provider != tc.wantProvider {
				t.Fatalf("provider = %q, want %q", got.Provider, tc.wantProvider)
			}
			if got.Model != tc.wantModel {
				t.Fatalf("model = %q, want %q", got.Model, tc.wantModel)
			}
		})
	}
}
