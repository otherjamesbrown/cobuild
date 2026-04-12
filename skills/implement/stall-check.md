---
name: stall-check
version: "0.1"
description: Diagnose a task that may be stalled, crashed, or rate-limited. Trigger on health check, stall detection, or agent crash.
summary: >-
  Detects when a dispatched agent has stalled, crashed, or been rate-limited. Checks the tmux session, diagnoses the problem, and either re-dispatches, escalates to the developer, or waits for recovery.
---

# Skill: Agent Health Check

You are the pipeline orchestrator, diagnosing a task that may be stalled, crashed, or rate-limited.

## Input

- Task shard ID
- Trigger reason: `stall`, `crash`, or `retry-exhausted`
- Retry count (how many times this task has been re-dispatched)

## Step 1: Determine status

Inspect the task record and the agent session for this shard.

Confirm:
- `status` — should be `in_progress`
- `updated_at` — when was the last update?
- whether the expected tmux window still exists for the task

## Step 2: Diagnose

### Agent crashed (tmux window gone, task still in_progress)

The agent session exited — could be rate limit, OOM, context overflow, or bug.

If retry count is still below the retry limit, reset the task to a dispatchable state, remove any stale worktree/session state, and let the normal cooldown and poller flow re-dispatch it. Record that a crash was detected and which retry this is.

If the task has already exhausted its retries, append an escalation note with the last known status and timestamp, then mark the shard blocked so a human can re-scope or unblock it.

### Agent stalled (tmux window exists, no progress for > stall_timeout)

The agent is running but not making progress — could be stuck in a loop, waiting for input, or hitting repeated failures.

Inspect the recent terminal output from the tmux pane for clues.

Look for:
- Rate limit messages → wait for cooldown, agent should recover
- Error loops → likely a code issue, needs re-scoping
- "thinking" for > 5 min → might be a complex problem, give it more time
- Idle prompt → agent finished but didn't mark needs-review

If the agent appears to be finished and is sitting at an idle prompt, check the shard for completion evidence. When the evidence is there, move the task to `needs-review`.

If the pane shows a repeated error loop with no forward progress, append the diagnosis to the task and mark it blocked for re-scoping or manual intervention.

If the agent is rate limited, record that in the shard and leave the task alone so it can recover on the next cycle.

### Retry exhausted (max retries hit)

Append an escalation note that captures the design, task title, retry count, and the most likely cause of failure. Mark the task blocked so it stops recycling through the dispatcher.

## Step 3: Record

Always append health check results to the work item. Every check should be visible in the audit trail, even if no action is taken.

## Gotchas

<!-- Add failure patterns here as they're discovered -->

Use a consistent note format that captures:

- timestamp
- trigger type
- retry count
- diagnosis
- action taken

## Final Step

After you have appended the health check result and taken the required status action, exit the session immediately. Run `/exit` so the dispatched session ends cleanly.
## Final step

After recording the stall diagnosis and taking the configured health action, stop. This monitoring skill is not a task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
