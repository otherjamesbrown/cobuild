# Subsystem: hooks

Claude Code session hooks integration. Records session events for observability and warns the agent about wasteful patterns (notably repeated file reads).

## Files

- `hooks/hooks.json` — registers handlers for all 8 Claude Code lifecycle events.
- `hooks/cobuild-event.sh` — single bash script handling every event (~170 lines).
- Hooks are installed into the dispatched agent's `.claude/settings.local.json` by `internal/runtime/claudecode/...` during dispatch.

## Events fired

`hooks.json` registers all eight at 10s timeout each:
`SessionStart, SessionEnd, PreToolUse, PostToolUse, PreCompact, PostCompact, Stop, StopFailure`.

All events route to one script: `bash ${COBUILD_HOOKS_DIR:-$HOME/.cobuild/hooks}/cobuild-event.sh`.

## Activation guard

The script no-ops unless `COBUILD_DISPATCH=true` (set by the dispatch runner script). This means: regular interactive Claude Code sessions are not tracked. Only CoBuild-dispatched agents emit events.

## Per-event behaviour (`cobuild-event.sh`)

| Event | What the script does |
|---|---|
| `SessionStart` | Initialise `.cobuild/session-state/files_read.json = {}`; append `session_start` to `.cobuild/events.jsonl`. |
| `PreToolUse: Read` | Extract `file_path`, estimate tokens (chars/4). Look up prior read count. If repeated, log `repeated_read` event AND emit stderr warning the agent will see (`⚡ CoBuild: <file> was already read...`). Increment count. |
| `PreToolUse: Edit / Write` | Log `file_write` with tool name + token estimate. |
| `PreToolUse: Bash` | Log `bash_run` with first 200 chars of command. |
| `PreToolUse: Grep / Glob` | Log generic `search` event. |
| `PreToolUse: Agent` | Log `subagent_spawn` event. |
| `PreCompact` / `PostCompact` | Log `compact_start` / `compact_end`. |
| `Stop` | Log `turn_complete`. |
| `StopFailure` | Extract error, log `turn_error`. |
| `SessionEnd` | Aggregate `events.jsonl` → write `session_summary` event. If `COBUILD_SESSION_ID` set + DB credentials available, insert one row into `pipeline_session_events`. |

## What lands in the database

One row per session in `pipeline_session_events`, written at `SessionEnd`:

- `id`: `pse-<random hex>`
- `session_id`: `$COBUILD_SESSION_ID`
- `event_type`: `session_summary`
- `detail`: `"Reads: N, Repeated: N, Tokens saved: N, Writes: N, Compactions: N, Errors: N"`
- `tokens_used`: estimated tokens saved by detecting repeated reads
- `timestamp`: UTC ISO-8601

DB insert is best-effort (`2>/dev/null || true`). If `psql` fails or DSN is missing, the in-worktree `.cobuild/events.jsonl` is the only record.

## Repeated-read detection

State lives in `.cobuild/session-state/files_read.json` for the duration of the session. Any second `Read` of the same path:
- Increments the counter.
- Logs `repeated_read` to events with token estimate.
- **Prints a stderr line that Claude sees and can act on** — this is the only hook that influences agent behaviour live.

In-session only — does not persist across re-dispatches or worktree re-use.

## What hooks do NOT do

- **No token budgeting.** Pure observation; no event causes the session to abort.
- **No write to `pipeline_runs` or shard metadata.** Only `pipeline_session_events`.
- **No cross-session aggregation.** Each session emits one summary row; analysis (e.g., `cobuild admin tokens`) reads back across rows separately.
- **No per-tool-use rows.** Only the session summary lands in DB. Per-tool detail stays in the worktree's `events.jsonl`.

## Reading the events later

- `cobuild admin tokens` and similar analyse `pipeline_session_events`.
- For per-tool detail, `.cobuild/events.jsonl` in the worktree is authoritative — but disappears when the worktree is cleaned. If you need the detail post-merge, read it before `cobuild complete` runs.
