package cmd

import (
	"context"
	"os/exec"
)

// execCommandOutput runs a command and returns stdout; tests can override it.
var execCommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// execCommandCombinedOutput runs a command and returns stdout+stderr; tests can override it.
var execCommandCombinedOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
