# Phase 3 — Dispatch And Monitor

Use this file only when `pipeline.phase = implement`, or when a task looks stalled or crashed.

## Decision tree

Start by checking task state:

```bash
cobuild deps <design-id>
```

```
Any tasks in "dispatchable"?
  YES → dispatch them up to max_concurrent
  NO  → are all tasks closed?
    YES → move to review
    NO  → are any tasks stalled or crashed?
      YES → follow the health rules below
      NO  → exit and let the poller resume later
```

## Dispatch

```bash
cobuild task dispatch <task-id>
```

Dispatch creates the worktree, injects dispatch context, starts the agent in tmux, marks the task `in_progress`, captures logs, and appends `cobuild task complete <id>` to the task session.

## After agent completion

When the agent finishes, `cobuild task complete` runs automatically and should:

1. Restore the original `CLAUDE.md`
2. Commit remaining changes
3. Push the branch
4. Create the PR if needed
5. Append evidence to the shard
6. Mark the task `needs-review`

## Health handling

Read the `monitoring:` section in `pipeline.yaml`.

- Crash: tmux window is gone but task is still `in_progress` → use `on_crash`, usually redispatch
- Stall: no shard update for `stall_timeout` → use `on_stall`, usually `skill:implement/stall-check`
- Retry limit exceeded: `max_retries` reached → escalate

## When all tasks are closed

Advance to review:

```bash
cobuild update <id> --phase review
```
