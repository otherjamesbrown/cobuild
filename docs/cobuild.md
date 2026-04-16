# CoBuild

CoBuild turns designs into working code by orchestrating agents through a structured pipeline with enforced stage gates. M is an ephemeral orchestrator -- it reads shard state, takes one action, updates state, and exits. Kill it anytime; the next M picks up from the shard.

## Quick Start

Install from Homebrew with `brew tap otherjamesbrown/cobuild && brew install cobuild`, or with Go via `go install github.com/otherjamesbrown/cobuild/cmd/cobuild@latest`.

### 1. Register a repo

```bash
cd ~/github/otherjamesbrown/penfold
cobuild setup
```

Auto-detects language (Go, Node, Rust, Python), build/test commands, GitHub remote, and default branch. Creates `.cobuild/pipeline.yaml` and registers the repo in `~/.cobuild/repos.yaml`.

Flags: `--project <name>`, `--force` (overwrite existing), `--dry-run`.

### 2. Copy skills into the repo

```bash
cobuild init-skills
```

Copies default skill files from `~/.cobuild/skills/` or the context-palace repo into the repo's `skills/` directory. Existing files are not overwritten unless `--force` is specified.

### 3. Initialize a pipeline on a design

```bash
cobuild init <design-id>
```

Sets `metadata.pipeline` on the design shard with phase=`design`, timestamps, and empty task list.

### 4. Check status

```bash
cobuild status                             # high-level overview of all active pipelines
cobuild show <design-id>    # pipeline state + lock + iterations
cobuild audit <design-id>   # full gate timeline
cobuild wi links <design-id>              # dependency graph with dispatch plan
```

### 5. Run the poller

```bash
cobuild poller                     # current project, every 30s
cobuild poller --all-projects      # all registered repos
cobuild poller --once --dry-run    # one check, print only
```

---

## Workflows

Workflows define which phases a shard goes through. Configured in `pipeline.yaml` under `workflows:`.

| Workflow | Phases | Use case |
|----------|--------|----------|
| `design` | design, decompose, implement, review, done | Full design-to-delivery |
| `bug` | fix, review, done | Bug fixes — default single-session investigate+implement |
| `bug-complex` | investigate, implement, review, done | Complex bugs (label `needs-investigation` to use this) |
| `task` | implement, review, done | Standalone tasks (skip design/decompose) |

The pipeline resolves the workflow from the shard type. If no match, falls back to `design`, then the full phase list.

---

## Pipeline Phases

### Phase 1: Design Review (`design` -> `decompose`)

An agent evaluates the design against 5 readiness criteria and an implementability check.

```bash
cobuild review <design-id> --verdict pass|fail --readiness <1-5> --body "<findings>"
```

The review command:
- Creates a `review` sub-shard linked to the design (audit trail)
- Updates pipeline metadata with structured verdict, round number, history
- If pass: advances phase to `decompose`
- If fail: stays in `design` for iteration

Gate config drives this: the `readiness-review` gate references skill `design/gate-readiness-review`, requires a `readiness` field (int, 1-5), and runs on model `haiku`.

### Phase 2: Decomposition (`decompose` -> `implement`)

A domain agent breaks the design into tasks:

1. Produce task tree with titles, scope, deps
2. Create tasks with `cobuild wi create --parent <design-id>`
3. Create an integration test task labeled `integration-test`, blocked by all other tasks
4. Record verdict:

```bash
cobuild decompose <design-id> --verdict pass|fail --body "<findings>"
```

The decompose gate validates:
- At least one task exists
- All tasks have substantive content
- Dependencies are acyclic
- An integration test task with label `integration-test` exists (when `requires_label` is set in gate config)

### Phase 3: Implement (`implement`)

Tasks dispatched to agents in isolated worktrees:

```bash
cobuild dispatch <task-id>              # single task
```

Dispatch checks blocker edges are satisfied, creates a worktree, generates a `CLAUDE.md` from context layers, spawns a Claude session in tmux, sets status to `in_progress`, records dispatch metadata, and captures session output via `tmux pipe-pane`.

### Phase 4: Review (`review`)

Two strategies, configured per-repo:

- **`agent`** -- a pipeline agent reviews the PR using the `review_skill` (e.g. `review/gate-review-pr`)
- **`external`** -- an external reviewer (e.g. Gemini) reviews, and a process skill (e.g. `review/gate-process-review`) evaluates the result

After approval:
```bash
cobuild merge <task-id>
```

### Phase 5: Done

All tasks merged. A `retrospective` gate (skill `done/gate-retrospective`, model `haiku`) can run to capture lessons learned.

### Cross-Design Ordering

When multiple designs touch the same codebase, use `blocked-by` edges between designs:

```bash
cobuild wi links add <later-design> --blocked-by <earlier-design>
```

---

## Configurable Context Layers

When `cobuild dispatch` runs, it generates a `CLAUDE.md` for the worktree by assembling context layers defined in `pipeline.yaml`.

Each layer has:
- **`name`** -- identifier
- **`source`** -- where content comes from
- **`when`** -- which mode activates the layer: `always`, `interactive`, `dispatch`, `phase:<name>`, or `gate:<name>`

### Source types

| Source | Resolves to |
|--------|-------------|
| `file:<path>` | Read file from repo (relative to repo root) |
| `work-item:<id>` | Fetch content via connector (CP shard, Bead, etc.) |
| `skills:<name>` | Resolve skill file (repo then global) |
| `skills-dir` | Load all `.md` files from skills directory (optional `filter` list) |
| `claude-md` | Read the repo's `CLAUDE.md` |
| `dispatch-prompt` | Injected task prompt (dispatch mode only) |
| `parent-design` | Injected parent design content (dispatch mode only) |
| `hook:<name>` | Deferred to Claude Code hooks |

### When filters

| When | Active for |
|------|-----------|
| `always` (or empty) | Every session |
| `interactive` | Interactive sessions (human typing) |
| `dispatch` | All dispatched tasks |
| `phase:<name>` | Specific pipeline phase (e.g., `phase:design`, `phase:implement`) |
| `gate:<name>` | Specific gate evaluation |

### Example: penfold context layers

```yaml
context:
    layers:
        # Always — every agent sees these
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: principles
          source: work-item:pf-eeb256
          when: always

        # Phase-specific — design agents get domain docs, implement agents get testing standards
        - name: ingest-pipeline
          source: work-item:pf-c66536
          when: phase:design
        - name: testing
          source: work-item:pf-129647
          when: phase:implement

        # Dispatch — task prompt and parent design injected at dispatch time
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

When no layers are configured, dispatch mode injects the task prompt and parent design; interactive mode loads the repo `CLAUDE.md`.

---

## Per-Phase Model Selection

Models are resolved with this priority: gate model > phase model > dispatch `default_model`.

```yaml
dispatch:
    default_model: sonnet         # fallback for everything

phases:
    design:
        model: haiku                # readiness checks are judgment
        gates:
            - name: readiness-review
              model: haiku          # gate can override phase
    implement:
        model: sonnet               # code writing needs capability
    review:
        model: haiku                # reviewing is judgment

monitoring:
    model: haiku                  # health checks

review:
    model: haiku                  # PR review evaluation
```

Use `cfg.ModelForPhase(phaseName, gateName)` in code. The model flag is appended to `claude_flags` at dispatch time.

---

## Stage Gates

Every phase transition is enforced by a gate command that validates preconditions and creates an audit trail.

### Built-in gates

| Gate | Command | Phase transition | Checks |
|------|---------|-----------------|--------|
| `readiness-review` | `cobuild review` | design -> decompose | 5 readiness criteria, implementability, readiness score 1-5 |
| `decomposition-review` | `cobuild decompose` | decompose -> implement | Tasks exist, have content, deps acyclic, integration-test label |

### Generic gate

```bash
cobuild gate <design-id> <gate-name> --verdict pass|fail --body "<findings>"
```

Works for any gate defined in config. Creates a review sub-shard, records the verdict, and advances the phase if pass.

### Audit trail

```bash
cobuild audit <design-id>
```

Shows timeline of all gate records: timestamp, gate name, round number, verdict, review shard ID, and body preview.

Gate config supports:
- `skill` -- which skill file the reviewing agent should follow
- `model` -- model to use for the gate evaluation
- `fields` -- structured fields with type validation (e.g. `readiness: {type: int, min: 1, max: 5, required: true}`)
- `requires_label` -- a label that must exist on a child task (e.g. `integration-test`)

---

## Health Monitoring

The poller runs health checks on every cycle alongside trigger checks.

### Configuration

```yaml
monitoring:
    stall_timeout: 30m
    crash_check: true
    max_retries: 3
    cooldown: 5m
    model: haiku
    actions:
        on_stall: skill:implement/stall-check
        on_crash: redispatch
        on_max_retries: escalate
```

### Stall detection

A task is stalled if status is `in_progress` and `updated_at` exceeds `stall_timeout`. Actions:

| Action | Behavior |
|--------|----------|
| `skill:<name>` | Spawn M with the named skill to diagnose |
| `redispatch` | Reset to open, remove worktree, re-dispatch |
| `escalate` | Append note, label `blocked` |

### Crash recovery

A crash is detected when the tmux window for a task no longer exists. The action (usually `redispatch`) resets the task and re-dispatches. Retry count is tracked in `metadata.dispatch_retries`.

When `max_retries` is exceeded, the `on_max_retries` action fires (usually `escalate`).

### Reconciliation

The poller also reconciles stale `pipeline_runs` on every cycle. The common case is a run stuck at `status=active` while its work item is already `closed` — produced by old versions that didn't advance the run to `done` after the agent finished. The reconciler detects this and marks the run `done/completed` automatically.

End-of-run maintenance after `cobuild orchestrate` and `cobuild doctor --fix` apply the same recovery. Users upgrading from a version that pre-dates the reconciler should run `cobuild doctor --fix` once to clean up any accumulated stuck-active pipelines.

---

## Review Process

### Strategy: external

Used when an external service (e.g. Gemini) reviews PRs. Configure:

```yaml
review:
    strategy: external
    external_reviewers: [gemini]
    process_skill: review/gate-process-review
    ci:
        mode: pr-only       # "pr-only", "all-pass", or "ignore"
        wait: true           # wait for CI before reviewing
```

### Strategy: agent

A pipeline agent reviews using a skill:

```yaml
review:
    strategy: agent
    review_skill: review/gate-review-pr
    review_agent: agent-mycroft
    ci:
        mode: ignore
        wait: false
```

### CI modes

| Mode | Behavior |
|------|----------|
| `pr-only` | Only check CI on the PR branch |
| `all-pass` | All CI checks must pass before review |
| `ignore` | Skip CI checks entirely |

---

## Auto-Deploy

Configured per-repo. After a PR merge, changed paths are matched to services:

```yaml
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

Each service has a name, a list of path globs that trigger it (`trigger_paths`), and a deploy command.

---

## Post-Agent Completion

`cobuild complete <task-id>` runs automatically via two mechanisms:

1. **Stop hook** (primary) — dispatch writes `.claude/settings.local.json` into the worktree with a `Stop` hook that fires `cobuild complete $COBUILD_TASK_ID --auto` when the agent terminates. The `--auto` flag verifies the worktree has commits and isn't dirty (excluding `.cobuild/` and `CLAUDE.md` — dispatch artifacts) before proceeding.
2. **Script fallback** — the dispatch shell script also calls `cobuild complete` after `claude` exits, as a safety net. The command is idempotent.

Steps when complete runs:

1. Commit any uncommitted changes (excluding `.cobuild/` and `CLAUDE.md` via pathspec)
2. Push the branch
3. Create a PR via `gh pr create` if one does not exist (stores `pr_url` in metadata)
4. Append evidence to the shard (commit hash, files changed, PR URL)
5. Transition the pipeline run to the `review` phase
6. Mark the task `needs-review`

If the task is already `needs-review`, it runs validation instead: checks for commits on the branch and a PR, auto-creating the PR if missing.

Note: `cobuild dispatch` no longer requires a prior `cobuild init` call. If no pipeline run exists, dispatch auto-creates one based on the work item type (bug → `bug` workflow, design → `design`, task → `task`).

---

## Feedback Loop

### Insights

```bash
cobuild insights
cobuild insights --project penfold
cobuild insights -o json
```

Analyzes pipeline execution data and produces a report with:
- **Overview** -- designs, tasks, PR counts
- **Gate pass rates** -- first-try pass percentage per gate
- **Common failure reasons** -- extracted from gate review bodies
- **Agent performance** -- task completion, PR creation, timing
- **Friction points** -- detected patterns
- **Suggested improvements** -- data-driven recommendations

### Improve

```bash
cobuild improve
cobuild improve --apply
cobuild improve -o json
```

Analyzes patterns from insights data and proposes specific changes to skills, config, and process files. Detected patterns include:

- Low readiness-review pass rate -> strengthen `create-design` skill
- Missing model configuration -> add per-phase models
- No monitoring configured -> add health check config
- Missing integration test gate requirement -> add `requires_label`
- Skills not initialized -> suggest `cobuild init-skills`

`--apply` auto-applies config/process changes (skill changes require human review).

### Retrospective

The `done` phase has a `retrospective` gate (skill `done/gate-retrospective`, model `haiku`) for capturing lessons learned after a design is fully delivered.

---

## Portable Pipelines

### init-skills

```bash
cobuild init-skills
cobuild init-skills --force
cobuild init-skills --dry-run
```

Copies these skill files into the repo's skills directory:

| Skill | Purpose |
|-------|---------|
| `shared/create-design.md` | Design authoring guide (structure, implementability) |
| `shared/playbook.md` + `shared/playbook/*.md` | M's routing hub plus phase-specific decision trees |
| `design/gate-readiness-review.md` | Phase 1 readiness + implementability evaluation |
| `design/implementability.md` | Implementability criteria reference |
| `implement/dispatch-task.md` | Phase 3 task dispatch procedure |
| `review/gate-review-pr.md` | Phase 4 PR review procedure (agent strategy) |
| `review/gate-process-review.md` | Process external reviewer output |
| `review/merge-and-verify.md` | Merge + post-merge verification |
| `implement/stall-check.md` | Stall diagnosis for stuck agents |
| `done/gate-retrospective.md` | Post-delivery retrospective |

Source resolution: `~/.cobuild/skills/` first, then context-palace `skills/` directory.

### Config hierarchy

```
~/.cobuild/pipeline.yaml          # global defaults
<repo>/.cobuild/pipeline.yaml     # repo overrides (wins on conflict)
```

Merge rules:
- Slices (build, test, phases): override replaces entirely
- Maps (agents): override keys replace, base keys kept
- Scalars: override replaces if non-zero

### Skill resolution

When a skill is referenced (e.g. in a gate config), it resolves:
1. `<repo>/<skills_dir>/<skill-name>` (repo-level)
2. `~/.cobuild/skills/<skill-name>` (global fallback)

---

## Multi-Project

### Repo registry

`~/.cobuild/repos.yaml` maps project names to local paths:

```yaml
repos:
    penfold:
        path: /Users/james/github/otherjamesbrown/penfold
        default_branch: main
    context-palace:
        path: /Users/james/github/otherjamesbrown/context-palace
        default_branch: main
```

Created/updated by `cobuild setup`.

### All-projects poller

```bash
cobuild poller --all-projects
```

Loads all entries from `~/.cobuild/repos.yaml` and polls each project's pipeline config per cycle. Each project gets its own trigger and health checks.

---

## Poller Triggers

The poller checks three conditions each cycle:

| Trigger | Condition | Action |
|---------|-----------|--------|
| `new-design` | Open design shard without pipeline metadata | Init pipeline, spawn M |
| `needs-review` | Task in `needs-review` with parent design in `implement` phase | Spawn M for the parent design |
| `wait-satisfied` | Pipeline `waiting_for` shards all closed | Spawn M to continue |

Before spawning, the poller checks the pipeline lock. If locked, the design is skipped. Stale locks (5-min TTL) are treated as unlocked.

M sessions are spawned as tmux windows with:
- Working directory set to the repo root
- Playbook path and design ID passed as prompt
- `claude_flags` from dispatch config

---

## Lock Protocol

```bash
cobuild lock <design-id>           # acquire (5-min TTL)
cobuild unlock <design-id>         # release
cobuild lock-check <design-id>     # check status
```

Session ID defaults to `<agent-name>-<unix-timestamp>`. If M crashes without unlocking, the TTL ensures recovery.

---

## Commands Reference

### Pipeline commands

| Command | Purpose |
|---------|---------|
| `cobuild setup` | Register repo, create `.cobuild/pipeline.yaml`, update `~/.cobuild/repos.yaml` |
| `cobuild poller` | Poll for triggers and health issues, spawn M sessions |
| `cobuild init-skills` | Copy default skill files into repo |
| `cobuild init-skills --update` | Update skills, overwriting existing files |
| `cobuild insights` | Analyze execution data, produce report |
| `cobuild improve` | Suggest/apply pipeline improvements from patterns |
| `cobuild status` | Show all active pipelines with phase, tasks, last activity, and ACTIVITY state |
| `cobuild explain` | Show the full pipeline in human-readable markdown |
| `cobuild update-agents` | Regenerate AGENTS.md from current skills and config |
| `cobuild scan` | Generate project anatomy (file index with token estimates) |
| `cobuild retro <id>` | Run pipeline retrospective, record gate |
| `cobuild admin tokens` | Parse transcript for exact token usage and cost |
| `cobuild admin waste` | Detect token waste patterns from session events |

### Shard pipeline commands

| Command | Purpose |
|---------|---------|
| `cobuild init <id>` | Initialize pipeline metadata on a design |
| `cobuild show <id>` | Display pipeline state, lock, iterations |
| `cobuild update <id>` | Update phase, waiting-for, add-task, tokens |
| `cobuild review <id>` | Phase 1 gate: readiness review (requires `--verdict`, `--readiness`) |
| `cobuild decompose <id>` | Phase 2 gate: decomposition review (requires `--verdict`) |
| `cobuild investigate <id>` | Bug investigation gate (requires `--verdict`) |
| `cobuild gate <id> <gate>` | Generic gate: any named gate (requires `--verdict`) |
| `cobuild audit <id>` | Show full gate timeline |
| `cobuild lock <id>` | Acquire pipeline lock |
| `cobuild unlock <id>` | Release pipeline lock |
| `cobuild lock-check <id>` | Check lock status |

### Task commands

| Command | Purpose |
|---------|---------|
| `cobuild dispatch <shard-id>` | Spawn agent in tmux with full context; implement-phase designs default to child-task wave dispatch unless `--mono` is passed |
| `cobuild dispatch-wave <design-id>` | Dispatch all ready tasks for a design |
| `cobuild wait <task-id> [id...]` | Wait for tasks to reach target status |
| `cobuild complete <task-id>` | Post-agent: commit, push, PR, evidence, needs-review |
| `cobuild merge <task-id>` | Merge approved PR, close task |
| `cobuild merge-design <design-id>` | Merge all tasks for a design |
| `cobuild deploy <design-id>` | Deploy affected services (matches trigger_paths) |
| `cobuild gate <id>` | Generic gate evaluation |

### Work item commands

| Command | Purpose |
|---------|---------|
| `cobuild wi show <id>` | Show work item details |
| `cobuild wi list` | List work items |
| `cobuild wi links <id>` | Show work item dependencies |
| `cobuild wi create` | Create a new work item |
| `cobuild wi append <id>` | Append content to a work item |

### Status commands

| Command | Purpose |
|---------|---------|
| `cobuild status` | Outcomes, pipelines, blockers, agents — includes ACTIVITY column |

The ACTIVITY column on `cobuild status` shows a derived state for each pipeline run:

| Activity | Meaning |
|----------|---------|
| `dispatched` | At least one session has no `ended_at` (agent in flight) |
| `awaiting-transition` | All sessions ended, phase unchanged, no failed gate |
| `blocked` | Most recent gate for current phase failed, or retry cap hit |

The `--active` filter includes blocked runs alongside active ones.

---

## Skills Reference

| Skill | Who uses it | Purpose |
|-------|------------|---------|
| `shared/create-design.md` | Any agent creating designs | Required structure, implementability test |
| `shared/playbook.md` | M (orchestrator) | Decision trees, phase rules, commands |
| `design/gate-readiness-review.md` | M | Phase 1 readiness + implementability evaluation |
| `design/implementability.md` | M | Implementability criteria reference |
| `implement/dispatch-task.md` | M | Phase 3 task dispatch procedure |
| `review/gate-review-pr.md` | M | Phase 4 PR review procedure (agent strategy) |
| `review/gate-process-review.md` | M | Process external reviewer output (external strategy) |
| `review/merge-and-verify.md` | M | Phase 4 merge + post-merge verification |
| `implement/stall-check.md` | M | Stall diagnosis for stuck agents |
| `done/gate-retrospective.md` | M | Post-delivery retrospective |

`shared/create-design.md` and `design/gate-readiness-review.md` are cross-referenced -- the design skill tells authors what is required, the readiness check evaluates against the same criteria.

---

## Config Reference

Full `pipeline.yaml` schema:

```yaml
# Build and test commands
build:
    - cd cxp && go build ./...
test:
    - cd cxp && go test ./...
    - cd cxp && go vet ./...

# Agent roster and domain capabilities
agents:
    agent-steve:
        domains: [cli, migrations, shard-model]
    agent-mycroft:
        domains: [backend, services]

# Dispatch configuration
dispatch:
    max_concurrent: 3             # max parallel agents
    # tmux_session: cobuild-myproject  # optional — defaults to cobuild-<project>
    claude_flags: "--dangerously-skip-permissions"
    default_model: sonnet         # fallback model

# Health monitoring
monitoring:
    stall_timeout: 30m            # duration string
    crash_check: true             # check for missing tmux windows
    max_retries: 3                # per-task retry limit
    cooldown: 5m                  # between retries
    model: haiku                  # model for health checks
    actions:
        on_stall: skill:implement/stall-check   # or "redispatch" or "escalate"
        on_crash: redispatch            # or "skill:<name>" or "escalate"
        on_max_retries: escalate        # or "redispatch"

# Review configuration
review:
    ci:
        mode: pr-only             # "pr-only", "all-pass", "ignore"
        wait: true                # wait for CI before reviewing
    strategy: external            # "external" or "agent"
    external_reviewers: [gemini]  # GitHub bot usernames (external strategy)
    process_skill: review/gate-process-review  # skill to process external reviews
    review_skill: review/gate-review-pr     # skill for agent-based reviews (agent strategy)
    review_agent: agent-mycroft   # who does agent reviews (agent strategy)
    model: haiku                  # model for review tasks

# Context layers for CLAUDE.md assembly
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always            # "always", "interactive", "dispatch", "gate:<name>"
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch

# Workflow definitions (shard type -> phase list)
workflows:
    design:
        phases: [design, decompose, implement, review, done]
    bug:
        phases: [implement, review, done]
    task:
        phases: [implement, review, done]

# Auto-deploy after PR merge
deploy:
    enabled: true
    services:
        - name: ai
          trigger_paths: [services/ai/]
          command: ./scripts/deploy.sh ai

# GitHub repository
github:
    owner_repo: otherjamesbrown/penfold

# Skills directory (relative to repo root)
skills_dir: skills

# Pipeline phases with models and gates
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
              skill: decompose/gate-decomposition-review
              model: haiku
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
```

---

## Registered Repos

| Project | Repo | Build | Test |
|---------|------|-------|------|
| penfold | ~/github/otherjamesbrown/penfold | `cd penfold-go-pipeline && go build ./...` | `cd penfold-go-pipeline && go test/vet ./...` |
| context-palace | ~/github/otherjamesbrown/context-palace | `cd cxp && go build ./...` | `cd cxp && go test/vet ./...` |
