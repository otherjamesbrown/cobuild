package state

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

type RecoveryKind string

const (
	RecoveryCancelOrphanedSession RecoveryKind = "cancel_orphaned_session"
	RecoveryKillOrphanTmuxWindow  RecoveryKind = "kill_orphan_tmux_window"
	RecoveryCompleteStaleRun      RecoveryKind = "complete_stale_run"
)

// RecoveryRecommendation describes one concrete reconciliation action derived
// from a resolved PipelineState.
type RecoveryRecommendation struct {
	Kind     RecoveryKind  `json:"kind"`
	DesignID string        `json:"design_id,omitempty"`
	Reason   string        `json:"reason"`
	Session  *SessionState `json:"session,omitempty"`
	Window   *TmuxWindow   `json:"window,omitempty"`
}

// RecoveryResult reports whether a recovery action changed persisted state.
type RecoveryResult struct {
	Kind    RecoveryKind `json:"kind"`
	Reason  string       `json:"reason"`
	Changed bool         `json:"changed"`
}

// RecoveryStore is the minimal mutable store surface required by recovery actions.
type RecoveryStore interface {
	EndSession(ctx context.Context, id string, result store.SessionResult) error
	UpdateRunPhase(ctx context.Context, designID, phase string) error
	UpdateRunStatus(ctx context.Context, designID, status string) error
}

// RecoveryDependencies defines the mutable dependencies needed by recovery actions.
type RecoveryDependencies struct {
	Store RecoveryStore
	Exec  CommandRunner
}

// RecommendRecoveries returns the concrete reconciliation actions that can be
// applied to a resolved pipeline state.
func RecommendRecoveries(state *PipelineState) []RecoveryRecommendation {
	if state == nil {
		return nil
	}

	recommendations := []RecoveryRecommendation{}
	tmuxAvailable := !hasSourceError(state, "tmux")

	matchedWindows := map[int]bool{}
	if tmuxAvailable {
		for _, session := range state.Sessions {
			if session.Status != "running" {
				continue
			}
			matchIndex := findMatchingWindow(session, state.Tmux)
			if matchIndex >= 0 {
				matchedWindows[matchIndex] = true
				continue
			}
			sessionCopy := session
			recommendations = append(recommendations, RecoveryRecommendation{
				Kind:     RecoveryCancelOrphanedSession,
				DesignID: session.DesignID,
				Reason:   orphanedSessionReason(session),
				Session:  &sessionCopy,
			})
		}

		for index, window := range state.Tmux {
			if matchedWindows[index] {
				continue
			}
			windowCopy := window
			recommendations = append(recommendations, RecoveryRecommendation{
				Kind:     RecoveryKillOrphanTmuxWindow,
				DesignID: state.DesignID,
				Reason:   orphanTmuxWindowReason(window),
				Window:   &windowCopy,
			})
		}
	}

	if shouldCompleteStaleRun(state) {
		recommendations = append(recommendations, RecoveryRecommendation{
			Kind:     RecoveryCompleteStaleRun,
			DesignID: state.DesignID,
			Reason:   staleRunReason(state),
		})
	}

	return recommendations
}

func CancelOrphanedSession(ctx context.Context, deps RecoveryDependencies, session SessionState) (RecoveryResult, error) {
	result := RecoveryResult{
		Kind:   RecoveryCancelOrphanedSession,
		Reason: orphanedSessionReason(session),
	}
	if session.Status != "running" {
		return result, nil
	}
	if deps.Store == nil {
		return result, fmt.Errorf("recovery store is required")
	}
	if err := deps.Store.EndSession(ctx, session.ID, store.SessionResult{
		ExitCode:       -1,
		Status:         "orphaned",
		CompletionNote: result.Reason,
	}); err != nil {
		return result, fmt.Errorf("cancel orphaned session %s: %w", session.ID, err)
	}
	result.Changed = true
	return result, nil
}

func KillOrphanTmuxWindow(ctx context.Context, deps RecoveryDependencies, window TmuxWindow) (RecoveryResult, error) {
	result := RecoveryResult{
		Kind:   RecoveryKillOrphanTmuxWindow,
		Reason: orphanTmuxWindowReason(window),
	}
	target := tmuxWindowTarget(window)
	if target == "" {
		return result, nil
	}

	execFn := deps.Exec
	if execFn == nil {
		execFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}

	if _, err := execFn(ctx, "tmux", "kill-window", "-t", target); err != nil {
		if isMissingTmuxTarget(err) {
			return result, nil
		}
		return result, fmt.Errorf("kill orphan tmux window %s: %w", target, err)
	}
	result.Changed = true
	return result, nil
}

func CompleteStaleRun(ctx context.Context, deps RecoveryDependencies, state *PipelineState) (RecoveryResult, error) {
	result := RecoveryResult{
		Kind:   RecoveryCompleteStaleRun,
		Reason: staleRunReason(state),
	}
	if !shouldCompleteStaleRun(state) {
		return result, nil
	}
	if deps.Store == nil {
		return result, fmt.Errorf("recovery store is required")
	}

	if state.Run.Phase != "done" {
		if err := deps.Store.UpdateRunPhase(ctx, state.DesignID, "done"); err != nil {
			return result, fmt.Errorf("complete stale run %s: update phase: %w", state.DesignID, err)
		}
		result.Changed = true
	}
	if state.Run.Status != "completed" {
		if err := deps.Store.UpdateRunStatus(ctx, state.DesignID, "completed"); err != nil {
			return result, fmt.Errorf("complete stale run %s: update status: %w", state.DesignID, err)
		}
		result.Changed = true
	}
	return result, nil
}

func shouldCompleteStaleRun(state *PipelineState) bool {
	return state != nil &&
		state.Run != nil &&
		state.WorkItem != nil &&
		state.Run.Status == "active" &&
		state.WorkItem.Status == "closed"
}

func orphanedSessionReason(session SessionState) string {
	return fmt.Sprintf("session %s is running but no tmux window exists", session.ID)
}

func orphanTmuxWindowReason(window TmuxWindow) string {
	return fmt.Sprintf("tmux window %s:%s exists but no matching pipeline session exists", window.SessionName, window.WindowName)
}

func staleRunReason(state *PipelineState) string {
	if state == nil {
		return "pipeline run is active but work item is closed"
	}
	return fmt.Sprintf("pipeline %s run is active but work item is closed", state.DesignID)
}

func tmuxWindowTarget(window TmuxWindow) string {
	if window.WindowID != "" {
		return window.WindowID
	}
	if window.SessionName != "" && window.WindowName != "" {
		return fmt.Sprintf("%s:%s", window.SessionName, window.WindowName)
	}
	return ""
}

func isMissingTmuxTarget(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't find window") ||
		strings.Contains(msg, "can't find session") ||
		strings.Contains(msg, "no server running")
}
