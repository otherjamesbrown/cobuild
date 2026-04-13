package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func (h *Harness) GetRun(ctx context.Context, designID string) (*store.PipelineRun, error) {
	return h.Store.GetRun(ctx, designID)
}

func (h *Harness) ListRunningSessions(ctx context.Context) ([]store.SessionRecord, error) {
	return h.Store.ListRunningSessions(ctx, h.Project)
}

func (h *Harness) ListTmuxWindows(ctx context.Context) ([]string, error) {
	windows, err := h.Tmux.ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(windows))
	for _, window := range windows {
		if window == "" || window == "bootstrap" {
			continue
		}
		filtered = append(filtered, window)
	}
	sort.Strings(filtered)
	return filtered, nil
}

func (h *Harness) CountMainCommitsByPrefix(ctx context.Context, prefix string) (int, []string, error) {
	out, err := h.RunCobuildGit(ctx, "log", "--format=%s", h.Repo.DefaultBranch)
	if err != nil {
		return 0, nil, err
	}
	var matches []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			matches = append(matches, line)
		}
	}
	return len(matches), matches, nil
}

func (h *Harness) RunCobuildGit(ctx context.Context, args ...string) (string, error) {
	cmd := h.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *Harness) SessionLogTail(taskID string, lines int) string {
	if lines <= 0 {
		lines = 20
	}
	candidates := []string{
		filepath.Join(h.Repo.Root, ".cobuild", "sessions", taskID, "dispatch.log"),
		filepath.Join(h.Repo.Root, ".cobuild", "sessions", taskID, "session.log"),
		filepath.Join(h.Repo.Root, ".cobuild", "sessions", taskID, "session.err"),
		filepath.Join(h.HomeDir, "worktrees", h.Project, taskID, ".cobuild", "dispatch.log"),
		filepath.Join(h.HomeDir, "worktrees", h.Project, taskID, ".cobuild", "session.log"),
		filepath.Join(h.HomeDir, "worktrees", h.Project, taskID, ".cobuild", "session.err"),
	}
	var parts []string
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:\n%s", filepath.Base(path), tailLines(string(data), lines)))
	}
	return strings.Join(parts, "\n\n")
}

func tailLines(body string, lines int) string {
	if lines <= 0 {
		lines = 20
	}
	split := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(split) <= lines {
		return strings.Join(split, "\n")
	}
	return strings.Join(split[len(split)-lines:], "\n")
}
