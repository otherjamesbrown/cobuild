package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
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
	// cb-a08acd: pipeline run for the task so markPipelineBlocked can
	// set its status to "blocked" after a stale kill.
	fs.runs["cb-task"] = &store.PipelineRun{
		ID:           "run-task",
		DesignID:     "cb-task",
		Project:      "test-project",
		CurrentPhase: "implement",
		Status:       "active",
	}

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

	if fs.lastProject != "" {
		t.Fatalf("ListRunningSessions project = %q, want empty (all projects)", fs.lastProject)
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
	// cb-a08acd: verify pipeline is marked blocked after stale kill.
	if fs.runs["cb-task"].Status != "blocked" {
		t.Fatalf("pipeline status = %q, want blocked", fs.runs["cb-task"].Status)
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

func TestInspectSessionHealth_StaleHeartbeatKills(t *testing.T) {
	worktree := t.TempDir()
	cobuildDir := filepath.Join(worktree, ".cobuild")
	if err := os.MkdirAll(cobuildDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	// session.log is fresh (5 min old)
	logPath := filepath.Join(cobuildDir, "session.log")
	if err := os.WriteFile(logPath, []byte("active\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, now.Add(-5*time.Minute), now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// heartbeat is stale (5 min old, > 2m threshold)
	hbPath := filepath.Join(cobuildDir, "heartbeat")
	if err := os.WriteFile(hbPath, []byte("1746186000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(hbPath, now.Add(-5*time.Minute), now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	session := store.SessionRecord{
		ID:           "ps-hb-stale",
		TaskID:       "cb-hb-test",
		WorktreePath: ptr(worktree),
	}

	outcome, note, _, err := inspectSessionHealth(session, 30*time.Minute, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != "stale-killed" {
		t.Fatalf("outcome = %q, want stale-killed", outcome)
	}
	if !strings.Contains(note, "heartbeat stale") {
		t.Fatalf("note = %q, want heartbeat stale mention", note)
	}
}

func TestInspectSessionHealth_FreshHeartbeatPreventsKill(t *testing.T) {
	worktree := t.TempDir()
	cobuildDir := filepath.Join(worktree, ".cobuild")
	if err := os.MkdirAll(cobuildDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	// session.log is stale (45 min old, > 30m stall_timeout)
	logPath := filepath.Join(cobuildDir, "session.log")
	if err := os.WriteFile(logPath, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, now.Add(-45*time.Minute), now.Add(-45*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// heartbeat is fresh (30s old, < 2m threshold)
	hbPath := filepath.Join(cobuildDir, "heartbeat")
	if err := os.WriteFile(hbPath, []byte("1746186000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(hbPath, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	session := store.SessionRecord{
		ID:           "ps-hb-fresh",
		TaskID:       "cb-hb-test",
		WorktreePath: ptr(worktree),
	}

	outcome, _, _, err := inspectSessionHealth(session, 30*time.Minute, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != "" {
		t.Fatalf("fresh heartbeat should prevent kill, got outcome %q", outcome)
	}
}

func TestInspectSessionHealth_NoHeartbeatFallsBackToSessionLog(t *testing.T) {
	worktree := t.TempDir()
	cobuildDir := filepath.Join(worktree, ".cobuild")
	if err := os.MkdirAll(cobuildDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	// session.log is stale (45 min old)
	logPath := filepath.Join(cobuildDir, "session.log")
	if err := os.WriteFile(logPath, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, now.Add(-45*time.Minute), now.Add(-45*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// No heartbeat file — older dispatch without heartbeat support.

	session := store.SessionRecord{
		ID:           "ps-no-hb",
		TaskID:       "cb-no-hb",
		WorktreePath: ptr(worktree),
	}

	outcome, note, _, err := inspectSessionHealth(session, 30*time.Minute, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != "stale-killed" {
		t.Fatalf("outcome = %q, want stale-killed (session.log fallback)", outcome)
	}
	if !strings.Contains(note, "session.log mtime") {
		t.Fatalf("note = %q, want session.log mtime mention", note)
	}
}

func TestReconcileStaleStateAppliesSharedRecoveries(t *testing.T) {
	ctx := context.Background()

	fc := newFakeConnector()
	fc.items["cb-orphaned"] = &connector.WorkItem{ID: "cb-orphaned", Type: "design", Status: "open", Project: "test-project"}
	fc.items["cb-inconsistent"] = &connector.WorkItem{ID: "cb-inconsistent", Type: "design", Status: "closed", Project: "test-project"}

	fs := newFakeStore()
	fs.runs["cb-orphaned"] = &store.PipelineRun{
		ID:           "run-orphaned",
		DesignID:     "cb-orphaned",
		Project:      "test-project",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.runs["cb-inconsistent"] = &store.PipelineRun{
		ID:           "run-inconsistent",
		DesignID:     "cb-inconsistent",
		Project:      "test-project",
		CurrentPhase: "review",
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:          "ps-1",
		DesignID:    "cb-orphaned",
		PipelineID:  "run-orphaned",
		Project:     "test-project",
		Status:      "running",
		TmuxSession: ptr("cobuild-test-project"),
		TmuxWindow:  ptr("cb-orphaned"),
	}}

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			t.Fatalf("unexpected command %q", name)
		}
		if len(args) >= 2 && args[0] == "list-sessions" {
			return []byte("cobuild-test-project\n"), nil
		}
		if len(args) >= 3 && args[0] == "list-windows" && args[1] == "-t" && args[2] == "cobuild-test-project" {
			return []byte("@7\tcb-inconsistent\n"), nil
		}
		t.Fatalf("unexpected tmux args %v", args)
		return nil, nil
	})
	defer restore()

	prevExec := pollerExec
	t.Cleanup(func() { pollerExec = prevExec })

	var killed [][]string
	pollerExec = func(_ context.Context, name string, args ...string) ([]byte, error) {
		killed = append(killed, append([]string{name}, args...))
		return nil, nil
	}

	restoreLevel := setLogLevelForTest(slog.LevelInfo)
	defer restoreLevel()

	_, stderr := captureStdoutAndStderr(t, func() {
		reconcileStaleState(ctx, false)
	})

	if result, ok := fs.ended["ps-1"]; !ok {
		t.Fatalf("session ps-1 not ended")
	} else if result.Status != "orphaned" {
		t.Fatalf("session status = %q, want orphaned", result.Status)
	}
	if got := fs.runs["cb-inconsistent"].CurrentPhase; got != "done" {
		t.Fatalf("phase = %q, want done", got)
	}
	if got := fs.runs["cb-inconsistent"].Status; got != "completed" {
		t.Fatalf("status = %q, want completed", got)
	}
	if len(killed) != 1 {
		t.Fatalf("killed windows = %#v, want one kill", killed)
	}
	if got := strings.Join(killed[0], " "); got != "tmux kill-window -t @7" {
		t.Fatalf("kill command = %q, want tmux kill-window -t @7", got)
	}
	// cb-e7edc9: reconcile events moved to slog.Info with structured attrs
	// (component, id, kind, reason). Tests now assert on the attribute shape
	// rather than the previous "[reconcile] id kind: reason" line.
	for _, want := range []string{
		`component=reconcile id=cb-orphaned kind=cancel_orphaned_session`,
		`session ps-1 is running but no tmux window exists`,
		`component=reconcile id=cb-inconsistent kind=kill_orphan_tmux_window`,
		`tmux window cobuild-test-project:cb-inconsistent exists but no matching pipeline session exists`,
		`component=reconcile id=cb-inconsistent kind=complete_stale_run`,
		`pipeline cb-inconsistent run is active but work item is closed`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("missing reconcile log %q:\n%s", want, stderr)
		}
	}
}

func TestReconcileStaleStateDryRunLogsWithoutMutating(t *testing.T) {
	ctx := context.Background()

	fc := newFakeConnector()
	fc.items["cb-orphaned"] = &connector.WorkItem{ID: "cb-orphaned", Type: "design", Status: "open", Project: "test-project"}

	fs := newFakeStore()
	fs.runs["cb-orphaned"] = &store.PipelineRun{
		ID:           "run-orphaned",
		DesignID:     "cb-orphaned",
		Project:      "test-project",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:          "ps-1",
		DesignID:    "cb-orphaned",
		PipelineID:  "run-orphaned",
		Project:     "test-project",
		Status:      "running",
		TmuxSession: ptr("cobuild-test-project"),
		TmuxWindow:  ptr("cb-orphaned"),
	}}

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			t.Fatalf("unexpected command %q", name)
		}
		if len(args) >= 2 && args[0] == "list-sessions" {
			return []byte(""), nil
		}
		if len(args) >= 3 && args[0] == "list-windows" {
			return []byte(""), nil
		}
		t.Fatalf("unexpected tmux args %v", args)
		return nil, nil
	})
	defer restore()

	prevExec := pollerExec
	t.Cleanup(func() { pollerExec = prevExec })
	pollerExec = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("dry-run should not execute %s %v", name, args)
		return nil, nil
	}

	restoreLevel := setLogLevelForTest(slog.LevelInfo)
	defer restoreLevel()

	_, stderr := captureStdoutAndStderr(t, func() {
		reconcileStaleState(ctx, true)
	})

	if len(fs.ended) != 0 {
		t.Fatalf("ended sessions = %#v, want none", fs.ended)
	}
	if got := fs.runs["cb-orphaned"].Status; got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	if !strings.Contains(stderr, `component=reconcile id=cb-orphaned kind=cancel_orphaned_session`) {
		t.Fatalf("missing dry-run reconcile log:\n%s", stderr)
	}
	if !strings.Contains(stderr, "session ps-1 is running but no tmux window exists") {
		t.Fatalf("missing reason text:\n%s", stderr)
	}
}

func TestReconcileStaleStateLeavesHealthyPipelineUntouched(t *testing.T) {
	ctx := context.Background()

	fc := newFakeConnector()
	fc.items["cb-healthy"] = &connector.WorkItem{ID: "cb-healthy", Type: "design", Status: "open", Project: "test-project"}

	fs := newFakeStore()
	fs.runs["cb-healthy"] = &store.PipelineRun{
		ID:           "run-healthy",
		DesignID:     "cb-healthy",
		Project:      "test-project",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:          "ps-healthy",
		DesignID:    "cb-healthy",
		PipelineID:  "run-healthy",
		Project:     "test-project",
		Status:      "running",
		TmuxSession: ptr("cobuild-test-project"),
		TmuxWindow:  ptr("cb-healthy"),
	}}

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			t.Fatalf("unexpected command %q", name)
		}
		if len(args) >= 2 && args[0] == "list-sessions" {
			return []byte("cobuild-test-project\n"), nil
		}
		if len(args) >= 3 && args[0] == "list-windows" && args[1] == "-t" && args[2] == "cobuild-test-project" {
			return []byte("@3\tcb-healthy\n"), nil
		}
		t.Fatalf("unexpected tmux args %v", args)
		return nil, nil
	})
	defer restore()

	prevExec := pollerExec
	t.Cleanup(func() { pollerExec = prevExec })
	pollerExec = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("healthy pipeline should not execute %s %v", name, args)
		return nil, nil
	}

	out := captureStdout(t, func() {
		reconcileStaleState(ctx, false)
	})

	if len(fs.ended) != 0 {
		t.Fatalf("ended sessions = %#v, want none", fs.ended)
	}
	if got := fs.runs["cb-healthy"].CurrentPhase; got != "implement" {
		t.Fatalf("phase = %q, want implement", got)
	}
	if strings.Contains(out, "[reconcile]") {
		t.Fatalf("healthy pipeline should not log reconcile actions:\n%s", out)
	}
}
