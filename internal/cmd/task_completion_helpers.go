package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
)

type directCompletionDecision struct {
	direct bool
	reason string
}

type completionPathDecision struct {
	Direct bool
	Note   string
}

func detectDirectCompletion(ctx context.Context, task *connector.WorkItem, worktreePath string) (directCompletionDecision, error) {
	if task != nil && task.Metadata != nil {
		if mode := metadataString(task.Metadata, "completion_mode"); mode == "direct" {
			return directCompletionDecision{direct: true, reason: "completion_mode=direct"}, nil
		}
	}

	prURL := ""
	if conn != nil && task != nil {
		prURL, _ = conn.GetMetadata(ctx, task.ID, "pr_url")
	}
	if prURL != "" {
		return directCompletionDecision{}, nil
	}

	if worktreePath == "" {
		if task != nil && task.Metadata != nil {
			if repo := metadataString(task.Metadata, "repo"); repo == "" {
				return directCompletionDecision{direct: true, reason: "no repo metadata and no worktree"}, nil
			}
		}
		return directCompletionDecision{}, nil
	}

	statusOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain", "--", ".", ":!.cobuild", ":!CLAUDE.md").Output()
	if err != nil {
		return directCompletionDecision{}, err
	}
	if strings.TrimSpace(string(statusOut)) == "" {
		return directCompletionDecision{direct: true, reason: "git worktree has no tracked changes"}, nil
	}
	return directCompletionDecision{}, nil
}

func determineCompletionPath(ctx context.Context, task *connector.WorkItem, _ string, worktreePath, _ string) (completionPathDecision, error) {
	decision, err := detectDirectCompletion(ctx, task, worktreePath)
	if err != nil {
		return completionPathDecision{}, err
	}
	return completionPathDecision{Direct: decision.direct, Note: decision.reason}, nil
}

func completeDirectTask(ctx context.Context, taskID, worktreePath, reason string, pCfg ...*config.Config) error {
	fmt.Printf("Direct completion for %s: %s\n", taskID, reason)

	bodyReason := reason
	if reason == "git worktree has no tracked changes" {
		bodyReason = "no repo changes detected in worktree"
	}
	body := fmt.Sprintf("## Auto-completion evidence\n\nDirect close: %s\nPR: none\n", bodyReason)
	if conn != nil {
		if err := conn.AppendContent(ctx, taskID, body); err != nil {
			fmt.Printf("Warning: failed to append evidence: %v\n", err)
		}
	}

	if cbStore != nil {
		var cfg *config.Config
		if len(pCfg) > 0 {
			cfg = pCfg[0]
		}
		if _, err := RecordGateVerdict(ctx, conn, cbStore, taskID, "review", "pass", directReviewPassBody, 0, cfg); err != nil {
			fmt.Printf("Warning: failed to record direct completion gate: %v\n", err)
		}
	}

	endTaskSession(ctx, taskID, worktreePath, store.SessionResult{
		ExitCode:       0,
		FilesChanged:   0,
		Commits:        0,
		Status:         "completed",
		CompletionNote: reason,
	})

	cleanupTaskWorktree(ctx, taskID, worktreePath)
	closeTaskAndAdvance(ctx, taskID)

	fmt.Printf("Task %s complete -> closed (direct)\n", taskID)
	printNextStep(taskID, "done", "complete")
	return nil
}

func maybeProcessDirectReview(ctx context.Context, taskID string, task *connector.WorkItem, dryRun bool) (bool, error) {
	worktreePath := ""
	if conn != nil {
		worktreePath, _ = conn.GetMetadata(ctx, taskID, "worktree_path")
	}

	decision, err := detectDirectCompletion(ctx, task, worktreePath)
	if err != nil {
		return false, err
	}
	if !decision.direct {
		return false, nil
	}

	if task != nil && task.Status == "closed" {
		fmt.Printf("Task %s already closed directly, no PR review required.\n", taskID)
		if err := handlePostCloseProgress(ctx, taskID); err != nil {
			return true, err
		}
		return true, nil
	}

	if dryRun {
		fmt.Printf("[dry-run] Would close %s directly without PR review.\n", taskID)
		return true, nil
	}

	fmt.Printf("No PR for %s. Closing via direct-review path.\n", taskID)
	if cbStore != nil {
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if _, err := RecordGateVerdict(ctx, conn, cbStore, taskID, "review", "pass", directReviewPassBody, 0, pCfg); err != nil {
			fmt.Printf("Warning: failed to record direct review gate: %v\n", err)
		}
	}

	closeTaskAndAdvance(ctx, taskID)
	cleanupTaskWorktree(ctx, taskID, worktreePath)
	printNextStep(taskID, "merged", "process-review")
	return true, nil
}

func closeTaskAndAdvance(ctx context.Context, taskID string) {
	if conn != nil {
		if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
			fmt.Printf("Warning: failed to close task: %v\n", err)
		} else {
			fmt.Printf("  Task %s -> closed.\n", taskID)
		}
	}

	if cbStore != nil {
		if err := cbStore.UpdateTaskStatus(ctx, taskID, "closed"); err != nil {
			fmt.Printf("Warning: failed to update pipeline task status: %v\n", err)
		}
		// Advance task's own pipeline run to done (if it has one).
		// Use AdvancePhase so concurrent closures don't stomp each other.
		if run, err := cbStore.GetRun(ctx, taskID); err == nil {
			repoRoot, _ := config.RepoForProject(projectName)
			pCfg, _ := config.LoadConfig(repoRoot)
			advanceDesignToCompleted(ctx, cbStore, conn, pCfg, taskID, run.CurrentPhase)
		}
	}

	if conn == nil {
		return
	}
	edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil || len(edges) == 0 {
		return
	}
	designID := edges[0].ItemID
	siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return
	}
	for _, s := range siblings {
		if s.Status != "closed" {
			return
		}
	}
	if cbStore != nil {
		fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
		if run, err := cbStore.GetRun(ctx, designID); err == nil {
			repoRoot, _ := config.RepoForProject(projectName)
			pCfg, _ := config.LoadConfig(repoRoot)
			advanceDesignToCompleted(ctx, cbStore, conn, pCfg, designID, run.CurrentPhase)
		} else {
			fmt.Printf("  Warning: no pipeline run for %s: %v\n", designID, err)
		}
	}
}

func endTaskSession(ctx context.Context, taskID, worktreePath string, result store.SessionResult) {
	if cbStore == nil || conn == nil {
		return
	}
	sessionID, _ := conn.GetMetadata(ctx, taskID, "session_id")
	if sessionID == "" {
		return
	}
	if worktreePath != "" {
		logData, err := os.ReadFile(filepath.Join(worktreePath, ".cobuild", "session.log"))
		if err == nil {
			result.SessionLog = string(logData)
		}
	}
	if err := cbStore.EndSession(ctx, sessionID, result); err != nil {
		fmt.Printf("Warning: failed to end session: %v\n", err)
	}
}

func cleanupTaskWorktree(ctx context.Context, taskID, worktreePath string) {
	if worktreePath == "" {
		return
	}
	archiveSessionLogs(worktreePath, taskID)
	repoForCleanup, err := config.RepoForProject(projectName)
	if err != nil || repoForCleanup == "" {
		if err := os.RemoveAll(worktreePath); err != nil {
			fmt.Printf("Warning: failed to remove worktree: %v\n", err)
		}
		return
	}
	if err := worktree.Remove(ctx, repoForCleanup, worktreePath, taskID); err != nil {
		fmt.Printf("Warning: failed to remove worktree: %v\n", err)
	}
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	v, ok := metadata[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
