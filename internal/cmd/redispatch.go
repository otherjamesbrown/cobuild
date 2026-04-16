package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var redispatchCmd = &cobra.Command{
	Use:   "redispatch <shard-id>",
	Short: "Kill the running agent and re-dispatch with fresh context",
	Long: `Kills any running session for the shard, ends the session record in the
database, and re-dispatches with a fresh read of the shard body. Use this
when you've updated the shard's scope after dispatch and want the agent to
pick up the changes immediately rather than waiting for the current run to
fail and retry.

With --reset-context (default true), the dispatch re-reads the shard body
and regenerates .cobuild/dispatch-context.md from scratch. This is the
normal case — dispatch always reads fresh, but redispatch makes the intent
explicit.`,
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild redispatch cb-abc123
  cobuild redispatch --reset-context cb-abc123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		// Find and kill any running session for this shard.
		sessions, err := cbStore.ListSessions(ctx, taskID)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}

		killed := 0
		for _, s := range sessions {
			if s.Status != "running" {
				continue
			}

			// Kill tmux window if we can identify it.
			sessionName, windowName := sessionTarget(s)
			pCfg := pipelineConfigLoader()
			target := fmt.Sprintf("%s:%s", sessionName, windowName)
			_ = tmuxRun(ctx, pCfg, "kill-window", "-t", target)

			if err := cbStore.EndSession(ctx, s.ID, store.SessionResult{
				ExitCode:       -1,
				Status:         "cancelled",
				CompletionNote: "killed by cobuild redispatch --reset-context",
			}); err != nil {
				fmt.Printf("Warning: failed to end session %s: %v\n", s.ID, err)
			} else {
				fmt.Printf("Killed session %s (tmux %s)\n", s.ID, target)
				killed++
			}
		}

		if killed == 0 {
			fmt.Printf("No running sessions for %s.\n", taskID)
		}

		// Reset task status so dispatch accepts it.
		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		if task.Status == domain.StatusNeedsReview {
			if err := conn.UpdateStatus(ctx, taskID, domain.StatusInProgress); err != nil {
				fmt.Printf("Warning: failed to reset status: %v\n", err)
			}
			syncPipelineTaskStatus(ctx, taskID, domain.StatusInProgress)
		}

		// Re-dispatch. dispatch.go re-reads the shard body fresh every time,
		// so this naturally picks up any scope changes appended since the
		// previous dispatch (cb-44a9d7).
		fmt.Printf("Re-dispatching %s with fresh context...\n", taskID)
		out, err := execCommandCombinedOutput(ctx, "cobuild", "dispatch", taskID)
		if err != nil {
			return fmt.Errorf("dispatch failed: %w\n%s", err, string(out))
		}
		fmt.Printf("%s\n", string(out))
		return nil
	},
}

func init() {
	redispatchCmd.Flags().Bool("reset-context", true, "Re-read shard body and regenerate dispatch context (default true)")
	rootCmd.AddCommand(redispatchCmd)
}
