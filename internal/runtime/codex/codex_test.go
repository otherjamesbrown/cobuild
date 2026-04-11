package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

func TestRuntimeMetadata(t *testing.T) {
	r := New()
	if got := r.Name(); got != "codex" {
		t.Errorf("Name() = %q, want %q", got, "codex")
	}
	if got := r.ContextFile(); got != "AGENTS.md" {
		t.Errorf("ContextFile() = %q, want %q", got, "AGENTS.md")
	}
}

func TestRegistered(t *testing.T) {
	rt, err := runtime.Get("codex")
	if err != nil {
		t.Fatalf("runtime.Get(codex): %v", err)
	}
	if rt.Name() != "codex" {
		t.Errorf("registered runtime Name = %q", rt.Name())
	}
}

func TestPreDispatchAndWriteSettings_NoOp(t *testing.T) {
	r := New()
	if err := r.PreDispatch(nil, "/nonexistent/worktree"); err != nil {
		t.Errorf("PreDispatch should be a no-op, got %v", err)
	}
	if err := r.WriteSettings("/nonexistent/worktree"); err != nil {
		t.Errorf("WriteSettings should be a no-op, got %v", err)
	}
}

func TestBuildRunnerScript_RequiredFields(t *testing.T) {
	r := New()
	cases := []struct {
		name string
		in   runtime.RunnerInput
	}{
		{"missing WorktreePath", runtime.RunnerInput{TaskID: "t", PromptFile: "/p", RepoRoot: "/r"}},
		{"missing TaskID", runtime.RunnerInput{WorktreePath: "/w", PromptFile: "/p", RepoRoot: "/r"}},
		{"missing PromptFile", runtime.RunnerInput{WorktreePath: "/w", TaskID: "t", RepoRoot: "/r"}},
		{"missing RepoRoot", runtime.RunnerInput{WorktreePath: "/w", TaskID: "t", PromptFile: "/p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.BuildRunnerScript(tc.in); err == nil {
				t.Errorf("expected error, got nil")
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
		Model:        "gpt-5.4",
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
		"export COBUILD_TASK_ID='cb-abc123'",
		"export COBUILD_REPO_ROOT='/home/u/repo'",
		"codex exec --json --full-auto --model gpt-5.4 -C \"$PWD\"",
		"--output-last-message .cobuild/last-message.md",
		"\"$PROMPT\"",
		"> .cobuild/session.log 2> .cobuild/session.err",
		`.type=="thread.started"`,
		`.type=="turn.completed"`,
		`rm -f "$0"`,
		"cobuild complete 'cb-abc123'",
	}
	for _, s := range mustContain {
		if !strings.Contains(script, s) {
			t.Errorf("script missing %q\n---\n%s\n---", s, script)
		}
	}
}

func TestBuildRunnerScript_ExtraFlagsOverrides(t *testing.T) {
	r := New()
	script, err := r.BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/w",
		RepoRoot:     "/r",
		TaskID:       "t",
		PromptFile:   "/p",
		ExtraFlags:   "--json --dangerously-bypass-approvals-and-sandbox",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "codex exec --json --dangerously-bypass-approvals-and-sandbox -C") {
		t.Errorf("expected ExtraFlags to replace default flags, got:\n%s", script)
	}
	if strings.Contains(script, "--full-auto") {
		t.Errorf("expected --full-auto to be absent when ExtraFlags replaces defaults")
	}
}

func TestParseSessionStats_RealEvents(t *testing.T) {
	// This is the exact event shape produced by `codex exec --json` in the
	// smoke test we ran earlier — see the thread "codex exec --json through
	// tmux" investigation. Keep this test in sync with empirical reality.
	logLines := `{"type":"thread.started","thread_id":"019d7c51-7fce-7053-a839-7ef9b554ddb6"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Creating hello.txt..."}}
{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"/tmp/x","kind":"add"}]}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":20543,"cached_input_tokens":13696,"output_tokens":99}}
`
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte(logLines), 0644); err != nil {
		t.Fatal(err)
	}

	r := New()
	stats, err := r.ParseSessionStats(logPath)
	if err != nil {
		t.Fatalf("ParseSessionStats: %v", err)
	}
	if stats.SessionUUID != "019d7c51-7fce-7053-a839-7ef9b554ddb6" {
		t.Errorf("SessionUUID = %q", stats.SessionUUID)
	}
	if stats.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", stats.TurnCount)
	}
	if stats.InputTokens != 20543 {
		t.Errorf("InputTokens = %d, want 20543", stats.InputTokens)
	}
	if stats.CachedInputTokens != 13696 {
		t.Errorf("CachedInputTokens = %d, want 13696", stats.CachedInputTokens)
	}
	if stats.OutputTokens != 99 {
		t.Errorf("OutputTokens = %d, want 99", stats.OutputTokens)
	}
	if stats.LastMessage != "done" {
		t.Errorf("LastMessage = %q, want 'done'", stats.LastMessage)
	}
}

func TestParseSessionStats_MultiTurnSum(t *testing.T) {
	logLines := `{"type":"thread.started","thread_id":"t-1"}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":10}}
{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":75,"output_tokens":20}}
{"type":"turn.completed","usage":{"input_tokens":300,"cached_input_tokens":100,"output_tokens":30}}
`
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte(logLines), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := New().ParseSessionStats(logPath)
	if err != nil {
		t.Fatalf("ParseSessionStats: %v", err)
	}
	if stats.TurnCount != 3 {
		t.Errorf("TurnCount = %d, want 3", stats.TurnCount)
	}
	if stats.InputTokens != 600 {
		t.Errorf("InputTokens = %d, want 600", stats.InputTokens)
	}
	if stats.CachedInputTokens != 225 {
		t.Errorf("CachedInputTokens = %d, want 225", stats.CachedInputTokens)
	}
	if stats.OutputTokens != 60 {
		t.Errorf("OutputTokens = %d, want 60", stats.OutputTokens)
	}
}

func TestParseSessionStats_MalformedLinesSkipped(t *testing.T) {
	logLines := `{"type":"thread.started","thread_id":"t"}
not json at all
{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}

{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":0,"output_tokens":7}}
`
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte(logLines), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := New().ParseSessionStats(logPath)
	if err != nil {
		t.Fatalf("ParseSessionStats: %v", err)
	}
	if stats.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", stats.TurnCount)
	}
	if stats.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", stats.InputTokens)
	}
}
