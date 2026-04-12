// Package claudecode implements the Runtime interface for Anthropic's
// Claude Code CLI. It handles the pre-flight hacks required to run claude
// in a fresh git worktree (workspace-trust pre-acceptance, settings file
// write with Stop hook), builds the tmux runner script, and parses the
// post-dispatch transcript for token analytics.
package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

// Runtime is the Claude Code implementation of runtime.Runtime.
// Exported so test code can construct it directly; production dispatch goes
// through the registry via runtime.Get("claude-code").
type Runtime struct{}

// New returns a fresh Claude Code runtime. Prefer runtime.Get("claude-code")
// in production code — this constructor exists for tests and init().
func New() *Runtime { return &Runtime{} }

func init() {
	runtime.Register(New())
}

// Name implements runtime.Runtime.
func (r *Runtime) Name() string { return "claude-code" }

// ContextFile implements runtime.Runtime.
func (r *Runtime) ContextFile() string { return "CLAUDE.md" }

// PreDispatch pre-accepts Claude Code's workspace trust dialog for the
// worktree. Without this, dispatched agents block on "Is this a project
// you created or one you trust?" in fresh worktrees.
func (r *Runtime) PreDispatch(_ context.Context, worktreePath string) error {
	return ensureClaudeTrust(worktreePath)
}

// settingsLocalImplement is written for implementation phases (implement, fix).
// The Stop hook fires `cobuild complete --auto` so any uncommitted work is
// pushed and a PR is created.
const settingsLocalImplement = `{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cd \"$COBUILD_REPO_ROOT\" && cobuild complete \"$COBUILD_TASK_ID\" --auto"
      }]
    }]
  },
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)",
      "MultiEdit(.claude/**)"
    ]
  }
}`

// settingsLocalGate is written for gate phases (design, decompose, review,
// investigate, done). No Stop hook — gate verdict processing is handled by
// the runner script after the agent exits, reading .cobuild/gate-verdict.json.
// Running `cobuild complete` on a gate phase would misidentify the session as
// a direct-mode task (no code changes) and skip the real gate logic.
const settingsLocalGate = `{
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)",
      "MultiEdit(.claude/**)"
    ]
  }
}`

// WriteSettings writes .claude/settings.local.json into the worktree. For
// implementation phases, the Stop hook fires `cobuild complete --auto`; for
// gate phases the hook is omitted (the runner script handles gate verdicts).
func (r *Runtime) WriteSettings(worktreePath string) error {
	settingsDir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", settingsDir, err)
	}
	path := filepath.Join(settingsDir, "settings.local.json")

	body := settingsLocalImplement
	phaseFile := filepath.Join(worktreePath, ".cobuild", "phase")
	if data, err := os.ReadFile(phaseFile); err == nil {
		phase := strings.TrimSpace(string(data))
		if runtime.IsGatePhase(phase) {
			body = settingsLocalGate
		}
	}

	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// BuildRunnerScript returns the full bash script that dispatch writes to a
// temp file and spawns inside a tmux window. The script:
//
//  1. exports the COBUILD_* env vars (session id, task id, repo root, hooks
//     dir, phase) so hooks and post-completion logic know the dispatch context
//  2. writes .cobuild/phase so WriteSettings can select the right settings
//     template (Stop hook for implement/fix, no hook for gate phases)
//  3. loads the prompt from the temp prompt file, saves a copy to
//     .cobuild/last-prompt.md for debugging, and deletes the temp file
//  4. runs `claude <flags> "$PROMPT"` in interactive mode
//  5. post-hoc parses the newest ~/.claude/projects/*.jsonl transcript for
//     usage data and appends it to .cobuild/dispatch.log
//  6. phase-aware completion:
//     - implement/fix: runs `cobuild complete <task-id>` (commit→PR→needs-review)
//     - gate phases: reads .cobuild/gate-verdict.json and records the gate
//       verdict via the appropriate cobuild subcommand (review, decompose, etc.)
//
// For implement/fix phases, the Stop hook also invokes cobuild complete --auto
// as a belt-and-braces measure. For gate phases, the Stop hook is omitted to
// prevent cobuild complete from misidentifying the session as a direct-mode
// task (no code changes) and short-circuiting the pipeline.
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

	// Resolve the final claude invocation flags.
	// Gate phases use -p (print/headless) mode so the process exits
	// automatically when the agent finishes — no reliance on the agent
	// typing /exit. Implementation phases keep interactive mode for
	// multi-turn editing work.
	flags := "--dangerously-skip-permissions"
	if in.ExtraFlags != "" {
		flags = in.ExtraFlags
	}
	if runtime.IsGatePhase(in.Phase) {
		flags += " -p --output-format json --max-turns 200"
	}
	if in.Model != "" {
		flags += " --model " + in.Model
	}

	// Shell-escape every interpolated path so task IDs with apostrophes or
	// paths with spaces don't break the script. Backslash-escaping of single
	// quotes uses the standard '\'' sequence.
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
echo "$COBUILD_PHASE" > .cobuild/phase
echo "$COBUILD_SESSION_ID" > .cobuild/session_id
echo "[$(date)] Dispatch starting (session: $COBUILD_SESSION_ID, phase: $COBUILD_PHASE)" >> "$LOGFILE"

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

# Run claude — gate phases use -p (headless) mode for auto-exit;
# implement/fix use interactive mode for multi-turn work.
if [ "$COBUILD_PHASE" = "implement" ] || [ "$COBUILD_PHASE" = "fix" ]; then
    claude %s "$PROMPT"
else
    claude %s "$PROMPT" > .cobuild/session-result.json 2>&1
    if [ -f .cobuild/session-result.json ] && command -v jq &>/dev/null; then
        STOP=$(jq -r '.subtype // .stop_reason // "unknown"' .cobuild/session-result.json 2>/dev/null)
        TURNS=$(jq -r '.num_turns // "?"' .cobuild/session-result.json 2>/dev/null)
        COST=$(jq -r '.total_cost_usd // .cost_usd // "?"' .cobuild/session-result.json 2>/dev/null)
        echo "[$(date)] Headless session: stop=$STOP turns=$TURNS cost=$COST" >> "$LOGFILE"
    fi
fi
echo "[$(date)] Claude session ended" >> "$LOGFILE"

# Parse transcript for token/cost data after session ends
# The transcript JSONL has usage data in each API response
TRANSCRIPT_DIR="$HOME/.claude/projects"
TRANSCRIPT=$(find "$TRANSCRIPT_DIR" -name "*.jsonl" -newer "$LOGFILE" -type f 2>/dev/null | head -1)
if [ -n "$TRANSCRIPT" ] && command -v jq &>/dev/null; then
    # Extract usage from the last result message in the transcript
    USAGE=$(tail -100 "$TRANSCRIPT" | grep '"usage"' | tail -1 | jq -c '.usage // empty' 2>/dev/null)
    if [ -n "$USAGE" ]; then
        echo "[$(date)] Transcript usage: $USAGE" >> "$LOGFILE"
    fi
fi

# Cleanup: remove this script itself. Safe because the open FD keeps the
# running process alive even after the file is unlinked on Unix.
rm -f "$0"

# Gate phases (design, decompose, review, done, investigate) don't produce
# code — the agent writes its verdict to .cobuild/gate-verdict.json and
# the runner records it post-exit. Implementation phases (implement, fix)
# use cobuild complete for the commit→PR→needs-review flow.
# The Stop hook handles cobuild complete for implement/fix; for gate phases
# the Stop hook is omitted so we handle it here.
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
`,
		shellQuote(in.WorktreePath),
		in.SessionID, // already safe (store-generated id, no special chars)
		shellQuote(in.HooksDir),
		shellQuote(in.TaskID),
		shellQuote(in.RepoRoot),
		shellQuote(in.Phase),
		shellQuote(in.PromptFile),
		flags, // interactive (implement/fix)
		flags, // headless (gate phases)
		shellQuote(in.TaskID),
	)
	return script, nil
}

// ParseSessionStats is a post-hoc stub. Claude Code analytics currently
// live inline in the runner script (which tails ~/.claude/projects/*.jsonl
// and appends usage to .cobuild/dispatch.log). Moving that into Go is a
// separate cleanup.
func (r *Runtime) ParseSessionStats(sessionLogPath string) (runtime.SessionStats, error) {
	return runtime.SessionStats{}, nil
}

// shellQuote returns s with embedded single quotes escaped so it can be
// safely dropped into a single-quoted bash literal. It returns only the
// inner string — wrap the call site in ' ' to complete the literal.
func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
