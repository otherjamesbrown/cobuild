package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/testutil/pgtest"
)

type testTmux struct{}

func withTestTmux(t *testing.T) *testTmux {
	t.Helper()

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}

	dir, err := os.MkdirTemp("", "cbtmux-")
	if err != nil {
		t.Fatalf("mktemp tmux dir: %v", err)
	}
	socketPath := filepath.Join(dir, "tmux.sock")
	wrapperPath := filepath.Join(dir, "tmux")
	wrapper := fmt.Sprintf("#!/bin/sh\nunset TMUX\nexec %q -S %q \"$@\"\n", tmuxPath, socketPath)
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0755); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmux := &testTmux{}
	tmux.mustRun(t, "new-session", "-d", "-s", "cobuild-test-server", "-n", "__keepalive__", "sleep", "300")

	t.Cleanup(func() {
		cmd := exec.Command("tmux", "kill-server")
		cmd.Env = append(os.Environ(), "TMUX=")
		_, _ = cmd.CombinedOutput()
		_ = os.Remove(socketPath)
		_ = os.RemoveAll(dir)
	})

	return tmux
}

func (testTmux) mustRun(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (tm testTmux) hasWindow(t *testing.T, sessionName, windowName string) bool {
	t.Helper()

	cmd := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "can't find session") {
			return false
		}
		t.Fatalf("tmux list-windows -t %s: %v\n%s", sessionName, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == windowName {
			return true
		}
	}
	return false
}

func TestPollerStaleHelpersUseSharedTmuxAndPostgresFixtures(t *testing.T) {
	ctx := context.Background()
	tmux := withTestTmux(t)
	pg := pgtest.New(t, ctx)

	designID := fmt.Sprintf("cb-stale-helper-%d", time.Now().UnixNano())
	taskID := designID + "-task"
	worktree := t.TempDir()
	logPath := filepath.Join(worktree, ".cobuild", "session.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir session log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("stale\n"), 0644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	now := time.Date(2026, 4, 13, 11, 0, 0, 0, time.UTC)
	oldTime := now.Add(-45 * time.Minute)
	if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes session log: %v", err)
	}

	run, err := pg.Store.CreateRun(ctx, designID, "test-project", "implement")
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	t.Cleanup(func() {
		pg.CleanupDesign(t, ctx, designID)
	})

	sessionName := "cobuild-test-project"
	windowName := taskID
	tmux.mustRun(t, "new-session", "-d", "-s", sessionName, "-n", windowName, "sleep", "300")

	session, err := pg.Store.CreateSession(ctx, store.SessionInput{
		PipelineID:   run.ID,
		DesignID:     designID,
		TaskID:       taskID,
		Phase:        "implement",
		Project:      "test-project",
		WorktreePath: worktree,
		TmuxSession:  sessionName,
		TmuxWindow:   windowName,
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	restore := installTestGlobals(t, newFakeConnector(), pg.Store, "test-project")
	defer restore()

	prevNow := pollerNow
	prevKill := pollerKillWindow
	t.Cleanup(func() {
		pollerNow = prevNow
		pollerKillWindow = prevKill
	})
	pollerNow = func() time.Time { return now }
	pollerKillWindow = func(ctx context.Context, sessionName, windowName string) error {
		target := fmt.Sprintf("%s:%s", sessionName, windowName)
		return exec.CommandContext(ctx, "tmux", "kill-window", "-t", target).Run()
	}

	checkStaleSessions(ctx, &config.Config{
		Monitoring: config.MonitoringCfg{StallTimeout: "30m"},
	}, false)

	got, err := pg.Store.GetSession(ctx, taskID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.ID != session.ID {
		t.Fatalf("GetSession() returned %q, want %q", got.ID, session.ID)
	}
	if got.Status != "stale-killed" {
		t.Fatalf("session status = %q, want stale-killed", got.Status)
	}
	if got.CompletionNote == nil || !strings.Contains(*got.CompletionNote, "stall_timeout") {
		t.Fatalf("completion note = %v, want stall timeout note", got.CompletionNote)
	}
	if tmux.hasWindow(t, sessionName, windowName) {
		t.Fatalf("tmux window %s:%s still exists after stale cleanup", sessionName, windowName)
	}
}
