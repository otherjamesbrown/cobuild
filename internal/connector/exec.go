package connector

import (
	"context"
	"os/exec"
)

var connectorCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
