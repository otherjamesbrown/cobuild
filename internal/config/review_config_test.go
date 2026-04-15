package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_ReviewFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoRoot := t.TempDir()
	repoCfgDir := filepath.Join(repoRoot, ".cobuild")
	if err := os.MkdirAll(repoCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}

	repoPipeline := []byte("" +
		"review:\n" +
		"  mode: builtin\n" +
		"  provider: auto\n" +
		"  model: \"\"\n" +
		"  cross_model: false\n" +
		"  post_comments: false\n" +
		"  timeout: 45s\n")
	if err := os.WriteFile(filepath.Join(repoCfgDir, "pipeline.yaml"), repoPipeline, 0o644); err != nil {
		t.Fatalf("write repo pipeline: %v", err)
	}

	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Review.EffectiveProvider(); got != "auto" {
		t.Fatalf("provider = %q, want auto", got)
	}
	if got := cfg.Review.EffectiveMode(); got != "builtin" {
		t.Fatalf("mode = %q, want builtin", got)
	}
	if got := cfg.Review.CrossModelEnabled(); got {
		t.Fatalf("cross_model = true, want false")
	}
	if got := cfg.Review.PostCommentsEnabled(); got {
		t.Fatalf("post_comments = true, want false")
	}
	if got := cfg.Review.ReviewTimeout(); got != 45*time.Second {
		t.Fatalf("timeout = %s, want 45s", got)
	}
}

func TestReviewCfgDefaultsAndLegacyFallback(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.Review.EffectiveMode(); got != "dispatched" {
		t.Fatalf("default mode = %q, want dispatched", got)
	}
	if got := cfg.Review.EffectiveProvider(); got != "external" {
		t.Fatalf("default provider = %q, want external", got)
	}
	phase := cfg.FindPhase("review")
	if phase == nil {
		t.Fatalf("review phase missing from defaults")
	}
	if phase.Gate != "review" {
		t.Fatalf("review phase gate = %q, want review", phase.Gate)
	}
	if phase.Skill != "review/dispatch-review.md" {
		t.Fatalf("review phase skill = %q, want review/dispatch-review.md", phase.Skill)
	}
	if got := cfg.Review.CrossModelEnabled(); !got {
		t.Fatalf("default cross_model = false, want true")
	}
	if got := cfg.Review.PostCommentsEnabled(); !got {
		t.Fatalf("default post_comments = false, want true")
	}
	if got := cfg.Review.ReviewTimeout(); got != 120*time.Second {
		t.Fatalf("default timeout = %s, want 120s", got)
	}

	legacy := ReviewCfg{Strategy: "external"}
	if got := legacy.EffectiveProvider(); got != "external" {
		t.Fatalf("legacy strategy fallback = %q, want external", got)
	}
}

func TestMergeConfig_ReviewBoolAndTimeoutOverrides(t *testing.T) {
	base := DefaultConfig()
	override := &Config{
		Review: ReviewCfg{
			Mode:         "external",
			Provider:     "auto",
			CrossModel:   boolPtr(false),
			PostComments: boolPtr(false),
			Timeout:      "30s",
		},
	}

	merged := MergeConfig(base, override)
	if got := merged.Review.EffectiveProvider(); got != "auto" {
		t.Fatalf("provider = %q, want auto", got)
	}
	if got := merged.Review.EffectiveMode(); got != "external" {
		t.Fatalf("mode = %q, want external", got)
	}
	if got := merged.Review.CrossModelEnabled(); got {
		t.Fatalf("cross_model = true, want false")
	}
	if got := merged.Review.PostCommentsEnabled(); got {
		t.Fatalf("post_comments = true, want false")
	}
	if got := merged.Review.ReviewTimeout(); got != 30*time.Second {
		t.Fatalf("timeout = %s, want 30s", got)
	}
}

func TestReviewCfg_EffectiveModeAcceptsSupportedValues(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "default empty", mode: "", want: "dispatched"},
		{name: "dispatched", mode: "dispatched", want: "dispatched"},
		{name: "builtin", mode: "builtin", want: "builtin"},
		{name: "external", mode: "external", want: "external"},
		{name: "invalid falls back", mode: "something-else", want: "dispatched"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ReviewCfg{Mode: tt.mode}
			if got := cfg.EffectiveMode(); got != tt.want {
				t.Fatalf("EffectiveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanupCfg_DefaultsAndOverrides(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.AutoOnMergeEnabled() {
		t.Fatalf("default auto_on_merge = false, want true")
	}

	override := &Config{
		Cleanup: CleanupCfg{
			AutoOnMerge: boolPtr(false),
		},
	}
	merged := MergeConfig(cfg, override)
	if merged.AutoOnMergeEnabled() {
		t.Fatalf("merged auto_on_merge = true, want false")
	}
}

func TestLoadConfig_CleanupAutoOnMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoRoot := t.TempDir()
	repoCfgDir := filepath.Join(repoRoot, ".cobuild")
	if err := os.MkdirAll(repoCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}

	repoPipeline := []byte("" +
		"cleanup:\n" +
		"  auto_on_merge: false\n")
	if err := os.WriteFile(filepath.Join(repoCfgDir, "pipeline.yaml"), repoPipeline, 0o644); err != nil {
		t.Fatalf("write repo pipeline: %v", err)
	}

	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AutoOnMergeEnabled() {
		t.Fatalf("loaded auto_on_merge = true, want false")
	}
}
