# CoBuild

Config-driven AI agent pipeline: design to deployed code with stage gates, audit trails, and self-improvement.

## What is CoBuild?

CoBuild orchestrates AI agents through a structured pipeline — from design review through decomposition, implementation, code review, and deployment. Every phase transition is enforced by a configurable gate that creates an audit trail.

**Key features:**
- [**Config-driven**](docs/guides/config.md) — phases, gates, models, context layers, deploy rules — all YAML
- [**Skills as markdown**](docs/guides/skills.md) — extend the pipeline by writing a `.md` file
- [**Connectors**](#connectors) — pluggable work-item backends (Context Palace, Beads, future Jira)
- [**Storage**](#storage) — pluggable data store for pipeline state (Postgres, future SQLite/files)
- [**Context layers**](docs/guides/context-layers.md) — control exactly what each agent sees per phase
- [**Phase-aware dispatch**](#how-it-works) — `cobuild dispatch` auto-detects the phase and generates the right prompt
- [**Self-improving**](docs/guides/feedback-loop.md) — retrospectives feed findings back into skills
- [**Audit trail**](docs/guides/audit-trail.md) — every decision recorded in Postgres
- [**Session analytics**](#session-tracking) — token usage, file changes, events per dispatch

## Quick Start

```bash
# Install from Homebrew
brew tap otherjamesbrown/cobuild
brew install cobuild

# Or install with Go
go install github.com/otherjamesbrown/cobuild/cmd/cobuild@latest

# In your project repo:
cobuild setup                  # register the repo
cobuild init-skills            # copy default skills
cobuild scan                   # generate file index for agents
cobuild update-agents          # generate AGENTS.md with pipeline instructions
cobuild explain                # see your pipeline in human-readable form

# Submit a design to the pipeline:
cobuild init <design-id>       # initialise pipeline (auto-detects type)
cobuild dispatch <design-id>   # spawn agent for the current phase
cobuild wait <design-id>       # wait for agent to complete
cobuild status                 # see all active pipelines
```

For full interactive setup (connector, storage, context layers), read the [bootstrap guide](skills/shared/bootstrap.md) or ask your AI assistant to follow it.

Tagged releases publish Homebrew formula updates into this repository's `Formula/` directory, so the repo doubles as its own tap. If branch protection blocks workflow writes, set `HOMEBREW_TAP_GITHUB_TOKEN` for the release job.

### Using Beads

```yaml
# .cobuild.yaml
project: my-project
prefix: mp-

# .cobuild/pipeline.yaml
connectors:
    work_items:
        type: beads
```

CoBuild uses the `bd` CLI to read/write work items. All `cobuild wi` commands work the same regardless of connector:

```bash
cobuild wi show mp-abc123      # works with Beads
cobuild wi list --type design   # works with Beads
cobuild wi create --type bug --title "..."  # works with Beads
```

### Using Context Palace

```yaml
# .cobuild/pipeline.yaml
connectors:
    work_items:
        type: context-palace
```

Uses the `cxp` CLI with `-o json`.

## How It Works

### Workflows

| Workflow | Phases | Use case |
|----------|--------|----------|
| `design` | design → decompose → implement → review → done | Full design-to-delivery |
| `bug` | fix → review → done | Bug fixes (default — investigate+implement in one session) |
| `bug-complex` | investigate → implement → review → done | Complex bugs (label `needs-investigation` to escalate) |
| `task` | implement → review → done | Standalone tasks |

### Bug Workflow

By default, bugs go straight to a `fix` phase — the agent investigates as it fixes, in one session:
1. Read the bug report
2. If cause isn't obvious, trace the code
3. Append findings to the bug body
4. Implement the fix, run tests

For bugs where the root cause is unknown, the fix spans multiple systems, or there are data/security implications, label the bug `needs-investigation`. This routes it through the `bug-complex` workflow: a read-only investigation phase first, then a separate implement phase.

**When to use `needs-investigation`:** root cause unknown · cross-system interaction · data or security impact · intermittent/environment-dependent · fix shape non-obvious · requires stakeholder decision

### Pipeline Phases

1. **Design Review** — evaluate readiness + implementability against 5 criteria
2. **Decomposition** — break design into tasks with dependency ordering and wave assignment
3. **Fix** (bugs, default) — single-session investigate+implement; agent traces cause then fixes
4. **Investigation** (bugs with `needs-investigation` label) — read-only root cause analysis, fix specification
5. **Implement** — dispatch agents in isolated worktrees with phase-aware context
6. **Review** — external (Gemini) or agent-based, with CI integration
7. **Done** — retrospective captures lessons and feeds back into skills

### Phase-Aware Dispatch

`cobuild dispatch` reads the current pipeline phase and generates the right prompt automatically:

| Phase | What the dispatched agent does |
|-------|-------------------------------|
| design | Evaluate readiness, check 5 criteria, record gate |
| decompose | Break into tasks, assign waves, set dependencies |
| fix | Investigate cause and implement fix in one session (default for bugs) |
| investigate | Read-only root cause analysis, create fix task (bugs with `needs-investigation`) |
| implement | Task/bug shards: write code, run tests, create PR. Design shards: dispatch child-task waves by default; pass `--mono --force` to force one design-level PR |
| review | Check PR against spec, evaluate CI, record verdict |
| done | Run retrospective, suggest improvements |

### Dispatch Reliability

Dispatched agents complete reliably without manual intervention:

- **Stop hook** — `.claude/settings.local.json` is written into each worktree with a `Stop` hook that runs `cobuild complete` when the agent finishes. Agents don't need to remember to call it.
- **Auto-create pipeline run** — `cobuild dispatch <id>` works without `cobuild init`. A pipeline run is created on the fly if one doesn't exist.
- **Workspace trust** — Claude Code's "trust this folder" dialog is pre-accepted for worktrees so agents start immediately.
- **Artifact guard** — `.cobuild/` dispatch artifacts and the injected `CLAUDE.md` section are excluded from auto-commits and protected by `.gitignore`.
- **Permission deny list** — agents cannot edit `.claude/**` files in worktrees, preventing permission-prompt stalls.

### Manual vs Autonomous

**Manual mode** (default) — you step through each phase:

```bash
cobuild init <id>              # start pipeline (optional — dispatch auto-creates)
cobuild dispatch <id>          # spawn agent for current phase
cobuild wait <id>              # wait for completion (Stop hook handles `cobuild complete`)
# repeat for each phase
```

For designs that have reached `implement`, the default path is wave-based child-task dispatch. Use `cobuild dispatch-wave <design-id>` directly, or `cobuild dispatch <design-id>` if you want the phase-aware wrapper to choose that path for you. Use `cobuild dispatch --mono --force <design-id>` only when you intentionally want one design-level PR and accept overlap risk with child tasks.

**Autonomous mode** — the poller handles everything:

```bash
cobuild run <id>               # mark for autonomous processing
cobuild poller                 # processes all autonomous pipelines
```

Or label-based: add the `cobuild` label to any work item and the poller picks it up automatically.

## Configuration

Full example with comments: [`examples/pipeline.yaml`](examples/pipeline.yaml)
Minimal example: [`examples/pipeline-minimal.yaml`](examples/pipeline-minimal.yaml)

```yaml
# .cobuild/pipeline.yaml

github:
    owner_repo: your-org/your-repo

build:
    - go build ./...
test:
    - go test ./...

connectors:
    work_items:
        type: beads                  # or context-palace

storage:
    backend: postgres

phases:
    design:
        gate: readiness-review
        skill: design/gate-readiness-review.md
    decompose:
        gate: decomposition-review
    fix:
        skill: fix/fix-bug.md             # single-session investigate+implement
    investigate:
        gate: investigation
        skill: investigate/bug-investigation.md
    implement:
        skill: implement/dispatch-task.md
        stall_check: implement/stall-check.md
    review:
        gate: review
        skill: review/gate-process-review.md
    done:
        gate: retrospective
        skill: done/gate-retrospective.md

workflows:
    design:
        phases: [design, decompose, implement, review, done]
    bug:
        phases: [fix, review, done]             # default: single-session fix
    bug-complex:
        phases: [investigate, implement, review, done]  # escalation: label needs-investigation
    task:
        phases: [implement, review, done]

dispatch:
    max_concurrent: 3
    default_model: sonnet
    # tmux_session auto-creates as cobuild-<project>

review:
    strategy: external
    external_reviewers: [gemini]
    ci:
        mode: pr-only
        wait: true

deploy:
    pre_deploy: "./scripts/migrate.sh"    # run before any service deploy
    services:
        - name: api
          trigger_paths: [services/api/**]
          command: ./scripts/deploy.sh api
          smoke_test: curl -sf https://api.example.com/health
          rollback: ./scripts/rollback.sh api

context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

Or use the zero-config directory convention:

```
.cobuild/context/
    always/           # every agent
    design/           # design phase agents
    fix/              # bug fix agents (default)
    implement/        # implementing agents
    investigate/      # bug investigation agents (needs-investigation escalation)
```

## Connectors

CoBuild reads and writes work items through **connectors** — pluggable backends for external systems. CoBuild's own orchestration data is stored separately.

| Connector | Backend | CLI | Config |
|-----------|---------|-----|--------|
| `context-palace` | Context Palace (Postgres) | `cxp` | `type: context-palace` |
| `beads` | Beads (Dolt) | `bd` | `type: beads` |

All `cobuild wi` commands work identically regardless of connector.

## Storage

CoBuild stores its own data (pipeline runs, gate records, session analytics) in Postgres:

| Table | What it stores |
|-------|---------------|
| `pipeline_runs` | One row per pipeline — phase, status, mode |
| `pipeline_gates` | Gate audit records — verdicts, findings |
| `pipeline_tasks` | Task tracking — wave assignments, status |
| `pipeline_sessions` | Per-dispatch records — timing, model, prompt, results |
| `pipeline_session_events` | Per-tool-call events — file reads, edits, commands |

## Skills

Skills are markdown files with YAML frontmatter that tell agents what to do:

| Directory | Skills | Purpose |
|-----------|--------|---------|
| `design/` | gate-readiness-review, implementability | Design evaluation |
| `decompose/` | decompose-design | Break designs into tasks |
| `fix/` | fix-bug | Single-session bug fix (default bug workflow) |
| `investigate/` | bug-investigation | Root cause analysis (needs-investigation escalation) |
| `implement/` | dispatch-task, stall-check | Task dispatch and monitoring |
| `review/` | gate-review-pr, gate-process-review, merge-and-verify | Code review |
| `done/` | gate-retrospective | Post-delivery retrospective |
| `shared/` | playbook, create-design, design-review | Cross-phase reference |

```bash
cobuild init-skills            # copy defaults to your repo
cobuild init-skills --update   # refresh defaults, preserve your gotchas
```

## Context Layers

Control what context each agent sees per phase. See the full [context layers guide](docs/guides/context-layers.md).

Sources: `file:<path>`, `work-item:<id>`, `skills:<name>`, `claude-md`, `dispatch-prompt`, `parent-design`

When: `always`, `interactive`, `dispatch`, `phase:<name>`, `gate:<name>`

Or use the zero-config directory convention — drop `.md` files in `.cobuild/context/<phase>/`.

## Project Anatomy

`cobuild scan` generates a file index for the codebase — every file with its line count, estimated token cost, and auto-detected description:

```bash
cobuild scan              # generate .cobuild/context/implement/anatomy.md
cobuild scan --check      # check if stale
```

The anatomy loads automatically for all dispatched agents (it's in the `always/` context directory). Agents use it to understand the codebase structure without reading every file — saving significant tokens on exploration.

Run `cobuild scan` during bootstrap, before each dispatch wave, and after merging significant changes.

## Token Optimization

CoBuild tracks token usage and detects waste patterns:

- **Repeated read detection** — hooks warn when an agent re-reads a file it already has in context
- **Project anatomy** — agents check the file index before reading large files
- **Transcript analysis** — `cobuild admin tokens` extracts exact token counts and costs from session transcripts
- **Waste detection** — `cobuild admin waste` identifies repeated reads, oversized reads, context overflow, and error loops

### Token Reduction in Practice — Penfold

CoBuild's dispatch reliability rework (cb-7aa91d) was specifically designed to reduce wasted tokens in dispatched sessions. Key changes, validated on the [penfold](https://github.com/otherjamesbrown/penfold) project:

- **Context isolation** — assembled context (anatomy, design, layers) goes to `.cobuild/dispatch-context.md` instead of being injected into `CLAUDE.md`. Agents read it on demand rather than having it forced into every prompt turn. This reduced the base context size from ~2900 lines per session to ~5 lines (the pointer section).
- **Stop hook completion** — agents no longer need to read AGENTS.md to learn the completion protocol. The Stop hook fires `cobuild complete` automatically, eliminating the "forgot to complete" failure mode that wasted full sessions (~$5-15 per stalled dispatch).
- **Fix-phase collapse** — bugs go straight to a single `fix` session instead of separate `investigate` + `implement` dispatches. For penfold's bug backlog (5 bugs), this halved the number of dispatched sessions and the associated context-setup overhead.
- **Errcheck cleanup** — 103 unchecked error returns fixed across penfold's codebase in a single dispatch (PR #98, 107 files). This prevents agents from hitting silent failures that trigger error-recovery loops, a major source of wasted tokens.
- **Workflow determinism** — 7+1 Temporal workflows fixed for map-iteration and json.Marshal non-determinism. Non-deterministic replays cause cascading failures that agents often try to debug, burning tokens on infrastructure issues rather than feature work.

## Session Tracking

Every dispatched agent session is recorded in Postgres with:
- Timing (start, end, duration)
- Model used
- Full prompt and assembled context
- Files changed, lines added/removed, commits
- PR URL
- Session log (raw output)

Claude Code hooks record per-event data (tool calls, compaction, errors) for detailed analytics.

## Commands

| Command | Purpose |
|---------|---------|
| **Setup** | |
| `cobuild setup` | Register repo, create config |
| `cobuild init-skills` | Copy default skills into repo |
| `cobuild init-skills --update` | Refresh skills, preserving gotchas |
| `cobuild scan` | Generate project anatomy (file index for agents) |
| `cobuild update-agents` | Regenerate AGENTS.md from current skills/config |
| `cobuild explain` | Show pipeline in human-readable form |
| **Pipeline** | |
| `cobuild init <id>` | Start pipeline (auto-detects type → start phase) |
| `cobuild init <id> --autonomous` | Start in autonomous mode |
| `cobuild run <id>` | Submit for autonomous processing by poller |
| `cobuild dispatch <id>` | Spawn agent for current phase (phase-aware) |
| `cobuild dispatch-wave <id>` | Dispatch all ready tasks for a design |
| `cobuild wait <id> [id...]` | Wait for tasks to complete |
| `cobuild complete <id>` | Post-agent completion (PR, evidence) |
| `cobuild gate <id> <name>` | Record gate verdict |
| `cobuild review <id>` | Readiness review gate |
| `cobuild decompose <id>` | Decomposition gate |
| `cobuild investigate <id>` | Bug investigation gate |
| `cobuild merge <id>` | Merge approved PR, close task |
| `cobuild merge-design <id>` | Smart merge all PRs (conflict detection) |
| `cobuild deploy <id>` | Deploy affected services |
| `cobuild retro <id>` | Run pipeline retrospective |
| **Status** | |
| `cobuild status` | Show all active pipelines |
| `cobuild audit <id>` | Show gate timeline |
| **Work Items** | |
| `cobuild wi show <id>` | Show work item |
| `cobuild wi list` | List work items |
| `cobuild wi links <id>` | Show relationships |
| `cobuild wi status <id> <status>` | Update status |
| `cobuild wi create` | Create work item |
| `cobuild wi append <id>` | Append content |
| `cobuild wi label add <id> <label>` | Add label |
| **Admin** | |
| `cobuild admin health` | System health check (includes anatomy freshness) |
| `cobuild admin cleanup` | Remove stale worktrees, branches, old data |
| `cobuild admin db-stats` | Database usage |
| `cobuild admin stuck` | Find stuck pipelines and orphan tasks |
| `cobuild admin tokens` | Parse transcript for exact token usage and costs |
| `cobuild admin waste` | Detect token waste patterns from session events |
| **Autonomous** | |
| `cobuild poller` | Process autonomous pipelines continuously |
| `cobuild poller --once` | Single pass |
| `cobuild insights` | Execution analysis |
| `cobuild improve` | Suggest pipeline improvements |

## Tips

### Check status with `audit`, not `wait`

`cobuild wait` is a blocking command with a 2-hour timeout. **Do not run it as a background task and expect it to report back** — it's designed for fully automated pipelines, not interactive sessions.

When you want to know "is it done?", use:

```bash
cobuild audit <id>     # instant — shows gate timeline, verdicts, current phase
cobuild status         # instant — shows all active pipelines
```

### Run manually before going autonomous

Don't jump straight to `cobuild poller`. Step through the pipeline manually for your first few designs:

```bash
cobuild init <id>                # start pipeline (optional — dispatch auto-creates)
cobuild dispatch <id>            # spawn agent for current phase
cobuild audit <id>               # check if the gate passed (instant, no waiting)
cobuild dispatch <id>            # next phase
cobuild merge-design <id>        # merge all PRs
cobuild deploy <id>              # deploy affected services
cobuild retro <id>               # review what happened
```

Every project has quirks that the default skills don't know about. Manual runs surface these. Each retrospective finding becomes a skill gotcha, a decompose guideline, or a review rule.

### Use retrospectives to improve the pipeline

After each design completes, `cobuild retro <id>` generates a retrospective. The most valuable parts: **What Failed** and **Suggested Changes**. Common findings:

- **Agents hardcode values** → add "read from config" instructions to task specs
- **Migration number collisions** → assign numbers explicitly during decomposition
- **Review comments ignored** → agents must address critical review findings before merge
- **Context missing** → add the missing document to the right phase directory

### Keep skills lean, add gotchas over time

Start with defaults. Run the pipeline. Add a gotcha each time an agent trips. The Gotchas section is the highest-value part of any skill.

## License

MIT
