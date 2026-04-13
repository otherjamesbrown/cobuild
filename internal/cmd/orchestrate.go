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

	"github.com/otherjamesbrown/cobuild/internal/orchestrator"
	"github.com/spf13/cobra"
)

type orchestrateCommandRunner interface {
	Run(ctx context.Context, shardID string) error
}

var newOrchestrateRunner = func(opts orchestrator.Options) orchestrateCommandRunner {
	// Wire up implement/review support so orchestrate can drive
	// the full pipeline end-to-end, not just gate phases.
	opts.Tasks = orchestrator.StoreTaskSource{Store: cbStore}
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

		timeout, _ := cmd.Flags().GetDuration("timeout")
		pollInterval, _ := cmd.Flags().GetDuration("poll-interval")
		stepMode, _ := cmd.Flags().GetBool("step")
		interactiveMode, _ := cmd.Flags().GetBool("interactive")

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
		if opts.StepMode {
			opts.BeforeStep = newBeforeStepPrompt(cmd.InOrStdin(), cmd.OutOrStdout())
		}

		err := newOrchestrateRunner(opts).Run(cmd.Context(), args[0])

		// End-of-run maintenance: reconcile any stale state this run created
		// or surfaced (zombie sessions, orphan tmux windows, inconsistent
		// state). Always runs, even on failure, so dead-ends don't leave
		// behind garbage. Best-effort — failures don't change the exit code.
		runEndOfRunMaintenance(cmd.Context(), cmd.OutOrStdout())

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
// orchestrate run completes. It cleans up zombies/orphans this run may have
// left behind, so the next dashboard view stays signal-rich (no accumulating
// noise from prior runs).
func runEndOfRunMaintenance(ctx context.Context, w io.Writer) {
	if cbStore == nil || conn == nil {
		return
	}
	fmt.Fprintln(w, "\n[maintenance] Reconciling stale state...")
	subCmd, _, err := rootCmd.Find([]string{"doctor"})
	if err != nil || subCmd == nil {
		fmt.Fprintln(w, "[maintenance] doctor command not found; skipping")
		return
	}
	subCmd.SetOut(w)
	if err := subCmd.Flags().Set("fix", "true"); err != nil {
		fmt.Fprintf(w, "[maintenance] failed to set --fix: %v\n", err)
		return
	}
	if err := subCmd.RunE(subCmd, nil); err != nil {
		fmt.Fprintf(w, "[maintenance] doctor --fix returned: %v\n", err)
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
	rootCmd.AddCommand(orchestrateCmd)
}
