package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var pollerCmd = &cobra.Command{
	Use:   "poller",
	Short: "Continuously poll for actionable pipeline state and dispatch agents",
	Long: `Runs a polling loop that checks all active pipelines and takes action:

For each active pipeline:
  1. Check if there's an active agent session (running in tmux)
  2. If no agent is working → dispatch one for the current phase
  3. If agent completed → check if next phase needs dispatching

Also runs health checks for stalled agents.`,
	Example: `  cobuild poller
  cobuild poller --interval 60
  cobuild poller --once --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		interval, _ := cmd.Flags().GetInt("interval")
		once, _ := cmd.Flags().GetBool("once")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if interval < 10 {
			interval = 30
		}

		if cbStore == nil {
			return fmt.Errorf("no store configured — poller needs database access")
		}
		if conn == nil {
			return fmt.Errorf("no connector configured — poller needs work-item access")
		}

		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		fmt.Printf("[poller] Starting (project: %s, interval: %ds)\n", projectName, interval)

		for {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("\n[%s] Polling...\n", ts)

			pollActivePipelines(ctx, repoRoot, pCfg, dryRun)
			pollNeedsReviewTasks(ctx, repoRoot, pCfg, dryRun)

			if once {
				break
			}
			time.Sleep(time.Duration(interval) * time.Second)
		}
		return nil
	},
}

// pollActivePipelines checks each active pipeline and dispatches if no agent is working.
func pollActivePipelines(ctx context.Context, repoRoot string, pCfg *config.Config, dryRun bool) {
	runs, err := cbStore.ListRuns(ctx, projectName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [error] list runs: %v\n", err)
		return
	}

	for _, run := range runs {
		if run.Status != "active" {
			continue
		}

		// Check if there's already an agent session running for this pipeline
		if hasActiveSession(ctx, run.DesignID) {
			fmt.Printf("  [%s] %s — agent running\n", run.Phase, run.DesignID)
			continue
		}

		// Check phase — some phases need task-level dispatch, not design-level
		switch run.Phase {
		case "implement":
			// Check for tasks that need dispatch
			dispatchReadyTasks(ctx, repoRoot, pCfg, run.DesignID, dryRun)

		case "design", "decompose", "investigate", "review", "done":
			// Design-level dispatch — spawn agent for this phase
			if dryRun {
				fmt.Printf("  [%s] %s — would dispatch\n", run.Phase, run.DesignID)
			} else {
				fmt.Printf("  [%s] %s — dispatching...\n", run.Phase, run.DesignID)
				dispatchForPhase(ctx, run.DesignID)
			}

		default:
			fmt.Printf("  [%s] %s — unknown phase, skipping\n", run.Phase, run.DesignID)
		}
	}
}

// pollNeedsReviewTasks finds tasks in needs-review and triggers review/merge.
func pollNeedsReviewTasks(ctx context.Context, repoRoot string, pCfg *config.Config, dryRun bool) {
	if conn == nil {
		return
	}

	// List tasks needing review
	result, err := conn.List(ctx, connectorListFilters("task", "needs-review"))
	if err != nil {
		return
	}

	for _, item := range result.Items {
		// Find parent design
		edges, err := conn.GetEdges(ctx, item.ID, "outgoing", []string{"child-of"})
		if err != nil || len(edges) == 0 {
			continue
		}
		designID := edges[0].ItemID

		// Check if all sibling tasks are done
		allDone := true
		siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
		if err == nil {
			for _, s := range siblings {
				if s.Status != "closed" && s.Status != "needs-review" {
					allDone = false
					break
				}
			}
		}

		if allDone {
			if dryRun {
				fmt.Printf("  [needs-review] %s — all tasks done, would advance %s to review\n", item.ID, designID)
			} else {
				fmt.Printf("  [needs-review] %s — all tasks done, advancing %s\n", item.ID, designID)
				cbStore.UpdateRunPhase(ctx, designID, "review")
			}
		} else {
			// Check if this task's wave is complete — dispatch next wave
			if dryRun {
				fmt.Printf("  [needs-review] %s — checking if next wave is ready\n", item.ID)
			}
		}
	}
}

// hasActiveSession checks if there's a running tmux window for this design.
func hasActiveSession(ctx context.Context, designID string) bool {
	tmuxSession := fmt.Sprintf("cobuild-%s", projectName)

	// Check for design-level window
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-t", tmuxSession, "-F", "#{window_name}").CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == designID || strings.HasPrefix(name, designID) {
			return true
		}
	}
	return false
}

// dispatchForPhase runs cobuild dispatch for a design/bug at its current phase.
func dispatchForPhase(ctx context.Context, workItemID string) {
	out, err := exec.CommandContext(ctx, "cobuild", "dispatch", workItemID).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [dispatch] %s failed: %v\n%s\n", workItemID, err, string(out))
		return
	}
	fmt.Printf("  [dispatch] %s — %s\n", workItemID, strings.TrimSpace(string(out)))
}

// dispatchReadyTasks dispatches ready tasks for a design in the implement phase.
func dispatchReadyTasks(ctx context.Context, repoRoot string, pCfg *config.Config, designID string, dryRun bool) {
	edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return
	}

	ready := 0
	inProgress := 0
	done := 0

	for _, e := range edges {
		switch e.Status {
		case "closed":
			done++
		case "in_progress":
			inProgress++
		case "needs-review":
			done++ // effectively done
		case "open":
			// Check if blockers are satisfied
			blockers, _ := conn.GetEdges(ctx, e.ItemID, "outgoing", []string{"blocked-by"})
			allSatisfied := true
			for _, b := range blockers {
				if b.Status != "closed" {
					allSatisfied = false
					break
				}
			}
			if allSatisfied {
				ready++
				if !dryRun && !hasActiveSession(ctx, e.ItemID) {
					maxConcurrent := 3
					if pCfg.Dispatch.MaxConcurrent > 0 {
						maxConcurrent = pCfg.Dispatch.MaxConcurrent
					}
					if inProgress < maxConcurrent {
						dispatchForPhase(ctx, e.ItemID)
						inProgress++
					}
				} else if dryRun {
					fmt.Printf("  [implement] %s — ready to dispatch\n", e.ItemID)
				}
			}
		}
	}

	total := ready + inProgress + done
	if total > 0 {
		fmt.Printf("  [implement] %s — %d/%d done, %d in-progress, %d ready\n",
			designID, done, total, inProgress, ready)
	}
}

func init() {
	pollerCmd.Flags().Int("interval", 30, "Poll interval in seconds")
	pollerCmd.Flags().Bool("once", false, "Run once and exit")
	pollerCmd.Flags().Bool("dry-run", false, "Show what would be done")
	rootCmd.AddCommand(pollerCmd)
}
