package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

// earlyDeathProbeChecks defines the sample offsets after dispatch. The
// first pass at 10s catches "the agent crashed before its first turn"
// (the pattern seen on cb-0e0482). The second pass at 60s catches slower
// silent deaths. Beyond 60s, a dead agent is better diagnosed via the
// orchestrator's dead-agent recovery (cb-f93173 #1) — same signal, no
// point duplicating.
var earlyDeathProbeChecks = []time.Duration{10 * time.Second, 60 * time.Second}

// startEarlyDeathProbe launches an async goroutine that samples the task's
// tmux window at well-known offsets. If the window is gone before the
// agent has had time to complete its work, marks the session as
// early_death and captures session.log into dispatch-error.log so the
// operator can investigate why.
//
// Fire-and-forget: dispatch returns immediately; the probe outlives the
// command. Uses a fresh background context.
func startEarlyDeathProbe(pCfg *config.Config, sessionID, tmuxSessionName, tmuxWindowName, worktreePath string) {
	go func() {
		ctx := context.Background()
		target := fmt.Sprintf("%s:%s", tmuxSessionName, tmuxWindowName)
		for _, delay := range earlyDeathProbeChecks {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			if tmuxWindowExists(ctx, pCfg, target) {
				continue
			}
			// Window gone. Record and persist diagnostic.
			captureDispatchError(worktreePath, delay, tmuxSessionName, tmuxWindowName)
			if cbStore != nil {
				detail := fmt.Sprintf("tmux window %s gone after %s", target, delay)
				if err := cbStore.MarkSessionEarlyDeath(ctx, sessionID, detail); err != nil {
					internalLogger().Warn("mark-early-death failed", "component", "probe", "session", sessionID, "err", err)
				}
			}
			return
		}
	}()
}

// tmuxWindowExists returns true if a tmux window is present at the given
// target. Errors and empty output count as "not present" — the probe is
// meant to detect missing windows, not to raise false alarms on
// transient tmux failures.
func tmuxWindowExists(ctx context.Context, pCfg *config.Config, target string) bool {
	out, err := tmuxCombinedOutput(ctx, pCfg, "display-message", "-p", "-t", target, "#{window_id}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// captureDispatchError writes what we know about the failed session into
// worktree/.cobuild/dispatch-error.log. Best-effort — the worktree may
// already be gone if the session was cleaned aggressively, in which case
// we skip.
func captureDispatchError(worktreePath string, deathAfter time.Duration, tmuxSessionName, tmuxWindowName string) {
	if worktreePath == "" {
		return
	}
	cobuildDir := filepath.Join(worktreePath, ".cobuild")
	if _, err := os.Stat(cobuildDir); err != nil {
		return
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "=== dispatch-error %s ===\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&buf, "tmux window %s:%s disappeared within %s of dispatch\n", tmuxSessionName, tmuxWindowName, deathAfter)
	fmt.Fprintln(&buf, "This is almost always a silent agent crash — see cb-0e0482 for hypotheses.")

	// Tail session.log so the captured artefact carries whatever output the
	// runner managed to pipe before dying.
	if data, err := os.ReadFile(filepath.Join(cobuildDir, "session.log")); err == nil {
		fmt.Fprintln(&buf, "\n--- session.log ---")
		buf.Write(data)
	} else {
		fmt.Fprintf(&buf, "\nsession.log not readable: %v\n", err)
	}
	// Include dispatch.log too — it has banner + context info the runner
	// writes before handing off to the agent.
	if data, err := os.ReadFile(filepath.Join(cobuildDir, "dispatch.log")); err == nil {
		fmt.Fprintln(&buf, "\n--- dispatch.log ---")
		buf.Write(data)
	}

	errLog := filepath.Join(cobuildDir, "dispatch-error.log")
	if err := os.WriteFile(errLog, []byte(buf.String()), 0644); err != nil {
		internalLogger().Warn("write dispatch-error.log failed", "component", "probe", "path", errLog, "err", err)
	}
}
