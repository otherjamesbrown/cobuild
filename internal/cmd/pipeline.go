package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/pipeline/livestate"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	pipelineCommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	}
	pipelineCommandCombinedOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
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
		startPhase := "design"
		if conn != nil {
			item, err := conn.Get(ctx, id)
			if err == nil {
				repoRoot := findRepoRoot()
				pCfg, _ := config.LoadConfig(repoRoot)
				if pCfg != nil {
					sp := pCfg.StartPhaseForType(item.Type)
					if sp != "" {
						startPhase = sp
					}
				}
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
					s, _ := client.FormatJSON(existing)
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
				s, _ := client.FormatJSON(run)
				fmt.Println(s)
				return nil
			}
			fmt.Printf("Initialised pipeline on %s\n", id)
			fmt.Printf("  Phase:    %s\n", run.CurrentPhase)
			fmt.Printf("  Mode:     %s\n", mode)
			fmt.Printf("  Progress: %s\n", run.CreatedAt.Format(time.RFC3339))
			printNextStep(id, startPhase, "init")
		} else if cbClient != nil {
			state, err := cbClient.PipelineInit(ctx, id)
			if err != nil {
				return err
			}
			fmt.Printf("Initialised pipeline on %s\n", id)
			fmt.Printf("  Phase:    %s\n", state.Phase)
			fmt.Printf("  Progress: %s\n", state.LastProgress)
		} else {
			return fmt.Errorf("no store or client configured")
		}
		return nil
	},
}

var showCmd = &cobra.Command{
	Use:   "show <shard-id>",
	Short: "Display current pipeline state",
	Long: `Display the current pipeline state for a design, bug, or task.

Reads from the pipeline_runs / pipeline_gates / pipeline_tasks tables that
cobuild init, review, gate, and dispatch all write to. Falls back to the
legacy shards.metadata.pipeline JSONB field only if the store is
unavailable — this is the pre-cobuild cxp-pipeline-MVP path and is
retained for old designs that were never migrated.`,
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild show pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		// Preferred path: read from the store tables that init/review/gate
		// all write to. This is what cobuild audit and cobuild status use;
		// before cb-a8ca46, cobuild show was the only holdout reading from
		// legacy shard metadata, so an initialised pipeline would look
		// "empty" to show while audit/status reported it correctly.
		var run *store.PipelineRun
		if cbStore != nil {
			r, err := cbStore.GetRun(ctx, id)
			if err == nil {
				run = r
			} else if !errors.Is(err, store.ErrNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: store lookup failed: %v\n", err)
			}
		}

		// Legacy fallback: read from shards.metadata.pipeline. This path is
		// reached only if the store has no record — either because the store
		// isn't configured or because this is an old MVP-era pipeline that
		// never got migrated into pipeline_runs.
		var legacyState *client.PipelineState
		if run == nil && cbClient != nil {
			if s, err := cbClient.PipelineGet(ctx, id); err == nil {
				legacyState = s
			}
		}

		if run == nil && legacyState == nil {
			// Not found in either storage. Exit non-zero and write to stderr
			// so scripts that pipe `cobuild show` don't silently succeed with
			// error text on stdout (agent-mycroft flagged this sharp edge).
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
		if run != nil && cbStore != nil {
			gates, _ = cbStore.GetGateHistory(ctx, id)
			tasks, _ = cbStore.ListTasks(ctx, run.ID)
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id}
			if title != "" {
				out["title"] = title
			}
			if run != nil {
				out["run"] = run
				if len(gates) > 0 {
					out["gates"] = gates
				}
				if len(tasks) > 0 {
					out["tasks"] = tasks
				}
			} else if legacyState != nil {
				out["pipeline"] = legacyState
				out["source"] = "legacy-metadata"
			}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		// Text output
		header := id
		if title != "" {
			header = fmt.Sprintf("%s: %s", id, title)
		}
		fmt.Println(header)

		if run != nil {
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
		}

		// Legacy path output (old MVP metadata) — preserve original format
		// verbatim so any existing scripts depending on it keep working.
		fmt.Printf("  Phase:          %s  (legacy metadata)\n", legacyState.Phase)
		if legacyState.LockedBy != nil {
			fmt.Printf("  Locked by:      %s\n", *legacyState.LockedBy)
		}
		if legacyState.LockExpires != nil {
			fmt.Printf("  Lock expires:   %s\n", legacyState.LockExpires.Format("2006-01-02 15:04"))
		}
		if len(legacyState.WaitingFor) > 0 {
			fmt.Printf("  Waiting for:    %s\n", strings.Join(legacyState.WaitingFor, ", "))
		}
		fmt.Printf("  Last progress:  %s\n", legacyState.LastProgress)
		if len(legacyState.TaskShards) > 0 {
			fmt.Printf("  Task shards:    %s\n", strings.Join(legacyState.TaskShards, ", "))
		}
		fmt.Printf("  Tokens:         %d\n", legacyState.CumulativeTokens)
		if len(legacyState.IterationCounts) > 0 {
			parts := make([]string, 0, len(legacyState.IterationCounts))
			for phase, count := range legacyState.IterationCounts {
				parts = append(parts, fmt.Sprintf("%s=%d", phase, count))
			}
			fmt.Printf("  Iterations:     %s\n", strings.Join(parts, ", "))
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

		if verdict == "" {
			return fmt.Errorf("--verdict is required")
		}
		if verdict != "pass" && verdict != "fail" {
			return fmt.Errorf("--verdict must be 'pass' or 'fail', got %q", verdict)
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

		// Use new store+connector orchestration if available, fall back to legacy
		var gateResult *GateVerdictResult
		if cbStore != nil {
			gateResult, err = RecordGateVerdict(ctx, conn, cbStore, designID, gateName, verdict, content, readiness, pCfg)
		} else if cbClient != nil {
			legacyResult, legacyErr := cbClient.PipelineGatePass(ctx, designID, gateName, verdict, content, readiness, pCfg)
			if legacyErr != nil {
				return legacyErr
			}
			gateResult = &GateVerdictResult{
				DesignID:      legacyResult.DesignID,
				GateName:      legacyResult.GateName,
				Phase:         legacyResult.Phase,
				Round:         legacyResult.Round,
				Verdict:       legacyResult.Verdict,
				ReviewShardID: legacyResult.ReviewShardID,
				NextPhase:     legacyResult.NextPhase,
			}
		} else {
			return fmt.Errorf("no store or client configured")
		}
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(gateResult)
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
			printNextStep(designID, gateResult.NextPhase, "gate-pass")
		} else {
			printNextStep(designID, gateResult.Phase, "gate-fail")
		}
		return nil
	},
}

var reviewCmd = &cobra.Command{
	Use:     "review <shard-id>",
	Short:   "Record Phase 1 readiness review verdict",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild review pf-design-123 --verdict pass --readiness 4 --body "All criteria met."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]

		verdict, _ := cmd.Flags().GetString("verdict")
		readiness, _ := cmd.Flags().GetInt("readiness")
		body, _ := cmd.Flags().GetString("body")
		bodyFile, _ := cmd.Flags().GetString("body-file")

		if verdict == "" {
			return fmt.Errorf("--verdict is required")
		}
		if verdict != "pass" && verdict != "fail" {
			return fmt.Errorf("--verdict must be 'pass' or 'fail', got %q", verdict)
		}
		if readiness < 1 || readiness > 5 {
			return fmt.Errorf("--readiness must be 1-5, got %d", readiness)
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
			result, err = RecordGateVerdict(ctx, conn, cbStore, designID, "readiness-review", verdict, content, readiness, pCfg)
		} else {
			return fmt.Errorf("no store configured")
		}
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(result)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Recorded Phase 1 review for %s\n", result.DesignID)
		fmt.Printf("  Review shard: %s\n", result.ReviewShardID)
		fmt.Printf("  Round:        %d\n", result.Round)
		phaseTransition := result.Phase
		if result.Verdict == "pass" {
			phaseTransition = fmt.Sprintf("%s -> %s", result.Phase, result.NextPhase)
			if result.NextPhase == "" {
				phaseTransition = "design -> decompose"
			}
		}
		fmt.Printf("  Verdict:      %s (%d/5)\n", result.Verdict, readiness)
		fmt.Printf("  Phase:        %s\n", phaseTransition)
		if result.Verdict == "pass" {
			printNextStep(designID, result.NextPhase, "gate-pass")
		} else {
			printNextStep(designID, "design", "gate-fail")
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

		if verdict == "" {
			return fmt.Errorf("--verdict is required")
		}
		if verdict != "pass" && verdict != "fail" {
			return fmt.Errorf("--verdict must be 'pass' or 'fail', got %q", verdict)
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

		if verdict == "pass" {
			if err := validateSingleRepoChildTasks(ctx, conn, designID); err != nil {
				return err
			}
		}

		var result *GateVerdictResult
		if cbStore != nil {
			if verdict == "pass" {
				if err := ValidateDecompositionTaskRepos(ctx, conn, designID, projectName); err != nil {
					return err
				}
			}
			result, err = RecordGateVerdict(ctx, conn, cbStore, designID, "decomposition-review", verdict, content, 0, pCfg)
		} else {
			return fmt.Errorf("no store configured")
		}
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(result)
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

		if verdict == "" {
			return fmt.Errorf("--verdict is required")
		}
		if verdict != "pass" && verdict != "fail" {
			return fmt.Errorf("--verdict must be 'pass' or 'fail', got %q", verdict)
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
			s, _ := client.FormatJSON(result)
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
			printNextStep(bugID, result.NextPhase, "gate-pass")
		} else {
			printNextStep(bugID, "investigate", "gate-fail")
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
		if phase == "" && cbClient != nil {
			state, err := cbClient.PipelineGet(ctx, designID)
			if err == nil {
				phase = state.Phase
				status = "active"
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
			gates, _ = cbStore.GetGateHistory(ctx, designID)
			sessions, _ = cbStore.ListSessions(ctx, designID)
		}
		lifecycleEvents := sessionLifecycleEvents(sessions)

		if len(gates) == 0 && len(lifecycleEvents) == 0 {
			// Fall back to legacy audit
			if cbClient != nil {
				entries, err := cbClient.PipelineAudit(ctx, designID)
				if err == nil && len(entries) > 0 {
					if outputFormat == "json" {
						s, _ := client.FormatJSON(entries)
						fmt.Println(s)
						return nil
					}
					fmt.Println("TIMELINE")
					for _, e := range entries {
						ts := e.Timestamp.Format("2006-01-02 15:04")
						fmt.Printf("  %s  %-22s  Round %d  %-4s   %s\n", ts, e.GateName, e.Round, strings.ToUpper(e.Verdict), e.ReviewShardID)
						if e.Body != "" {
							fmt.Printf("    %s\n", client.Truncate(e.Body, 100))
						}
					}
					return nil
				}
			}
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
			s, _ := client.FormatJSON(out)
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
						fmt.Printf("    %s\n", client.Truncate(*gate.Body, 100))
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
						fmt.Printf("    %s\n", client.Truncate(event.Note, 100))
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

var lockCmd = &cobra.Command{
	Use:     "lock <shard-id>",
	Short:   "Acquire pipeline lock",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild lock pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		sessionID, _ := cmd.Flags().GetString("session")
		if sessionID == "" {
			sessionID = fmt.Sprintf("%s-%d", agentFlag, time.Now().Unix())
		}

		state, err := cbClient.PipelineLock(ctx, id, sessionID)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "pipeline": state}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Locked pipeline on %s\n", id)
		fmt.Printf("  Locked by:    %s\n", *state.LockedBy)
		fmt.Printf("  Lock expires: %s\n", state.LockExpires.Format("2006-01-02 15:04"))
		return nil
	},
}

var unlockCmd = &cobra.Command{
	Use:     "unlock <shard-id>",
	Short:   "Release pipeline lock",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild unlock pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		state, err := cbClient.PipelineUnlock(ctx, id)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "pipeline": state}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Unlocked pipeline on %s\n", id)
		fmt.Printf("  Phase: %s\n", state.Phase)
		return nil
	},
}

var lockCheckCmd = &cobra.Command{
	Use:     "lock-check <shard-id>",
	Short:   "Check pipeline lock status",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild lock-check pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		status, state, err := cbClient.PipelineLockCheck(ctx, id)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{
				"id":          id,
				"lock_status": status,
				"pipeline":    state,
			}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Pipeline %s: %s\n", id, status)
		if state.LockedBy != nil {
			fmt.Printf("  Locked by:    %s\n", *state.LockedBy)
		}
		if state.LockExpires != nil {
			fmt.Printf("  Lock expires: %s\n", state.LockExpires.Format("2006-01-02 15:04"))
		}
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update <shard-id>",
	Short: "Update pipeline state on a design shard",
	Args:  cobra.ExactArgs(1),
	Example: `  cobuild update pf-design-123 --phase implement
  cobuild update pf-design-123 --add-task pf-task-456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		phaseFlag, _ := cmd.Flags().GetString("phase")
		waitingForFlag, _ := cmd.Flags().GetString("waiting-for")
		addTaskFlag, _ := cmd.Flags().GetString("add-task")
		tokensFlag, _ := cmd.Flags().GetInt("tokens")

		if phaseFlag == "" && waitingForFlag == "" && addTaskFlag == "" && tokensFlag == 0 {
			return fmt.Errorf("at least one of --phase, --waiting-for, --add-task, or --tokens is required")
		}

		var phase *string
		if phaseFlag != "" {
			phase = &phaseFlag
		}

		var waitingFor *json.RawMessage
		if waitingForFlag != "" {
			raw := json.RawMessage(waitingForFlag)
			waitingFor = &raw
		}

		var addTask *string
		if addTaskFlag != "" {
			addTask = &addTaskFlag
		}

		var addTokens *int
		if tokensFlag != 0 {
			addTokens = &tokensFlag
		}

		state, err := cbClient.PipelineUpdate(ctx, id, phase, waitingFor, addTask, addTokens)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "pipeline": state}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Updated pipeline on %s\n", id)
		fmt.Printf("  Phase:    %s\n", state.Phase)
		fmt.Printf("  Tokens:   %d\n", state.CumulativeTokens)
		if len(state.TaskShards) > 0 {
			fmt.Printf("  Tasks:    %s\n", strings.Join(state.TaskShards, ", "))
		}
		return nil
	},
}

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
		phase = "design"
		if conn != nil {
			item, err := conn.Get(ctx, id)
			if err == nil && item != nil {
				if sp := pipelineConfigLoader().StartPhaseForType(item.Type); sp != "" {
					phase = sp
				}
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
				_ = conn.SetMetadata(ctx, state.TaskID, "worktree_path", "")
				_ = conn.SetMetadata(ctx, state.TaskID, "session_id", "")
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
				if err := livestate.ClosePR(ctx, pipelineCommandCombinedOutput, pr.Repo, number, fmt.Sprintf("Closed by cobuild reset %s", id)); err != nil {
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

	// lock flags
	lockCmd.Flags().String("session", "", "Session ID for the lock")

	// update flags
	updateCmd.Flags().String("phase", "", "Pipeline phase")
	updateCmd.Flags().String("waiting-for", "", "JSON array of shard IDs to wait for")
	updateCmd.Flags().String("add-task", "", "Shard ID to append to task_shards")
	updateCmd.Flags().Int("tokens", 0, "Token count to add to cumulative_tokens")

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
	rootCmd.AddCommand(lockCmd)
	rootCmd.AddCommand(unlockCmd)
	rootCmd.AddCommand(lockCheckCmd)
	rootCmd.AddCommand(updateCmd)
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
				state.WorktreePath, _ = conn.GetMetadata(ctx, state.TaskID, "worktree_path")
			}
			if state.PRURL == "" {
				state.PRURL, _ = conn.GetMetadata(ctx, state.TaskID, "pr_url")
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
	case "implement", "review", "deploy", "retrospective", "done":
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
		if task.Status != "" && task.Status != "pending" {
			if err := cbStore.UpdateTaskStatus(ctx, task.TaskShardID, task.Status); err != nil {
				return err
			}
		}
	}
	return nil
}

func killDesignOrchestrateProcesses(ctx context.Context, designID string) (int, error) {
	rows, err := livestate.CollectProcesses(ctx, pipelineCommandCombinedOutput, time.Now())
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
	windows, err := livestate.CollectTmux(ctx, pipelineCommandCombinedOutput)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, window := range windows {
		if window.TargetID != designID && window.WindowName != designID && !strings.HasPrefix(window.WindowName, designID+".") {
			continue
		}
		if _, err := pipelinestate.KillOrphanTmuxWindow(ctx, pipelinestate.RecoveryDependencies{
			Exec: pipelineCommandCombinedOutput,
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
	out, err := pipelineCommandOutput(ctx, "git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
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
	out, err := pipelineCommandOutput(ctx, "git", "-C", worktreePath, "remote", "get-url", "origin")
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
		prs, err := livestate.CollectPRs(ctx, pipelineCommandCombinedOutput, repos, time.Now())
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
	out, err := pipelineCommandCombinedOutput(ctx, "gh", "api", fmt.Sprintf("repos/%s/branches/%s", repo, branch))
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
