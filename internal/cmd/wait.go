package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/spf13/cobra"
)

var waitCmd = &cobra.Command{
	Use:   "wait <task-id> [task-id...]",
	Short: "Wait for tasks to reach a target status",
	Long: `Polls task status at regular intervals until all specified tasks reach
the target status (default: needs-review). Useful after dispatching a wave
of tasks — blocks until all agents have completed their work.

Exit codes:
  0 — all tasks reached target status
  1 — timeout reached before all tasks completed
  2 — a task reached a terminal failure state (blocked)`,
	Args: cobra.MinimumNArgs(1),
	Example: `  cobuild wait pf-abc123 pf-def456              # wait for needs-review
  cobuild wait pf-abc123 --status closed          # wait for closed
  cobuild wait pf-abc123 --timeout 30m            # custom timeout
  cobuild wait pf-abc123 --interval 30            # poll every 30 seconds`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		targetStatus, _ := cmd.Flags().GetString("status")
		if targetStatus == "" {
			targetStatus = "needs-review"
		}
		intervalSec, _ := cmd.Flags().GetInt("interval")
		if intervalSec < 5 {
			intervalSec = 60
		}
		timeoutStr, _ := cmd.Flags().GetString("timeout")
		timeout := 2 * time.Hour
		if timeoutStr != "" {
			parsed, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return fmt.Errorf("invalid timeout %q: %v", timeoutStr, err)
			}
			timeout = parsed
		}

		taskIDs := args
		deadline := time.Now().Add(timeout)

		fmt.Printf("Waiting for %d task(s) to reach %q (poll every %ds, timeout %s)\n",
			len(taskIDs), targetStatus, intervalSec, timeout)

		for {
			allDone := true
			var statusLines []string

			for _, id := range taskIDs {
				item, err := conn.Get(ctx, id)
				if err != nil {
					statusLines = append(statusLines, fmt.Sprintf("  %s: error (%v)", id, err))
					allDone = false
					continue
				}

				done := false
				switch {
				case item.Status == targetStatus:
					done = true
				case item.Status == "closed" && targetStatus != "closed":
					done = true // closed supersedes any target
				}

				marker := "..."
				if done {
					marker = "done"
				}

				statusLines = append(statusLines, fmt.Sprintf("  %s: %-14s [%s] %s",
					id, item.Status, marker, cliutil.Truncate(item.Title, 50)))

				if !done {
					allDone = false
					// Check for blocked/failed states
					if hasLabel(item.Labels, "blocked") {
						fmt.Printf("\n%s is blocked — aborting wait\n", id)
						for _, l := range statusLines {
							fmt.Println(l)
						}
						return fmt.Errorf("task %s is blocked", id)
					}
				}
			}

			// Print status
			ts := time.Now().Format("15:04:05")
			remaining := time.Until(deadline).Round(time.Second)
			fmt.Printf("\n[%s] (timeout in %s)\n", ts, remaining)
			for _, l := range statusLines {
				fmt.Println(l)
			}

			if allDone {
				fmt.Printf("\nAll %d task(s) reached target status.\n", len(taskIDs))
				// Phase-aware next step for the single-task case; multi-task
				// falls through to a generic "run cobuild next <id>" list
				// since the tasks may be in different pipeline states.
				if len(taskIDs) == 1 && cbStore != nil {
					if run, err := cbStore.GetRun(ctx, taskIDs[0]); err == nil && run != nil {
						printNextStep(taskIDs[0], run.CurrentPhase, "wait-complete")
						return nil
					}
				}
				fmt.Println()
				fmt.Println("Next step:")
				for _, id := range taskIDs {
					fmt.Printf("  cobuild next %s\n", id)
				}
				return nil
			}

			if time.Now().After(deadline) {
				fmt.Printf("\nTimeout reached. Not all tasks completed.\n")
				return fmt.Errorf("timeout after %s", timeout)
			}

			time.Sleep(time.Duration(intervalSec) * time.Second)
		}
	},
}

func hasLabel(labels []string, target string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, target) {
			return true
		}
	}
	return false
}

func init() {
	waitCmd.Flags().String("status", "needs-review", "Target status to wait for")
	waitCmd.Flags().Int("interval", 60, "Poll interval in seconds")
	waitCmd.Flags().String("timeout", "2h", "Maximum wait time (e.g., 30m, 2h)")
	rootCmd.AddCommand(waitCmd)
}
