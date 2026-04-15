package cmd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

// Staleness thresholds for cobuild status (cb-fcf5e4). Tuned for typical
// agent cycles: within 10 minutes is healthy progress; beyond 10 the agent
// likely stalled; beyond 60 it's almost certainly dead and needs recovery.
const (
	statusStaleAfter = 10 * time.Minute
	statusDeadAfter  = 60 * time.Minute
)

var (
	statusActiveOnly      bool
	statusActiveRecentFor time.Duration
	statusNow             = time.Now
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show pipeline runs and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbStore == nil {
			return fmt.Errorf("no store configured (need database connection)")
		}

		reconcileExitedSessionsRun(ctx)

		runs, err := cbStore.ListRuns(ctx, projectName)
		if err != nil {
			return fmt.Errorf("list pipeline runs: %w", err)
		}
		runs = statusFilterAndSortRuns(runs, statusActiveOnly, statusActiveRecentFor, statusNow())

		if len(runs) == 0 {
			if statusActiveOnly {
				fmt.Println("No active pipelines.")
			} else {
				fmt.Println("No pipelines.")
			}
			return nil
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(runs)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("%-12s %-14s %-10s %-8s %-8s %-6s %s\n", "ID", "PHASE", "STATUS", "HEALTH", "REBASE", "TASKS", "LAST ACTIVITY")
		fmt.Printf("%-12s %-14s %-10s %-8s %-8s %-6s %s\n", "----", "-----", "------", "------", "------", "-----", "-------------")
		for _, r := range runs {
			taskSummary := "-"
			if r.TaskTotal > 0 {
				taskSummary = fmt.Sprintf("%d/%d", r.TaskDone, r.TaskTotal)
			}

			lastActivity := cliutil.TimeAgo(r.LastProgress)
			health := statusHealthFor(r.Status, r.LastProgress)
			rebase := statusRebaseFor(r.RebaseConflicts)

			fmt.Printf("%-12s %-14s %-10s %-8s %-8s %-6s %s\n",
				r.DesignID,
				r.Phase,
				r.Status,
				health,
				rebase,
				taskSummary,
				lastActivity,
			)
		}
		return nil
	},
}

func statusFilterAndSortRuns(runs []store.PipelineRunStatus, activeOnly bool, recentWindow time.Duration, now time.Time) []store.PipelineRunStatus {
	filtered := make([]store.PipelineRunStatus, 0, len(runs))
	for _, run := range runs {
		if activeOnly && !statusRunMatchesActiveFilter(run, recentWindow, now) {
			continue
		}
		filtered = append(filtered, run)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left := statusLatestActivity(filtered[i])
		right := statusLatestActivity(filtered[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		if filtered[i].LastProgress != filtered[j].LastProgress {
			return filtered[i].LastProgress.After(filtered[j].LastProgress)
		}
		return filtered[i].DesignID < filtered[j].DesignID
	})
	return filtered
}

func statusRunMatchesActiveFilter(run store.PipelineRunStatus, recentWindow time.Duration, now time.Time) bool {
	switch run.Status {
	case "active", "in_progress":
		return true
	}
	if recentWindow <= 0 || run.LastSessionAt.IsZero() {
		return false
	}
	return now.Sub(run.LastSessionAt) <= recentWindow
}

func statusLatestActivity(run store.PipelineRunStatus) time.Time {
	if run.LastSessionAt.After(run.LastProgress) {
		return run.LastSessionAt
	}
	return run.LastProgress
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

func statusRebaseFor(conflicts int) string {
	if conflicts <= 0 {
		return "-"
	}
	return "CONFLICT"
}

func init() {
	statusCmd.Flags().BoolVar(&statusActiveOnly, "active", false, "Filter to active/in-progress pipelines or ones with a recent session")
	statusCmd.Flags().DurationVar(&statusActiveRecentFor, "active-recent-for", 24*time.Hour, "Recent-session window used by --active")
	rootCmd.AddCommand(statusCmd)
}
