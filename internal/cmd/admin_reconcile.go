package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var adminReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Fix pipeline_runs stuck in review after PRs were merged",
	Long: `Scans active pipeline_runs in review or implement phase,
checks if the associated work items are closed or PRs are merged,
and advances the pipeline_run to done/completed.

This fixes the gap where PRs are merged manually on GitHub
without going through cobuild merge.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		runs, err := cbStore.ListRuns(ctx, projectName)
		if err != nil {
			return fmt.Errorf("list runs: %w", err)
		}

		reconciled := 0
		for _, r := range runs {
			if r.Status != "active" {
				continue
			}
			if r.Phase == "done" {
				continue
			}

			// Check if this is a task (direct dispatch) or a design (has children)
			item, err := conn.Get(ctx, r.DesignID)
			if err != nil {
				fmt.Printf("  %-12s skip (cannot read work item: %v)\n", r.DesignID, err)
				continue
			}

			resolved := false
			reason := ""

			if item.Status == "closed" {
				// Work item already closed — pipeline should be done
				resolved = true
				reason = "work item closed"
			} else if item.Type == "task" || item.Type == "bug" {
				// Check if PR is merged
				prURL, _ := conn.GetMetadata(ctx, r.DesignID, "pr_url")
				if prURL != "" {
					resolved, reason = checkPRMerged(ctx, prURL)
				} else {
					// Try finding PR by branch name
					out, err := exec.CommandContext(ctx, "gh", "pr", "list",
						"--head", r.DesignID, "--state", "merged",
						"--json", "url", "--jq", ".[0].url").Output()
					if err == nil && len(strings.TrimSpace(string(out))) > 0 {
						resolved = true
						reason = "PR merged (found by branch)"
					}
				}
			} else if item.Type == "design" {
				// Check if all child tasks are closed
				children, err := conn.GetEdges(ctx, r.DesignID, "incoming", []string{"child-of"})
				if err == nil && len(children) > 0 {
					allClosed := true
					for _, c := range children {
						if c.Status != "closed" {
							allClosed = false
							break
						}
					}
					if allClosed {
						resolved = true
						reason = fmt.Sprintf("all %d child tasks closed", len(children))
					}
				}
			}

			if resolved {
				action := "would advance"
				if !dryRun {
					action = "advancing"
					if err := cbStore.UpdateRunPhase(ctx, r.DesignID, "done"); err != nil {
						fmt.Printf("  %-12s error advancing phase: %v\n", r.DesignID, err)
						continue
					}
					if err := cbStore.UpdateRunStatus(ctx, r.DesignID, "completed"); err != nil {
						fmt.Printf("  %-12s error marking completed: %v\n", r.DesignID, err)
						continue
					}
				}
				fmt.Printf("  %-12s %s → done/completed (%s)\n", r.DesignID, action, reason)
				reconciled++
			}
		}

		if reconciled == 0 {
			fmt.Println("No pipelines need reconciliation.")
		} else {
			verb := "Reconciled"
			if dryRun {
				verb = "Would reconcile"
			}
			fmt.Printf("\n%s %d pipeline(s).\n", verb, reconciled)
		}

		return nil
	},
}

func checkPRMerged(ctx context.Context, prURL string) (bool, string) {
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", prURL,
		"--json", "state", "--jq", ".state").Output()
	if err != nil {
		return false, ""
	}
	state := strings.TrimSpace(string(out))
	if state == "MERGED" {
		return true, "PR merged"
	}
	return false, ""
}

func init() {
	adminReconcileCmd.Flags().Bool("dry-run", false, "Show what would be reconciled without making changes")
	adminCmd.AddCommand(adminReconcileCmd)
}
