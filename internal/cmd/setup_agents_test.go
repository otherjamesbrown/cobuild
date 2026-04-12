package cmd

import (
	"strings"
	"testing"
)

func TestGenerateAgentsContentIncludesDirectCompletionGuidance(t *testing.T) {
	content := generateAgentsContent("cobuild", "cb", nil, nil, nil)

	for _, want := range []string{
		"**Preferred path:** run `cobuild orchestrate <id>`",
		"prefer `cobuild orchestrate <id>`",
		"| **`cobuild orchestrate <id>`** | **Preferred foreground driver for running a pipeline end-to-end** |",
		"### Non-code tasks",
		"`completion_mode: direct`",
		"CoBuild will use the direct path for `completion_mode: direct`",
		"`code` remains the normal path",
		"falls back to auto-detection",
		"This does not change deploy behavior.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("AGENTS content missing %q\ncontent:\n%s", want, content)
		}
	}
}
