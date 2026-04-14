package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/spf13/cobra"
)

// Staleness thresholds for cobuild status (cb-fcf5e4). Tuned for typical
// agent cycles: within 10 minutes is healthy progress; beyond 10 the agent
// likely stalled; beyond 60 it's almost certainly dead and needs recovery.
const (
	statusStaleAfter = 10 * time.Minute
	statusDeadAfter  = 60 * time.Minute
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all active pipelines and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbStore == nil {
			return fmt.Errorf("no store configured (need database connection)")
		}

		runs, err := cbStore.ListRuns(ctx, projectName)
		if err != nil {
			return fmt.Errorf("list pipeline runs: %w", err)
		}

		if len(runs) == 0 {
			fmt.Println("No active pipelines.")
			return nil
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(runs)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("%-12s %-14s %-10s %-8s %-6s %s\n", "ID", "PHASE", "STATUS", "HEALTH", "TASKS", "LAST ACTIVITY")
		fmt.Printf("%-12s %-14s %-10s %-8s %-6s %s\n", "----", "-----", "------", "------", "-----", "-------------")
		for _, r := range runs {
			taskSummary := "-"
			if r.TaskTotal > 0 {
				taskSummary = fmt.Sprintf("%d/%d", r.TaskDone, r.TaskTotal)
			}

			lastActivity := client.TimeAgo(r.LastProgress)
			health := statusHealthFor(r.Status, r.LastProgress)

			fmt.Printf("%-12s %-14s %-10s %-8s %-6s %s\n",
				r.DesignID,
				r.Phase,
				r.Status,
				health,
				taskSummary,
				lastActivity,
			)
		}
		return nil
	},
}

// statusHealthFor returns a single-word health label for a pipeline row.
// Terminal runs ("completed"/"cancelled") return empty — their staleness
// is irrelevant. Active runs get ACTIVE / STALE / DEAD based on how long
// since any child session progressed.
func statusHealthFor(runStatus string, lastProgress time.Time) string {
	if runStatus != "active" {
		return "-"
	}
	if lastProgress.IsZero() {
		return "UNKNOWN"
	}
	idle := time.Since(lastProgress)
	switch {
	case idle >= statusDeadAfter:
		return "DEAD"
	case idle >= statusStaleAfter:
		return "STALE"
	default:
		return "ACTIVE"
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
