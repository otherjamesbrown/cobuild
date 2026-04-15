package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var rebaseWarningWriter io.Writer = os.Stderr

type siblingRebaseSummary struct {
	Eligible  int
	Rebased   int
	Conflicts int
	Failures  int
}

var rebaseCmd = &cobra.Command{
	Use:   "rebase <task-id>",
	Short: "Rebase open sibling PRs for a task's design",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		taskID := args[0]
		summary, designID, err := rebaseSiblingsForTask(ctx, taskID, rebaseWarnf)
		if err != nil {
			return err
		}
		printSiblingRebaseSummary(taskID, designID, summary)
		return nil
	},
}

var rebaseSiblingsCmd = &cobra.Command{
	Use:   "rebase-siblings <design-id>",
	Short: "Rebase open PR tasks for a design onto origin/main",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		designID := args[0]
		summary, err := rebaseOpenSiblingPRs(ctx, designID, "", rebaseWarnf)
		if err != nil {
			return err
		}
		printSiblingRebaseSummary("", designID, summary)
		return nil
	},
}

func rebaseWarnf(format string, args ...any) {
	fmt.Fprintf(rebaseWarningWriter, format, args...)
}

func onMergeSuccess(
	ctx context.Context,
	taskID string,
	cleanupFn func(context.Context, string) error,
	cleanupWarnf func(string, error),
	warnf func(string, ...any),
) {
	if cleanupAutoOnMergeEnabled() {
		if err := cleanupFn(ctx, taskID); err != nil {
			cleanupWarnf(taskID, err)
		}
	} else {
		warnf("cleanup auto_on_merge=false; skipping automatic local cleanup for %s\n", taskID)
	}

	if cbStore == nil || conn == nil {
		return
	}

	if _, _, err := rebaseSiblingsForTask(ctx, taskID, warnf); err != nil {
		warnf("Warning: sibling rebase skipped for %s: %v\n", taskID, err)
	}
}

func rebaseSiblingsForTask(ctx context.Context, taskID string, warnf func(string, ...any)) (siblingRebaseSummary, string, error) {
	designID, err := designIDForTask(ctx, taskID)
	if err != nil {
		return siblingRebaseSummary{}, "", err
	}
	summary, err := rebaseOpenSiblingPRs(ctx, designID, taskID, warnf)
	return summary, designID, err
}

func designIDForTask(ctx context.Context, taskID string) (string, error) {
	if cbStore == nil {
		return "", fmt.Errorf("no store configured (need database connection)")
	}

	task, err := cbStore.GetTaskByShardID(ctx, taskID)
	if err == nil && task != nil && strings.TrimSpace(task.DesignID) != "" {
		return task.DesignID, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("get pipeline task %s: %w", taskID, err)
	}

	designID, parentErr := parentDesignID(ctx, taskID)
	if parentErr != nil {
		return "", parentErr
	}
	if strings.TrimSpace(designID) == "" {
		return "", fmt.Errorf("could not resolve design for task %s", taskID)
	}
	return designID, nil
}

func rebaseOpenSiblingPRs(ctx context.Context, designID, excludeTaskID string, warnf func(string, ...any)) (siblingRebaseSummary, error) {
	if cbStore == nil {
		return siblingRebaseSummary{}, fmt.Errorf("no store configured (need database connection)")
	}
	if conn == nil {
		return siblingRebaseSummary{}, fmt.Errorf("no connector configured")
	}

	tasks, err := cbStore.ListTasksByDesign(ctx, designID)
	if err != nil {
		return siblingRebaseSummary{}, fmt.Errorf("list pipeline tasks for %s: %w", designID, err)
	}

	var summary siblingRebaseSummary
	for _, task := range tasks {
		if task.TaskShardID == excludeTaskID || !taskHasOpenPRStatus(task.Status) {
			continue
		}

		worktreePath, err := conn.GetMetadata(ctx, task.TaskShardID, domain.MetaWorktreePath)
		if err != nil {
			summary.Failures++
			warnf("Warning: sibling %s missing worktree metadata: %v\n", task.TaskShardID, err)
			continue
		}
		worktreePath = strings.TrimSpace(worktreePath)
		if worktreePath == "" {
			summary.Failures++
			warnf("Warning: sibling %s has no worktree path; skipping rebase.\n", task.TaskShardID)
			continue
		}

		branch, err := currentBranchInWorktree(ctx, worktreePath)
		if err != nil {
			summary.Failures++
			warnf("Warning: sibling %s branch lookup failed: %v\n", task.TaskShardID, err)
			continue
		}
		if branch == "" || branch == "main" {
			summary.Failures++
			warnf("Warning: sibling %s is not on a rebasable branch; skipping.\n", task.TaskShardID)
			continue
		}

		summary.Eligible++
		if err := rebaseTaskWorktreeOntoMain(ctx, task.TaskShardID, worktreePath, warnf); err != nil {
			if errors.Is(err, errSiblingRebaseConflict) {
				summary.Conflicts++
				continue
			}
			summary.Failures++
			warnf("Warning: sibling %s rebase failed: %v\n", task.TaskShardID, err)
			continue
		}
		summary.Rebased++
	}

	return summary, nil
}

func taskHasOpenPRStatus(status string) bool {
	switch status {
	case domain.StatusNeedsReview, domain.StatusInProgress:
		return true
	default:
		return false
	}
}

func currentBranchInWorktree(ctx context.Context, worktreePath string) (string, error) {
	out, err := execCommandOutput(ctx, "git", "-C", worktreePath, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var errSiblingRebaseConflict = errors.New("sibling rebase conflict")

func rebaseTaskWorktreeOntoMain(ctx context.Context, taskID, worktreePath string, warnf func(string, ...any)) error {
	if _, err := execCommandCombinedOutput(ctx, "git", "-C", worktreePath, "fetch", "origin", "main"); err != nil {
		return err
	}

	if _, err := execCommandCombinedOutput(ctx, "git", "-C", worktreePath, "rebase", "origin/main"); err != nil {
		_, _ = execCommandCombinedOutput(ctx, "git", "-C", worktreePath, "rebase", "--abort")
		if cbStore != nil {
			if updateErr := cbStore.UpdateTaskRebaseStatus(ctx, taskID, domain.RebaseStatusConflict); updateErr != nil {
				warnf("Warning: failed to record rebase conflict for %s: %v\n", taskID, updateErr)
			}
		}
		warnf("Sibling %s has rebase conflict with main; manual resolution needed.\n", taskID)
		return errSiblingRebaseConflict
	}

	if _, err := execCommandCombinedOutput(ctx, "git", "-C", worktreePath, "push", "--force-with-lease"); err != nil {
		return err
	}

	if cbStore != nil {
		if err := cbStore.UpdateTaskRebaseStatus(ctx, taskID, domain.RebaseStatusRebased); err != nil {
			warnf("Warning: failed to record rebased status for %s: %v\n", taskID, err)
		}
	}
	return nil
}

func printSiblingRebaseSummary(taskID, designID string, summary siblingRebaseSummary) {
	if summary.Eligible == 0 {
		if taskID != "" {
			fmt.Printf("No open sibling PRs to rebase for %s.\n", taskID)
			return
		}
		fmt.Printf("No open PR tasks to rebase for %s.\n", designID)
		return
	}

	fmt.Printf(
		"Sibling rebase complete for %s: %d rebased, %d conflict(s), %d warning(s).\n",
		designID,
		summary.Rebased,
		summary.Conflicts,
		summary.Failures,
	)
}

func init() {
	rootCmd.AddCommand(rebaseCmd)
	rootCmd.AddCommand(rebaseSiblingsCmd)
}
