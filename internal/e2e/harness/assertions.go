package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func (h *Harness) GetRun(ctx context.Context, designID string) (*store.PipelineRun, error) {
	return h.Store.GetRun(ctx, designID)
}

func (h *Harness) ListRunningSessions(ctx context.Context) ([]store.SessionRecord, error) {
	return h.Store.ListRunningSessions(ctx, "")
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
	}
	for project := range h.Repos {
		candidates = append(candidates,
			filepath.Join(h.HomeDir, "worktrees", project, taskID, ".cobuild", "dispatch.log"),
			filepath.Join(h.HomeDir, "worktrees", project, taskID, ".cobuild", "session.log"),
			filepath.Join(h.HomeDir, "worktrees", project, taskID, ".cobuild", "session.err"),
		)
	}
	var parts []string
	seen := map[string]struct{}{}
	for _, path := range candidates {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:\n%s", filepath.Base(path), tailLines(string(data), lines)))
	}
	return strings.Join(parts, "\n\n")
}

func (h *Harness) GetWorkItem(id string) (*connector.WorkItem, error) {
	var out *connector.WorkItem
	err := h.withFakeState(false, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		cloned := cloneWorkItem(item)
		cloned.Edges = append(cloned.Edges, cloneEdges(state.Incoming[id])...)
		cloned.Edges = append(cloned.Edges, cloneEdges(state.Outgoing[id])...)
		out = &cloned
		return nil
	})
	return out, err
}

func (h *Harness) SetWorkItemProject(id, project string) error {
	return h.withFakeState(true, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		item.Project = project
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		item.Metadata["project"] = project
		if _, ok := item.Metadata["repo"]; !ok {
			item.Metadata["repo"] = project
		}
		state.Items[id] = item
		return nil
	})
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
