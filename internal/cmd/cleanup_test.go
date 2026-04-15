package cmd

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestCleanupTaskResourcesRemovesBranchWorktreeAndTmux(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-cleanup"
	worktreePath := newTestWorktree(t, taskID)
	repoRoot := repoRootForWorktree(ctx, worktreePath)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     taskID,
		Title:  taskID,
		Type:   "task",
		Status: "closed",
		Metadata: map[string]any{
			domain.MetaWorktreePath: worktreePath,
			domain.MetaTmuxWindow:   taskID,
		},
	})
	fs := newFakeStore()
	tmuxSession := "cobuild-test"
	tmuxWindow := taskID
	fs.sessions = []store.SessionRecord{{
		ID:           "ps-1",
		TaskID:       taskID,
		Status:       "completed",
		WorktreePath: strPtr(worktreePath),
		TmuxSession:  &tmuxSession,
		TmuxWindow:   &tmuxWindow,
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevRunner := cleanupCommandRunner
	prevLoader := cleanupConfigLoader
	prevWriter := cleanupLogWriter
	t.Cleanup(func() {
		cleanupCommandRunner = prevRunner
		cleanupConfigLoader = prevLoader
		cleanupLogWriter = prevWriter
	})

	var tmuxCalls []string
	cleanupLogWriter = ioDiscard{}
	cleanupConfigLoader = func(string) *config.Config {
		return config.DefaultConfig()
	}
	cleanupCommandRunner = func(ctx context.Context, pCfg *config.Config, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			tmuxCalls = append(tmuxCalls, strings.Join(append([]string{name}, args...), " "))
			return []byte("killed"), nil
		}
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}

	if err := cleanupTaskResources(ctx, taskID); err != nil {
		t.Fatalf("cleanupTaskResources() error = %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after cleanup: stat err = %v", err)
	}
	if out := runGit(t, repoRoot, "branch", "--list", taskID); strings.TrimSpace(out) != "" {
		t.Fatalf("branch %s still exists after cleanup: %q", taskID, out)
	}
	if len(tmuxCalls) != 1 || tmuxCalls[0] != "tmux kill-window -t cobuild-test:"+taskID {
		t.Fatalf("tmux calls = %v, want kill-window for %s", tmuxCalls, taskID)
	}
	if got := fc.metadata[taskID][domain.MetaWorktreePath]; got != "" {
		t.Fatalf("worktree metadata = %q, want cleared", got)
	}
	if got := fc.metadata[taskID][domain.MetaTmuxWindow]; got != "" {
		t.Fatalf("tmux metadata = %q, want cleared", got)
	}
}

func TestCleanupCommandOrphanedDryRunListsCandidates(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-orphaned"
	worktreePath := newTestWorktree(t, taskID)
	repoRoot := repoRootForWorktree(ctx, worktreePath)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     taskID,
		Title:  taskID,
		Type:   "task",
		Status: "open",
		Metadata: map[string]any{
			domain.MetaWorktreePath: worktreePath,
		},
	})
	fs := newFakeStore()

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	withWorkingDir(t, repoRoot, func() {
		cleanupCmd.Flags().Set("orphaned", "true")
		cleanupCmd.Flags().Set("dry-run", "true")
		defer cleanupCmd.Flags().Set("orphaned", "false")
		defer cleanupCmd.Flags().Set("dry-run", "false")

		out, err := runCommandWithOutputs(t, cleanupCmd, nil)
		if err != nil {
			t.Fatalf("cleanup --orphaned --dry-run returned error: %v", err)
		}
		if !strings.Contains(out, "[dry-run] Would clean "+taskID+": task has no pipeline run") {
			t.Fatalf("dry-run output missing orphaned reason:\n%s", out)
		}
	})

	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree should still exist after dry-run: %v", err)
	}
}

func TestCleanupCommandOlderThanDryRunFiltersByClosedAge(t *testing.T) {
	ctx := context.Background()
	oldTaskID := "cb-old"
	newTaskID := "cb-new"
	oldWorktree := newTestWorktree(t, oldTaskID)
	newWorktree := newTestWorktree(t, newTaskID)
	repoRoot := repoRootForWorktree(ctx, oldWorktree)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:        oldTaskID,
		Title:     oldTaskID,
		Type:      "task",
		Status:    "closed",
		UpdatedAt: time.Now().Add(-72 * time.Hour),
		Metadata: map[string]any{
			domain.MetaWorktreePath: oldWorktree,
		},
	})
	fc.addItem(&connector.WorkItem{
		ID:        newTaskID,
		Title:     newTaskID,
		Type:      "task",
		Status:    "closed",
		UpdatedAt: time.Now().Add(-2 * time.Hour),
		Metadata: map[string]any{
			domain.MetaWorktreePath: newWorktree,
		},
	})

	restore := installTestGlobals(t, fc, newFakeStore(), "test-project")
	defer restore()

	withWorkingDir(t, repoRoot, func() {
		cleanupCmd.Flags().Set("older-than", "24h")
		cleanupCmd.Flags().Set("dry-run", "true")
		defer cleanupCmd.Flags().Set("older-than", "")
		defer cleanupCmd.Flags().Set("dry-run", "false")

		out, err := runCommandWithOutputs(t, cleanupCmd, nil)
		if err != nil {
			t.Fatalf("cleanup --older-than returned error: %v", err)
		}
		if !strings.Contains(out, "[dry-run] Would clean "+oldTaskID) {
			t.Fatalf("dry-run output missing old task:\n%s", out)
		}
		if strings.Contains(out, "[dry-run] Would clean "+newTaskID) {
			t.Fatalf("dry-run output should not include new task:\n%s", out)
		}
	})
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd %s: %v", wd, err)
		}
	}()
	fn()
}
