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

	var invalid []string
	for _, edge := range childEdges {
		item, err := cn.Get(ctx, edge.ItemID)
		if err != nil || item == nil || item.Type != "task" {
			continue
		}

		if _, err := resolveTaskTargetRepo(ctx, cn, item, item.Project); err != nil {
			repo, _ := cn.GetMetadata(ctx, item.ID, "repo")
			repo = strings.TrimSpace(repo)

			switch {
			case repo == "":
				invalid = append(invalid, fmt.Sprintf("%s (missing `repo` metadata)", item.ID))
			case repoMetadataLooksAmbiguous(repo):
				invalid = append(invalid, fmt.Sprintf("%s (ambiguous `repo` metadata: %q)", item.ID, repo))
			default:
				invalid = append(invalid, fmt.Sprintf("%s (%v)", item.ID, err))
			}
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
