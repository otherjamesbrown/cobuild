package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
)

var dispatchTmuxWindowExists = func(ctx context.Context, pCfg *config.Config, sessionName, windowName string) (bool, error) {
	sessionName = strings.TrimSpace(sessionName)
	windowName = strings.TrimSpace(windowName)
	if sessionName == "" || windowName == "" {
		return false, nil
	}

	target := fmt.Sprintf("%s:%s", sessionName, windowName)
	out, err := execCommandCombinedOutput(ctx, "tmux", tmuxCommandArgs(pCfg, "has-session", "-t", target)...)
	if err == nil {
		return true, nil
	}
	if tmuxTargetMissing(err, out) {
		return false, nil
	}
	return false, fmt.Errorf("probe tmux window %s: %w", target, err)
}

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

	if _, reason, err := resetRecoveredDispatchState(ctx, taskID, ""); err != nil {
		return false, "", err
	} else {
		return true, reason, nil
	}
}

// resetRecoveredDispatchState resets state for a dispatch whose agent is gone.
// deadWindowName is the tmux window name of the dispatch that died — used to
// infer whether the session was a review redispatch, a fix redispatch, or an
// initial implement dispatch, so the reset lands in the correct pre-dispatch
// state. Empty string means "unknown" (called from `cobuild recover`) and
// falls back to the safe default of reopening the task.
func resetRecoveredDispatchState(ctx context.Context, taskID, deadWindowName string) (string, string, error) {
	if cbStore == nil || conn == nil {
		return "", "", nil
	}

	workItemStatus := "open"
	pipelineStatus := domain.StatusPending

	// Distinguish review / fix / implement dispatches by the tmux window
	// name pattern set in dispatchTmuxWindowName. Review dispatches prefix
	// with "review-"; fix redispatches reuse the implement-style name but
	// are identifiable by the presence of a pr_url (set by review.go at
	// 769-777 after review requests changes).
	if deadWindowName != "" {
		prURL, _ := conn.GetMetadata(ctx, taskID, domain.MetaPRURL)
		hasPR := strings.TrimSpace(prURL) != ""
		if strings.HasPrefix(deadWindowName, "review-") {
			workItemStatus = domain.StatusNeedsReview
			pipelineStatus = domain.StatusNeedsReview
		} else if hasPR {
			workItemStatus = domain.StatusInProgress
			pipelineStatus = domain.StatusInProgress
		}
	}

	cancelled, err := cbStore.CancelRunningSessionsForShard(ctx, taskID)
	if err != nil {
		return "", "", fmt.Errorf("cancel stale sessions for %s: %w", taskID, err)
	}
	if err := cbStore.UpdateTaskStatus(ctx, taskID, pipelineStatus); err != nil {
		return "", "", fmt.Errorf("reset pipeline task status: %w", err)
	}
	if err := conn.UpdateStatus(ctx, taskID, workItemStatus); err != nil {
		return "", "", fmt.Errorf("reset work item status: %w", err)
	}

	reason := "no tmux window for task"
	if cancelled > 0 {
		reason = fmt.Sprintf("%s (cancelled %d stale session record(s))", reason, cancelled)
	}
	return workItemStatus, reason, nil
}

func tmuxTargetMissing(err error, out []byte) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error() + "\n" + string(out))
	return strings.Contains(text, "can't find window") ||
		strings.Contains(text, "can't find session") ||
		strings.Contains(text, "no server running")
}
