package cmd

import (
	"context"
	"os/exec"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

func tmuxCommandArgs(pCfg *config.Config, args ...string) []string {
	if pCfg == nil {
		pCfg = config.DefaultConfig()
	}
	return pCfg.TmuxArgs(args...)
}

func tmuxRun(ctx context.Context, pCfg *config.Config, args ...string) error {
	return exec.CommandContext(ctx, "tmux", tmuxCommandArgs(pCfg, args...)...).Run()
}

func tmuxCombinedOutput(ctx context.Context, pCfg *config.Config, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "tmux", tmuxCommandArgs(pCfg, args...)...).CombinedOutput()
}

func tmuxCommandRunner(pCfg *config.Config) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = tmuxCommandArgs(pCfg, args...)
		}
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
}
