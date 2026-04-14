package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var recoverCmd = &cobra.Command{
	Use:   "recover <shard-id>",
	Short: "Clear stale dispatch state without resetting gates or phase",
	Long: `Scoped cleanup that restores a pipeline to a dispatchable state without
discarding completed work.

recover does four things:
  1. Cancels stale pipeline_sessions rows still marked 'running'
  2. Kills orphaned tmux windows for the design and its child tasks
  3. Resets in_progress tasks whose agent is dead back to pending/open
  4. Does NOT touch passed gates, does NOT change pipeline phase

Use this when dispatch refuses because of accumulated stale state from a
failed attempt. Use 'cobuild reset' when you actually want to rewind the
pipeline's phase.`,
	Args: cobra.ExactArgs(1),
	Example: `  cobuild recover cb-design-123
  cobuild recover cb-task-abc`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		shardID := args[0]
		return runRecover(ctx, cmd, shardID)
	},
}

func runRecover(ctx context.Context, cmd *cobra.Command, shardID string) error {
	out := cmd.OutOrStdout()

	run, runErr := cbStore.GetRun(ctx, shardID)
	if runErr != nil || run == nil {
		fmt.Fprintf(out, "No pipeline run found for %s. Attempting best-effort cleanup anyway.\n", shardID)
	} else {
		fmt.Fprintf(out, "Recovering %s (phase=%s, status=%s — gates/phase will be preserved)\n",
			shardID, run.CurrentPhase, run.Status)
	}

	// 1. Cancel stale sessions for the shard tree. Covers both design- and
	// task-keyed session rows regardless of whether the pipeline_tasks
	// table tracks them correctly.
	totalCancelled := 0
	if n, err := cbStore.CancelRunningSessionsForShard(ctx, shardID); err != nil {
		fmt.Fprintf(out, "Warning: sweep sessions for %s failed: %v\n", shardID, err)
	} else {
		totalCancelled += n
	}

	// 2. Also sweep child-task sessions by their own task_id. Necessary when
	// session rows got orphaned with a mismatched design_id.
	var childTasks []store.PipelineTaskRecord
	if run != nil {
		var err error
		childTasks, err = cbStore.ListTasks(ctx, run.ID)
		if err != nil {
			fmt.Fprintf(out, "Warning: list tasks for %s failed: %v\n", shardID, err)
		}
	}
	for _, t := range childTasks {
		if t.TaskShardID == "" || t.TaskShardID == shardID {
			continue
		}
		if n, err := cbStore.CancelRunningSessionsForShard(ctx, t.TaskShardID); err != nil {
			fmt.Fprintf(out, "Warning: sweep sessions for %s failed: %v\n", t.TaskShardID, err)
		} else {
			totalCancelled += n
		}
	}
	if totalCancelled > 0 {
		fmt.Fprintf(out, "  Cancelled %d stale session record(s)\n", totalCancelled)
	}

	// 3. Kill orphaned tmux windows for the design and its child tasks.
	if killed, err := killDesignTmuxWindows(ctx, shardID); err != nil {
		fmt.Fprintf(out, "Warning: failed to sweep tmux windows for %s: %v\n", shardID, err)
	} else if killed > 0 {
		fmt.Fprintf(out, "  Killed %d orphan tmux window(s) for %s\n", killed, shardID)
	}
	for _, t := range childTasks {
		if t.TaskShardID == "" || t.TaskShardID == shardID {
			continue
		}
		if killed, err := killDesignTmuxWindows(ctx, t.TaskShardID); err != nil {
			fmt.Fprintf(out, "Warning: failed to sweep tmux windows for %s: %v\n", t.TaskShardID, err)
		} else if killed > 0 {
			fmt.Fprintf(out, "  Killed %d orphan tmux window(s) for %s\n", killed, t.TaskShardID)
		}
	}

	// 4. Reset in_progress tasks whose agent is dead to pending/open.
	resetCount := 0
	for _, t := range childTasks {
		if t.Status != "in_progress" {
			continue
		}
		ok, reason, err := recoverDeadAgent(ctx, t.TaskShardID)
		if err != nil {
			fmt.Fprintf(out, "Warning: recover %s: %v\n", t.TaskShardID, err)
			continue
		}
		if ok {
			fmt.Fprintf(out, "  Reset %s → pending/open (%s)\n", t.TaskShardID, reason)
			resetCount++
		}
	}

	// 5. If the shard itself is a bug (has its own pipeline run and is
	// currently in_progress), probe it too.
	if run != nil {
		if item, err := conn.Get(ctx, shardID); err == nil && item != nil && item.Status == "in_progress" {
			ok, reason, err := recoverDeadAgent(ctx, shardID)
			if err != nil {
				fmt.Fprintf(out, "Warning: recover %s: %v\n", shardID, err)
			} else if ok {
				fmt.Fprintf(out, "  Reset %s → pending/open (%s)\n", shardID, reason)
				resetCount++
			}
		}
	}

	if resetCount == 0 && totalCancelled == 0 {
		fmt.Fprintln(out, "  Nothing to recover — pipeline appears healthy.")
	}

	fmt.Fprintf(out, "\nRecovery complete. Next: `cobuild orchestrate %s` to continue.\n", shardID)
	return nil
}

func init() {
	rootCmd.AddCommand(recoverCmd)
}
