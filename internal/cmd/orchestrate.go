package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/orchestrator"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/spf13/cobra"
)

type orchestrateCommandRunner interface {
	Run(ctx context.Context, shardID string) error
}

var newOrchestrateRunner = func(opts orchestrator.Options) orchestrateCommandRunner {
	// Wire up implement/review support so orchestrate can drive
	// the full pipeline end-to-end, not just gate phases.
	opts.Tasks = orchestrator.StoreTaskSource{Store: cbStore}
	opts.GateHistory = cbStore // store satisfies GateHistorySource via GetGateHistory
	opts.WaveDispatcher = orchestrator.WaveDispatchFunc(func(_ context.Context, designID string) error {
		return dispatchWaveCmd.RunE(dispatchWaveCmd, []string{designID})
	})
	opts.Reviewer = orchestrator.ReviewProcessFunc(func(_ context.Context, shardID string) (orchestrator.ReviewResult, error) {
		err := processReviewCmd.RunE(processReviewCmd, []string{shardID})
		if err != nil {
			return orchestrator.ReviewResult{Outcome: "error", Message: err.Error()}, nil
		}
		// Check task status after process-review to determine outcome
		if conn != nil {
			if task, err := conn.Get(context.Background(), shardID); err == nil {
				switch task.Status {
				case "closed":
					return orchestrator.ReviewResult{Outcome: "merged"}, nil
				case "in_progress":
					return orchestrator.ReviewResult{Outcome: "redispatched"}, nil
				}
			}
		}
		return orchestrator.ReviewResult{Outcome: "waiting"}, nil
	})
	opts.DeadAgentRecoverer = orchestrator.DeadAgentRecoverFunc(recoverDeadAgent)
	if conn != nil {
		// ShardTypeSource lets the implement phase route task-type shards
		// through direct dispatch instead of wave dispatch (cb-55f364).
		opts.ShardTypeSource = orchestrator.ShardTypeFunc(func(ctx context.Context, shardID string) (string, error) {
			item, err := conn.Get(ctx, shardID)
			if err != nil {
				return "", err
			}
			if item == nil {
				return "", nil
			}
			return item.Type, nil
		})
	}

	return orchestrator.NewRunner(
		orchestrator.StorePhaseSource{Store: cbStore},
		orchestrator.DispatchFunc(func(_ context.Context, shardID string) error {
			return dispatchCmd.RunE(dispatchCmd, []string{shardID})
		}),
		opts,
	)
}

var orchestrateCmd = &cobra.Command{
	Use:   "orchestrate <shard-id>",
	Short: "Run a pipeline in the foreground until completion or a human stop point",
	Long: `Drives the current pipeline phase in the foreground with structured log output.

This is the preferred path for end-to-end orchestration from a shell or agent.
It exits 0 on completion, 2 when deploy approval is required, and 1 on real
failures.`,
	Args: cobra.ExactArgs(1),
	Example: `  cobuild orchestrate cb-design-123
  cobuild orchestrate cb-design-123 --step
  cobuild orchestrate cb-design-123 --timeout 45m --poll-interval 10s`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}
		shardID := args[0]

		// Auto-init: if no pipeline run exists yet for this shard, create
		// one in manual mode. Previously required a separate `cobuild init`
		// or `cobuild run` call; refusing here was pure friction for the
		// common case (cb-d5e1dd #6).
		if _, err := cbStore.GetRun(cmd.Context(), shardID); err != nil {
			startPhase := "design"
			if conn != nil {
				if item, itemErr := conn.Get(cmd.Context(), shardID); itemErr == nil && item != nil {
					repoRoot := findRepoRoot()
					pCfg, _ := config.LoadConfig(repoRoot)
					bootstrap, resolveErr := pipelinestate.ResolveBootstrap(item, pCfg)
					if resolveErr != nil {
						return fmt.Errorf("resolve pipeline bootstrap for %s: %w", shardID, resolveErr)
					}
					startPhase = bootstrap.StartPhase
					fmt.Fprintf(cmd.OutOrStdout(), "Auto-init: no pipeline run for %s (type=%s) — creating at phase %s (manual mode)\n",
						shardID, item.Type, startPhase)
				}
			}
			if _, createErr := cbStore.CreateRunWithMode(cmd.Context(), shardID, projectName, startPhase, "manual"); createErr != nil {
				return fmt.Errorf("auto-init pipeline for %s: %w", shardID, createErr)
			}
		}

		timeout, _ := cmd.Flags().GetDuration("timeout")
		pollInterval, _ := cmd.Flags().GetDuration("poll-interval")
		stepMode, _ := cmd.Flags().GetBool("step")
		interactiveMode, _ := cmd.Flags().GetBool("interactive")
		logLevelFlag, _ := cmd.Flags().GetString("log-level")
		if env := os.Getenv("COBUILD_LOG_LEVEL"); env != "" && logLevelFlag == "" {
			logLevelFlag = env
		}

		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, os.Interrupt)
		defer signal.Stop(signalCh)

		opts := orchestrator.Options{
			PollInterval: pollInterval,
			PhaseTimeout: timeout,
			StepMode:     stepMode || interactiveMode,
			Output:       cmd.OutOrStdout(),
			SignalCh:     signalCh,
		}
		if logLevelFlag != "" {
			opts.OnEvent = orchestrator.LevelFilteredHandler(cmd.OutOrStdout(), orchestrator.ParseLevel(logLevelFlag))
		}
		if opts.StepMode {
			opts.BeforeStep = newBeforeStepPrompt(cmd.InOrStdin(), cmd.OutOrStdout())
		}

		err := newOrchestrateRunner(opts).Run(cmd.Context(), shardID)

		// End-of-run maintenance: reconcile any stale state this run created
		// or surfaced (zombie sessions, orphan tmux windows, inconsistent
		// state). Scoped to THIS design only — reconciling across every
		// pipeline in the project would cancel sibling orchestrates' live
		// sessions (cb-d5e1dd #3). Best-effort: failures don't change exit code.
		runEndOfRunMaintenance(cmd.Context(), cmd.OutOrStdout(), shardID)

		if err == nil {
			return nil
		}
		if errors.Is(err, orchestrator.ErrDeployApprovalRequired) {
			return commandErrorWithExitCodeAndPrint(err, 2, false)
		}
		return commandErrorWithExitCode(err, 1)
	},
}

// runEndOfRunMaintenance invokes the doctor's reconciliation pass after an
// orchestrate run completes, scoped to the design this run drove. It cleans
// up zombies/orphans the run may have left behind — but only for its own
// design, so parallel orchestrates don't cancel each other's live sessions
// (cb-d5e1dd #3).
func runEndOfRunMaintenance(ctx context.Context, w io.Writer, designID string) {
	if cbStore == nil || conn == nil {
		return
	}
	fmt.Fprintf(w, "\n[maintenance] Reconciling stale state for %s...\n", designID)
	if _, err := runDoctor(ctx, doctorOptions{PipelineID: designID, Fix: true}); err != nil {
		fmt.Fprintf(w, "[maintenance] doctor returned: %v\n", err)
	}
}

func newBeforeStepPrompt(in io.Reader, out io.Writer) func(ctx context.Context, shardID, phase string) error {
	reader := bufio.NewReader(in)
	return func(_ context.Context, shardID, phase string) error {
		if _, err := fmt.Fprintf(out, "Next phase for %s: %s. Press Enter to continue or Ctrl-C to stop.\n", shardID, phase); err != nil {
			return err
		}
		_, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read step confirmation: %w", err)
		}
		return nil
	}
}

func init() {
	orchestrateCmd.Flags().Bool("step", false, "Pause before each dispatch and wait for Enter")
	orchestrateCmd.Flags().Bool("interactive", false, "Alias for --step")
	orchestrateCmd.Flags().Duration("timeout", 2*time.Hour, "Maximum wait per phase before exiting")
	orchestrateCmd.Flags().Duration("poll-interval", 30*time.Second, "Polling interval while waiting for phase changes")
	orchestrateCmd.Flags().String("log-level", "", "Output level: debug|info|warn|error (default info; COBUILD_LOG_LEVEL env fallback)")
	rootCmd.AddCommand(orchestrateCmd)
}
