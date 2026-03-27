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
# Install
go install github.com/otherjamesbrown/cobuild/cmd/cobuild@latest

# In your project repo:
cobuild setup                  # register the repo
cobuild init-skills            # copy default skills
cobuild update-agents          # generate AGENTS.md with pipeline instructions
cobuild explain                # see your pipeline in human-readable form

# Submit a design to the pipeline:
cobuild init <design-id>       # initialise pipeline (auto-detects type)
cobuild dispatch <design-id>   # spawn agent for the current phase
cobuild wait <design-id>       # wait for agent to complete
cobuild status                 # see all active pipelines
```

For full interactive setup (connector, storage, context layers), read the [bootstrap guide](skills/shared/bootstrap.md) or ask your AI assistant to follow it.

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
| `bug` | investigate → implement → review → done | Bug fixes (investigation before fixing) |
| `task` | implement → review → done | Standalone tasks |

### Pipeline Phases

1. **Design Review** — evaluate readiness + implementability against 5 criteria
2. **Decomposition** — break design into tasks with dependency ordering and wave assignment
3. **Investigation** (bugs only) — read-only root cause analysis, fragility assessment, fix specification
4. **Implement** — dispatch agents in isolated worktrees with phase-aware context
5. **Review** — external (Gemini) or agent-based, with CI integration
6. **Done** — retrospective captures lessons and feeds back into skills

### Phase-Aware Dispatch

`cobuild dispatch` reads the current pipeline phase and generates the right prompt automatically:

| Phase | What the dispatched agent does |
|-------|-------------------------------|
| design | Evaluate readiness, check 5 criteria, record gate |
| decompose | Break into tasks, assign waves, set dependencies |
| investigate | Read-only root cause analysis, create fix task |
| implement | Write code, run tests, create PR |
| review | Check PR against spec, evaluate CI, record verdict |
| done | Run retrospective, suggest improvements |

### Manual vs Autonomous

**Manual mode** (default) — you step through each phase:

```bash
cobuild init <id>              # start pipeline
cobuild dispatch <id>          # spawn agent for current phase
cobuild wait <id>              # wait for completion
# repeat for each phase
```

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
        phases: [investigate, implement, review, done]
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
    implement/        # implementing agents
    investigate/      # bug investigation agents
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
| `investigate/` | bug-investigation | Root cause analysis for bugs |
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
| `cobuild admin health` | System health check |
| `cobuild admin cleanup` | Remove stale worktrees, branches, old data |
| `cobuild admin db-stats` | Database usage |
| `cobuild admin stuck` | Find stuck pipelines and orphan tasks |
| **Autonomous** | |
| `cobuild poller` | Process autonomous pipelines continuously |
| `cobuild poller --once` | Single pass |
| `cobuild insights` | Execution analysis |
| `cobuild improve` | Suggest pipeline improvements |

## Tips

### Run manually before going autonomous

Don't jump straight to `cobuild poller`. Step through the pipeline manually for your first few designs:

```bash
cobuild init <id>                # start pipeline
cobuild dispatch <id>            # spawn agent for current phase
cobuild wait <id>                # wait for completion
cobuild dispatch <id>            # next phase
cobuild wait <id>                # ...
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
