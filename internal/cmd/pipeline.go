package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:     "init <shard-id>",
	Short:   "Initialize pipeline metadata on a design shard",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild init pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		state, err := cbClient.PipelineInit(ctx, id)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "pipeline": state}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Initialised pipeline on %s\n", id)
		fmt.Printf("  Phase:    %s\n", state.Phase)
		fmt.Printf("  Progress: %s\n", state.LastProgress)
		return nil
	},
}

var showCmd = &cobra.Command{
	Use:     "show <shard-id>",
	Short:   "Display current pipeline state",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild show pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		state, err := cbClient.PipelineGet(ctx, id)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			out := map[string]any{"id": id, "pipeline": state}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Pipeline: %s\n", id)
		fmt.Printf("  Phase:          %s\n", state.Phase)
		if state.LockedBy != nil {
			fmt.Printf("  Locked by:      %s\n", *state.LockedBy)
		}
		if state.LockExpires != nil {
			fmt.Printf("  Lock expires:   %s\n", state.LockExpires.Format("2006-01-02 15:04"))
		}
		if len(state.WaitingFor) > 0 {
			fmt.Printf("  Waiting for:    %s\n", strings.Join(state.WaitingFor, ", "))
		}
		fmt.Printf("  Last progress:  %s\n", state.LastProgress)
		if len(state.TaskShards) > 0 {
			fmt.Printf("  Task shards:    %s\n", strings.Join(state.TaskShards, ", "))
		}
		fmt.Printf("  Tokens:         %d\n", state.CumulativeTokens)
		if len(state.IterationCounts) > 0 {
			parts := make([]string, 0, len(state.IterationCounts))
			for phase, count := range state.IterationCounts {
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

		var result *GateVerdictResult
		if cbStore != nil {
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

var auditCmd = &cobra.Command{
	Use:     "audit <shard-id>",
	Short:   "Show pipeline audit trail",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild audit pf-design-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]

		entries, err := cbClient.PipelineAudit(ctx, designID)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(entries)
			fmt.Println(s)
			return nil
		}

		state, stateErr := cbClient.PipelineGet(ctx, designID)
		shard, shardErr := conn.Get(ctx, designID)

		title := designID
		if shardErr == nil {
			title = shard.Title
		}
		phase := "unknown"
		status := "unknown"
		if stateErr == nil {
			phase = state.Phase
			status = "active"
		}

		fmt.Printf("%s: %s\n", designID, title)
		fmt.Printf("Phase: %s | Status: %s\n", phase, status)
		fmt.Println()

		if len(entries) == 0 {
			fmt.Println("No gate records found.")
			return nil
		}

		fmt.Println("TIMELINE")
		for _, e := range entries {
			verdictUpper := strings.ToUpper(e.Verdict)
			ts := e.Timestamp.Format("2006-01-02 15:04")
			fmt.Printf("  %s  %-22s  Round %d  %-4s   %s\n",
				ts, e.GateName, e.Round, verdictUpper, e.ReviewShardID)
			if e.Body != "" {
				bodyPreview := e.Body
				if len(bodyPreview) > 100 {
					bodyPreview = bodyPreview[:100] + "..."
				}
				fmt.Printf("    %s\n", bodyPreview)
			}
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

	// lock flags
	lockCmd.Flags().String("session", "", "Session ID for the lock")

	// update flags
	updateCmd.Flags().String("phase", "", "Pipeline phase")
	updateCmd.Flags().String("waiting-for", "", "JSON array of shard IDs to wait for")
	updateCmd.Flags().String("add-task", "", "Shard ID to append to task_shards")
	updateCmd.Flags().Int("tokens", 0, "Token count to add to cumulative_tokens")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(gateCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(decomposeCmd)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(lockCmd)
	rootCmd.AddCommand(unlockCmd)
	rootCmd.AddCommand(lockCheckCmd)
	rootCmd.AddCommand(updateCmd)
}
