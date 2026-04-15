package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/pipeline/livestate"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	pipelineCommandRun = func(ctx context.Context, name string, args ...string) error {
		return exec.CommandContext(ctx, name, args...).Run()
	}
	pipelineConfigLoader = func() *config.Config {
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}
		return pCfg
	}
)

var initCmd = &cobra.Command{
	Use:     "init <shard-id>",
	Short:   "Initialize pipeline metadata on a design shard",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild init pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]
		autonomous, _ := cmd.Flags().GetBool("autonomous")

		// Determine start phase from work item type + workflow config
		startPhase := domain.PhaseDesign
		if conn != nil {
			item, err := conn.Get(ctx, id)
			if err == nil {
				repoRoot := findRepoRoot()
				pCfg, _ := config.LoadConfig(repoRoot)
				bootstrap, resolveErr := pipelinestate.ResolveBootstrap(item, pCfg)
				if resolveErr != nil {
					return fmt.Errorf("resolve pipeline bootstrap for %s: %w", id, resolveErr)
				}
				startPhase = bootstrap.StartPhase
				fmt.Printf("Work item type: %s → start phase: %s\n", item.Type, startPhase)
			}
		}

		// Use store if available, fall back to legacy client
		mode := "manual"
		if autonomous {
			mode = "autonomous"
		}

		if cbStore != nil {
			// Idempotent: return existing run rather than duplicating
			if existing, err := cbStore.GetRun(ctx, id); err == nil && existing != nil {
				if outputFormat == "json" {
					s, _ := cliutil.FormatJSON(existing)
					fmt.Println(s)
					return nil
				}
				fmt.Printf("Pipeline run already exists for %s\n", id)
				fmt.Printf("  Phase:    %s\n", existing.CurrentPhase)
				fmt.Printf("  Mode:     %s\n", existing.Mode)
				return nil
			}
			run, err := cbStore.CreateRunWithMode(ctx, id, projectName, startPhase, mode)
			if err != nil {
				return fmt.Errorf("init pipeline: %w", err)
			}
			if outputFormat == "json" {
				s, _ := cliutil.FormatJSON(run)
				fmt.Println(s)
				return nil
			}
			fmt.Printf("Initialised pipeline on %s\n", id)
			fmt.Printf("  Phase:    %s\n", run.CurrentPhase)
			fmt.Printf("  Mode:     %s\n", mode)
			fmt.Printf("  Progress: %s\n", run.CreatedAt.Format(time.RFC3339))
			printNextStep(id, startPhase, domain.ActionInit)
		} else {
			return fmt.Errorf("no store configured — set up ~/.cobuild/config.yaml or COBUILD_* env vars")
		}
		return nil
	},
}

var showCmd = &cobra.Command{
	Use:   "show <shard-id>",
	Short: "Display current pipeline state",
	Long: `Display the current pipeline state for a design, bug, or task.

Reads from the pipeline_runs / pipeline_gates / pipeline_tasks tables that
cobuild init, review, gate, and dispatch all write to.`,
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild show pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		var run *store.PipelineRun
		if cbStore != nil {
			r, err := cbStore.GetRun(ctx, id)
			if err == nil {
				run = r
			} else if !errors.Is(err, store.ErrNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: store lookup failed: %v\n", err)
			}
		}

		if run == nil {
			// Not found. Exit non-zero and write to stderr so scripts that
			// pipe `cobuild show` don't silently succeed with error text on
			// stdout (agent-mycroft flagged this sharp edge).
			fmt.Fprintf(cmd.ErrOrStderr(), "shard %s has no pipeline; run `cobuild init %s` first\n", id, id)
			return fmt.Errorf("no pipeline record for %s", id)
		}

		// Enrich with title, gate history, task list — each optional so a
		// partial store failure still returns useful data.
		var title string
		if conn != nil {
			if item, err := conn.Get(ctx, id); err == nil && item != nil {
				title = item.Title
			}
		}

		var gates []store.PipelineGateRecord
		var tasks []store.PipelineTaskRecord
		if cbStore != nil {
			var err error
			gates, err = cbStore.GetGateHistory(ctx, id)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to load gate history for %s: %v\n", id, err)
			}
			tasks, err = cbStore.ListTasks(ctx, run.ID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to load tasks for %s: %v\n", id, err)
			}
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "run": run}
			if title != "" {
				out["title"] = title
			}
			if len(gates) > 0 {
				out["gates"] = gates
			}
			if len(tasks) > 0 {
				out["tasks"] = tasks
			}
			s, _ := cliutil.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		// Text output
		header := id
		if title != "" {
			header = fmt.Sprintf("%s: %s", id, title)
		}
		fmt.Println(header)

		fmt.Printf("  Phase:          %s\n", run.CurrentPhase)
		fmt.Printf("  Status:         %s\n", run.Status)
		if run.Mode != "" {
			fmt.Printf("  Mode:           %s\n", run.Mode)
		}
		fmt.Printf("  Started:        %s\n", run.CreatedAt.Format("2006-01-02 15:04"))
		fmt.Printf("  Last updated:   %s\n", run.UpdatedAt.Format("2006-01-02 15:04"))

		if len(gates) > 0 {
			passes := 0
			fails := 0
			for _, g := range gates {
				if g.Verdict == "pass" {
					passes++
				} else if g.Verdict == "fail" {
					fails++
				}
			}
			fmt.Printf("  Gates:          %d recorded (%d pass, %d fail)\n", len(gates), passes, fails)
			// Show the most recent gate inline — handy for "what just happened"
			last := gates[len(gates)-1]
			shardID := ""
			if last.ReviewShardID != nil {
				shardID = " " + *last.ReviewShardID
			}
			fmt.Printf("  Latest gate:    %s round %d %s%s\n",
				last.GateName, last.Round, strings.ToUpper(last.Verdict), shardID)
		}

		if len(tasks) > 0 {
			byStatus := map[string]int{}
			for _, t := range tasks {
				byStatus[t.Status]++
			}
			parts := make([]string, 0, len(byStatus))
			for s, n := range byStatus {
				parts = append(parts, fmt.Sprintf("%d %s", n, s))
			}
			fmt.Printf("  Tasks:          %d (%s)\n", len(tasks), strings.Join(parts, ", "))
		}
		return nil
	},
}

var gateCmd = &cobra.Command{
	Use:   "gate <shard-id> <gate-name>",
	Short: "Record a pipeline gate verdict",
	Long:  "Generic gate command for recording review verdicts at any pipeline phase.",
	Args:  cobra.ExactArgs(2),
	Example: `  cobuild gate pf-design-123 readiness-review --verdict pass --readiness 4 --body "All criteria met."
  cobuild gate pf-design-123 custom-gate --verdict fail --body-file notes.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]
		gateName := args[1]

		verdict, _ := cmd.Flags().GetString("verdict")
		body, _ := cmd.Flags().GetString("body")
		bodyFile, _ := cmd.Flags().GetString("body-file")
		readiness, _ := cmd.Flags().GetInt("readiness")

		verdict, err := normalizeGateVerdict(verdict)
		if err != nil {
			return err
		}

		content, err := resolveBody(body, bodyFile)
		if err != nil {
			return err
		}

		repoRoot := findRepoRoot()
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			pCfg = config.DefaultConfig()
		}

		if cbStore == nil {
			return fmt.Errorf("no store configured — set up ~/.cobuild/config.yaml or COBUILD_* env vars")
		}
		gateResult, err := RecordGateVerdict(ctx, conn, cbStore, designID, gateName, verdict, content, readiness, pCfg)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(gateResult)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Recorded gate %q for %s\n", gateResult.GateName, gateResult.DesignID)
		fmt.Printf("  Review shard: %s\n", gateResult.ReviewShardID)
		fmt.Printf("  Round:        %d\n", gateResult.Round)
		fmt.Printf("  Verdict:      %s\n", gateResult.Verdict)
		if gateResult.NextPhase != "" {
			fmt.Printf("  Phase:        %s -> %s\n", gateResult.Phase, gateResult.NextPhase)
		} else {
			fmt.Printf("  Phase:        %s\n", gateResult.Phase)
		}
		if gateResult.Verdict == "pass" {
			printNextStep(designID, gateResult.NextPhase, domain.ActionGatePass)
		} else {
			printNextStep(designID, gateResult.Phase, domain.ActionGateFail)
		}
		return nil
	},
}

var reviewCmd = &cobra.Command{
	Use:   "review <shard-id>",
	Short: "Record a review verdict (Phase 1 readiness-review or task PR-review)",
	Args:  cobra.ExactArgs(1),
	Example: `  cobuild review pf-design-123 --verdict pass --readiness 4 --body "All criteria met."
  cobuild review pf-task-abc   --verdict pass --body "PR looks good."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		shardID := args[0]

		verdict, _ := cmd.Flags().GetString("verdict")
		readiness, _ := cmd.Flags().GetInt("readiness")
		body, _ := cmd.Flags().GetString("body")
		bodyFile, _ := cmd.Flags().GetString("body-file")

		verdict, err := normalizeGateVerdict(verdict)
		if err != nil {
			return err
		}

		// Pick the gate from the readiness flag, not the pipeline phase.
		// readiness > 0 → caller is recording a readiness-review (Phase 1
		// design gate), so require the 1-5 score. readiness == 0 (default
		// when --readiness wasn't passed) → caller is recording a PR
		// review verdict from the dispatched-review runner script.
		//
		// Earlier (cb-3b091b) this derived gate from pipeline_run.CurrentPhase
		// — but that broke for any task whose phase had already advanced past
		// "review" (e.g. to "done" after a previous successful review), and
		// for direct calls where the lookup returned ErrNotFound. The flag
		// presence is the caller's actual intent and doesn't depend on
		// transient pipeline state.
		gateName := domain.GateReview
		if readiness > 0 {
			gateName = domain.GateReadinessReview
			if readiness > 5 {
				return fmt.Errorf("--readiness must be 1-5 for readiness-review, got %d", readiness)
			}
		}

		content, err := resolveBody(body, bodyFile)
		if err != nil {
			return err
		}

		repoRoot := findRepoRoot()
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			pCfg = config.DefaultConfig()
		}

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}

		recordedReadiness := 0
		if gateName == domain.GateReadinessReview {
			recordedReadiness = readiness
		}
		result, err := RecordGateVerdict(ctx, conn, cbStore, shardID, gateName, verdict, content, recordedReadiness, pCfg)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(result)
			fmt.Println(s)
			return nil
		}

		label := "Phase 1 review"
		if gateName == domain.GateReview {
			label = "PR review"
		}
		fmt.Printf("Recorded %s for %s\n", label, result.DesignID)
		fmt.Printf("  Review shard: %s\n", result.ReviewShardID)
		fmt.Printf("  Round:        %d\n", result.Round)
		phaseTransition := result.Phase
		if result.Verdict == "pass" {
			phaseTransition = fmt.Sprintf("%s -> %s", result.Phase, result.NextPhase)
			if result.NextPhase == "" && gateName == domain.GateReadinessReview {
				phaseTransition = "design -> decompose"
			}
		}
		if gateName == domain.GateReadinessReview {
			fmt.Printf("  Verdict:      %s (%d/5)\n", result.Verdict, readiness)
		} else {
			fmt.Printf("  Verdict:      %s\n", result.Verdict)
		}
		fmt.Printf("  Phase:        %s\n", phaseTransition)
		if result.Verdict == "pass" {
			printNextStep(shardID, result.NextPhase, domain.ActionGatePass)
		} else {
			printNextStep(shardID, result.Phase, domain.ActionGateFail)
		}
		return nil
	},
}

var decomposeCmd = &cobra.Command{
	Use:     "decompose <shard-id>",
	Short:   "Record Phase 2 decomposition verdict",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild decompose pf-design-123 --verdict pass --body "Tasks are well-defined."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]

		verdict, _ := cmd.Flags().GetString("verdict")
		body, _ := cmd.Flags().GetString("body")
		bodyFile, _ := cmd.Flags().GetString("body-file")

		verdict, err := normalizeGateVerdict(verdict)
		if err != nil {
			return err
		}

		content, err := resolveBody(body, bodyFile)
		if err != nil {
			return err
		}

		repoRoot := findRepoRoot()
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			pCfg = config.DefaultConfig()
		}

		var overlapWarnings []fileOverlapWarning
		if verdict == "pass" {
			if err := validateSingleRepoChildTasks(ctx, conn, designID); err != nil {
				return err
			}
			// Block the gate if any two new sibling tasks touch the same
			// file. Parallel dispatch of overlapping tasks causes merge
			// conflicts and duplicate code (cb-7cda32).
			siblingOverlaps, err := collectSiblingFileOverlapProblems(ctx, conn, designID)
			if err != nil {
				return fmt.Errorf("collect sibling overlap: %w", err)
			}
			if err := renderSiblingFileOverlapError(siblingOverlaps); err != nil {
				return err
			}
			overlapWarnings, err = collectDecomposeFileOverlapWarnings(ctx, conn, designID, repoRoot)
			if err != nil {
				return fmt.Errorf("collect file-overlap warnings: %w", err)
			}
		}

		var result *GateVerdictResult
		if cbStore != nil {
			if verdict == "pass" {
				if err := ValidateDecompositionTaskRepos(ctx, conn, designID, projectName); err != nil {
					return err
				}
			}
			result, err = RecordGateVerdict(ctx, conn, cbStore, designID, domain.GateDecompositionReview, verdict, content, 0, pCfg)
		} else {
			return fmt.Errorf("no store configured")
		}
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(result)
			fmt.Println(s)
			return nil
		}

		phaseTransition := result.Phase
		if result.Verdict == "pass" {
			phaseTransition = fmt.Sprintf("%s -> %s", result.Phase, result.NextPhase)
			if result.NextPhase == "" {
				phaseTransition = "decompose -> implement"
			}
		}
		fmt.Printf("Recorded Phase 2 decomposition for %s\n", result.DesignID)
		fmt.Printf("  Decompose shard: %s\n", result.ReviewShardID)
		fmt.Printf("  Round:           %d\n", result.Round)
		fmt.Printf("  Verdict:         %s\n", result.Verdict)
		fmt.Printf("  Phase:           %s\n", phaseTransition)
		if warningOutput := renderFileOverlapWarnings(overlapWarnings); warningOutput != "" {
			fmt.Println()
			fmt.Println(warningOutput)
		}
		return nil
	},
}

var investigateCmd = &cobra.Command{
	Use:     "investigate <bug-id>",
	Short:   "Record bug investigation verdict",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild investigate pf-71f0cf --verdict pass --body "Root cause: struct field mismatch..."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		bugID := args[0]

		verdict, _ := cmd.Flags().GetString("verdict")
		body, _ := cmd.Flags().GetString("body")
		bodyFile, _ := cmd.Flags().GetString("body-file")

		verdict, err := normalizeGateVerdict(verdict)
		if err != nil {
			return err
		}

		content, err := resolveBody(body, bodyFile)
		if err != nil {
			return err
		}

		repoRoot := findRepoRoot()
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			pCfg = config.DefaultConfig()
		}

		var result *GateVerdictResult
		if cbStore != nil {
			result, err = RecordGateVerdict(ctx, conn, cbStore, bugID, "investigation", verdict, content, 0, pCfg)
		} else {
			return fmt.Errorf("no store configured")
		}
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(result)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Recorded investigation for %s\n", result.DesignID)
		fmt.Printf("  Review shard: %s\n", result.ReviewShardID)
		fmt.Printf("  Round:        %d\n", result.Round)
		fmt.Printf("  Verdict:      %s\n", result.Verdict)
		if result.NextPhase != "" {
			fmt.Printf("  Phase:        %s → %s\n", result.Phase, result.NextPhase)
		}
		if result.Verdict == "pass" {
			printNextStep(bugID, result.NextPhase, domain.ActionGatePass)
		} else {
			printNextStep(bugID, domain.PhaseInvestigate, domain.ActionGateFail)
		}
		return nil
	},
}

var auditCmd = &cobra.Command{
	Use:     "audit <shard-id>",
	Short:   "Show pipeline audit trail",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild audit pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]

		// Try store first, fall back to legacy
		var phase, status string
		if cbStore != nil {
			run, err := cbStore.GetRun(ctx, designID)
			if err == nil {
				phase = run.CurrentPhase
				status = run.Status
			}
		}
		shard, _ := conn.Get(ctx, designID)
		title := designID
		if shard != nil {
			title = shard.Title
		}

		fmt.Printf("%s: %s\n", designID, title)
		fmt.Printf("Phase: %s | Status: %s\n", phase, status)
		fmt.Println()

		// Gate history from store
		var gates []store.PipelineGateRecord
		var sessions []store.SessionRecord
		if cbStore != nil {
			var err error
			gates, err = cbStore.GetGateHistory(ctx, designID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to load gate history for %s: %v\n", designID, err)
			}
			sessions, err = cbStore.ListSessions(ctx, designID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to load sessions for %s: %v\n", designID, err)
			}
		}
		lifecycleEvents := sessionLifecycleEvents(sessions)

		if len(gates) == 0 && len(lifecycleEvents) == 0 {
			fmt.Println("No gate records found.")
			return nil
		}

		if outputFormat == "json" {
			out := map[string]any{
				"id":     designID,
				"phase":  phase,
				"status": status,
				"gates":  gates,
			}
			if len(lifecycleEvents) > 0 {
				out["sessions"] = lifecycleEvents
			}
			s, _ := cliutil.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Println("TIMELINE")
		type auditTimelineEntry struct {
			timestamp time.Time
			print     func()
		}
		entries := make([]auditTimelineEntry, 0, len(gates)+len(lifecycleEvents))
		for _, g := range gates {
			gate := g
			entries = append(entries, auditTimelineEntry{
				timestamp: gate.CreatedAt,
				print: func() {
					ts := gate.CreatedAt.Format("2006-01-02 15:04")
					shardID := ""
					if gate.ReviewShardID != nil {
						shardID = *gate.ReviewShardID
					}
					fmt.Printf("  %s  %-22s  Round %d  %-4s   %s\n", ts, gate.GateName, gate.Round, strings.ToUpper(gate.Verdict), shardID)
					if gate.Body != nil && *gate.Body != "" {
						fmt.Printf("    %s\n", cliutil.Truncate(*gate.Body, 100))
					}
				},
			})
		}
		for _, e := range lifecycleEvents {
			event := e
			entries = append(entries, auditTimelineEntry{
				timestamp: event.Timestamp,
				print: func() {
					ts := event.Timestamp.Format("2006-01-02 15:04")
					fmt.Printf("  %s  %-22s  %-10s  %s\n", ts, "session "+event.Status, event.TaskID, event.Phase)
					if event.Note != "" {
						fmt.Printf("    %s\n", cliutil.Truncate(event.Note, 100))
					}
				},
			})
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].timestamp.Before(entries[j].timestamp)
		})
		for _, entry := range entries {
			entry.print()
		}

		return nil
	},
}

// lock / unlock / lock-check / update commands were MVP-era cobuild
// features that wrote into the legacy shards.metadata.pipeline JSONB
// blob via internal/client. They had no store equivalents, zero
// reference from the rest of the CLI, zero tests, and no downstream
// users by the time cb-3f5be6 / cb-b2f3ac retired internal/client.
// Removed outright rather than ported.

var resetCmd = &cobra.Command{
	Use:   "reset <shard-id>",
	Short: "Reset a pipeline run to a given phase",
	Long: `Resets a pipeline run back to a specified phase, clearing all gates, tasks,
and session records. The run is marked active so the poller (or cobuild orchestrate)
will pick it up again.

Use --phase to specify the target phase (default: the start phase for the work item type).`,
	Args:    cobra.ExactArgs(1),
	Example: "  cobuild reset cp-b25138\n  cobuild reset cp-b25138 --phase decompose",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]
		phase, _ := cmd.Flags().GetString("phase")
		keepWorktree, _ := cmd.Flags().GetBool("keep-worktree")
		forceClosePRs, _ := cmd.Flags().GetBool("force-close-prs")

		return runPipelineReset(ctx, id, resetOptions{
			Phase:         phase,
			KeepWorktree:  keepWorktree,
			ForceClosePRs: forceClosePRs,
		})
	},
}

type resetOptions struct {
	Phase         string
	KeepWorktree  bool
	ForceClosePRs bool
}

type resetTaskPRState struct {
	TaskID string
	Repo   string
	URL    string
	Open   bool
}

type resetTaskBranchState struct {
	TaskID string
	Repo   string
}

type resetTaskState struct {
	TaskID       string
	WorktreePath string
	Repo         string
	PRURL        string
}

func runPipelineReset(ctx context.Context, id string, opts resetOptions) error {
	phase := opts.Phase

	if cbStore == nil {
		return fmt.Errorf("no store configured")
	}

	existing, err := cbStore.GetRun(ctx, id)
	if err != nil {
		return fmt.Errorf("no pipeline run for %s: %w", id, err)
	}

	if phase == "" {
		phase = domain.PhaseDesign
		if conn != nil {
			item, err := conn.Get(ctx, id)
			if err == nil && item != nil {
				bootstrap, resolveErr := pipelinestate.ResolveBootstrap(item, pipelineConfigLoader())
				if resolveErr != nil {
					return fmt.Errorf("resolve pipeline bootstrap for %s: %w", id, resolveErr)
				}
				phase = bootstrap.StartPhase
			}
		}
	}

	sessions, err := cbStore.ListSessions(ctx, id)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	tasks, err := cbStore.ListTasks(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	taskStates := collectResetTaskState(ctx, id, sessions, tasks)

	fmt.Printf("Resetting %s: %s/%s → %s/active\n",
		id, existing.CurrentPhase, existing.Status, phase)

	if killed, err := killDesignOrchestrateProcesses(ctx, id); err != nil {
		fmt.Printf("Warning: failed to inspect orchestrate processes: %v\n", err)
	} else if killed > 0 {
		fmt.Printf("  Killed %d orchestrate process(es)\n", killed)
	}

	if killed, err := killDesignTmuxWindows(ctx, id); err != nil {
		fmt.Printf("Warning: failed to inspect tmux windows: %v\n", err)
	} else if killed > 0 {
		fmt.Printf("  Killed %d stale tmux window(s)\n", killed)
	}

	cancelled := 0
	for _, session := range sessions {
		if session.Status != "running" {
			continue
		}
		if err := cbStore.EndSession(ctx, session.ID, store.SessionResult{
			ExitCode:       -1,
			Status:         "cancelled",
			CompletionNote: fmt.Sprintf("Cancelled by cobuild reset %s", id),
		}); err != nil {
			fmt.Printf("Warning: failed to cancel session %s: %v\n", session.ID, err)
			continue
		}
		cancelled++
	}
	// Belt-and-braces: catch any running sessions whose design_id was
	// misrecorded during dispatch (e.g. parent-edge lookup failed). This is
	// the path that historically left stale sessions blocking re-dispatch,
	// requiring multiple resets to clear (cb-f93173 #2). Also covers child
	// tasks for a design reset and individual task rows for a task reset.
	if extra, err := cbStore.CancelRunningSessionsForShard(ctx, id); err != nil {
		fmt.Printf("Warning: failed to sweep stale sessions for %s: %v\n", id, err)
	} else {
		cancelled += extra
	}
	// Also sweep by each known child task ID in case their sessions carry
	// a design_id that doesn't match this shard (e.g. tasks moved between
	// designs, or dispatched with stale parent metadata).
	for _, task := range tasks {
		if task.TaskShardID == "" || task.TaskShardID == id {
			continue
		}
		if extra, err := cbStore.CancelRunningSessionsForShard(ctx, task.TaskShardID); err != nil {
			fmt.Printf("Warning: failed to sweep sessions for task %s: %v\n", task.TaskShardID, err)
		} else {
			cancelled += extra
		}
	}
	if cancelled > 0 {
		fmt.Printf("  Cancelled %d running session(s)\n", cancelled)
	}

	if !opts.KeepWorktree {
		removed := 0
		for _, state := range taskStates {
			if strings.TrimSpace(state.WorktreePath) == "" {
				continue
			}
			if err := removeResetWorktree(ctx, state.TaskID, state.WorktreePath); err != nil {
				fmt.Printf("Warning: failed to remove worktree for %s: %v\n", state.TaskID, err)
				continue
			}
			if conn != nil {
				_ = conn.SetMetadata(ctx, state.TaskID, domain.MetaWorktreePath, "")
				_ = conn.SetMetadata(ctx, state.TaskID, domain.MetaSessionID, "")
			}
			removed++
		}
		if removed > 0 {
			fmt.Printf("  Removed %d worktree(s)\n", removed)
		}
	}

	preserveTasks := shouldPreserveTasksForResetPhase(phase)
	if err := cbStore.ResetRun(ctx, id, phase); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}
	if preserveTasks && len(tasks) > 0 {
		if err := restoreResetTasks(ctx, existing.ID, tasks); err != nil {
			return fmt.Errorf("restore tasks after reset: %w", err)
		}
		fmt.Printf("  Restored %d pipeline task(s) for phase %s\n", len(tasks), phase)
	}

	if conn != nil {
		item, err := conn.Get(ctx, id)
		if err == nil && item != nil && item.Status != "open" {
			if err := conn.UpdateStatus(ctx, id, "open"); err != nil {
				fmt.Printf("Warning: failed to reopen work item: %v\n", err)
			} else {
				fmt.Printf("  Work item %s → open\n", id)
			}
		}
	}

	prs, branches, err := inspectResetGitState(ctx, id, taskStates)
	if err != nil {
		fmt.Printf("Warning: failed to inspect PR/branch state: %v\n", err)
	}
	if len(prs) > 0 {
		if opts.ForceClosePRs {
			closed := 0
			for _, pr := range prs {
				if !pr.Open {
					continue
				}
				number := mustParsePRNumber(pr.URL)
				if number <= 0 {
					fmt.Printf("Warning: failed to parse PR number from %s\n", pr.URL)
					continue
				}
				if err := livestate.ClosePR(ctx, execCommandCombinedOutput, pr.Repo, number, fmt.Sprintf("Closed by cobuild reset %s", id)); err != nil {
					fmt.Printf("Warning: failed to close PR %s: %v\n", pr.URL, err)
					continue
				}
				closed++
			}
			if closed > 0 {
				fmt.Printf("  Closed %d open PR(s)\n", closed)
			}
		} else {
			fmt.Println("  Open unmerged PRs:")
			for _, pr := range prs {
				fmt.Printf("    %s (%s) — rerun review or close with `cobuild reset %s --force-close-prs`\n", pr.URL, pr.TaskID, id)
			}
		}
	}
	if len(branches) > 0 {
		fmt.Println("  Open branches:")
		for _, branch := range branches {
			fmt.Printf("    %s:%s — inspect or delete manually once the reset is confirmed\n", branch.Repo, branch.TaskID)
		}
	}

	fmt.Printf("  Pipeline reset to phase: %s\n", phase)
	printNextStep(id, phase, "reset")
	return nil
}

func init() {
	// gate flags
	gateCmd.Flags().String("verdict", "", "Gate verdict: 'pass' or 'fail' (required)")
	gateCmd.Flags().String("body", "", "Findings text")
	gateCmd.Flags().String("body-file", "", "Read findings from file")
	gateCmd.Flags().Int("readiness", 0, "Optional readiness score")

	// review flags
	reviewCmd.Flags().String("verdict", "", "Review verdict: 'pass' or 'fail' (required)")
	reviewCmd.Flags().Int("readiness", 0, "Readiness score 1-5 (required)")
	reviewCmd.Flags().String("body", "", "Findings text")
	reviewCmd.Flags().String("body-file", "", "Read findings from file")

	// decompose flags
	decomposeCmd.Flags().String("verdict", "", "Decomposition verdict: 'pass' or 'fail' (required)")
	decomposeCmd.Flags().String("body", "", "Findings text")
	decomposeCmd.Flags().String("body-file", "", "Read findings from file")

	// investigate flags
	investigateCmd.Flags().String("verdict", "", "Investigation verdict: 'pass' or 'fail' (required)")
	investigateCmd.Flags().String("body", "", "Investigation findings text")
	investigateCmd.Flags().String("body-file", "", "Read findings from file")

	// init flags
	initCmd.Flags().Bool("autonomous", false, "Submit for autonomous processing (poller handles all phases)")

	// reset flags
	resetCmd.Flags().String("phase", "", "Target phase to reset to (default: start phase for work item type)")
	resetCmd.Flags().Bool("keep-worktree", false, "Keep worktrees on disk while resetting pipeline state")
	resetCmd.Flags().Bool("force-close-prs", false, "Close open unmerged PRs discovered during reset")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(gateCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(decomposeCmd)
	rootCmd.AddCommand(investigateCmd)
	rootCmd.AddCommand(auditCmd)
}

func collectResetTaskState(ctx context.Context, designID string, sessions []store.SessionRecord, tasks []store.PipelineTaskRecord) []resetTaskState {
	byTask := map[string]*resetTaskState{}
	add := func(taskID string) *resetTaskState {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			return nil
		}
		if existing, ok := byTask[taskID]; ok {
			return existing
		}
		state := &resetTaskState{TaskID: taskID}
		byTask[taskID] = state
		return state
	}

	for _, session := range sessions {
		state := add(session.TaskID)
		if state == nil {
			continue
		}
		if state.WorktreePath == "" && session.WorktreePath != nil {
			state.WorktreePath = strings.TrimSpace(*session.WorktreePath)
		}
		if state.Repo == "" {
			state.Repo = detectGitHubRepoFromWorktree(ctx, state.WorktreePath)
		}
		if state.PRURL == "" && session.PRURL != nil {
			state.PRURL = strings.TrimSpace(*session.PRURL)
		}
	}
	for _, task := range tasks {
		add(task.TaskShardID)
	}
	add(designID)

	if conn != nil {
		for _, state := range byTask {
			if state.WorktreePath == "" {
				state.WorktreePath, _ = conn.GetMetadata(ctx, state.TaskID, domain.MetaWorktreePath)
			}
			if state.PRURL == "" {
				state.PRURL, _ = conn.GetMetadata(ctx, state.TaskID, domain.MetaPRURL)
			}
			if state.Repo == "" {
				if repo, _, err := parsePRURL(state.PRURL); err == nil {
					state.Repo = repo
				}
			}
			if state.Repo == "" {
				state.Repo = detectGitHubRepoFromWorktree(ctx, state.WorktreePath)
			}
		}
	}

	out := make([]resetTaskState, 0, len(byTask))
	for _, state := range byTask {
		out = append(out, *state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

func shouldPreserveTasksForResetPhase(phase string) bool {
	switch phase {
	case domain.PhaseImplement, domain.PhaseReview, domain.PhaseDeploy, domain.PhaseRetrospective, domain.PhaseDone:
		return true
	default:
		return false
	}
}

func restoreResetTasks(ctx context.Context, pipelineID string, tasks []store.PipelineTaskRecord) error {
	for _, task := range tasks {
		if err := cbStore.AddTask(ctx, pipelineID, task.TaskShardID, task.DesignID, task.Wave); err != nil {
			return err
		}
		if task.Status != "" && task.Status != domain.StatusPending {
			if err := cbStore.UpdateTaskStatus(ctx, task.TaskShardID, task.Status); err != nil {
				return err
			}
		}
	}
	return nil
}

func killDesignOrchestrateProcesses(ctx context.Context, designID string) (int, error) {
	rows, err := livestate.CollectProcesses(ctx, execCommandCombinedOutput, time.Now())
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, row := range rows {
		if row.Kind != "orchestrate" || row.TargetID != designID {
			continue
		}
		if err := pipelineCommandRun(ctx, "kill", strconv.Itoa(row.PID)); err != nil {
			return killed, err
		}
		killed++
	}
	return killed, nil
}

func killDesignTmuxWindows(ctx context.Context, designID string) (int, error) {
	pCfg := pipelineConfigLoader()
	tmuxExec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = tmuxCommandArgs(pCfg, args...)
		}
		return execCommandCombinedOutput(ctx, name, args...)
	}
	windows, err := livestate.CollectTmux(ctx, tmuxExec)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, window := range windows {
		if window.TargetID != designID && window.WindowName != designID && !strings.HasPrefix(window.WindowName, designID+".") {
			continue
		}
		if _, err := pipelinestate.KillOrphanTmuxWindow(ctx, pipelinestate.RecoveryDependencies{
			Exec: tmuxExec,
		}, pipelinestate.TmuxWindow{
			SessionName: window.SessionName,
			WindowID:    window.WindowID,
			WindowName:  window.WindowName,
		}); err != nil {
			return killed, err
		}
		killed++
	}
	return killed, nil
}

func removeResetWorktree(ctx context.Context, taskID, worktreePath string) error {
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return nil
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	repoRoot := repoRootForWorktree(ctx, worktreePath)
	if repoRoot != "" {
		return worktree.Remove(ctx, repoRoot, worktreePath, taskID)
	}
	return os.RemoveAll(worktreePath)
}

func repoRootForWorktree(ctx context.Context, worktreePath string) string {
	out, err := execCommandOutput(ctx, "git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	commonDir := strings.TrimSpace(string(out))
	if commonDir == "" {
		return ""
	}
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return filepath.Dir(filepath.Dir(commonDir))
}

func detectGitHubRepoFromWorktree(ctx context.Context, worktreePath string) string {
	if strings.TrimSpace(worktreePath) == "" {
		return ""
	}
	out, err := execCommandOutput(ctx, "git", "-C", worktreePath, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return parseGitHubRepoURL(strings.TrimSpace(string(out)))
}

func parseGitHubRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"git@github.com:", "https://github.com/"} {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(raw, prefix), ".git")
		}
	}
	return ""
}

func inspectResetGitState(ctx context.Context, designID string, taskStates []resetTaskState) ([]resetTaskPRState, []resetTaskBranchState, error) {
	taskIDs := map[string]bool{}
	repoSet := map[string]bool{}
	repoByTask := map[string]string{}
	for _, state := range taskStates {
		taskIDs[state.TaskID] = true
		if state.Repo != "" {
			repoSet[state.Repo] = true
			repoByTask[state.TaskID] = state.Repo
		}
		if state.PRURL != "" {
			if repo, _, err := parsePRURL(state.PRURL); err == nil {
				repoSet[repo] = true
				if repoByTask[state.TaskID] == "" {
					repoByTask[state.TaskID] = repo
				}
			}
		}
	}
	if ownerRepo := pipelineConfigLoader().GitHub.OwnerRepo; ownerRepo != "" {
		repoSet[ownerRepo] = true
	}

	repos := make([]string, 0, len(repoSet))
	for repo := range repoSet {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	openPRs := []resetTaskPRState{}
	if len(repos) > 0 {
		prs, err := livestate.CollectPRs(ctx, execCommandCombinedOutput, repos, time.Now())
		if err != nil {
			return nil, nil, err
		}
		for _, pr := range prs {
			taskID := pr.TaskID
			if taskID == "" {
				taskID = pr.Branch
			}
			if !taskIDs[taskID] {
				continue
			}
			openPRs = append(openPRs, resetTaskPRState{
				TaskID: taskID,
				Repo:   pr.Repo,
				URL:    pr.URL,
				Open:   true,
			})
			repoByTask[taskID] = pr.Repo
		}
	}

	openPRByTask := map[string]bool{}
	for _, pr := range openPRs {
		openPRByTask[pr.TaskID] = true
	}

	branches := []resetTaskBranchState{}
	for taskID, repo := range repoByTask {
		if taskID == designID || repo == "" || openPRByTask[taskID] {
			continue
		}
		exists, err := remoteBranchExists(ctx, repo, taskID)
		if err != nil {
			return openPRs, nil, err
		}
		if exists {
			branches = append(branches, resetTaskBranchState{TaskID: taskID, Repo: repo})
		}
	}
	sort.Slice(openPRs, func(i, j int) bool { return openPRs[i].TaskID < openPRs[j].TaskID })
	sort.Slice(branches, func(i, j int) bool { return branches[i].TaskID < branches[j].TaskID })
	return openPRs, branches, nil
}

func remoteBranchExists(ctx context.Context, repo, branch string) (bool, error) {
	if repo == "" || branch == "" {
		return false, nil
	}
	out, err := execCommandCombinedOutput(ctx, "gh", "api", fmt.Sprintf("repos/%s/branches/%s", repo, branch))
	if err == nil {
		return true, nil
	}
	msg := strings.ToLower(err.Error() + " " + string(out))
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return false, nil
	}
	return false, err
}

func mustParsePRNumber(prURL string) int {
	_, number, err := parsePRURL(prURL)
	if err != nil {
		return 0
	}
	return number
}
