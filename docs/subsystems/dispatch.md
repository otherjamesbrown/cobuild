# Subsystem: dispatch

What `cobuild dispatch <shard-id>` actually does today. For architectural intent see `docs/cobuild.md`.

## Entry point

`cobuild dispatch <shard-id>` — `internal/cmd/dispatch.go:40-57`. Flags: `--runtime [claude-code|codex]`, `--mono`, `--force`, `--dry-run`, `--foreground`, `--agent`.

Also invoked indirectly by `cobuild orchestrate` and `cobuild poller` via the same code path.

## Lifecycle

1. **Validate** (`dispatch.go:69-135`) — resolve task; check status (`open | ready | in_progress | needs-review`); check `blocked-by` edges; detect phase from pipeline state or bootstrap.
2. **Worktree** (`dispatch.go:201-234`) — get or create at `task.metadata.worktree_path`. If missing, `worktree.Create(...)`. Install pre-push hook rejecting `main | master | develop`.
3. **Context assembly** (`dispatch.go:239-404`) —
   - Load parent design via connector edges (`child-of`).
   - `config.AssembleContext()` merges project config + design + task + phase-specific instructions.
   - Write to `.cobuild/dispatch-context.md`.
   - Append "## CoBuild Dispatch Context" pointer block (idempotent) to runtime-native context file: `CLAUDE.md` for claude-code, `AGENTS.md` for codex.
   - Warn at >30KB; log "strong correlation with degraded output" at >100KB.
4. **Runtime selection** (`dispatch.go:269-291`) — priority: `--runtime` flag > task `dispatch_runtime` metadata > project config default > `claude-code`. Call `rt.PreDispatch()` (claude-code pre-accepts trust dialog in `~/.claude.json`; codex no-op).
5. **Tmux** (`dispatch.go:427-475`) — resolve session name from project config; window name = `<phase>-<task-id>`. Ensure session exists (`tmux new-session -d`). **Create `pipeline_sessions` row before window spawn** (`store.CreateSession`) so a crashed spawn still leaves a tracked session for postmortem.
6. **Runner script** (`dispatch.go:486-500`) — `rt.BuildRunnerScript(...)` produces a bash template. Each runtime owns its template.
7. **Spawn** (`dispatch.go:511-590`) — `tmux new-window` with the script. Foreground: inherit stdio. Background: detached.
8. **Cleanup on failure** (`dispatch.go:593-612`) — kill tmux window if anything after window creation fails; otherwise the worktree gets stuck in a re-dispatch loop.

## Files written into the worktree's `.cobuild/`

- `dispatch-context.md` — assembled context (project, design, task, phase instructions).
- `.gitignore` containing `*\n` — prevents dispatch artefacts being committed.
- `session-state/files_read.json` — populated by hooks (see `hooks.md`).
- `events.jsonl` — hook event log (file reads, tool use, compactions).
- `gate-verdict.json` — written by the agent at end of skill, consumed by `process-review`.

Plus runtime-specific:
- `.claude/settings.local.json` — Stop hook + deny list for `.claude/**` edits (claude-code only).

## Runtime selection details

`pCfg.ResolveRuntime()` picks among registered runtimes (`internal/runtime/claudecode`, `internal/runtime/codex`).

`pCfg.ModelForPhaseRuntime()` resolves model: phase-specific override > runtime default. Defaults: claude-code → `sonnet`; codex → `gpt-5.4`.

## What dispatch does NOT do

- No queueing or concurrency control beyond tmux session/window isolation.
- No cross-repo dispatch — one worktree, one repo, one agent.
- No live progress stream back to the orchestrator — observation is via `cobuild status` / `cobuild inspect` polling the `pipeline_sessions` row and `events.jsonl`.

## Common failure modes

- **Worktree stuck in re-dispatch loop** when window-spawn fails after worktree creation. Cleanup path at `dispatch.go:593-612` is the guard.
- **Stale dispatch-context.md** when the parent shard is updated after dispatch. `cobuild inspect` warns; `cobuild redispatch <id>` kills the session and re-dispatches with fresh context.
