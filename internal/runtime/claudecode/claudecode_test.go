package claudecode

import (
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

func TestRuntimeMetadata(t *testing.T) {
	r := New()
	if got := r.Name(); got != "claude-code" {
		t.Errorf("Name() = %q, want %q", got, "claude-code")
	}
	if got := r.ContextFile(); got != "CLAUDE.md" {
		t.Errorf("ContextFile() = %q, want %q", got, "CLAUDE.md")
	}
}

func TestRegistered(t *testing.T) {
	rt, err := runtime.Get("claude-code")
	if err != nil {
		t.Fatalf("runtime.Get(claude-code): %v", err)
	}
	if rt.Name() != "claude-code" {
		t.Errorf("registered runtime Name = %q", rt.Name())
	}
}

func TestBuildRunnerScript_RequiredFields(t *testing.T) {
	r := New()
	cases := []struct {
		name string
		in   runtime.RunnerInput
	}{
		{"missing WorktreePath", runtime.RunnerInput{TaskID: "t1", PromptFile: "/tmp/p", RepoRoot: "/r"}},
		{"missing TaskID", runtime.RunnerInput{WorktreePath: "/w", PromptFile: "/tmp/p", RepoRoot: "/r"}},
		{"missing PromptFile", runtime.RunnerInput{WorktreePath: "/w", TaskID: "t1", RepoRoot: "/r"}},
		{"missing RepoRoot", runtime.RunnerInput{WorktreePath: "/w", TaskID: "t1", PromptFile: "/tmp/p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.BuildRunnerScript(tc.in); err == nil {
				t.Errorf("BuildRunnerScript(%+v): expected error, got nil", tc.in)
			}
		})
	}
}

func TestBuildRunnerScript_Shape(t *testing.T) {
	r := New()
	script, err := r.BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/tmp/wt-abc",
		RepoRoot:     "/home/u/repo",
		TaskID:       "cb-abc123",
		PromptFile:   "/tmp/prompt.md",
		Model:        "sonnet",
		ExtraFlags:   "",
		SessionID:    "ps-xyz",
		HooksDir:     "/home/u/repo/hooks",
	})
	if err != nil {
		t.Fatalf("BuildRunnerScript: %v", err)
	}

	mustContain := []string{
		"#!/bin/bash",
		"cd '/tmp/wt-abc'",
		"export COBUILD_DISPATCH=true",
		"export COBUILD_SESSION_ID='ps-xyz'",
		"export COBUILD_HOOKS_DIR='/home/u/repo/hooks'",
		"export COBUILD_TASK_ID='cb-abc123'",
		"export COBUILD_REPO_ROOT='/home/u/repo'",
		"PROMPT_FILE='/tmp/prompt.md'",
		"claude --dangerously-skip-permissions --model sonnet \"$PROMPT\"",
		`rm -f "$0"`,
		"cobuild complete 'cb-abc123'",
	}
	for _, s := range mustContain {
		if !strings.Contains(script, s) {
			t.Errorf("script missing %q\n---\n%s\n---", s, script)
		}
	}
}

func TestBuildRunnerScript_GatePhaseUsesHeadlessMode(t *testing.T) {
	r := New()
	for _, phase := range []string{"design", "decompose", "investigate", "review", "done"} {
		t.Run(phase, func(t *testing.T) {
			script, err := r.BuildRunnerScript(runtime.RunnerInput{
				WorktreePath: "/w",
				RepoRoot:     "/r",
				TaskID:       "cb-test",
				PromptFile:   "/p",
				Phase:        phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(script, "-p --output-format json --max-turns 200") {
				t.Errorf("gate phase %q: script missing -p headless flags", phase)
			}
			if !strings.Contains(script, "session-result.json") {
				t.Errorf("gate phase %q: script missing session-result.json capture", phase)
			}
		})
	}
}

func TestBuildRunnerScript_ImplementPhaseUsesInteractiveMode(t *testing.T) {
	r := New()
	for _, phase := range []string{"implement", "fix"} {
		t.Run(phase, func(t *testing.T) {
			script, err := r.BuildRunnerScript(runtime.RunnerInput{
				WorktreePath: "/w",
				RepoRoot:     "/r",
				TaskID:       "cb-test",
				PromptFile:   "/p",
				Phase:        phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Implement/fix should NOT have -p in flags
			if strings.Contains(script, "-p --output-format json") {
				t.Errorf("implement phase %q: should not use headless mode", phase)
			}
		})
	}
}

// TestBuildRunnerScript_ReviewGateRoutesToProcessReview verifies the cb-465d17
// fix: the runner's gate-handling switch routes the `review` gate to
// `cobuild process-review`, not `cobuild review`. `cobuild review` doesn't
// merge the PR — using it on a task PR advances phase=done with the PR still
// open (observed on cb-b78c67).
func TestBuildRunnerScript_ReviewGateRoutesToProcessReview(t *testing.T) {
	r := New()
	script, err := r.BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/w",
		RepoRoot:     "/r",
		TaskID:       "cb-test",
		PromptFile:   "/p",
		Phase:        "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `cobuild process-review "$SHARD_ID"`) {
		t.Errorf("review gate must route to `cobuild process-review`, got:\n%s", script)
	}
	// Guard against regression: the old `cobuild review $SHARD_ID --verdict ...`
	// invocation must not appear in the review-gate case.
	if strings.Contains(script, `cobuild review "$SHARD_ID" --verdict "$VERDICT" --body "$BODY"`) {
		t.Errorf("cb-465d17 regression: review gate still uses `cobuild review` (doesn't merge PR)")
	}
}

func TestBuildRunnerScript_ExtraFlagsOverrides(t *testing.T) {
	r := New()
	script, err := r.BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/w",
		RepoRoot:     "/r",
		TaskID:       "t",
		PromptFile:   "/p",
		ExtraFlags:   "--print",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "claude --print \"$PROMPT\"") {
		t.Errorf("expected ExtraFlags to override default, got:\n%s", script)
	}
	if strings.Contains(script, "--dangerously-skip-permissions") {
		t.Errorf("expected default flags to be replaced when ExtraFlags is set")
	}
}

func TestBuildRunnerScript_ShellQuoting(t *testing.T) {
	// Task IDs with apostrophes shouldn't break the script. Backslash-escaping
	// single quotes using the standard '\'' sequence is the canonical way to
	// drop untrusted data into a single-quoted bash literal.
	r := New()
	script, err := r.BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/w",
		RepoRoot:     "/r",
		TaskID:       "it's-a-task",
		PromptFile:   "/p",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantExport := `export COBUILD_TASK_ID='it'\''s-a-task'`
	if !strings.Contains(script, wantExport) {
		t.Errorf("expected escaped task ID in env export, got:\n%s", script)
	}
}
