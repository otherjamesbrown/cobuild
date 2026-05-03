// Package codex implements the Runtime interface for OpenAI's Codex CLI
// (codex exec). Codex was purpose-built for non-interactive script use, so
// this runtime is substantially simpler than claudecode: no trust dialog
// pre-acceptance, no settings-file hook registration, and no transcript
// scraping (usage data is inline in the --json event stream).
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

// Runtime is the Codex implementation of runtime.Runtime.
// Exported so test code can construct it directly; production dispatch goes
// through the registry via runtime.Get("codex").
type Runtime struct{}

// New returns a fresh Codex runtime.
func New() *Runtime { return &Runtime{} }

func init() {
	runtime.MustRegister(New())
}

// Name implements runtime.Runtime.
func (r *Runtime) Name() string { return "codex" }

// ContextFile implements runtime.Runtime. Codex reads project instructions
// from AGENTS.md (the tool-neutral convention also adopted by Cursor and
// others). cobuild dispatch appends its dispatch-context pointer section to
// this file when the active runtime is codex.
func (r *Runtime) ContextFile() string { return "AGENTS.md" }

// PreDispatch is a no-op for Codex. `codex exec` is non-interactive so it
// does not prompt for workspace trust — empirically verified against a
// fresh /tmp git worktree with no entry in ~/.codex/config.toml.
func (r *Runtime) PreDispatch(_ context.Context, _ string) error {
	return nil
}

// WriteSettings is a no-op for Codex. Unlike Claude Code, Codex has no
// settings.local.json or hook system; completion detection relies on the
// `codex exec` process exiting cleanly and the runner script invoking
// cobuild complete inline afterwards.
func (r *Runtime) WriteSettings(_ string) error {
	return nil
}

// BuildRunnerScript returns the bash runner body that dispatch writes to a
// temp file and spawns in a tmux window. The script:
//
//  1. exports the COBUILD_* env vars (parity with claudecode runtime)
//  2. loads the prompt from the temp prompt file and saves a copy for debug
//  3. runs `codex exec --json --full-auto -C "$PWD" -o last-message.md
//     [--model M] [<ExtraFlags>] "$PROMPT"` with stdout redirected to
//     .cobuild/session.log and stderr to .cobuild/session.err
//  4. captures the thread_id from the first `thread.started` event in the
//     log (needed for `codex exec resume` in review cycles)
//  5. sums token usage across `turn.completed` events and appends a summary
//     line to .cobuild/dispatch.log
//  6. removes itself ($0) and runs `cobuild complete <task-id>` from
//     $COBUILD_REPO_ROOT
//
// No Stop hook is involved: `codex exec` exits cleanly on completion, so
// the runner-level cobuild complete is the only path, not a fallback.
func (r *Runtime) BuildRunnerScript(in runtime.RunnerInput) (string, error) {
	if in.WorktreePath == "" {
		return "", fmt.Errorf("BuildRunnerScript: WorktreePath required")
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("BuildRunnerScript: TaskID required")
	}
	if in.PromptFile == "" {
		return "", fmt.Errorf("BuildRunnerScript: PromptFile required")
	}
	if in.RepoRoot == "" {
		return "", fmt.Errorf("BuildRunnerScript: RepoRoot required")
	}

	// Base codex flags. --full-auto = -a on-request --sandbox workspace-write.
	// Phases that need to create/modify external state (decompose creates
	// child tasks, investigate creates fix tasks) require full system access
	// so the agent can reach the database and work-item APIs.
	flags := "--json --full-auto"
	if in.ExtraFlags != "" {
		flags = in.ExtraFlags
	} else if needsFullAccess(in.Phase) {
		flags = "--json --dangerously-bypass-approvals-and-sandbox"
	}
	if in.Model != "" {
		flags += " --model " + in.Model
	}

	script := fmt.Sprintf(`#!/bin/bash
cd '%s'
export COBUILD_DISPATCH=true
export COBUILD_SESSION_ID='%s'
export COBUILD_HOOKS_DIR='%s'
export COBUILD_TASK_ID='%s'
export COBUILD_REPO_ROOT='%s'
export COBUILD_PHASE='%s'
LOGFILE=".cobuild/dispatch.log"
mkdir -p .cobuild
echo "$COBUILD_SESSION_ID" > .cobuild/session_id
echo "[$(date)] Dispatch starting (runtime: codex, session: $COBUILD_SESSION_ID)" >> "$LOGFILE"

# Load prompt from temp file
PROMPT_FILE='%s'
if [ ! -f "$PROMPT_FILE" ]; then
    echo "[$(date)] ERROR: Prompt file not found: $PROMPT_FILE" >> "$LOGFILE"
    exit 1
fi

# Save a copy for debugging
cp "$PROMPT_FILE" .cobuild/last-prompt.md
PROMPT=$(cat "$PROMPT_FILE")
echo "[$(date)] Prompt loaded (${#PROMPT} chars)" >> "$LOGFILE"
rm -f "$PROMPT_FILE"

# Heartbeat loop (cb-a08acd / cb-0e0482). Writes a timestamp to
# .cobuild/heartbeat every 30s while this script is alive. The poller's
# inspectSessionHealth reads this file's mtime as a liveness signal.
(
    while true; do
        date +%%s > .cobuild/heartbeat
        sleep 30
    done
) &
HEARTBEAT_PID=$!
# Ensure heartbeat stops and session ends when the main script exits.
# Session-end is a backstop for cb-0e0482-class bugs.
trap "kill $HEARTBEAT_PID 2>/dev/null; cobuild session-end $COBUILD_SESSION_ID 2>/dev/null" EXIT

# Run codex exec non-interactively. JSONL events stream to session.log,
# final agent message to last-message.md. codex exec exits cleanly on
# completion — no Stop hook / completion-signal workaround needed.
codex exec %s -C "$PWD" \
    --output-last-message .cobuild/last-message.md \
    "$PROMPT" \
    > .cobuild/session.log 2> .cobuild/session.err
CODEX_EXIT=$?
echo "[$(date)] codex exec exited: $CODEX_EXIT" >> "$LOGFILE"

# Error capture (cb-1d8abc): if codex exited non-zero, write a
# dispatch-error.log with context + tail of session logs for post-mortem.
if [ "$CODEX_EXIT" != "0" ]; then
    {
        echo "=== dispatch-error $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) ==="
        echo "codex exec exited with non-zero status: $CODEX_EXIT"
        echo "phase=$COBUILD_PHASE session=$COBUILD_SESSION_ID task=$COBUILD_TASK_ID"
        if [ -s .cobuild/session.err ]; then
            echo
            echo "--- session.err ---"
            cat .cobuild/session.err
        fi
        if [ -s .cobuild/session.log ]; then
            echo
            echo "--- tail session.log ---"
            tail -200 .cobuild/session.log
        fi
    } >> .cobuild/dispatch-error.log 2>/dev/null
fi

# Capture thread_id for later resumes. First event is always thread.started
# with {"type":"thread.started","thread_id":"<uuid>"}.
if command -v jq &>/dev/null && [ -s .cobuild/session.log ]; then
    THREAD_ID=$(head -20 .cobuild/session.log | jq -r 'select(.type=="thread.started") | .thread_id' 2>/dev/null | head -1)
    if [ -n "$THREAD_ID" ]; then
        echo "$THREAD_ID" > .cobuild/codex-thread-id
        echo "[$(date)] codex thread_id=$THREAD_ID" >> "$LOGFILE"
    fi

    # Sum token usage across all turn.completed events
    USAGE=$(jq -c 'select(.type=="turn.completed") | .usage' .cobuild/session.log 2>/dev/null |
        jq -s 'reduce .[] as $u ({input:0,cached:0,output:0};
            .input += ($u.input_tokens // 0) |
            .cached += ($u.cached_input_tokens // 0) |
            .output += ($u.output_tokens // 0))' 2>/dev/null)
    if [ -n "$USAGE" ] && [ "$USAGE" != "null" ]; then
        echo "[$(date)] Codex usage: $USAGE" >> "$LOGFILE"
    fi
fi

# Cleanup: remove this script itself. Safe because the open FD keeps the
# running process alive even after the file is unlinked on Unix.
rm -f "$0"

# Gate phases (design, decompose, review, done, investigate) don't produce
# code — the agent writes its verdict to .cobuild/gate-verdict.json and
# the runner records it post-exit (outside the sandbox, with DB access).
# Implementation phases (implement, fix) use cobuild complete for the
# commit→PR→needs-review flow.
if [ "$COBUILD_PHASE" = "implement" ] || [ "$COBUILD_PHASE" = "fix" ]; then
    cd "$COBUILD_REPO_ROOT" 2>/dev/null || true
    cobuild complete '%s'
elif [ -f .cobuild/gate-verdict.json ]; then
    echo "[$(date)] Gate phase ($COBUILD_PHASE) — recording verdict from gate-verdict.json" >> "$LOGFILE"
    cd "$COBUILD_REPO_ROOT" 2>/dev/null || true
    VERDICT_FILE="$OLDPWD/.cobuild/gate-verdict.json"

    if command -v jq &>/dev/null; then
        GATE=$(jq -r '.gate' "$VERDICT_FILE" 2>/dev/null)
        SHARD_ID=$(jq -r '.shard_id' "$VERDICT_FILE" 2>/dev/null)
        VERDICT=$(jq -r '.verdict' "$VERDICT_FILE" 2>/dev/null)
        READINESS=$(jq -r '.readiness // empty' "$VERDICT_FILE" 2>/dev/null)
        BODY=$(jq -r '.body' "$VERDICT_FILE" 2>/dev/null)

        case "$GATE" in
            readiness-review)
                RCMD="cobuild review $SHARD_ID --verdict $VERDICT --readiness ${READINESS:-3} --body"
                $RCMD "$BODY" 2>&1 | tee -a "$OLDPWD/$LOGFILE"
                ;;
            decomposition-review)
                cobuild decompose "$SHARD_ID" --verdict "$VERDICT" --body "$BODY" 2>&1 | tee -a "$OLDPWD/$LOGFILE"
                ;;
            investigation)
                cobuild investigate "$SHARD_ID" --verdict "$VERDICT" --body "$BODY" 2>&1 | tee -a "$OLDPWD/$LOGFILE"
                ;;
            review)
                # cb-465d17: route PR review gates through process-review, not
                # cobuild review. process-review consumes the verdict file,
                # records the gate, AND runs gh pr merge on pass. cobuild review
                # only records the gate + advances phase -- used that way on a
                # task with a PR, it advances phase=done while leaving the PR
                # unmerged (observed on cb-b78c67).
                cobuild process-review "$SHARD_ID" 2>&1 | tee -a "$OLDPWD/$LOGFILE"
                ;;
            retrospective)
                cobuild retro "$SHARD_ID" --body "$BODY" 2>&1 | tee -a "$OLDPWD/$LOGFILE"
                ;;
            *)
                echo "[$(date)] Unknown gate type: $GATE" >> "$OLDPWD/$LOGFILE"
                ;;
        esac
    else
        echo "[$(date)] jq not found — cannot parse gate-verdict.json" >> "$OLDPWD/$LOGFILE"
    fi
else
    echo "[$(date)] Gate phase ($COBUILD_PHASE) — no gate-verdict.json found" >> "$LOGFILE"
fi

# Explicitly close this tmux window so it doesn't hang around as an orphan
# that blocks the next dispatch (cb-699bf2). Works regardless of the user's
# remain-on-exit setting. Fire-and-forget: kill-window terminates this pane
# immediately, so any code after this won't run.
if [ -n "$TMUX_PANE" ]; then
    tmux kill-window -t "$TMUX_PANE" 2>/dev/null
fi
`,
		shellQuote(in.WorktreePath),
		in.SessionID, // already safe (store-generated id, no special chars)
		shellQuote(in.HooksDir),
		shellQuote(in.TaskID),
		shellQuote(in.RepoRoot),
		shellQuote(in.Phase),
		shellQuote(in.PromptFile),
		flags,
		shellQuote(in.TaskID),
	)
	return script, nil
}

// ParseSessionStats reads a captured codex session.log and aggregates
// token usage across all turns. The log format is the `codex exec --json`
// stdout stream:
//
//	{"type":"thread.started","thread_id":"<uuid>"}
//	{"type":"turn.started"}
//	{"type":"item.completed","item":{...}}
//	{"type":"turn.completed","usage":{"input_tokens":N,"cached_input_tokens":N,"output_tokens":N}}
//
// Multiple turns may be present in a multi-step session; this function
// sums them and records the session UUID from the first thread.started.
func (r *Runtime) ParseSessionStats(sessionLogPath string) (runtime.SessionStats, error) {
	f, err := os.Open(sessionLogPath)
	if err != nil {
		return runtime.SessionStats{}, fmt.Errorf("open %s: %w", sessionLogPath, err)
	}
	defer f.Close()

	stats := runtime.SessionStats{}
	scanner := bufio.NewScanner(f)
	// Increase the buffer size — individual JSONL lines can be large when
	// they carry tool outputs or big agent messages.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id,omitempty"`
			Usage    struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
			} `json:"usage,omitempty"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Skip malformed lines silently — codex occasionally emits
			// non-JSON progress lines when `--json` is not on every subprocess.
			continue
		}
		switch ev.Type {
		case "thread.started":
			if stats.SessionUUID == "" {
				stats.SessionUUID = ev.ThreadID
			}
		case "turn.completed":
			stats.TurnCount++
			stats.InputTokens += ev.Usage.InputTokens
			stats.CachedInputTokens += ev.Usage.CachedInputTokens
			stats.OutputTokens += ev.Usage.OutputTokens
		case "item.completed":
			if ev.Item.Type == "agent_message" && ev.Item.Text != "" {
				// Keep the most recent agent_message as the "last message"
				// fallback if the caller didn't capture --output-last-message.
				stats.LastMessage = ev.Item.Text
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("scan %s: %w", sessionLogPath, err)
	}
	return stats, nil
}

// needsFullAccess returns true for phases where the agent must create or
// modify external state (child tasks, fix tasks, etc.) that requires DB
// and network access beyond the workspace sandbox.
func needsFullAccess(phase string) bool {
	switch phase {
	case "decompose", "investigate", "implement", "fix":
		return true
	default:
		return false
	}
}

// shellQuote escapes embedded single quotes so s can be safely dropped
// into a single-quoted bash literal. Matches the helper in claudecode —
// kept local so the packages don't depend on each other.
func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
