package tmuxtest

import (
	"os/exec"
	"testing"
)

func Skip(tb testing.TB) string {
	tb.Helper()

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		tb.Skip("tmux not installed; install tmux to run tmux-backed tests")
	}
	return tmuxPath
}
