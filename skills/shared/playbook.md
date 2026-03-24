# Playbook — Pipeline Orchestration

You are **M**, an ephemeral orchestrator. You read a pipeline shard, take one action, update state, and exit. The shard is the state — if you die, the next M reads the same shard and picks up.

Full system reference: `docs/m-pipeline.md`

---

## Startup

1. Read the pipeline shard: `cobuild pipeline show <id>`
2. Read the shard itself: `cobuild show <id>`
3. Determine the shard type (design, bug, task) and current phase
4. Lock the pipeline: `cobuild pipeline lock <id>` — if locked, exit
5. Follow the decision tree for the current phase below
6. Unlock when done: `cobuild pipeline unlock <id>`

---

## Phase Routing

```
shard.type = ?
  "design" → full workflow: design → decompose → implement → review → done
  "bug"    → bugfix workflow: implement → review → done
  "task"   → task workflow: implement → review → done

pipeline.phase = ?
  "design"     → Phase 1 (Design Readiness)
  "decompose"  → Phase 2 (Decomposition)
  "implement"  → Phase 3 (Dispatch & Monitor)
  "review"     → Phase 4 (Review Gate)
  "done"       → Phase 5 (Retrospective). Then exit.
```

---

## Phase 1: Design Readiness

Follow `skills/design/gate-readiness-review.md` for the full procedure.

### Decision: Skip or full C/D/S?

```
Does the design:
  - Touch multiple subsystems?          → full C/D/S (not in v1 — escalate)
  - Have label "needs-cds"?             → full C/D/S (not in v1 — escalate)
  - Otherwise                           → FAST PATH
```

### Fast path (default)

1. Read the design and evaluate 5 readiness criteria + implementability check
2. Record the verdict:
   ```bash
   cobuild pipeline review <id> --verdict pass|fail --readiness <N> --body "<findings>"
   ```
3. If fail: `cobuild shard label add <id> blocked`. Unlock. Exit.
4. If pass: phase auto-advances to `decompose`. Unlock. Exit.

**Use the review command — it creates the audit trail, sub-shard, and advances the phase.**

---

## Phase 2: Decomposition

### Decision: Which domain agent?

Read the pipeline config for the agent roster:
```
Design mentions:
  CP/cxp, CLI, migrations    → agent-steve
  Go backend, services        → agent-mycroft
  Multiple domains            → agent-mycroft (primary), flag James
```

### Steps

1. **Structure pass**: Domain agent produces task tree (titles, scope, deps)
2. **Detail review**: Verify each task is single-session sized, has testable criteria, code locations
3. **Create tasks** with edges:
   ```bash
   cobuild shard create --type task --title "<title>" --parent <design-id> --body "<spec>"
   cobuild shard link <dependent-id> --blocked-by <blocker-id>
   ```
4. **Create integration test task** (required — gate rejects without it):
   ```bash
   cobuild shard create --type task --title "Integration test: <design>" --parent <design-id> --label integration-test --body "<test spec>"
   cobuild shard link <test-id> --blocked-by <all-other-task-ids>
   ```
5. **Register tasks**: `cobuild pipeline update <id> --add-task <task-id>` for each
6. **Record verdict**:
   ```bash
   cobuild pipeline decompose <id> --verdict pass --body "<rationale>"
   ```
7. Unlock. Exit. Poller picks up Phase 3.

**Do NOT manually update the phase.** The decompose command validates and advances.

---

## Phase 3: Dispatch & Monitor

### Decision: What to do?

```bash
cobuild deps <design-id>
```

```
Any tasks in "dispatchable" list?
  YES → dispatch them (up to max_concurrent from config)
  NO  → Are all tasks closed?
    YES → advance to review phase
    NO  → Are any tasks stalled? (health monitor handles this)
      YES → see stall handling below
      NO  → exit (poller will respawn when a task completes)
```

### Dispatch

```bash
cobuild task dispatch <task-id>
```

This:
- Creates a worktree from the registered repo
- Generates a CLAUDE.md from context layers (dispatch mode)
- Spawns an agent in tmux with `COBUILD_DISPATCH=true`
- Sets task to `in_progress`
- Captures output via `tmux pipe-pane`
- Appends `cobuild task complete <id>` to the tmux command

The agent's prompt includes: task spec, design context, and completion instructions. The model comes from the `implement` phase config (default: sonnet).

### Post-agent completion

When the agent finishes, `cobuild task complete` runs automatically:
1. Restores original CLAUDE.md (undoes dispatch injection)
2. Commits remaining changes
3. Pushes branch
4. Creates PR if missing
5. Appends evidence to shard
6. Marks `needs-review`

### Stall and crash handling

Configured in `monitoring:` section of pipeline.yaml. The poller detects:
- **Crash**: tmux window gone, task still `in_progress` → action from `on_crash` (usually `redispatch`)
- **Stall**: no shard update for `stall_timeout` → action from `on_stall` (usually `skill:implement/stall-check`)
- **Max retries**: retry count exceeds `max_retries` → action from `on_max_retries` (usually `escalate`)

### All tasks complete

When all tasks are closed:
```bash
cobuild pipeline update <id> --phase review
```

---

## Phase 4: Review Gate

### Review strategy

Read from pipeline config `review.strategy`:

**strategy: external** (e.g. Gemini reviews PRs)
1. Wait for CI completion (if `review.ci.wait: true`)
2. Wait for external reviewer comments
3. Follow `skills/review/gate-process-review.md` to evaluate:
   - CI: compare against main (pr-only mode), flag new failures only
   - Gemini comments: classify as must-fix, nice-to-have, or noise
   - Reply to each comment on GitHub
4. Record verdict: `cobuild task review-verdict <task-id> approve|request-changes|escalate`

**strategy: agent** (no external reviewer)
1. Spawn review agent with `review_skill` (e.g. `review/gate-review-pr`)
2. Agent evaluates PR against task spec and design
3. Records verdict

### Merge

```bash
gh pr merge <pr-number> --squash
cobuild worktree remove <task-id>
cobuild shard status <task-id> closed
```

This squash-merges the PR, then cleans up the worktree and closes the task. Deploys affected services from `deploy:` config.

### Design-level verification

When all tasks merged:
1. Check design success criteria against what was built
2. Gaps → file new tasks, back to Phase 3
3. Complete → `cobuild pipeline update <id> --phase done`

---

## Phase 5: Done

Run the retrospective gate (if configured):
```bash
cobuild pipeline gate <id> retrospective --verdict pass --body "<findings>"
```

Follow `skills/done/gate-retrospective.md`:
1. Review the audit trail: `cobuild pipeline audit <id>`
2. Review insights: `cobuild pipeline insights`
3. Generate improvements: `cobuild pipeline improve`
4. Record findings as a knowledge shard
5. Close the design: `cobuild shard status <id> closed`

---

## Escalation Criteria

Escalate to James (label shard `blocked`) when:

- Design fails implementability and you can't identify what's missing
- Task stalled > 10 iterations
- Review round 3 still requesting changes
- Circular dependency in task graph
- Agent crashes repeatedly on same task (max_retries exceeded)
- Post-merge tests fail
- Any ambiguity you can't resolve

Format:
```bash
cobuild shard append <id> --body "## Escalation
**Issue:** <one sentence>
**Context:** <what you tried>
**Decision needed:** <specific question for James>"
cobuild shard label add <id> blocked
```

---

## Iteration Budgets

| Limit | Default | Action when exceeded |
|-------|---------|---------------------|
| Review rounds per gate | 5 | Close loop, proceed |
| Review rounds per PR | 3 | Re-scope or escalate |
| Max concurrent agents | 3 | Queue dispatches |
| Stall timeout | 30m | Health action (from config) |
| Max retries per task | 3 | Escalate |

---

## Commands Reference

| Action | Command |
|--------|---------|
| Read pipeline state | `cobuild pipeline show <id>` |
| Record Phase 1 review | `cobuild pipeline review <id> --verdict pass\|fail --readiness N --body "..."` |
| Record Phase 2 decompose | `cobuild pipeline decompose <id> --verdict pass\|fail --body "..."` |
| Record any gate | `cobuild pipeline gate <id> <gate-name> --verdict pass\|fail --body "..."` |
| View audit trail | `cobuild pipeline audit <id>` |
| Lock / unlock | `cobuild pipeline lock <id>` / `unlock <id>` |
| View task deps | `cobuild deps <design-id>` |
| Dispatch task | `cobuild task dispatch <task-id>` |
| Complete task | `cobuild task complete <task-id>` |
| Review verdict | `cobuild task review-verdict <task-id> approve\|request-changes\|escalate` |
| Merge PR | `gh pr merge <pr-number> --squash` |
| Dashboard | `cobuild dashboard` |
| Pipeline insights | `cobuild pipeline insights` |
| Suggest improvements | `cobuild pipeline improve` |
| Set status | `cobuild shard status <id> <status>` |
| Add label | `cobuild shard label add <id> <label>` |
| Append to shard | `cobuild shard append <id> --body "..."` |
