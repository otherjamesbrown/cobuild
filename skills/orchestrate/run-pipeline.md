---
name: run-pipeline
version: "0.1"
description: Drive a CoBuild pipeline end-to-end as the orchestrator agent. Trigger when the user asks to run, orchestrate, execute, or complete a design/bug/task shard through CoBuild, or when you (the orchestrator) need to resume a pipeline that's in any phase other than done.
summary: >-
  The single source of truth for an orchestrator agent running a CoBuild pipeline to completion. Covers the loop (init → dispatch → poll → advance → repeat), per-phase concrete actions, common failure modes and their fixes, and the structured report format to use when returning to the user. Load this skill at the start of any "run <id>" request and follow it mechanically — do NOT reason about what phase needs which command from memory.
---

# Skill: Run a CoBuild pipeline end-to-end

## Prefer `cobuild orchestrate <id>`

If the `cobuild orchestrate <id>` command is available, use that first. It is the preferred foreground path and already implements the dispatch, poll, review, and phase-advance loop in Go with structured log output.

Use the manual in-session loop in this skill only when:

- you are on an older build that does not have `cobuild orchestrate`
- you are debugging the foreground driver itself
- you need to inspect or intervene in a pipeline step-by-step beyond what `--step` mode provides

You are the **orchestrator agent**. A user has asked you to run a CoBuild pipeline for a work item (design, bug, or task). This skill tells you how to do that from cold start to done — or to a legitimate stopping point — without bothering the user with CoBuild internals.

**Do not interleave your own reasoning about what phase needs what command.** CoBuild already knows. Follow the output of each command, fall back to `cobuild next <id>` when unsure, and only return to the user on real events.

## Inputs

- `<id>` — a work-item shard ID (design, bug, or task) with optional `--project <name>` scoping if the shard is in a different project than the current repo

## The loop (learn this; the rest is details)

```
1. cobuild next <id>              → prints the next concrete command
2. Run that command
3. Read its "Next step:" output line
4. Run that command — usually cobuild audit <id> to poll progress
5. When progress advances (phase changed, task reached needs-review, PR merged), go to step 1
6. Stop only when one of:
   - cobuild next says "Pipeline complete"
   - A gate failed and you can't auto-resolve it
   - Deploy phase reached (always human-approval)
   - Infrastructure error (merge conflict, CI red, Gemini unavailable past retry limit)
```

That is the whole orchestration logic. Everything below is edge cases and references for the specific situations you will hit.

## Rules — never do these

1. **Never use `cobuild wait`.** It's a 2-hour blocking command and it's semantically **task-only** — it waits for `needs-review` status which designs never reach. Use `cobuild audit <id>` for instant non-blocking checks. Poll every 60-180 seconds.
2. **Never execute phase work yourself** (decomposition, review, investigation, etc.). Every phase has a dispatchable skill — `cobuild dispatch <id>` spawns a dispatched CoBuild agent in a tmux worktree that runs the right skill automatically. Your only job is to type the commands.
3. **Never return to the user with just "Dispatched"** and stop. Dispatch is asynchronous — the dispatched agent runs in the background, your job is to watch it complete and report the actual outcome.
4. **Never approve a deploy automatically.** Deploy always requires human approval. When you reach the deploy phase, stop and ask the user.

## Per-phase concrete actions

### Phase: `design`

Readiness review. One dispatched agent evaluates the design against 6 criteria (problem, user, success, scope, test strategy, outcome link).

```bash
cobuild dispatch <design-id>
# wait for phase to advance (poll cobuild audit <id>)
# when phase = decompose → continue to decompose phase
# if gate failed → the design needs revision; report to user with findings from cobuild audit
```

### Phase: `decompose`

One dispatched agent breaks the design into child task shards, sets wave numbers and blocked-by edges, records `cobuild decompose <id> --verdict pass`.

```bash
cobuild dispatch <design-id>
# poll cobuild audit <id> until phase = implement
# if the design is multi-project (crosses context-palace / penfold / penf-cli / mycroft):
#   verify the decomposed tasks have correct --project and `repo` metadata before moving on
#   use: cobuild wi show <task-id> and check item.metadata.repo
# if the agent created tasks in the wrong project, fix metadata before dispatching them:
#   cxp shard metadata set <task-id> repo <correct-repo>
```

**Known decomposition failure mode (cp-c2ec47, 2026-04-11):** a decompose agent reading a multi-project design created all tasks in the design's home project and set `repo` metadata on only half of them. Always verify metadata after a decomposition gate passes, especially when the design mentions another project's shard prefixes or file paths.

### Phase: `investigate` (bugs with `needs-investigation` label)

One read-only dispatched agent produces an investigation report and creates a fix task.

```bash
cobuild dispatch <bug-id>
# poll cobuild audit until phase advances
# when done: the agent created a new fix task child, which is what runs next
# grab the fix task ID via: cobuild wi links <bug-id>
# then dispatch that fix task
```

### Phase: `fix` (default bug workflow)

Single-session dispatched agent investigates and fixes together.

```bash
cobuild dispatch <bug-id>
# poll cobuild audit
# the agent runs cobuild complete which opens a PR and marks needs-review
# → continue to review phase
```

### Phase: `implement`

Multiple task agents run in parallel by wave. This is where most of the work happens.

`cobuild dispatch <design-id>` now follows the same default at this phase: if the design has child tasks, it dispatches waves rather than opening one design-level PR. The mono-PR path is explicit and risky: `cobuild dispatch --mono --force <design-id>`. Use that only when you intentionally want one PR and accept overlap with any existing child-task PRs.

```bash
# Dispatch the first wave — cobuild dispatch-wave respects blocked-by edges and
# max_concurrent from pipeline config. It only dispatches tasks whose blockers
# are satisfied.
cobuild dispatch-wave <design-id>

# Poll cobuild audit <design-id> every 2-3 minutes. You're watching for:
# - tasks transitioning from in_progress → needs-review (they finished, opened a PR)
# - tasks going from needs-review → closed (after process-review merged them)
# - the wave completing (all dispatched tasks closed) → dispatch the next wave

# When ANY task reaches needs-review, process its review:
cobuild process-review <task-id>
# This command:
# - Fetches Gemini review + CI status
# - If clean (verdict=approve): merges the PR, closes the task, cleans up the worktree,
#   and if all siblings are also closed, advances the pipeline phase automatically
# - If there are findings (verdict=request-changes): appends feedback to the task and
#   re-dispatches it — the task goes back to in_progress
# - If Gemini hasn't reviewed yet: prints "Waiting" and exits. Retry in a few minutes.

# When a wave completes, check if there are more waves to dispatch:
cobuild dispatch-wave <design-id>
# Dispatches whichever blocked tasks have just been unblocked.

# Loop: dispatch-wave → poll → process-review → dispatch-wave → … → all tasks closed
```

**Implementation loop specifics:**

- `cobuild audit <design-id>` is your friend. It shows every gate, every task status, every phase transition. Run it whenever you need to know "where are we?"
- Don't `process-review` before Gemini has had a chance to review. The command will tell you "Waiting Nm" if the PR is too young. Retry after the timeout elapses (default 10 minutes).
- If a task fails review (`request-changes`), the agent is re-dispatched. Treat it like a new task in the loop — eventually it'll reach needs-review again.
- If a task has been re-dispatched 3+ times, that's a signal something structural is wrong. Stop the loop and report to the user.

### Phase: `review` (after all implement tasks merged)

For designs, this phase is usually consumed by `process-review` during the implement loop — the phase auto-advances when all tasks are closed. For standalone task/bug workflows, run `cobuild process-review <task-id>` directly and advance.

### Phase: `deploy`

**STOP. Always ask the user.** Do not deploy autonomously.

```bash
# Run a dry-run first to show what would be affected:
cobuild deploy <design-id> --dry-run

# Print the output to the user and ask: "Deploy <N> services? [y/n]"
# Only run cobuild deploy <design-id> (without --dry-run) on explicit user approval.
```

### Phase: `done`

Dispatched retrospective agent produces a done shard summarising gate history + metrics.

```bash
cobuild dispatch <design-id>
# poll audit until the retro shard is created and the pipeline is marked completed
# report the retro shard ID in your final summary
```

## Known failure modes and fixes

### "Dispatched agent is stuck at an interactive prompt"

**Symptom:** A tmux window for a task ID (e.g. `cp-c2ec47`) is still alive hours after dispatch. `tmux capture-pane` shows the dispatched agent waiting at a `❯` prompt.

**Diagnosis:** The agent finished its work and recorded a gate pass, but the skill didn't include an explicit `/exit` instruction so the session is idle. This is a skill-quality issue, not a pipeline bug.

**Fix:** Kill the window directly — it's not blocking anything, the gate is already recorded in the DB. The work is done.

```bash
tmux kill-window -t cobuild-<project>:<task-id>
```

Do not worry about "losing work" — the Stop hook writes session data as the agent goes, and the gate is already recorded.

### "process-review says 'Waiting' and exits without doing anything"

**Cause:** Either Gemini hasn't reviewed the PR yet, or CI is still running.

**Fix:** Wait. Retry in 2-5 minutes. Default timeout before process-review falls back to CI-only review is 10 minutes. If you're still seeing "Waiting" after 15+ minutes, check `gh pr view <pr-url> --comments` directly to see if Gemini posted anything.

### "dispatch-wave dispatched something that isn't a task"

**Cause:** Prior to commit c57e7cc, dispatch-wave did not filter child-of edges by shard type, so review gate records (type=review) could get dispatched as if they were implementation work.

**Fix:** Already patched. If you see this on an old cobuild binary, the fix is to kill the rogue window and revert the non-task shard's status:

```bash
tmux kill-window -t cobuild-<project>:<shard-id>
cxp shard status <shard-id> <previous-status> --project <name>
```

Then upgrade cobuild and re-run dispatch-wave.

### "cobuild wait is blocking my session for 2 hours"

**Cause:** You used `cobuild wait` (don't). It's a 2h blocker.

**Fix:** Cancel the task and never use `cobuild wait` again. Poll with `cobuild audit <id>` at 60-180 second intervals. For single-task waits, run `cobuild show <task-id>` or `cobuild wi show <task-id>` — both are instant.

### "cobuild status says phase X but cobuild show says phase Y"

**Cause:** Pre-cb-a8ca46 bug — `cobuild show` was reading legacy metadata. Fixed.

**Fix:** If you see divergence, something regressed. Trust `cobuild audit <id>` as the source of truth, report the bug.

### "A task reached needs-review but I don't see a PR"

**Cause:** `cobuild complete` failed to open the PR (usually: branch push failed, gh auth issue, or working-tree dirty with excluded files).

**Fix:** Check the dispatched agent's session.log in the worktree:

```bash
tail -50 ~/worktrees/<project>/<task-id>/.cobuild/session.log
tail -50 ~/worktrees/<project>/<task-id>/.cobuild/dispatch.log
```

Usually shows a clear error. Fix manually if you can (push the branch, retry complete); otherwise report to user.

## Report format

When the pipeline reaches `done`, hits a genuine blocker, or requires deploy approval, return to the user with a structured summary. Do not embellish, do not ask "want me to continue", just report:

```
Pipeline cp-<id> — <current status>

Completed phases: <list>
Current phase: <phase>
Gate results: <N pass, M fail across K rounds>

Child shards created: <list of IDs with titles>
PRs opened: <list of URLs>
PRs merged: <list of URLs>

<Failure details if any — file paths, error messages, stuck gates>

Next action: <concrete command OR "waiting for user approval (deploy)" OR "pipeline complete">
```

If the pipeline is blocked on deploy approval, also include a `cobuild deploy <id> --dry-run` output block so the user can see exactly what would happen.

If the pipeline hit a failure, include the specific failing gate's body (from `cobuild audit <id>`) so the user sees what's wrong without asking.

## Anti-patterns

- **Don't poll cobuild status repeatedly instead of cobuild audit <id>.** `status` lists all pipelines; `audit <id>` gives you the timeline for the one you care about.
- **Don't re-dispatch a task that's in needs-review.** Run `process-review <task-id>` instead — it handles the merge/reject loop.
- **Don't run `cobuild complete` from the orchestrator session.** That command is for dispatched CoBuild agents to run as their LAST action, not for orchestrators.
- **Don't ask the user "should I run X?" when X is the obvious mechanical next step.** The whole point of this skill is that you don't ask. Run the command, report the result.
- **Don't give up and hand off after one failure.** Most failures have well-defined recovery paths above. Retry, escalate a gate, re-dispatch — exhaust the options before bailing.

## Related

- `skills/design/gate-readiness-review.md` — what a design dispatched agent does
- `skills/decompose/decompose-design.md` — what a decompose dispatched agent does
- `skills/review/gate-process-review.md` — the skill behind `cobuild process-review`
- cobuild KB: `cb-5ae167` (CoBuild Reference root) — architecture, CLI, config, skills, helpers
