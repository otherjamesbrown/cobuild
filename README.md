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
            skill: m-readiness-check
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
            skill: m-retrospective

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
        on_stall: skill:m-stall-check
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

| Skill | Purpose |
|-------|---------|
| `create-design.md` | How to author a design that passes readiness review |
| `m-playbook.md` | M's decision trees and phase rules |
| `m-readiness-check.md` | Phase 1 evaluation criteria |
| `m-review-pr.md` | PR review procedure |
| `m-stall-check.md` | Diagnose stuck agents |
| `m-retrospective.md` | Post-delivery lessons learned |

Initialize defaults: `cobuild init-skills`

## Context Layers

Control what context each agent sees:

```yaml
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always                # loaded in all sessions
        - name: agent-identity
          source: file:.cobuild/context/identity.md
          when: interactive           # only when human is typing
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch              # only when pipeline dispatches
        - name: security-policy
          source: file:SECURITY.md
          when: gate:security-review  # only during security gate
```

Sources: `file:<path>`, `shard:<id>`, `skills:<name>`, `claude-md`, `dispatch-prompt`, `parent-design`

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
| `cobuild init <id>` | Start pipeline on a shard |
| `cobuild gate <id> <name>` | Record gate verdict |
| `cobuild review <id>` | Phase 1 readiness review |
| `cobuild decompose <id>` | Phase 2 decomposition gate |
| `cobuild dispatch <id>` | Dispatch agent to implement task |
| `cobuild complete <id>` | Post-agent completion (PR, evidence) |
| `cobuild audit <id>` | Show gate timeline |
| `cobuild poller` | Run trigger + health poller |
| `cobuild insights` | Execution analysis |
| `cobuild improve` | Suggest pipeline improvements |

## License

MIT
