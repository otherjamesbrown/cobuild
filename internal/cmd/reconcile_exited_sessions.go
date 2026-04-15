package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

var (
	reconcileExitedSessionsWriter = io.Writer(os.Stderr)
	reconcileExitedSessionsRun    = reconcileExitedSessions
	reconcileBranchExists         = func(ctx context.Context, repo, branch string) (bool, error) {
		if strings.TrimSpace(repo) == "" || strings.TrimSpace(branch) == "" {
			return false, nil
		}
		endpoint := fmt.Sprintf("repos/%s/branches/%s", repo, branch)
		out, err := execCommandCombinedOutput(ctx, "gh", "api", endpoint)
		if err != nil {
			trimmed := strings.TrimSpace(string(out))
			if strings.Contains(trimmed, "404") || strings.Contains(trimmed, "Not Found") {
				return false, nil
			}
			return false, fmt.Errorf("check branch %s on %s: %w", branch, repo, err)
		}
		return len(strings.TrimSpace(string(out))) > 0, nil
	}
	reconcilePRForBranch = func(ctx context.Context, repo, branch string) (*reconcilePRMatch, error) {
		openPR, err := reconcilePRListState(ctx, repo, branch, "open")
		if err != nil {
			return nil, err
		}
		if openPR != nil {
			return openPR, nil
		}

		mergedPR, err := reconcilePRListState(ctx, repo, branch, "merged")
		if err != nil {
			return nil, err
		}
		if mergedPR != nil {
			mergedPR.Merged = true
			return mergedPR, nil
		}

		return nil, nil
	}
)

type reconcilePRMatch struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Merged bool   `json:"-"`
}

func reconcileExitedSessions(ctx context.Context) {
	if cbStore == nil || conn == nil {
		return
	}

	runs, err := cbStore.ListRuns(ctx, "")
	if err != nil {
		fmt.Fprintf(reconcileExitedSessionsWriter, "[reconcile] exited sessions: list runs: %v\n", err)
		return
	}

	for _, run := range runs {
		if !reconcileRunNeedsExitedSessionCheck(run) {
			continue
		}
		if err := reconcileExitedSessionRun(ctx, run); err != nil {
			fmt.Fprintf(reconcileExitedSessionsWriter, "[reconcile] exited sessions: %s: %v\n", run.DesignID, err)
		}
	}
}

func reconcileRunNeedsExitedSessionCheck(run store.PipelineRunStatus) bool {
	if run.Status != "active" && run.Status != domain.StatusInProgress {
		return false
	}
	return run.Phase == domain.PhaseImplement || run.Phase == domain.PhaseFix
}

func reconcileExitedSessionRun(ctx context.Context, run store.PipelineRunStatus) error {
	item, err := conn.Get(ctx, run.DesignID)
	if err != nil || item == nil {
		return err
	}
	if !reconcileWorkItemEligible(item) {
		return nil
	}

	session, err := cbStore.GetSession(ctx, run.DesignID)
	if err != nil || session == nil || session.EndedAt == nil {
		return err
	}

	repo := reconcileRepoForRun(ctx, run, item, session)
	if repo == "" {
		return nil
	}

	branchExists, err := reconcileBranchExists(ctx, repo, run.DesignID)
	if err != nil {
		return err
	}
	if !branchExists {
		return nil
	}

	pr, err := reconcilePRForBranch(ctx, repo, run.DesignID)
	if err != nil {
		return err
	}
	if pr == nil || pr.Merged {
		return nil
	}

	if pr.URL != "" {
		_ = conn.SetMetadata(ctx, item.ID, domain.MetaPRURL, pr.URL)
	}

	if err := conn.UpdateStatus(ctx, item.ID, domain.StatusNeedsReview); err != nil {
		return fmt.Errorf("update status to %s: %w", domain.StatusNeedsReview, err)
	}
	syncPipelineTaskStatus(ctx, item.ID, domain.StatusNeedsReview)

	note := reconcileExitedSessionNote(*session, pr)
	if err := conn.AppendContent(ctx, item.ID, note); err != nil {
		fmt.Fprintf(reconcileExitedSessionsWriter, "[reconcile] exited sessions: %s append note failed: %v\n", item.ID, err)
	}

	fmt.Fprintf(
		reconcileExitedSessionsWriter,
		"Reconciled: %s advanced to needs-review (PR #%d exists, session exited at %s)\n",
		item.ID,
		pr.Number,
		session.EndedAt.UTC().Format(time.RFC3339),
	)
	return nil
}

func reconcileWorkItemEligible(item *connector.WorkItem) bool {
	if item == nil {
		return false
	}
	if item.Type != domain.WorkItemTypeTask && item.Type != domain.WorkItemTypeBug {
		return false
	}
	return item.Status != domain.StatusNeedsReview && item.Status != "closed"
}

func reconcileRepoForRun(ctx context.Context, run store.PipelineRunStatus, item *connector.WorkItem, session *store.SessionRecord) string {
	if repo := strings.TrimSpace(metadataString(item.Metadata, domain.MetaRepo)); repo != "" {
		return repo
	}
	if session != nil && session.WorktreePath != nil {
		if repo := detectGitHubRepoFromWorktree(ctx, strings.TrimSpace(*session.WorktreePath)); repo != "" {
			return repo
		}
	}
	repoRoot, err := config.RepoForProject(run.Project)
	if err != nil || repoRoot == "" {
		return ""
	}
	pCfg, err := config.LoadConfig(repoRoot)
	if err != nil || pCfg == nil {
		return ""
	}
	return strings.TrimSpace(pCfg.GitHub.OwnerRepo)
}

func reconcilePRListState(ctx context.Context, repo, branch, state string) (*reconcilePRMatch, error) {
	out, err := execCommandOutput(
		ctx,
		"gh",
		"pr",
		"list",
		"--repo", repo,
		"--head", branch,
		"--state", state,
		"--json", "number,url",
	)
	if err != nil {
		return nil, fmt.Errorf("list %s PRs for %s on %s: %w", state, branch, repo, err)
	}

	var prs []reconcilePRMatch
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("decode %s PR list for %s on %s: %w", state, branch, repo, err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

func reconcileExitedSessionNote(session store.SessionRecord, pr *reconcilePRMatch) string {
	exitTime := ""
	if session.EndedAt != nil {
		exitTime = session.EndedAt.UTC().Format(time.RFC3339)
	}

	return fmt.Sprintf(`
---
*Appended by CoBuild reconciler at %s*

## Reconciliation evidence

Most recent pipeline session exited without advancing this shard to "%s".

- Session ID: %s
- Session exit time: %s
- Open PR: %s

CoBuild advanced this shard to "%s" as a fallback because the Stop hook did not complete the normal handoff.
`, time.Now().UTC().Format(time.RFC3339), domain.StatusNeedsReview, session.ID, exitTime, pr.URL, domain.StatusNeedsReview)
}
