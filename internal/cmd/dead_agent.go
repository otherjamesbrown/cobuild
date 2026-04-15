package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
)

// recoverDeadAgent checks whether a task's dispatched agent is still alive.
// If no tmux window and no running session can be found, it cancels any
// stale session rows, resets the task's pipeline_tasks row to "pending",
// and reopens the work item so the next dispatch-wave picks it up.
//
// Returns (recovered, reason, err). recovered=false with nil err means the
// agent appears to be alive (don't reset). recovered=true means the task
// was successfully reset and can be re-dispatched (cb-f93173 #1).
//
// Callers: orchestrator.DeadAgentRecoverer seam, `cobuild recover` CLI.
func recoverDeadAgent(ctx context.Context, taskID string) (bool, string, error) {
	if cbStore == nil || conn == nil {
		return false, "", nil
	}

	// Tmux window is the ground-truth signal for a live agent. The bash
	// runner script exits (and kills its window) when the agent finishes
	// or dies, so "no window" = "no agent" regardless of what the session
	// record still says. Historically the session record alone was trusted,
	// which meant stale 'running' rows looked live forever after an
	// orchestrator crash (cb-d5e1dd #4).
	state, err := pipelinestate.Resolve(ctx, taskID)
	if err != nil && !errors.Is(err, pipelinestate.ErrNotFound) {
		return false, "", fmt.Errorf("resolve state for %s: %w", taskID, err)
	}
	if state != nil && len(state.Tmux) > 0 {
		return false, "", nil
	}

	// Reset.
	reason := "no tmux window for task"
	if cancelled, err := cbStore.CancelRunningSessionsForShard(ctx, taskID); err == nil && cancelled > 0 {
		reason = fmt.Sprintf("%s (cancelled %d stale session record(s))", reason, cancelled)
	}

	if err := cbStore.UpdateTaskStatus(ctx, taskID, domain.StatusPending); err != nil {
		return false, "", fmt.Errorf("reset pipeline task status: %w", err)
	}
	if err := conn.UpdateStatus(ctx, taskID, "open"); err != nil {
		return false, "", fmt.Errorf("reopen work item: %w", err)
	}
	return true, reason, nil
}
