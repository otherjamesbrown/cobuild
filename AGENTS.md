## CoBuild Dispatch Context

You are a dispatched CoBuild agent. Your task prompt was passed as the initial message.
Additional context is in `.cobuild/dispatch-context.md` — read it if you need architecture, design context, or project anatomy.

<!-- BEGIN COBUILD INTEGRATION v:1 hash:15c32ea4 -->
# CoBuild Pipeline Instructions

This project uses CoBuild for pipeline automation. If you are a dispatched CoBuild agent working on a task, follow these instructions.

## For orchestrators — the ONLY thing you need to know

Every CoBuild command prints a `Next step:` line telling you exactly what to run next. **Follow it mechanically.** Do not reason about which phase needs which command — that's what the CLI is for.

The loop:

1. `cobuild init <id>` (if the pipeline doesn't exist yet)
2. `cobuild dispatch <id>` — spawns a dispatched CoBuild agent for the current phase
3. Read the `Next step:` line printed by the command — run that
4. Repeat step 3 until phase = done
5. **Report back to the user with what shipped.** Not "dispatched, let me know when you're ready" — wait for completion, then summarise the outcome.

If you are ever unsure what to run next, run `cobuild next <id>` — it prints the single concrete command for the current state.

Do NOT execute phase work yourself (decompose, review, investigate, etc.) just because you could. **Every phase has a skill and a dispatched CoBuild agent runs it.** Your only job as orchestrator is to type the commands, follow the output, and report the result when it's done.

**When a user asks you to run/orchestrate/complete a shard through CoBuild end-to-end, load the `skills/orchestrate/run-pipeline.md` skill and follow it.** That skill is the single source of truth for the orchestrator loop, per-phase actions, failure modes, and the structured report format. Do not reason about what to do from memory — load the skill and follow it mechanically.

## Dispatch is not a handoff to the user

**Common failure mode:** an orchestrator agent runs `cobuild dispatch <id>`, sees "Dispatched" in the output, and stops. This is wrong. Dispatch spawns a **separate** dispatched CoBuild agent in a tmux worktree that runs asynchronously — CoBuild does not block your session while it runs. Your job is not done until that agent has completed and you have reported back to the user.

**After every `cobuild dispatch` or `cobuild dispatch-wave`:**

1. Follow the `Next step:` line — usually `cobuild audit <id>` or `cobuild wait <id>`
2. Poll with `cobuild audit <id>` every ~30-60 seconds (do NOT use `cobuild wait` — it's a 2h blocker). You can use a short `sleep` between polls or just retry manually.
3. When the dispatched agent completes (status = `needs-review`, or the pipeline phase has advanced), **inspect what happened** via `cobuild audit <id>` and `cobuild wi show <child-id>` for any new shards
4. **Then, and only then, report back to the user** with a concrete summary: which shards were created, which PRs were opened, which gates passed or failed, and what the next concrete action is

**Never return to the user with just "Dispatched" and nothing else.** The user has to chase you for the outcome every time, and that's exactly the manual overhead CoBuild exists to eliminate. If the dispatched agent will take a long time (implementation waves, review cycles), it is still your job to wait — CoBuild is designed so orchestrators follow through the full lifecycle. The only legitimate reasons to return to the user before completion are:

- The dispatched agent is genuinely blocked (gate failed, critical review finding, merge conflict) and needs a human decision
- Deploy phase reached — deploy always requires human approval
- The pipeline has hit a true dead-end (max retries exceeded, infrastructure error)

In any of those cases, explain WHY you're stopping and WHAT the user needs to decide. Don't just drop the ball.

## Terminology

Two roles show up throughout CoBuild's docs, skills, and commit messages. Use these terms consistently:

- **orchestrator agent** — whoever invokes `cobuild dispatch`, `cobuild run`, or any other pipeline CLI. Stays lightweight and delegates work. Can be an interactive Claude/Codex session, the `cobuild poller` daemon, a cron job, or a human at a shell prompt.
- **dispatched CoBuild agent** — the fresh Claude Code or Codex process CoBuild spawns in a tmux window inside a git worktree to execute a phase's skill. Does all the real reading, editing, and committing. Exits when the skill is done.

If you see "M", "parent session", "calling agent", "fresh session", or "implementing agent" in older docs, they all map onto one of these two terms — prefer the canonical terms above.

## Project

- **Name:** cobuild
- **Prefix:** cb-
- **Workflows:**
  - bug: fix → review → done
  - bug-complex: investigate → implement → review → done
  - design: design → decompose → implement → review → done
  - task: implement → review → done

## Commands

### Pipeline

| Command | When to use |
|---------|------------|
| `cobuild init <id>` | Submit a design/bug/task to the pipeline |
| `cobuild dispatch <task-id>` | Dispatch to a dispatched CoBuild agent (works for every phase) |
| **`cobuild next <id>`** | **Print the single next command to run for a pipeline — use when confused** |
| `cobuild dispatch-wave <design-id>` | Dispatch all ready tasks in a wave |
| `cobuild process-review <task-id>` | Process Gemini review → merge or re-dispatch |
| `cobuild merge <task-id>` | Merge an approved PR manually |
| `cobuild merge-design <design-id>` | Smart merge all PRs (conflict detection) |
| `cobuild deploy <design-id>` | Deploy affected services |
| `cobuild retro <design-id>` | Run retrospective |
| `cobuild status` | Show all active pipelines with ACTIVITY column (dispatched/awaiting-transition/blocked) |
| `cobuild inspect <id>` | Full diagnostic view of a pipeline (shard + sessions + PR + gates) |
| `cobuild audit <id>` | View gate history and timeline |
| `cobuild show <id>` | Compact current state for one pipeline |
| `cobuild scan` | Refresh project anatomy (file index for agents) |
| `cobuild wait <id> [id...]` | Block until tasks reach target status (2h max) |
| `cobuild complete <task-id>` | **Run as your LAST action** if you ARE the dispatched agent |

**Manual gate recording (Advanced — see below):** `cobuild review / decompose / investigate / gate` — use only when the gate work happened outside a dispatched agent session.

### Work Items

| Command | Purpose |
|---------|--------|
| `cobuild wi show <id>` | Read a design, task, or bug |
| `cobuild wi list --type <type>` | List work items |
| `cobuild wi links <id>` | See relationships |
| `cobuild wi status <id> <status>` | Update status |
| `cobuild wi append <id> --body "..."` | Append content |
| `cobuild wi create --type <type> --title "..."` | Create work item |

## How to Run a Pipeline

**Default and only flow: dispatch.** Every phase has a skill, and `cobuild dispatch` spawns a dispatched CoBuild agent that reads the skill and executes it. You never do the work yourself.

```bash
cobuild init <id>          # if the pipeline doesn't exist yet
cobuild dispatch <id>      # start — spawns a dispatched CoBuild agent for the current phase
# → follow the `Next step:` line it prints
# → repeat until phase = done
```

**`cobuild dispatch` is phase-aware.** It reads the current pipeline phase and generates the right prompt automatically — readiness review for design, decomposition for decompose, investigation for investigate, implementation for tasks, and so on. One command advances the entire pipeline.

**If you are ever confused:** run `cobuild next <id>`. It prints the single concrete command for the current state. Do not try to infer it from the workflow table or your memory of which phase comes next — let the CLI tell you.

**Do not own any phase yourself.** Even if you (the orchestrator agent) *could* do the work inline — read the design, break it into tasks, record the gate — **don't**. That pattern exists to be used in genuinely exceptional cases (see Advanced below), not as a default shortcut. The dispatched agent model keeps your context lean and produces a clean audit trail.

### Bug workflow note

**Most bugs** use the `fix` workflow — a single dispatched CoBuild agent investigates and fixes together in one session. **Complex bugs** (root cause unknown, multi-repo, data/security implications, non-obvious fix shape) should be labeled `needs-investigation` before `cobuild init` — this routes them to the `bug-complex` workflow with a read-only investigation phase first.

```bash
cobuild wi label add <bug-id> needs-investigation   # only if complex
cobuild init <bug-id>
cobuild dispatch <bug-id>    # follow the Next step: output from here
```

## Advanced: recording a gate without dispatching

**This is an exceptional path, not the default.** Use it only when the gate work genuinely happened outside a dispatched CoBuild agent session — for example, a design that was reviewed live with the developer in a meeting, or an investigation that was done by a human. For anything the pipeline can do, prefer `cobuild dispatch`.

```bash
cobuild review <id> --verdict pass --readiness 5 --body "<findings>"   # record design review
cobuild decompose <id> --verdict pass --body "<task summary>"          # record decomposition
cobuild investigate <id> --verdict pass --body "<root cause>"           # record investigation
```

These commands record the gate and advance the phase without dispatching. **If you find yourself reaching for them because you weren't sure whether decompose had a skill, stop and run `cobuild dispatch <id>` instead — every phase has one.**

## Task Completion Protocol

When you have completed your implementation:

1. **Run `cobuild complete <task-id>`**

The Stop hook will run `cobuild complete` automatically when you finish.
If it fails, run it manually as your last action.

### Non-code tasks

If the task was tagged `completion_mode: direct`, it is a non-code task. Use this only for work expected outside the repo/worktree, such as KB updates, config/data changes, or user-global state.

Your completion step does not change: still run `cobuild complete <task-id>` as the last action. CoBuild will use the direct path for `completion_mode: direct`; otherwise `code` remains the normal path, and if no mode was set CoBuild falls back to auto-detection. This does not change deploy behavior.

## Orchestrator Protocol

If you are the orchestrator agent (dispatching tasks, not executing them yourself),
**follow through the full lifecycle. Do not stop after dispatch.** See the "Dispatch is not a handoff to the user" section above for the common failure mode.

After dispatching tasks:

1. **Monitor** — use `cobuild audit <id>` or `cobuild status` for instant checks (do NOT use `cobuild wait` as a background task — it's a 2-hour blocking command)
2. **Process reviews** — run `cobuild process-review <task-id>` for each needs-review task. This automatically: waits for Gemini review, classifies findings, merges clean PRs, or re-dispatches agents for fixes. If it says "Waiting" — Gemini hasn't reviewed yet, retry after a few minutes.
3. **Report** — when the pipeline has advanced or completed, tell the user **what shipped** with specifics (shard IDs, PR URLs, gate verdicts). Not "dispatched, let me know". Not "want me to review?". Concrete outcome.
4. **Deploy** — do NOT deploy automatically. Run `cobuild deploy <id> --dry-run` to show which services would be affected, then **ask the user** for approval. On approval, run `cobuild deploy <id>` (triggers deploy commands from pipeline config with smoke tests and auto-rollback). Deploy touches production and is always a human decision.

Only pause for user input if there is an actual blocker: merge conflict, critical Gemini finding you can't resolve, a design decision, or deploy approval.

### Report format when work completes

When a dispatched agent's work has actually landed, return to the user with a short structured summary:

```
Completed <phase> for <work-item-id>.

- Child shards created: <cb-xxx, cb-yyy, ...>  (if decompose)
- PRs opened: <url1, url2, ...>                (if implement)
- PRs merged: <url1, url2, ...>                (if review/merge)
- Gate verdict: pass|fail round N              (always)
- Pipeline phase: <old> → <new>                (always)
- Next concrete action: cobuild <...>          (always)
```

Omit rows that don't apply. Do not embellish. Do not ask "want me to continue?" — if the next action isn't a deploy or a blocked state, just continue automatically.

## What CoBuild manages vs what you do directly

Be explicit when reporting status. State clearly whether an action is:
- **A CoBuild pipeline action** — "CoBuild will handle this: `cobuild merge-design <id>`"
- **A direct action you'll take** — "I'll run the deploy command now"
- **A human action needed** — "You need to approve this PR"

## Skills

| Directory | Skills | Purpose |
|-----------|--------|---------|
| `orchestrate/` | run-pipeline | Orchestrator-side pipeline driver (run-pipeline.md) — load this when asked to run a shard through CoBuild end-to-end |
| `design/` | gate-readiness-review, implementability | Design evaluation |
| `decompose/` | decompose-design | Break designs into tasks |
| `fix/` | fix-bug | Single-session bug fix (investigate + implement) |
| `investigate/` | bug-investigation | Root cause analysis for needs-investigation bugs |
| `implement/` | dispatch-task, stall-check | Task dispatch and monitoring |
| `review/` | gate-process-review, gate-review-pr, merge-and-verify | Code review |
| `done/` | gate-retrospective | Post-delivery retrospective |
| `shared/` | bootstrap-claude-md, bootstrap-connector-beads, bootstrap-connector-cp, bootstrap-context-layers, bootstrap-skills, bootstrap-storage-postgres, bootstrap, create-design, design-review, playbook | Cross-phase reference |

<!-- END COBUILD INTEGRATION -->
