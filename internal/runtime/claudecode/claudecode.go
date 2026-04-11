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

// settingsLocalJSON is the static JSON body written to
// <worktree>/.claude/settings.local.json. It registers a Stop hook that runs
// `cobuild complete --auto` when the agent session ends, and denies all
// edits to the .claude directory so the agent can't disable its own hooks.
const settingsLocalJSON = `{
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

// WriteSettings writes .claude/settings.local.json into the worktree. The
// hook registration makes the Stop event fire `cobuild complete --auto` on
// session end, and the deny list prevents the agent from editing its own
// hook configuration.
func (r *Runtime) WriteSettings(worktreePath string) error {
	settingsDir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", settingsDir, err)
	}
	path := filepath.Join(settingsDir, "settings.local.json")
	if err := os.WriteFile(path, []byte(settingsLocalJSON), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// BuildRunnerScript returns the full bash script that dispatch writes to a
// temp file and spawns inside a tmux window. The script:
//
//  1. exports the COBUILD_* env vars (session id, task id, repo root, hooks dir)
//     so the cobuild-event.sh hook script can link events to pipeline_sessions
//  2. loads the prompt from the temp prompt file, saves a copy to
//     .cobuild/last-prompt.md for debugging, and deletes the temp file
//  3. runs `claude <flags> "$PROMPT"` in interactive mode (proven reliable
//     for multi-turn work)
//  4. post-hoc parses the newest ~/.claude/projects/*.jsonl transcript for
//     usage data and appends it to .cobuild/dispatch.log
//  5. removes itself ($0) and runs `cobuild complete <task-id>` from
//     $COBUILD_REPO_ROOT (so the connector finds its config)
//
// The Stop hook registered by WriteSettings will also invoke cobuild complete,
// but with --auto — the trailing call in this script is the non-auto path
// used when the Stop hook doesn't fire (e.g. agent exits with error).
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

	// Resolve the final claude invocation flags. Default is interactive mode
	// with skip-permissions; ExtraFlags overrides this if set from config.
	// Byte-identical to the pre-refactor inline assembly — the `--model`
	// argument is appended without shell quoting because claude model names
	// are simple alphanumeric tokens.
	flags := "--dangerously-skip-permissions"
	if in.ExtraFlags != "" {
		flags = in.ExtraFlags
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
LOGFILE=".cobuild/dispatch.log"
mkdir -p .cobuild
echo "$COBUILD_SESSION_ID" > .cobuild/session_id
echo "[$(date)] Dispatch starting (session: $COBUILD_SESSION_ID)" >> "$LOGFILE"

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

# Run claude in interactive mode (proven reliable for multi-turn work)
claude %s "$PROMPT"
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

# Run completion from the main repo root (not worktree) so the connector
# can find the Beads/Dolt database or CP config. The Stop hook does the
# same via $COBUILD_REPO_ROOT.
cd "$COBUILD_REPO_ROOT" 2>/dev/null || true
cobuild complete '%s'
`,
		shellQuote(in.WorktreePath),
		in.SessionID, // already safe (store-generated id, no special chars)
		shellQuote(in.HooksDir),
		shellQuote(in.TaskID),
		shellQuote(in.RepoRoot),
		shellQuote(in.PromptFile),
		flags,
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
