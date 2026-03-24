# M Skill: Dispatch Task to Agent

You are M, dispatching a task to an implementing agent.

## Input
- Task shard ID
- Design shard ID (parent)

## Steps

1. Verify task is dispatchable:
   ```bash
   cxp task deps <design-id>
   ```
   Confirm task ID appears in the "dispatchable" list.

2. Dispatch the task:
   ```bash
   cxp task dispatch <task-id>
   ```
   This creates a worktree, sets status to in_progress, and spawns the agent in tmux.

3. Update pipeline:
   ```bash
   cxp shard pipeline update <design-id> --add-task <task-id>
   ```

4. If multiple tasks are ready, dispatch up to 3 concurrently:
   ```bash
   cxp task dispatch-all <design-id> --max 3
   ```

## Agent prompt includes

The dispatch command automatically provides:
- Task shard content (scope, acceptance criteria, code locations)
- Parent design context
- Working directory set to the task worktree

## On completion

The agent will:
1. Implement the task
2. Run tests
3. Append evidence to the task shard
4. Create a PR: `cxp task pr create <task-id>`
5. Mark needs-review: `cxp shard status <task-id> needs-review`

The poller detects needs-review and spawns M for Phase 4.
