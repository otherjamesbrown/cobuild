# CoBuild

Config-driven AI agent pipeline: design to deployed code with stage gates, audit trails, and self-improvement.

## What is CoBuild?

CoBuild orchestrates AI agents through a structured pipeline — from design review through decomposition, implementation, code review, and deployment. Every phase transition is enforced by a configurable gate that creates an audit trail.

**Key features:**
- [**Config-driven**](docs/guides/config.md) — phases, gates, models, context layers, deploy rules — all YAML
- [**Skills as markdown**](docs/guides/skills.md) — extend the pipeline by writing a `.md` file
- [**Connectors**](#connectors) — pluggable work-item backends (Context Palace, Beads, future Jira)
- [**Storage**](#storage) — pluggable data store for pipeline state (Postgres, future SQLite/files)
- [**Context layers**](docs/guides/context-layers.md) — control exactly what each agent sees per session type
- [**Per-phase model selection**](docs/guides/models.md) — haiku for judgment, sonnet for creation
- [**Self-improving**](docs/guides/feedback-loop.md) — feedback loop learns from execution patterns
- [**Multi-project**](docs/guides/multi-project.md) — one poller manages multiple repos
- [**Audit trail**](docs/guides/audit-trail.md) — every decision recorded with structured data

## Quick Start

```bash
# Install
go install github.com/otherjamesbrown/cobuild/cmd/cobuild@latest

# Register your repo
cd your-project
cobuild setup

# Copy default skills
cobuild init-skills

# Initialize a pipeline on a design
cobuild init <design-shard-id>

# Check status
cobuild audit <design-shard-id>

# Run the poller (autonomous mode)
cobuild poller --all-projects
```

## How It Works

### Workflows

| Workflow | Phases | Use case |
|----------|--------|----------|
| `design` | design → decompose → implement → review → done | Full design-to-delivery |
| `bug` | implement → review → done | Bug fixes |
| `task` | implement → review → done | Standalone tasks |

### Pipeline Phases

1. **Design Review** — evaluate readiness + implementability against 5 criteria
2. **Decomposition** — break design into tasks with dependency ordering + integration test
3. **Implement** — dispatch agents in isolated worktrees with configurable context
4. **Review** — external (Gemini) or agent-based, with CI mode (pr-only/all-pass/ignore)
5. **Done** — retrospective, docs update, feedback loop

### Stage Gates

Every phase transition goes through a gate:

```bash
cobuild gate <shard-id> <gate-name> --verdict pass|fail --body "<findings>"
```

Gates create a review sub-shard (audit trail), update pipeline metadata, and auto-advance the phase on pass.

## Configuration

```yaml
# .cobuild/pipeline.yaml

workflows:
    design:
        phases: [design, decompose, implement, review, done]
    bug:
        phases: [implement, review, done]

phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            skill: design/gate-readiness-review
            fields:
                readiness: {type: int, min: 1, max: 5, required: true}
    - name: decompose
      model: sonnet
      gates:
          - name: decomposition-review
            requires_label: integration-test
    - name: implement
      model: sonnet
    - name: review
      model: haiku
    - name: done
      gates:
          - name: retrospective
            skill: done/gate-retrospective

dispatch:
    max_concurrent: 3
    tmux_session: main
    claude_flags: "--dangerously-skip-permissions"
    default_model: sonnet

context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch

monitoring:
    stall_timeout: 30m
    crash_check: true
    max_retries: 3
    actions:
        on_stall: skill:implement/stall-check
        on_crash: redispatch
        on_max_retries: escalate

review:
    ci:
        mode: pr-only
        wait: true
    strategy: external
    external_reviewers: [gemini]

deploy:
    enabled: true
    services:
        - name: api
          paths: [services/api/]
          command: ./scripts/deploy.sh api
```

## Connectors

CoBuild reads and writes work items (designs, bugs, tasks) through **connectors** — pluggable backends for external systems. CoBuild's own orchestration data (pipeline runs, gates, audit trail) is stored separately.

```yaml
# .cobuild/pipeline.yaml
connectors:
    work_items:
        type: context-palace    # default — uses cxp CLI
```

| Connector | Backend | Access via |
|-----------|---------|-----------|
| `context-palace` | Context Palace (Postgres) | `cxp` CLI with `-o json` |
| `beads` | Beads (Dolt) | `bd` CLI with `--json` |

The connector interface follows Claude Code/CoWork patterns. See `research/claude-patterns.md` for the design rationale.

## Storage

CoBuild stores its own orchestration data (pipeline runs, gate audit records, task tracking) separately from work items. The storage backend is pluggable:

```yaml
# .cobuild/pipeline.yaml
storage:
    backend: postgres           # default
    dsn: "host=localhost dbname=cobuild user=cobuild sslmode=disable"
```

| Backend | Status | Use case |
|---------|--------|----------|
| `postgres` | Implemented | Teams, shared infrastructure |
| `sqlite` | [Designed](research/design-sqlite-store.md) | Single-user, local dev |
| `file` | [Designed](research/design-file-store.md) | Zero-dependency, git-trackable (YAML + JSONL) |

When no storage config is present, CoBuild uses the existing database connection settings (backward compatible).

## Skills

Skills are markdown files that tell agents what to do. Drop them in `skills/`:

| Skill | Phase | Purpose |
|-------|-------|---------|
| `shared/create-design.md` | — | How to author a design that passes readiness review |
| `shared/playbook.md` | — | Orchestrator decision trees and phase rules |
| `design/gate-readiness-review.md` | design | Gate: readiness evaluation criteria |
| `design/implementability.md` | design | Implementability reference |
| `implement/dispatch-task.md` | implement | Task dispatch procedure |
| `implement/stall-check.md` | implement | Diagnose stuck agents |
| `review/gate-review-pr.md` | review | Gate: PR review (agent strategy) |
| `review/gate-process-review.md` | review | Gate: PR review (external strategy) |
| `review/merge-and-verify.md` | review | Post-review merge procedure |
| `done/gate-retrospective.md` | done | Gate: post-delivery lessons learned |

Initialize defaults: `cobuild init-skills`

## Context Layers

Control what context each agent sees:

```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always                # loaded in all sessions
        - name: principles
          source: work-item:pf-eeb256
          when: always                # fetch from connector (CP shard, Bead, etc.)
        - name: ingest-docs
          source: work-item:pf-c66536
          when: phase:design          # only during design phase
        - name: testing-standards
          source: work-item:pf-129647
          when: phase:implement       # only during implement phase
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch              # only when pipeline dispatches
        - name: security-policy
          source: file:SECURITY.md
          when: gate:security-review  # only during security gate
```

Sources: `file:<path>`, `work-item:<id>`, `skills:<name>`, `claude-md`, `dispatch-prompt`, `parent-design`
When: `always`, `interactive`, `dispatch`, `phase:<name>`, `gate:<name>`

## Feedback Loop

```bash
cobuild insights          # execution analysis: gate pass rates, friction points
cobuild improve           # suggest changes to skills and config from patterns
cobuild improve --apply   # auto-apply non-skill changes
```

## Commands

| Command | Purpose |
|---------|---------|
| `cobuild setup` | Register repo, create config |
| `cobuild init-skills` | Copy default skills into repo |
| `cobuild init <id>` | Start pipeline on a design |
| `cobuild gate <id> <name>` | Record gate verdict |
| `cobuild review <id>` | Phase 1 readiness review |
| `cobuild decompose <id>` | Phase 2 decomposition gate |
| `cobuild dispatch <id>` | Dispatch agent to implement task |
| `cobuild dispatch-wave <id>` | Dispatch all ready tasks for a design |
| `cobuild wait <id> [id...]` | Wait for tasks to reach target status |
| `cobuild complete <id>` | Post-agent completion (PR, evidence) |
| `cobuild merge <id>` | Merge approved PR, close task |
| `cobuild retro <id>` | Run pipeline retrospective |
| `cobuild status` | Show all active pipelines |
| `cobuild audit <id>` | Show gate timeline |
| `cobuild wi show/list/links` | Work item operations (any connector) |
| `cobuild poller` | Run trigger + health poller |
| `cobuild insights` | Execution analysis |
| `cobuild improve` | Suggest pipeline improvements |

## Tips

### Run manually before going autonomous

Don't jump straight to `cobuild poller`. Step through the pipeline manually for your first few designs:

```bash
/design-review <id>          # review and submit
cobuild gate <id> ...        # step through gates
cobuild dispatch <id>        # dispatch tasks one at a time
cobuild wait <id>            # watch agents work
cobuild merge <id>           # merge PRs yourself
cobuild retro <id>           # review what happened
```

Every project has quirks — migration numbering conventions, architectural principles, deploy procedures, test patterns — that the default skills don't know about. Manual runs surface these as concrete issues that you can feed back into the skills.

### Use retrospectives to improve the pipeline

After each design completes, `cobuild retro <id>` generates a retrospective. Read it. The most valuable sections are **What Failed** and **Suggested Changes** — they tell you exactly which skills to update.

Common patterns from early runs:
- **Agents hardcode values** that should be configurable → add explicit "read from config" instructions to task specs during decomposition
- **Migration number collisions** in parallel tasks → assign numbers explicitly in the decomposition, don't let agents pick
- **Design review rates things too leniently** → strengthen severity rules in the design-reviewer for your project's constraints
- **Agents produce empty PRs** → check dispatch flags (`claude_flags` in pipeline.yaml) and worktree configuration

Each retrospective finding should become either a skill gotcha, a decompose guideline, or a project-specific review rule. After 3-4 manual runs, the skills will be tuned for your project and autonomous mode will work reliably.

### Keep skills lean, add gotchas over time

Don't try to write perfect skills upfront. Start with the defaults, run the pipeline, and add a gotcha line each time an agent trips on something. The Gotchas section is the highest-value part of any skill — it's the accumulated knowledge of what goes wrong.

## License

MIT
