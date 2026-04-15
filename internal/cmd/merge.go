package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

var mergeWorktreeRemove = worktree.Remove

var mergeCmd = &cobra.Command{
	Use:   "merge <task-id>",
	Short: "Merge an approved task PR and close the task",
	Long: `Merges the PR for a task that has been reviewed and approved.
After merging, marks the task as closed and cleans up the worktree.
If all tasks for the parent design are closed, advances to the done phase.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		// Get the task
		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}

		// Get PR URL from metadata
		prURL := ""
		if task.Metadata != nil {
			if pr, ok := task.Metadata[domain.MetaPRURL]; ok {
				prURL = fmt.Sprintf("%v", pr)
			}
		}
		if prURL == "" {
			// Try to find PR from branch name
			branch := taskID // convention: branch name = task ID
			out, err := execCommandOutput(ctx, "gh", "pr", "list", "--head", branch, "--json", "url", "--jq", ".[0].url")
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				prURL = strings.TrimSpace(string(out))
			}
		}

		if prURL == "" {
			return fmt.Errorf("no PR found for task %s — check that the agent created a PR", taskID)
		}

		if dryRun {
			fmt.Printf("[dry-run] Would merge PR: %s\n", prURL)
			fmt.Printf("[dry-run] Would close task: %s\n", taskID)
			return nil
		}

		// Check PR status
		out, err := execCommandOutput(ctx, "gh", "pr", "view", prURL, "--json", "state,reviewDecision,mergeable", "--jq", "[.state, .reviewDecision, .mergeable] | join(\",\")")
		if err != nil {
			return fmt.Errorf("check PR status: %w", err)
		}
		parts := strings.Split(strings.TrimSpace(string(out)), ",")
		if len(parts) >= 1 && parts[0] != "OPEN" {
			return fmt.Errorf("PR is not open (state: %s)", parts[0])
		}

		if err := remoteMerge(ctx, prURL); err != nil {
			return err
		}
		fmt.Printf("  Merged.\n")

		// Close the task
		if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
			fmt.Printf("  Warning: failed to close task: %v\n", err)
		} else {
			fmt.Printf("  Task %s → closed.\n", taskID)
		}
		syncPipelineTaskStatus(ctx, taskID, "closed")

		if err := localCleanup(ctx, taskID); err != nil {
			fmt.Printf("  Warning: merge succeeded, but local cleanup failed: %v\n", err)
			fmt.Printf("  Retry cleanup separately later if needed.\n")
		}

		if err := handlePostCloseProgress(ctx, taskID); err != nil {
			return err
		}

		printNextStep(taskID, domain.PhaseReview, domain.ActionMerge)
		return nil
	},
}

func remoteMerge(ctx context.Context, prURL string) error {
	fmt.Printf("Merging %s...\n", prURL)
	mergeOut, err := execCommandCombinedOutput(ctx, "gh", "pr", "merge", prURL, "--squash", "--delete-branch")
	if err != nil {
		return fmt.Errorf("merge failed: %w\n%s", err, string(mergeOut))
	}
	return nil
}

func localCleanup(ctx context.Context, taskID string) error {
	wtPath, _ := conn.GetMetadata(ctx, taskID, domain.MetaWorktreePath)
	if wtPath == "" {
		return nil
	}

	archiveSessionLogs(wtPath, taskID)

	repoForCleanup, _ := config.RepoForProject(projectName)
	if err := mergeWorktreeRemove(ctx, repoForCleanup, wtPath, taskID); err != nil {
		return err
	}

	fmt.Printf("  Worktree cleaned up.\n")
	return nil
}

// archiveSessionLogs copies session logs from a worktree to .cobuild/sessions/<task-id>/
// before the worktree is deleted. This preserves logs for retrospectives.
func archiveSessionLogs(wtPath, taskID string) {
	repoRoot := findRepoRoot()
	archiveDir := filepath.Join(repoRoot, ".cobuild", "sessions", taskID)

	cobuildDir := filepath.Join(wtPath, ".cobuild")
	entries, err := os.ReadDir(cobuildDir)
	if err != nil {
		return // no .cobuild dir in worktree
	}

	os.MkdirAll(archiveDir, 0755)
	archived := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(cobuildDir, e.Name())
		dst := filepath.Join(archiveDir, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			continue
		}
		archived++
	}
	if archived > 0 {
		fmt.Printf("  Archived %d session log(s) to %s\n", archived, archiveDir)
	}
}

func init() {
	mergeCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	rootCmd.AddCommand(mergeCmd)
}
