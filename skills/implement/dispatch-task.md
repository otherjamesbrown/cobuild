---
name: dispatch-task
version: "0.1"
description: Dispatch tasks to implementing agents and monitor until complete. Trigger when tasks are ready for implementation.
summary: >-
  Sends tasks to implementing agents. Creates an isolated git worktree for each task, spawns a Claude session with the task prompt and design context, and monitors until complete. Handles parallel dispatch within concurrency limits.
---

# Skill: Dispatch Tasks

Dispatch implementation tasks to agents in isolated worktrees, then wait for completion.

## Dispatching a single task

```bash
cobuild dispatch <task-id>
```

This creates a worktree from the correct repo (reads `repo` metadata on the task), spawns a Claude session in tmux with the task prompt and design context, and sets the task to `in_progress`.

## Dispatching a wave

For multiple tasks with no dependencies between them:

```bash
cobuild dispatch-wave <design-id>
```

This finds all tasks whose blockers are satisfied and dispatches them up to `max_concurrent`.

## Waiting for completion

After dispatching, wait for all tasks to finish:

```bash
# Wait for specific tasks
cobuild wait <task-id-1> <task-id-2>

# Wait with custom interval and timeout
cobuild wait <task-id-1> <task-id-2> --interval 30 --timeout 1h
```

This polls task status every 60 seconds (default) and exits when all tasks reach `needs-review`. If a task is labelled `blocked`, the wait aborts immediately.

## Full wave dispatch + wait pattern

```bash
# Dispatch all ready tasks
cobuild dispatch-wave <design-id>

# Wait for them to complete
cobuild wait <task-id-1> <task-id-2> <task-id-3>

# When done, check if next wave is ready
cobuild dispatch-wave <design-id>
```

Repeat until `dispatch-wave` reports "All tasks complete."

## What the dispatched agent does

The agent receives a prompt containing:
- Task spec (scope, acceptance criteria, code locations)
- Parent design context
- Build/test commands
- Instructions to run `cobuild complete <task-id>` as its last action

`cobuild complete` handles: commit remaining changes, push branch, create PR, append evidence to the task, mark `needs-review`.

## Multi-repo tasks

Tasks can target a specific repo by setting `repo` metadata during decomposition. The dispatch command reads this metadata and creates the worktree from the correct repo root.

If no `repo` metadata is set, the worktree is created from the current project's registered repo.

## Gotchas

- Dispatched agents run in interactive mode (not `-p` mode) so they can iterate on edits and tests
- The agent's tmux window is named after the task ID — check `tmux list-windows` to see active agents
- If an agent exits without running `cobuild complete`, the task stays `in_progress` — use the stall-check skill
- `cobuild wait` blocks the calling session — use it in the orchestrating agent, not in a dispatch agent
<!-- Add failure patterns here as they're discovered -->

## Final Step

This skill is for the orchestrator flow. Once you have finished dispatching or monitoring work with this skill, exit the session immediately with `/exit`.
