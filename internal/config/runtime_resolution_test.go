package config

import "testing"

func TestResolveRuntime_Priority(t *testing.T) {
	cfg := &Config{
		Dispatch: DispatchCfg{DefaultRuntime: "codex"},
	}

	// Flag override beats everything.
	if got := cfg.ResolveRuntime("claude-code", "codex"); got != "claude-code" {
		t.Errorf("flag override: got %q, want claude-code", got)
	}
	// Task metadata beats config default.
	if got := cfg.ResolveRuntime("", "claude-code"); got != "claude-code" {
		t.Errorf("metadata: got %q, want claude-code", got)
	}
	// Config default used when no flag/metadata.
	if got := cfg.ResolveRuntime("", ""); got != "codex" {
		t.Errorf("config default: got %q, want codex", got)
	}
	// Hardcoded fallback when config has no default.
	empty := &Config{}
	if got := empty.ResolveRuntime("", ""); got != "claude-code" {
		t.Errorf("hardcoded fallback: got %q, want claude-code", got)
	}
	// Nil receiver is safe.
	var nilCfg *Config
	if got := nilCfg.ResolveRuntime("", ""); got != "claude-code" {
		t.Errorf("nil receiver: got %q, want claude-code", got)
	}

	stubCfg := &Config{Dispatch: DispatchCfg{DefaultRuntime: "stub"}}
	if got := stubCfg.ResolveRuntime("", ""); got != "stub" {
		t.Errorf("stub config default: got %q, want stub", got)
	}
}

func TestModelForPhaseRuntime(t *testing.T) {
	cfg := &Config{
		Dispatch: DispatchCfg{
			Runtimes: map[string]RuntimeCfg{
				"claude-code": {Model: "sonnet"},
				"codex":       {Model: "gpt-5.4"},
			},
			DefaultModel: "legacy-fallback",
		},
		Review: ReviewCfg{Model: "haiku"},
		Phases: map[string]PhaseConfig{
			"implement": {Model: ""},
			"design":    {Model: "opus"},
			"review":    {Model: ""},
		},
	}

	// Per-runtime default picked up when phase has no model.
	if got := cfg.ModelForPhaseRuntime("implement", "", "claude-code"); got != "sonnet" {
		t.Errorf("implement/claude-code: got %q, want sonnet", got)
	}
	if got := cfg.ModelForPhaseRuntime("implement", "", "codex"); got != "gpt-5.4" {
		t.Errorf("implement/codex: got %q, want gpt-5.4", got)
	}
	// Phase-level model beats runtime default.
	if got := cfg.ModelForPhaseRuntime("design", "", "codex"); got != "opus" {
		t.Errorf("design/codex: got %q, want opus (phase override)", got)
	}
	// review.Model trumps runtime default for review phase WHEN compatible.
	if got := cfg.ModelForPhaseRuntime("review", "", "claude-code"); got != "haiku" {
		t.Errorf("review/claude-code: got %q, want haiku (review.model)", got)
	}
	// review.Model=haiku is Claude-only; skipped on codex so the runtime
	// default wins (cb-b3356d). Without this, dispatch would 400 at runtime.
	if got := cfg.ModelForPhaseRuntime("review", "", "codex"); got != "gpt-5.4" {
		t.Errorf("review/codex: got %q, want gpt-5.4 (haiku incompatible with codex)", got)
	}
	// Legacy DefaultModel when runtime is unknown.
	if got := cfg.ModelForPhaseRuntime("implement", "", "unknown-rt"); got != "legacy-fallback" {
		t.Errorf("unknown runtime: got %q, want legacy-fallback", got)
	}
	// Empty runtime ignores the Runtimes map and falls through to DefaultModel.
	if got := cfg.ModelForPhaseRuntime("implement", "", ""); got != "legacy-fallback" {
		t.Errorf("empty runtime: got %q, want legacy-fallback", got)
	}
	// Nil receiver is safe.
	var nilCfg *Config
	if got := nilCfg.ModelForPhaseRuntime("implement", "", "claude-code"); got != "" {
		t.Errorf("nil receiver: got %q, want empty", got)
	}
}

func TestFlagsForRuntime_BackCompatClaudeFlags(t *testing.T) {
	cfg := &Config{
		Dispatch: DispatchCfg{
			ClaudeFlags: "--legacy-flags",
			Runtimes: map[string]RuntimeCfg{
				"codex": {Flags: "--json --full-auto"},
			},
		},
	}
	// New path wins when runtime-specific flags are set.
	if got := cfg.FlagsForRuntime("codex"); got != "--json --full-auto" {
		t.Errorf("codex: got %q", got)
	}
	// Legacy ClaudeFlags field honoured for claude-code when no runtime-specific flags.
	if got := cfg.FlagsForRuntime("claude-code"); got != "--legacy-flags" {
		t.Errorf("claude-code legacy: got %q", got)
	}
	// Legacy ClaudeFlags NOT honoured for other runtimes.
	if got := cfg.FlagsForRuntime("codex"); got == "--legacy-flags" {
		t.Errorf("codex should not inherit legacy ClaudeFlags")
	}
}

func TestMergeConfig_RuntimesFieldMerge(t *testing.T) {
	base := DefaultConfig()
	// Override just the codex model, leave claude-code alone.
	override := &Config{
		Dispatch: DispatchCfg{
			Runtimes: map[string]RuntimeCfg{
				"codex": {Model: "gpt-5-codex"},
			},
		},
	}
	merged := MergeConfig(base, override)

	// Codex model overridden.
	if got := merged.Dispatch.Runtimes["codex"].Model; got != "gpt-5-codex" {
		t.Errorf("codex model: got %q, want gpt-5-codex", got)
	}
	// Claude-code model preserved from base defaults.
	if got := merged.Dispatch.Runtimes["claude-code"].Model; got != "sonnet" {
		t.Errorf("claude-code model: got %q, want sonnet (from base)", got)
	}
}

// TestMergeConfig_WorkflowsFieldMerge reproduces the cb-11a464 scenario:
// a stale global pipeline.yaml overrides just `bug` and `task` without
// touching `bug-complex`, and `bug-complex` must survive from the base
// DefaultConfig (pre-fix, MergeConfig replaced the whole map wholesale
// and bug-complex was lost, reappearing only because setup_agents had a
// duplicate hardcoded fallback).
func TestMergeConfig_WorkflowsFieldMerge(t *testing.T) {
	base := DefaultConfig()
	override := &Config{
		Workflows: map[string]WorkflowConfig{
			"bug": {Phases: []string{"investigate", "implement", "review", "done"}},
			// task and design present in base; override leaves them alone.
		},
	}
	merged := MergeConfig(base, override)

	// Bug workflow replaced by override.
	if got := merged.Workflows["bug"].Phases; len(got) != 4 || got[0] != "investigate" {
		t.Errorf("bug workflow: got %v, want [investigate implement review done]", got)
	}
	// bug-complex must survive from base — this is the regression the fix
	// addresses. Before the fix, this would be an empty WorkflowConfig{}.
	bugComplex, ok := merged.Workflows["bug-complex"]
	if !ok {
		t.Fatalf("bug-complex workflow missing after merge — wholesale replace regression")
	}
	wantComplex := []string{"investigate", "implement", "review", "kb-sync", "done"}
	if len(bugComplex.Phases) != len(wantComplex) {
		t.Errorf("bug-complex phases: got %v, want %v", bugComplex.Phases, wantComplex)
	}
	// Task and design also survive from base untouched.
	if _, ok := merged.Workflows["task"]; !ok {
		t.Errorf("task workflow missing after merge")
	}
	if _, ok := merged.Workflows["design"]; !ok {
		t.Errorf("design workflow missing after merge")
	}

	// Base must not have been mutated — copyWorkflows is supposed to deep-copy.
	baseBug := base.Workflows["bug"].Phases
	if len(baseBug) != 4 || baseBug[0] != "fix" {
		t.Errorf("base bug workflow was mutated: %v (expected [fix review kb-sync done])", baseBug)
	}
}
