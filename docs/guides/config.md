# Config-Driven Pipeline

CoBuild pipelines are defined entirely in YAML. Every phase, gate, model, context layer, and deploy rule lives in `pipeline.yaml`. You change behavior by editing config, not code.

## Quick start

```bash
cd your-project
cobuild setup          # creates .cobuild/pipeline.yaml with detected defaults
cobuild init-skills    # copies default skill files into skills/
cobuild init <id>      # starts a pipeline on a design shard
```

## How it works

CoBuild loads config from two locations and merges them:

```
~/.cobuild/pipeline.yaml          # global defaults (all projects)
<repo>/.cobuild/pipeline.yaml     # repo overrides (wins on conflict)
```

The load order is: built-in defaults, then global, then repo. Each layer can override the previous. The merged result is the effective config for any pipeline command.

### Merge rules

| Type | Behavior |
|------|----------|
| Scalars (strings, ints, bools) | Override replaces if non-zero |
| Maps (`agents`) | Override keys replace, base keys kept |
| Slices (`build`, `test`, `phases`) | Override replaces entirely |

This means if you define `phases:` in your repo config, it replaces the entire global phases list -- it does not append. If you add one agent to `agents:`, the global agents are kept alongside it.

### Adding a phase

Add an entry to the `phases:` list:

```yaml
phases:
    security:
        model: sonnet
        gates:
            - name: security-review
              skill: security/gate-security-check
              model: haiku
```

Then reference the phase in a workflow:

```yaml
workflows:
    design:
        phases: [design, decompose, implement, security, review, done]
```

### Adding a gate

Gates live inside phases. Add a gate by appending to the phase's `gates:` list:

```yaml
phases:
    decompose:
        model: sonnet
        gates:
            - name: decomposition-review
              requires_label: integration-test
            - name: architecture-check        # new gate
              skill: decompose/gate-architecture-check
              model: haiku
```

## Configuration

### Full schema reference

```yaml
# Build and test commands (run by CI and agents)
build:
    - cd penfold-go-pipeline && go build ./...
test:
    - cd penfold-go-pipeline && go test ./...
    - cd penfold-go-pipeline && go vet ./...

# Agent roster — maps agent names to domain capabilities
agents:
    agent-steve:
        domains: [cli, migrations, shard-model]
    agent-mycroft:
        domains: [backend, services, tests]

# Dispatch — how agents are spawned
dispatch:
    max_concurrent: 3                         # max parallel agents
    # tmux_session: cobuild-myproject          # optional — defaults to cobuild-<project>
    claude_flags: "--dangerously-skip-permissions"
    default_model: sonnet                     # fallback model

# Health monitoring — detect and recover from stuck agents
monitoring:
    stall_timeout: 30m                        # duration before stall
    crash_check: true                         # detect missing tmux windows
    max_retries: 3                            # per-task retry limit
    cooldown: 5m                              # wait between retries
    model: haiku                              # model for health checks
    actions:
        on_stall: skill:implement/stall-check         # or "redispatch" or "escalate"
        on_crash: redispatch
        on_max_retries: escalate

# Review — how PRs are evaluated
review:
    ci:
        mode: pr-only                         # "pr-only", "all-pass", "ignore"
        wait: true                            # wait for CI before reviewing
    strategy: external                        # "external" or "agent"
    external_reviewers: [gemini]              # GitHub bot usernames
    process_skill: review/gate-process-review
    model: haiku

# Context layers — what agents see per session type
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch

# Workflows — shard type to phase list mapping
workflows:
    design:
        phases: [design, decompose, implement, review, done]
    bug:
        phases: [implement, review, done]
    task:
        phases: [implement, review, done]

# Pipeline phases with per-phase models and gates
phases:
    design:
        model: haiku
        gates:
            - name: readiness-review
              skill: design/gate-readiness-review
              model: haiku
              fields:
                  readiness: {type: int, min: 1, max: 5, required: true}
    decompose:
        model: sonnet
        gates:
            - name: decomposition-review
              requires_label: integration-test
    implement:
        model: sonnet
    review:
        model: haiku
    done:
        gates:
            - name: retrospective
              skill: done/gate-retrospective
              model: haiku

# Auto-deploy after PR merge
deploy:
    enabled: true
    services:
        - name: api
          trigger_paths: [services/api/]
          command: ./scripts/deploy.sh api

# GitHub repository
github:
    owner_repo: otherjamesbrown/penfold

# Storage — where CoBuild stores its own orchestration data
storage:
    backend: postgres                     # "postgres" (default), future: "sqlite", "file"
    dsn: "host=localhost dbname=cobuild user=cobuild sslmode=disable"
    # SQLite (future):
    # backend: sqlite
    # path: .cobuild/cobuild.db
    # File-based (future):
    # backend: file
    # path: .cobuild/data/

# Connectors — external work-item systems
connectors:
    work_items:
        type: context-palace              # "context-palace" or "beads"
        # Beads example:
        # type: beads
        # config:
        #     prefix: cb
        #     repo: .

# Skills directory (relative to repo root)
skills_dir: skills
```

### Gate fields

Gates can require structured fields with type validation:

```yaml
fields:
    readiness: {type: int, min: 1, max: 5, required: true}
```

When a gate has `fields`, the gate command validates the provided values before recording the verdict.

### Phase stall_check

Each phase can reference a skill to run when an agent is detected as stalled:

```yaml
phases:
    implement:
        model: sonnet
        stall_check: implement/stall-check    # skill to run on stall detection
```

The `stall_check` field is a skill path (without `.md`). When the monitoring detects a stall in this phase, CoBuild loads and follows the skill to diagnose and recover. If not set, the global `monitoring.actions.on_stall` action applies.

### Minimal config

A working config needs very little. `cobuild setup` auto-detects most of this:

```yaml
dispatch:
    default_model: sonnet

github:
    owner_repo: yourorg/yourrepo
```

Everything else falls back to built-in defaults: 3 concurrent agents, 30m stall timeout, haiku for health checks, standard 5-phase design workflow.

## Examples

### Example 1: penfold (Go project, external review)

This is the real config from the penfold project:

```yaml
build:
    - cd penfold-go-pipeline && go build ./...
test:
    - cd penfold-go-pipeline && go test ./...
    - cd penfold-go-pipeline && go vet ./...

agents:
    agent-mycroft:
        domains: [backend, services, tests]
    agent-steve:
        domains: [cli, migrations]

dispatch:
    max_concurrent: 3
    tmux_session: dev-penf-cli    # optional override; default would be cobuild-penfold
    claude_flags: "--dangerously-skip-permissions"
    default_model: sonnet

review:
    strategy: external
    external_reviewers: [gemini]
    ci:
        mode: pr-only
        wait: true

deploy:
    enabled: true
    services:
        - name: ai
          trigger_paths: [services/ai/]
          command: ./scripts/deploy.sh ai
        - name: worker
          trigger_paths: [services/worker/, pkg/]
          command: ./scripts/deploy.sh worker
```

### Example 2: Bug-fix-only project (minimal)

```yaml
dispatch:
    default_model: sonnet

workflows:
    design:
        phases: [implement, review, done]

review:
    strategy: agent
    review_skill: review/gate-review-pr
    ci:
        mode: ignore

github:
    owner_repo: yourorg/small-tool
```

## Troubleshooting

**Config not loading:** Run `cobuild show <id>` -- it prints the effective config path. Check that `.cobuild/pipeline.yaml` exists in your repo root. Run `cobuild setup` to regenerate it.

**Phase not advancing:** Gates enforce transitions. Check `cobuild audit <id>` to see which gate is blocking. The gate may require a label (`requires_label`) or structured field that was not provided.

**Global config ignored:** If your repo config defines `phases:`, it replaces the entire global phases list (slice merge rule). Copy the full phases list into your repo config if you need to extend it.
