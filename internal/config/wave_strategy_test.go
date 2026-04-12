package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWaveStrategy(t *testing.T) {
	base := DefaultConfig()
	if got := base.ResolveWaveStrategy(); got != WaveStrategySerial {
		t.Fatalf("default wave strategy: got %q, want %q", got, WaveStrategySerial)
	}

	override := MergeConfig(base, &Config{
		Dispatch: DispatchCfg{WaveStrategy: "parallel"},
	})
	if got := override.ResolveWaveStrategy(); got != WaveStrategyParallel {
		t.Fatalf("parallel override: got %q, want %q", got, WaveStrategyParallel)
	}

	invalid := MergeConfig(base, &Config{
		Dispatch: DispatchCfg{WaveStrategy: "unexpected"},
	})
	if got := invalid.ResolveWaveStrategy(); got != WaveStrategySerial {
		t.Fatalf("invalid override should normalize to serial: got %q", got)
	}

	mixedCase := MergeConfig(base, &Config{
		Dispatch: DispatchCfg{WaveStrategy: " Parallel "},
	})
	if got := mixedCase.ResolveWaveStrategy(); got != WaveStrategyParallel {
		t.Fatalf("mixed-case override should normalize to parallel: got %q", got)
	}
}

func TestLoadConfig_WaveStrategyDefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoRoot := t.TempDir()
	repoCfgDir := filepath.Join(repoRoot, ".cobuild")
	if err := os.MkdirAll(repoCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}

	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig with no pipeline file: %v", err)
	}
	if got := cfg.Dispatch.WaveStrategy; got != WaveStrategySerial {
		t.Fatalf("missing wave_strategy should resolve to serial: got %q", got)
	}

	repoPipeline := []byte("dispatch:\n  wave_strategy: parallel\n")
	if err := os.WriteFile(filepath.Join(repoCfgDir, "pipeline.yaml"), repoPipeline, 0o644); err != nil {
		t.Fatalf("write repo pipeline: %v", err)
	}

	cfg, err = LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig with explicit parallel: %v", err)
	}
	if got := cfg.Dispatch.WaveStrategy; got != WaveStrategyParallel {
		t.Fatalf("explicit parallel should be preserved: got %q", got)
	}

	repoPipeline = []byte("dispatch:\n  wave_strategy: invalid-value\n")
	if err := os.WriteFile(filepath.Join(repoCfgDir, "pipeline.yaml"), repoPipeline, 0o644); err != nil {
		t.Fatalf("rewrite repo pipeline: %v", err)
	}

	cfg, err = LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig with invalid wave_strategy: %v", err)
	}
	if got := cfg.Dispatch.WaveStrategy; got != WaveStrategySerial {
		t.Fatalf("invalid wave_strategy should normalize to serial: got %q", got)
	}
}

func TestLoadConfig_ReviewAutoFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	homeCfgDir := filepath.Join(home, ".cobuild")
	if err := os.MkdirAll(homeCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir home config dir: %v", err)
	}
	globalPipeline := []byte("review:\n  provider: external\n  cross_model: false\n  post_comments: false\n  ci_mode: off\n  wait_for_ci: false\n  timeout: 10m\n")
	if err := os.WriteFile(filepath.Join(homeCfgDir, "pipeline.yaml"), globalPipeline, 0o644); err != nil {
		t.Fatalf("write global pipeline: %v", err)
	}

	repoRoot := t.TempDir()
	repoCfgDir := filepath.Join(repoRoot, ".cobuild")
	if err := os.MkdirAll(repoCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	repoPipeline := []byte("review:\n  provider: auto\n  cross_model: true\n  post_comments: true\n  ci_mode: pr-only\n  wait_for_ci: true\n  timeout: 120s\n")
	if err := os.WriteFile(filepath.Join(repoCfgDir, "pipeline.yaml"), repoPipeline, 0o644); err != nil {
		t.Fatalf("write repo pipeline: %v", err)
	}

	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadConfig review auto fields: %v", err)
	}
	if got := cfg.Review.Provider; got != "auto" {
		t.Fatalf("review.provider: got %q, want auto", got)
	}
	if cfg.Review.CrossModel == nil || !*cfg.Review.CrossModel {
		t.Fatalf("review.cross_model: got %#v, want true", cfg.Review.CrossModel)
	}
	if cfg.Review.PostComments == nil || !*cfg.Review.PostComments {
		t.Fatalf("review.post_comments: got %#v, want true", cfg.Review.PostComments)
	}
	if got := cfg.Review.CIMode; got != "pr-only" {
		t.Fatalf("review.ci_mode: got %q, want pr-only", got)
	}
	if cfg.Review.WaitForCI == nil || !*cfg.Review.WaitForCI {
		t.Fatalf("review.wait_for_ci: got %#v, want true", cfg.Review.WaitForCI)
	}
	if got := cfg.Review.Timeout; got != "120s" {
		t.Fatalf("review.timeout: got %q, want 120s", got)
	}
}
