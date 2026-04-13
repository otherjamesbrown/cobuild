package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func validateSingleRepoChildTasks(ctx context.Context, cn connector.Connector, designID string) error {
	if cn == nil {
		return nil
	}

	childEdges, err := cn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return fmt.Errorf("list child tasks for %s: %w", designID, err)
	}

	// Skip closed tasks — their work won't be dispatched, so they don't need
	// repo metadata (cb-1666b2). Check remaining tasks; auto-fill repo
	// metadata for missing cases when the task's project has exactly one
	// registered repo (cb-4ef390 — decompose agents frequently forget this
	// step even though the skill prompt instructs them to set it).
	var invalid []string
	for _, edge := range childEdges {
		item, err := cn.Get(ctx, edge.ItemID)
		if err != nil || item == nil || item.Type != "task" {
			continue
		}
		if item.Status == "closed" {
			continue
		}

		repo, _ := cn.GetMetadata(ctx, item.ID, "repo")
		repo = strings.TrimSpace(repo)

		switch {
		case repo == "":
			// Auto-fill from project if unambiguous. For a single-repo
			// project the answer is determined — no point failing the gate
			// over a metadata call the agent should have made.
			if autoRepo := inferSingleRepoForTask(item); autoRepo != "" {
				if err := cn.SetMetadata(ctx, item.ID, "repo", autoRepo); err != nil {
					invalid = append(invalid, fmt.Sprintf("%s (repo missing, auto-fill failed: %v)", item.ID, err))
				} else {
					fmt.Printf("  auto-set repo=%s on %s\n", autoRepo, item.ID)
				}
				continue
			}
			invalid = append(invalid, fmt.Sprintf("%s (missing `repo` metadata)", item.ID))
		case repoMetadataLooksAmbiguous(repo):
			invalid = append(invalid, fmt.Sprintf("%s (ambiguous `repo` metadata: %q)", item.ID, repo))
		}
	}

	if len(invalid) == 0 {
		return nil
	}

	return fmt.Errorf(
		"decomposition-review failed: child tasks must target exactly one repo; split cross-repo work into separate tasks and set `repo` metadata explicitly:\n  %s",
		strings.Join(invalid, "\n  "),
	)
}

// inferSingleRepoForTask returns the registered repo for a task's project
// when the project maps to exactly one repo. Returns "" if the project is
// unknown, empty, or maps to 0 or 2+ repos (in which case the agent must
// set repo metadata explicitly).
func inferSingleRepoForTask(task *connector.WorkItem) string {
	if task == nil {
		return ""
	}
	project := strings.TrimSpace(task.Project)
	if project == "" {
		return ""
	}
	reg, err := config.LoadRepoRegistry()
	if err != nil {
		return ""
	}
	repos := reposForProject(reg, project)
	if len(repos) == 1 {
		return repos[0]
	}
	return ""
}

func dispatchRepoTargetError(ctx context.Context, cn connector.Connector, taskID string) error {
	if cn == nil {
		return nil
	}

	parentEdges, err := cn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil || len(parentEdges) == 0 {
		return nil
	}

	parentID := parentEdges[0].ItemID
	parentItem, err := cn.Get(ctx, parentID)
	if err != nil || parentItem == nil {
		return nil
	}

	repos, err := referencedRepos(parentItem.Content)
	if err != nil || len(repos) < 2 {
		return nil
	}

	return fmt.Errorf(
		"task %s has no `repo` metadata, and parent design %s references multiple repos (%s); set `repo` metadata on this task before dispatching",
		taskID,
		parentID,
		strings.Join(repos, ", "),
	)
}

func referencedRepos(content string) ([]string, error) {
	reg, err := config.LoadRepoRegistry()
	if err != nil {
		return nil, err
	}

	contentLower := strings.ToLower(content)
	var repos []string
	for name := range reg.Repos {
		if strings.Contains(contentLower, strings.ToLower(name)) {
			repos = append(repos, name)
		}
	}

	return repos, nil
}

func repoMetadataLooksAmbiguous(repo string) bool {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return false
	}
	if strings.ContainsAny(repo, ",[]") {
		return true
	}
	return len(strings.Fields(repo)) > 1
}
