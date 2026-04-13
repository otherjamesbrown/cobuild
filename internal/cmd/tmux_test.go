package cmd

import (
	"reflect"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

func TestTmuxCommandArgsUsesConfiguredSocket(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dispatch.TmuxSocket = "/tmp/cobuild.sock"

	got := tmuxCommandArgs(cfg, "new-window", "-n", "cb-task")
	want := []string{"-S", "/tmp/cobuild.sock", "new-window", "-n", "cb-task"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmuxCommandArgs() = %#v, want %#v", got, want)
	}
}
