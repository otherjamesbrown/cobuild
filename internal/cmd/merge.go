package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

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
			if pr, ok := task.Metadata["pr_url"]; ok {
				prURL = fmt.Sprintf("%v", pr)
			}
		}
		if prURL == "" {
			// Try to find PR from branch name
			branch := taskID // convention: branch name = task ID
			out, err := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch, "--json", "url", "--jq", ".[0].url").Output()
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
		out, err := exec.CommandContext(ctx, "gh", "pr", "view", prURL, "--json", "state,reviewDecision,mergeable", "--jq", "[.state, .reviewDecision, .mergeable] | join(\",\")").Output()
		if err != nil {
			return fmt.Errorf("check PR status: %w", err)
		}
		parts := strings.Split(strings.TrimSpace(string(out)), ",")
		if len(parts) >= 1 && parts[0] != "OPEN" {
			return fmt.Errorf("PR is not open (state: %s)", parts[0])
		}

		// Merge
		fmt.Printf("Merging %s...\n", prURL)
		mergeOut, err := exec.CommandContext(ctx, "gh", "pr", "merge", prURL, "--squash", "--delete-branch").CombinedOutput()
		if err != nil {
			return fmt.Errorf("merge failed: %s\n%s", err, string(mergeOut))
		}
		fmt.Printf("  Merged.\n")

		// Close the task
		if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
			fmt.Printf("  Warning: failed to close task: %v\n", err)
		} else {
			fmt.Printf("  Task %s → closed.\n", taskID)
		}

		// Clean up worktree
		if cbClient != nil {
			if err := cbClient.RemoveWorktree(ctx, taskID); err != nil {
				fmt.Printf("  Warning: failed to remove worktree: %v\n", err)
			} else {
				fmt.Printf("  Worktree cleaned up.\n")
			}
		}

		// Check if all sibling tasks are done → advance to done phase
		edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
		if err == nil && len(edges) > 0 {
			designID := edges[0].ItemID
			siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
			if err == nil {
				allDone := true
				for _, s := range siblings {
					if s.Status != "closed" {
						allDone = false
						break
					}
				}
				if allDone {
					fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
					if cbClient != nil {
						_ = cbClient.UpdatePipelineRunPhase(ctx, designID, "done")
					}
				}
			}
		}

		return nil
	},
}

func init() {
	mergeCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	rootCmd.AddCommand(mergeCmd)
}
