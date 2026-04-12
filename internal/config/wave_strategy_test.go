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
