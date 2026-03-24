# M Skill: Agent Health Check

You are M, diagnosing a task that may be stalled, crashed, or rate-limited.

## Input

- Task shard ID
- Trigger reason: `stall`, `crash`, or `retry-exhausted`
- Retry count (how many times this task has been re-dispatched)

## Step 1: Determine status

```bash
cxp shard show <task-id> -o json
```

Check:
- `status` — should be `in_progress`
- `updated_at` — when was the last update?

Check tmux:
```bash
tmux list-windows -t <tmux-session> | grep <task-id>
```

## Step 2: Diagnose

### Agent crashed (tmux window gone, task still in_progress)

The agent session exited — could be rate limit, OOM, context overflow, or bug.

**If retry count < max_retries:**
```bash
# Reset and re-dispatch
cxp shard status <task-id> open
cxp task worktree remove <task-id>
# Wait for cooldown (handled by poller)
# Poller will re-dispatch on next cycle
```

Append to shard:
```bash
cxp shard append <task-id> --body "## Health Check — Crash detected
Agent session exited. Retry #<N>. Re-dispatching after cooldown."
```

**If retry count >= max_retries:**
```bash
cxp shard append <task-id> --body "## Health Check — Max retries exceeded
Agent crashed <N> times. Escalating to James.
Last status: <status>
Last update: <updated_at>"
cxp shard label add <task-id> blocked
```

### Agent stalled (tmux window exists, no progress for > stall_timeout)

The agent is running but not making progress — could be stuck in a loop, waiting for input, or hitting repeated failures.

**Check the tmux pane for clues:**
```bash
tmux capture-pane -t <session>:<window> -p | tail -20
```

Look for:
- Rate limit messages → wait for cooldown, agent should recover
- Error loops → likely a code issue, needs re-scoping
- "thinking" for > 5 min → might be a complex problem, give it more time
- Idle prompt → agent finished but didn't mark needs-review

**If idle prompt (agent finished but forgot to update status):**
```bash
# Check if there's evidence of completion in the shard
cxp shard show <task-id> -o json
# If evidence exists, mark it done
cxp shard status <task-id> needs-review
```

**If stuck in error loop (> 5 iterations with no progress):**
```bash
cxp shard append <task-id> --body "## Health Check — Stall detected
Agent stuck after <N> iterations. Possible causes:
- <diagnosis from tmux output>

Action: re-scoping or manual intervention needed."
cxp shard label add <task-id> blocked
```

**If rate limited:**
```bash
cxp shard append <task-id> --body "## Health Check — Rate limited
Agent hit rate limits. Will recover on next cycle."
# No action needed — agent will resume when limits clear
```

### Retry exhausted (max retries hit)

```bash
cxp shard append <task-id> --body "## Health Check — Escalation
Task failed after <max_retries> dispatch attempts.
Design: <design-id>
Task: <task-title>

Possible causes:
1. Task scope too large for single session
2. Missing information in task spec
3. Codebase issue blocking implementation

Action needed: James to review and re-scope or unblock."
cxp shard label add <task-id> blocked
```

## Step 3: Record

Always append health check results to the task shard. Every check should be visible in the audit trail, even if no action is taken.

Format:
```bash
cxp shard append <task-id> --body "## Health Check — <timestamp>
Trigger: <stall|crash|retry-exhausted>
Retry: <N>/<max>
Diagnosis: <what was found>
Action: <what was done>"
```
