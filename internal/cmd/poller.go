package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

// cb-e7edc9: the poller is a background daemon — its per-cycle output is
// noise a human reader never wants to see unless they explicitly ask. The
// startup banner stays on fmt.Printf so `cobuild poller` still prints one
// line on launch at the default warn level. Everything else migrates to
// slog: Debug for per-tick heartbeat ("Polling..."), Info for routine
// status lines (dispatch events, state transitions), Warn/Error for
// recovery failures. The "component" attribute names the subsystem so a
// human running with COBUILD_LOG_LEVEL=info can still grep for the
// familiar auto / reconcile / monitor / implement / dispatch / review
// markers.

var (
	pollerNow  = time.Now
	pollerStat = os.Stat
	pollerExec = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = tmuxCommandArgs(pipelineConfigLoader(), args...)
		}
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	pollerKillWindow = func(ctx context.Context, sessionName, windowName string) error {
		target := fmt.Sprintf("%s:%s", sessionName, windowName)
		return tmuxRun(ctx, pipelineConfigLoader(), "kill-window", "-t", target)
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
			internalLogger().Debug("poll tick", "component", "poller", "ts", time.Now().Format("15:04:05"))

			// Check for auto-labelled work items (Mode 1)
			autoLabel := "cobuild"
			if pCfg != nil && pCfg.Poller.AutoLabel != "" {
				autoLabel = pCfg.Poller.AutoLabel
			}
			pollAutoLabelledItems(ctx, autoLabel, dryRun)

			// Process autonomous pipelines (Mode 2)
			reconcileStaleState(ctx, dryRun)
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
	for _, itemType := range []string{domain.WorkItemTypeDesign, domain.WorkItemTypeBug} {
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
				internalLogger().Info("would init autonomous pipeline (dry run)", "component", "auto", "id", item.ID, "type", item.Type, "label", autoLabel)
			} else {
				internalLogger().Info("initialising autonomous pipeline", "component", "auto", "id", item.ID, "type", item.Type)
				startPhase := domain.PhaseDesign
				repoRoot := findRepoRoot()
				pCfg, _ := config.LoadConfig(repoRoot)
				bootstrap, resolveErr := pipelinestate.ResolveBootstrap(&item, pCfg)
				if resolveErr != nil {
					internalLogger().Warn("resolve bootstrap failed", "component", "auto", "id", item.ID, "err", resolveErr)
					continue
				}
				startPhase = bootstrap.StartPhase
				itemProject := item.Project
				if itemProject == "" {
					itemProject = projectName // fallback to global
				}
				_, err := cbStore.CreateRunWithMode(ctx, item.ID, itemProject, startPhase, "autonomous")
				if err != nil {
					internalLogger().Warn("init pipeline failed", "component", "auto", "id", item.ID, "err", err)
				}
			}
		}
	}
}

func reconcileStaleState(ctx context.Context, dryRun bool) {
	if cbStore == nil {
		return
	}

	runs, err := cbStore.ListRuns(ctx, "")
	if err != nil {
		internalLogger().Error("list-runs failed", "component", "reconcile", "err", err)
		return
	}

	for _, run := range runs {
		if run.Status != "active" {
			continue
		}

		resolvedState, err := pipelinestate.Resolve(ctx, run.DesignID)
		if err != nil {
			if errors.Is(err, pipelinestate.ErrNotFound) {
				continue
			}
			internalLogger().Warn("resolve failed", "component", "reconcile", "id", run.DesignID, "err", err)
			continue
		}

		recommendations := pipelinestate.RecommendRecoveries(resolvedState)
		for _, recommendation := range recommendations {
			internalLogger().Info("recovery", "component", "reconcile", "id", recommendation.DesignID, "kind", recommendation.Kind, "reason", recommendation.Reason)
			if dryRun {
				continue
			}
			if err := applyRecoveryRecommendation(ctx, resolvedState, recommendation); err != nil {
				internalLogger().Warn("recovery failed", "component", "reconcile", "id", recommendation.DesignID, "kind", recommendation.Kind, "err", err)
			}
		}
	}

	if !dryRun {
		reconcileExitedSessionsRun(ctx)
	}
}

func applyRecoveryRecommendation(ctx context.Context, resolvedState *pipelinestate.PipelineState, recommendation pipelinestate.RecoveryRecommendation) error {
	deps := pipelinestate.RecoveryDependencies{
		Store: cbStore,
		Exec:  pollerExec,
	}

	switch recommendation.Kind {
	case pipelinestate.RecoveryCancelOrphanedSession:
		if recommendation.Session == nil {
			return fmt.Errorf("missing session for %s", recommendation.Kind)
		}
		_, err := pipelinestate.CancelOrphanedSession(ctx, deps, *recommendation.Session)
		return err
	case pipelinestate.RecoveryKillOrphanTmuxWindow:
		if recommendation.Window == nil {
			return fmt.Errorf("missing tmux window for %s", recommendation.Kind)
		}
		_, err := pipelinestate.KillOrphanTmuxWindow(ctx, deps, *recommendation.Window)
		return err
	case pipelinestate.RecoveryCompleteStaleRun:
		_, err := pipelinestate.CompleteStaleRun(ctx, deps, resolvedState)
		return err
	default:
		return fmt.Errorf("unknown recovery kind %q", recommendation.Kind)
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
	sessions, err := cbStore.ListRunningSessions(ctx, "")
	if err != nil {
		internalLogger().Error("list running sessions failed", "component", "monitor", "err", err)
		return
	}

	now := pollerNow()
	for _, session := range sessions {
		outcome, note, idle, err := inspectSessionHealth(session, stallTimeout, now)
		if err != nil {
			internalLogger().Warn("inspect failed", "component", "monitor", "task", session.TaskID, "err", err)
			continue
		}
		if outcome == "" {
			continue
		}

		if outcome == "orphaned" {
			if dryRun {
				internalLogger().Info("would mark orphaned (dry run)", "component", "monitor", "task", session.TaskID, "note", note)
				continue
			}
			if err := cbStore.EndSession(ctx, session.ID, store.SessionResult{
				ExitCode:       -1,
				Status:         "orphaned",
				CompletionNote: note,
			}); err != nil {
				internalLogger().Warn("record orphaned failed", "component", "monitor", "task", session.TaskID, "err", err)
				continue
			}
			internalLogger().Info("marked orphaned", "component", "monitor", "task", session.TaskID)
			continue
		}

		sessionName, windowName := sessionTarget(session)
		if dryRun {
			internalLogger().Info("would kill stale session (dry run)", "component", "monitor", "task", session.TaskID, "idle", idle.Round(time.Second))
			continue
		}
		if err := pollerKillWindow(ctx, sessionName, windowName); err != nil {
			internalLogger().Warn("kill stale session failed", "component", "monitor", "task", session.TaskID, "err", err)
			continue
		}
		if err := cbStore.EndSession(ctx, session.ID, store.SessionResult{
			ExitCode:       -1,
			Status:         "stale-killed",
			CompletionNote: note,
		}); err != nil {
			internalLogger().Warn("record stale kill failed", "component", "monitor", "task", session.TaskID, "err", err)
			continue
		}
		internalLogger().Info("killed stale session", "component", "monitor", "task", session.TaskID, "idle", idle.Round(time.Second))
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
	runs, err := cbStore.ListRuns(ctx, "")
	if err != nil {
		internalLogger().Error("list runs failed", "component", "poller", "err", err)
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
			internalLogger().Debug("agent running", "component", "poller", "phase", run.Phase, "id", run.DesignID, "project", run.Project)
			continue
		}

		// Resolve config for this run's project (not the global projectName)
		runRepoRoot, _ := config.RepoForProject(run.Project)
		runCfg, _ := config.LoadConfig(runRepoRoot)
		if runCfg == nil {
			runCfg = pCfg // fall back to global config
		}

		// Check phase — some phases need task-level dispatch, not design-level
		switch run.Phase {
		case domain.PhaseImplement:
			// Check for child tasks that need dispatch
			edges, _ := conn.GetEdges(ctx, run.DesignID, "incoming", []string{"child-of"})
			if len(edges) > 0 {
				dispatchReadyTasks(ctx, runRepoRoot, runCfg, run.DesignID, dryRun)
			} else {
				// Standalone task — dispatch the item itself
				if dryRun {
					internalLogger().Info("would dispatch standalone task (dry run)", "component", "implement", "id", run.DesignID, "project", run.Project)
				} else {
					internalLogger().Info("dispatching standalone task", "component", "implement", "id", run.DesignID, "project", run.Project)
					dispatchForPhase(ctx, run.DesignID)
				}
			}

		case domain.PhaseDesign, domain.PhaseDecompose, domain.PhaseInvestigate, domain.PhaseReview, domain.PhaseDone:
			// Design-level dispatch — spawn agent for this phase
			if dryRun {
				internalLogger().Info("would dispatch (dry run)", "component", "poller", "phase", run.Phase, "id", run.DesignID, "project", run.Project)
			} else {
				internalLogger().Info("dispatching", "component", "poller", "phase", run.Phase, "id", run.DesignID, "project", run.Project)
				dispatchForPhase(ctx, run.DesignID)
			}

		default:
			internalLogger().Warn("unknown phase, skipping", "component", "poller", "phase", run.Phase, "id", run.DesignID)
		}
	}
}

// pollNeedsReviewTasks finds tasks in needs-review and triggers review/merge.
func pollNeedsReviewTasks(ctx context.Context, repoRoot string, pCfg *config.Config, dryRun bool) {
	if conn == nil {
		return
	}

	// List tasks needing review
	result, err := conn.List(ctx, connectorListFilters("task", domain.StatusNeedsReview))
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
				if s.Type != "" && s.Type != "task" {
					continue
				}
				if s.Status != "closed" && s.Status != domain.StatusNeedsReview {
					allDone = false
					break
				}
			}
		}

		if allDone {
			if dryRun {
				internalLogger().Info("all tasks done, would advance (dry run)", "component", "needs-review", "task", item.ID, "design", designID)
			} else {
				internalLogger().Info("all tasks done, advancing design", "component", "needs-review", "task", item.ID, "design", designID)
				if run, err := cbStore.GetRun(ctx, designID); err == nil {
					repoRoot, _ := config.RepoForProject(run.Project)
					pCfg, _ := config.LoadConfig(repoRoot)
					if _, err := advancePipelinePhase(ctx, cbStore, conn, pCfg, designID, run.CurrentPhase); err != nil {
						internalLogger().Warn("could not advance design", "component", "needs-review", "design", designID, "err", err)
					}
				}
			}
		} else {
			// Check if this task's wave is complete — dispatch next wave
			if dryRun {
				internalLogger().Debug("checking if next wave is ready", "component", "needs-review", "task", item.ID)
			}
		}
	}
}

// hasActiveSession checks if there's a running tmux window for this design.
func hasActiveSession(ctx context.Context, designID string) bool {
	// Try the run's project first, fall back to global projectName
	runProject := projectName
	if cbStore != nil {
		if run, err := cbStore.GetRun(ctx, designID); err == nil && run.Project != "" {
			runProject = run.Project
		}
	}
	pCfg := pipelineConfigLoader()
	tmuxSession := pCfg.ResolveTmuxSession(runProject)

	// Check for design-level window
	out, err := tmuxCombinedOutput(ctx, pCfg, "list-windows", "-t", tmuxSession, "-F", "#{window_name}")
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

// registerTaskForDispatch adds a task to pipeline_tasks so the orchestrator's
// implement loop can track it. Idempotent — silently skips on duplicate.
func registerTaskForDispatch(ctx context.Context, designID, taskID string, wave int) {
	if cbStore == nil {
		return
	}
	run, err := cbStore.GetRun(ctx, designID)
	if err != nil {
		return
	}
	var wavePtr *int
	if wave > 0 {
		wavePtr = &wave
	}
	if err := cbStore.AddTask(ctx, run.ID, taskID, designID, wavePtr); err != nil {
		// AddTask is idempotent via ON CONFLICT (cb-2d60c4) — any error here
		// is a real failure (connection lost, store misconfigured, etc).
		internalLogger().Warn("register task failed", "component", "register", "task", taskID, "err", err)
	}
}

// dispatchForPhase runs cobuild dispatch for a design/bug at its current phase.
func dispatchForPhase(ctx context.Context, workItemID string) {
	out, err := exec.CommandContext(ctx, "cobuild", "dispatch", workItemID).CombinedOutput()
	if err != nil {
		internalLogger().Warn("dispatch failed", "component", "dispatch", "id", workItemID, "err", err, "out", string(out))
		return
	}
	internalLogger().Info("dispatched", "component", "dispatch", "id", workItemID, "out", strings.TrimSpace(string(out)))
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
				internalLogger().Info("would mark pending for redispatch (dry run)", "component", "implement", "task", item.ID, "prior_status", rec.Status)
			} else {
				if err := markTaskPendingForRedispatch(ctx, item.ID, rec); err != nil {
					internalLogger().Warn("redispatch recovery failed", "component", "implement", "task", item.ID, "err", err)
				} else {
					internalLogger().Info("marked pending for redispatch", "component", "implement", "task", item.ID, "prior_status", rec.Status, "reason", reason)
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
		case domain.StatusInProgress:
			inProgress++
		case domain.StatusNeedsReview:
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
						// Register the task in pipeline_tasks before dispatch
						// so the orchestrator's implement loop can track it
						// via ListTasksByDesign. Without this, tasks dispatched
						// by the poller are invisible to orchestrate.
						registerTaskForDispatch(ctx, designID, e.ItemID, wave)
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
		internalLogger().Info("wave status", "component", "implement", "design", designID, "done", done, "total", total, "in_progress", inProgress, "ready", ready)
	}
	if dryRun {
		for _, taskID := range readyIDs {
			internalLogger().Info("ready to dispatch (dry run)", "component", "implement", "task", taskID)
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

	result, err := conn.List(ctx, connectorListFilters("task", domain.StatusNeedsReview))
	if err != nil {
		return
	}

	for _, item := range result.Items {
		prURL, _ := conn.GetMetadata(ctx, item.ID, domain.MetaPRURL)
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
			internalLogger().Info("Gemini review found, would process (dry run)", "component", "review", "task", item.ID)
			continue
		}

		internalLogger().Info("processing Gemini review", "component", "review", "task", item.ID)
		out, err := exec.CommandContext(ctx, "cobuild", "process-review", item.ID).CombinedOutput()
		if err != nil {
			internalLogger().Warn("process-review failed", "component", "review", "task", item.ID, "err", err, "out", string(out))
		} else {
			internalLogger().Info("process-review done", "component", "review", "task", item.ID, "out", strings.TrimSpace(string(out)))
		}
	}
}

func init() {
	pollerCmd.Flags().Int("interval", 30, "Poll interval in seconds")
	pollerCmd.Flags().Bool("once", false, "Run once and exit")
	pollerCmd.Flags().Bool("dry-run", false, "Show what would be done")
	rootCmd.AddCommand(pollerCmd)
}
