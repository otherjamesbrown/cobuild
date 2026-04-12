package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var (
	pollerNow        = time.Now
	pollerStat       = os.Stat
	pollerKillWindow = func(ctx context.Context, sessionName, windowName string) error {
		target := fmt.Sprintf("%s:%s", sessionName, windowName)
		return exec.CommandContext(ctx, "tmux", "kill-window", "-t", target).Run()
	}
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

			// Check for auto-labelled work items (Mode 1)
			autoLabel := "cobuild"
			if pCfg != nil && pCfg.Poller.AutoLabel != "" {
				autoLabel = pCfg.Poller.AutoLabel
			}
			pollAutoLabelledItems(ctx, autoLabel, dryRun)

			// Process autonomous pipelines (Mode 2)
			checkStaleSessions(ctx, pCfg, dryRun)
			pollActivePipelines(ctx, repoRoot, pCfg, dryRun)
			pollNeedsReviewTasks(ctx, repoRoot, pCfg, dryRun)
			pollTaskReviews(ctx, dryRun)

			if once {
				break
			}
			time.Sleep(time.Duration(interval) * time.Second)
		}
		return nil
	},
}

// pollAutoLabelledItems finds work items with the auto-process label
// that don't have a pipeline run yet, and initialises them in autonomous mode.
func pollAutoLabelledItems(ctx context.Context, autoLabel string, dryRun bool) {
	if conn == nil || autoLabel == "" {
		return
	}

	// Search for items with the label
	// This is connector-specific — for CP we'd search by label
	// For now, use a simple list and filter
	for _, itemType := range []string{"design", "bug"} {
		result, err := conn.List(ctx, connector.ListFilters{
			Type:   itemType,
			Status: "open",
			Limit:  50,
		})
		if err != nil {
			continue
		}

		for _, item := range result.Items {
			// Check if item has the auto-process label
			hasLabel := false
			for _, l := range item.Labels {
				if l == autoLabel {
					hasLabel = true
					break
				}
			}
			if !hasLabel {
				continue
			}

			// Check if it already has a pipeline run
			if cbStore != nil {
				_, err := cbStore.GetRun(ctx, item.ID)
				if err == nil {
					continue // already has a pipeline
				}
			}

			if dryRun {
				fmt.Printf("  [auto] %s (%s) — has label %q, would init autonomous pipeline\n", item.ID, item.Type, autoLabel)
			} else {
				fmt.Printf("  [auto] %s (%s) — initialising autonomous pipeline\n", item.ID, item.Type)
				startPhase := "design"
				repoRoot := findRepoRoot()
				pCfg, _ := config.LoadConfig(repoRoot)
				if pCfg != nil {
					sp := pCfg.StartPhaseForType(item.Type)
					if sp != "" {
						startPhase = sp
					}
				}
				_, err := cbStore.CreateRunWithMode(ctx, item.ID, projectName, startPhase, "autonomous")
				if err != nil {
					fmt.Fprintf(os.Stderr, "  [auto] init %s failed: %v\n", item.ID, err)
				}
			}
		}
	}
}

func checkStaleSessions(ctx context.Context, pCfg *config.Config, dryRun bool) {
	if cbStore == nil {
		return
	}
	if pCfg == nil {
		pCfg = config.DefaultConfig()
	}

	stallTimeout := resolveStallTimeout(pCfg)
	sessions, err := cbStore.ListRunningSessions(ctx, projectName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [error] list running sessions: %v\n", err)
		return
	}

	now := pollerNow()
	for _, session := range sessions {
		outcome, note, idle, err := inspectSessionHealth(session, stallTimeout, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [monitor] %s — inspect failed: %v\n", session.TaskID, err)
			continue
		}
		if outcome == "" {
			continue
		}

		if outcome == "orphaned" {
			if dryRun {
				fmt.Printf("  [monitor] %s — would mark orphaned (%s)\n", session.TaskID, note)
				continue
			}
			if err := cbStore.EndSession(ctx, session.ID, store.SessionResult{
				ExitCode:       -1,
				Status:         "orphaned",
				CompletionNote: note,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "  [monitor] %s — record orphaned failed: %v\n", session.TaskID, err)
				continue
			}
			fmt.Printf("  [monitor] %s — marked orphaned\n", session.TaskID)
			continue
		}

		sessionName, windowName := sessionTarget(session)
		if dryRun {
			fmt.Printf("  [monitor] %s — would kill stale session after %s idle\n", session.TaskID, idle.Round(time.Second))
			continue
		}
		if err := pollerKillWindow(ctx, sessionName, windowName); err != nil {
			fmt.Fprintf(os.Stderr, "  [monitor] %s — kill stale session failed: %v\n", session.TaskID, err)
			continue
		}
		if err := cbStore.EndSession(ctx, session.ID, store.SessionResult{
			ExitCode:       -1,
			Status:         "stale-killed",
			CompletionNote: note,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "  [monitor] %s — record stale kill failed: %v\n", session.TaskID, err)
			continue
		}
		fmt.Printf("  [monitor] %s — killed stale session after %s idle\n", session.TaskID, idle.Round(time.Second))
	}
}

func resolveStallTimeout(pCfg *config.Config) time.Duration {
	timeout := strings.TrimSpace(pCfg.Monitoring.StallTimeout)
	if timeout == "" {
		timeout = config.DefaultConfig().Monitoring.StallTimeout
	}
	d, err := time.ParseDuration(timeout)
	if err != nil || d <= 0 {
		return 30 * time.Minute
	}
	return d
}

func inspectSessionHealth(session store.SessionRecord, stallTimeout time.Duration, now time.Time) (outcome string, note string, idle time.Duration, err error) {
	if session.WorktreePath == nil || strings.TrimSpace(*session.WorktreePath) == "" {
		return "orphaned", "Marked orphaned by poller: missing worktree path for running session", 0, nil
	}

	worktreePath := strings.TrimSpace(*session.WorktreePath)
	if _, err := pollerStat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return "orphaned", fmt.Sprintf("Marked orphaned by poller: worktree path missing (%s)", worktreePath), 0, nil
		}
		return "", "", 0, err
	}

	sessionLog := filepath.Join(worktreePath, ".cobuild", "session.log")
	info, err := pollerStat(sessionLog)
	if err != nil {
		if os.IsNotExist(err) {
			return "orphaned", fmt.Sprintf("Marked orphaned by poller: session log missing (%s)", sessionLog), 0, nil
		}
		return "", "", 0, err
	}

	idle = now.Sub(info.ModTime())
	if idle <= stallTimeout {
		return "", "", idle, nil
	}

	idle = idle.Round(time.Second)
	note = fmt.Sprintf("Killed by poller: session.log mtime > stall_timeout (%s idle)", idle)
	return "stale-killed", note, idle, nil
}

func sessionTarget(session store.SessionRecord) (sessionName, windowName string) {
	sessionName = fmt.Sprintf("cobuild-%s", projectName)
	if session.Project != "" {
		sessionName = fmt.Sprintf("cobuild-%s", session.Project)
	}
	if session.TmuxSession != nil && strings.TrimSpace(*session.TmuxSession) != "" {
		sessionName = strings.TrimSpace(*session.TmuxSession)
	}

	windowName = session.TaskID
	if session.TmuxWindow != nil && strings.TrimSpace(*session.TmuxWindow) != "" {
		windowName = strings.TrimSpace(*session.TmuxWindow)
	}
	return sessionName, windowName
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

		// Only process autonomous pipelines
		// Check mode from the full run record
		fullRun, err := cbStore.GetRun(ctx, run.DesignID)
		if err != nil || fullRun.Mode != "autonomous" {
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
			// Check for child tasks that need dispatch
			edges, _ := conn.GetEdges(ctx, run.DesignID, "incoming", []string{"child-of"})
			if len(edges) > 0 {
				dispatchReadyTasks(ctx, repoRoot, pCfg, run.DesignID, dryRun)
			} else {
				// Standalone task — dispatch the item itself
				if dryRun {
					fmt.Printf("  [implement] %s — standalone task, would dispatch\n", run.DesignID)
				} else {
					fmt.Printf("  [implement] %s — dispatching standalone task\n", run.DesignID)
					dispatchForPhase(ctx, run.DesignID)
				}
			}

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
	if pCfg == nil {
		pCfg = config.DefaultConfig()
	}
	edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return
	}

	ready := 0
	inProgress := 0
	done := 0
	readyIDs := []string{}
	activeWave := 0

	for _, e := range edges {
		item, err := conn.Get(ctx, e.ItemID)
		if err != nil || item == nil || item.Type != "task" {
			continue
		}
		wave := taskWave(item)
		taskStatus := e.Status

		if shouldMarkTaskForRedispatch(taskStatus, latestTaskSession(ctx, item.ID)) && !hasActiveSession(ctx, item.ID) {
			rec, _ := cbStore.GetSession(ctx, item.ID)
			reason := redispatchReason(rec)
			if dryRun {
				fmt.Printf("  [implement] %s — %s left task stuck in progress, would mark pending for redispatch\n", item.ID, rec.Status)
			} else {
				if err := markTaskPendingForRedispatch(ctx, item.ID, rec); err != nil {
					fmt.Fprintf(os.Stderr, "  [implement] %s redispatch recovery failed: %v\n", item.ID, err)
				} else {
					fmt.Printf("  [implement] %s — marked pending for redispatch after %s\n", item.ID, rec.Status)
					if reason != "" {
						fmt.Printf("    %s\n", reason)
					}
				}
			}
			continue
		}

		if resolveWaveStrategy(pCfg) == "serial" && taskStatus != "closed" {
			if activeWave == 0 || (wave > 0 && wave < activeWave) {
				activeWave = wave
			}
		}

		switch taskStatus {
		case "closed":
			done++
		case "in_progress":
			inProgress++
		case "needs-review":
			if resolveWaveStrategy(pCfg) == "parallel" {
				done++ // effectively done for parallel dispatch
			}
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
				readyIDs = append(readyIDs, e.ItemID)
				if !dryRun && !hasActiveSession(ctx, e.ItemID) {
					maxConcurrent := 3
					if pCfg.Dispatch.MaxConcurrent > 0 {
						maxConcurrent = pCfg.Dispatch.MaxConcurrent
					}
					if inProgress < maxConcurrent {
						dispatchForPhase(ctx, e.ItemID)
						inProgress++
					}
				}
			}
		}
	}

	if resolveWaveStrategy(pCfg) == "serial" && activeWave > 0 {
		filtered := readyIDs[:0]
		for _, taskID := range readyIDs {
			item, err := conn.Get(ctx, taskID)
			if err != nil || item == nil {
				continue
			}
			if taskWave(item) == activeWave {
				filtered = append(filtered, taskID)
			}
		}
		readyIDs = filtered
		ready = len(readyIDs)
	}

	total := ready + inProgress + done
	if total > 0 {
		fmt.Printf("  [implement] %s — %d/%d done, %d in-progress, %d ready\n",
			designID, done, total, inProgress, ready)
	}
	if dryRun {
		for _, taskID := range readyIDs {
			fmt.Printf("  [implement] %s — ready to dispatch\n", taskID)
		}
	}
}

func latestTaskSession(ctx context.Context, taskID string) *store.SessionRecord {
	if cbStore == nil {
		return nil
	}
	rec, err := cbStore.GetSession(ctx, taskID)
	if err != nil {
		return nil
	}
	return rec
}

// pollTaskReviews finds needs-review tasks with PRs and processes Gemini reviews.
func pollTaskReviews(ctx context.Context, dryRun bool) {
	if conn == nil {
		return
	}

	result, err := conn.List(ctx, connectorListFilters("task", "needs-review"))
	if err != nil {
		return
	}

	for _, item := range result.Items {
		prURL, _ := conn.GetMetadata(ctx, item.ID, "pr_url")
		if prURL == "" {
			continue
		}

		// Check if Gemini has reviewed
		repo, prNumber, err := parsePRURL(prURL)
		if err != nil {
			continue
		}
		reviews, err := getGeminiReviews(ctx, repo, prNumber)
		if err != nil || len(reviews) == 0 {
			continue // no review yet, skip
		}

		if dryRun {
			fmt.Printf("  [review] %s — Gemini review found, would process\n", item.ID)
			continue
		}

		fmt.Printf("  [review] %s — processing Gemini review...\n", item.ID)
		out, err := exec.CommandContext(ctx, "cobuild", "process-review", item.ID).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [review] %s failed: %v\n%s\n", item.ID, err, string(out))
		} else {
			fmt.Printf("  [review] %s — %s\n", item.ID, strings.TrimSpace(string(out)))
		}
	}
}

func init() {
	pollerCmd.Flags().Int("interval", 30, "Poll interval in seconds")
	pollerCmd.Flags().Bool("once", false, "Run once and exit")
	pollerCmd.Flags().Bool("dry-run", false, "Show what would be done")
	rootCmd.AddCommand(pollerCmd)
}
