package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var adminCloseStaleCmd = &cobra.Command{
	Use:   "close-stale",
	Short: "Mark abandoned pipeline_runs as completed or abandoned",
	Long: `Finds pipeline_runs with no progress for longer than --stale-days
and marks them as abandoned. If the work item is already closed,
marks them as completed instead.

Safe to run — only changes pipeline_run metadata, not work items.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		staleDays, _ := cmd.Flags().GetInt("stale-days")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		staleDuration := time.Duration(staleDays) * 24 * time.Hour

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}

		runs, err := cbStore.ListRuns(ctx, projectName)
		if err != nil {
			return fmt.Errorf("list runs: %w", err)
		}

		closed := 0
		for _, r := range runs {
			if r.Status != "active" {
				continue
			}

			age := time.Since(r.LastProgress)
			if age < staleDuration {
				continue
			}

			// Mark as completed — check WI status to decide phase
			targetStatus := "completed"
			wiClosed := false
			if conn != nil {
				item, err := conn.Get(ctx, r.DesignID)
				if err == nil && (item.Status == "closed" || item.Status == "done") {
					wiClosed = true
				}
			}

			label := "stale"
			if wiClosed {
				label = "wi-closed"
			}

			action := "would mark"
			if !dryRun {
				action = "marking"
				if wiClosed {
					_ = cbStore.UpdateRunPhase(ctx, r.DesignID, "done")
				}
				if err := cbStore.UpdateRunStatus(ctx, r.DesignID, targetStatus); err != nil {
					fmt.Printf("  %-12s error: %v\n", r.DesignID, err)
					continue
				}
			}

			fmt.Printf("  %-12s %-14s %s → %s (%s, %dd old)\n",
				r.DesignID, r.Phase, action, targetStatus, label,
				int(age.Hours()/24))
			closed++
		}

		if closed == 0 {
			fmt.Printf("No stale pipelines found (threshold: %d days).\n", staleDays)
		} else {
			verb := "Closed"
			if dryRun {
				verb = "Would close"
			}
			fmt.Printf("\n%s %d stale pipeline(s).\n", verb, closed)
		}

		return nil
	},
}

func init() {
	adminCloseStaleCmd.Flags().Int("stale-days", 7, "Consider pipelines stale after this many days")
	adminCloseStaleCmd.Flags().Bool("dry-run", false, "Show what would be closed without making changes")
	adminCmd.AddCommand(adminCloseStaleCmd)
}
