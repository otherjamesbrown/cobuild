package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestCheckStaleSessionsKillsOldRunningSession(t *testing.T) {
	ctx := context.Background()
	worktree := t.TempDir()
	logPath := filepath.Join(worktree, ".cobuild", "session.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("old\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	now := time.Date(2026, 4, 12, 15, 0, 0, 0, time.UTC)
	oldTime := now.Add(-45 * time.Minute)
	if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sessionName := "custom-session"
	windowName := "cb-task"
	fs := newFakeStore()
	fs.runningSessions = []store.SessionRecord{{
		ID:           "ps-1",
		TaskID:       "cb-task",
		Project:      "test-project",
		Status:       "running",
		WorktreePath: ptr(worktree),
		TmuxSession:  ptr(sessionName),
		TmuxWindow:   ptr(windowName),
	}}

	restore := installTestGlobals(t, newFakeConnector(), fs, "test-project")
	defer restore()

	prevNow := pollerNow
	prevKill := pollerKillWindow
	t.Cleanup(func() {
		pollerNow = prevNow
		pollerKillWindow = prevKill
	})
	pollerNow = func() time.Time { return now }

	var killedTarget string
	pollerKillWindow = func(_ context.Context, sessionName, windowName string) error {
		killedTarget = sessionName + ":" + windowName
		return nil
	}

	checkStaleSessions(ctx, &config.Config{
		Monitoring: config.MonitoringCfg{StallTimeout: "30m"},
	}, false)

	if fs.lastProject != "test-project" {
		t.Fatalf("ListRunningSessions project = %q, want test-project", fs.lastProject)
	}
	if killedTarget != "custom-session:cb-task" {
		t.Fatalf("killed target = %q, want custom-session:cb-task", killedTarget)
	}
	result, ok := fs.ended["ps-1"]
	if !ok {
		t.Fatalf("session not ended")
	}
	if result.Status != "stale-killed" {
		t.Fatalf("status = %q, want stale-killed", result.Status)
	}
	if result.CompletionNote == "" || !strings.Contains(result.CompletionNote, "stall_timeout") {
		t.Fatalf("completion note = %q, want stale note", result.CompletionNote)
	}
}

func TestCheckStaleSessionsLeavesRecentSessionRunning(t *testing.T) {
	ctx := context.Background()
	worktree := t.TempDir()
	logPath := filepath.Join(worktree, ".cobuild", "session.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("recent\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	now := time.Date(2026, 4, 12, 15, 0, 0, 0, time.UTC)
	recentTime := now.Add(-5 * time.Minute)
	if err := os.Chtimes(logPath, recentTime, recentTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	fs := newFakeStore()
	fs.runningSessions = []store.SessionRecord{{
		ID:           "ps-2",
		TaskID:       "cb-active",
		Project:      "test-project",
		Status:       "running",
		WorktreePath: ptr(worktree),
	}}

	restore := installTestGlobals(t, newFakeConnector(), fs, "test-project")
	defer restore()

	prevNow := pollerNow
	prevKill := pollerKillWindow
	t.Cleanup(func() {
		pollerNow = prevNow
		pollerKillWindow = prevKill
	})
	pollerNow = func() time.Time { return now }

	killed := false
	pollerKillWindow = func(_ context.Context, sessionName, windowName string) error {
		killed = true
		return nil
	}

	checkStaleSessions(ctx, &config.Config{
		Monitoring: config.MonitoringCfg{StallTimeout: "30m"},
	}, false)

	if killed {
		t.Fatalf("recent session was killed")
	}
	if len(fs.ended) != 0 {
		t.Fatalf("ended sessions = %v, want none", fs.ended)
	}
}

func TestCheckStaleSessionsMarksMissingLogOrWorktreeOrphaned(t *testing.T) {
	ctx := context.Background()
	worktree := t.TempDir()

	fs := newFakeStore()
	fs.runningSessions = []store.SessionRecord{
		{
			ID:           "ps-3",
			TaskID:       "cb-missing-log",
			Project:      "test-project",
			Status:       "running",
			WorktreePath: ptr(worktree),
		},
		{
			ID:           "ps-4",
			TaskID:       "cb-missing-worktree",
			Project:      "test-project",
			Status:       "running",
			WorktreePath: ptr(filepath.Join(t.TempDir(), "gone")),
		},
	}

	restore := installTestGlobals(t, newFakeConnector(), fs, "test-project")
	defer restore()

	prevKill := pollerKillWindow
	t.Cleanup(func() { pollerKillWindow = prevKill })
	pollerKillWindow = func(_ context.Context, sessionName, windowName string) error {
		t.Fatalf("orphaned session should not be killed")
		return nil
	}

	checkStaleSessions(ctx, &config.Config{
		Monitoring: config.MonitoringCfg{StallTimeout: "30m"},
	}, false)

	for _, id := range []string{"ps-3", "ps-4"} {
		result, ok := fs.ended[id]
		if !ok {
			t.Fatalf("session %s not ended", id)
		}
		if result.Status != "orphaned" {
			t.Fatalf("session %s status = %q, want orphaned", id, result.Status)
		}
		if result.CompletionNote == "" {
			t.Fatalf("session %s missing completion note", id)
		}
	}
}

func ptr[T any](v T) *T {
	return &v
}
