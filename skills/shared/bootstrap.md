# Skill: Bootstrap CoBuild on a Project

Set up CoBuild pipeline automation on a new or existing repository. This skill walks through the complete setup process, from prerequisites to first pipeline run.

**Requires:** `~/.cobuild/bootstrap.md` for local infrastructure details.

---

## Prerequisites

Before starting, verify:

1. **CoBuild CLI** is installed: `cobuild version`
2. **Work-item connector CLI** is available:
   - Context Palace: `cxp --version`
   - Beads: `bd --version`
3. **Local bootstrap config** exists: `cat ~/.cobuild/bootstrap.md`
4. **Target repo** is a git repository with a remote

If `~/.cobuild/bootstrap.md` does not exist, stop and ask the developer to create it. See the CoBuild repo's `docs/bootstrap-template.md` for a template.

---

## Step 1: Read Local Bootstrap

Read `~/.cobuild/bootstrap.md` to understand the local infrastructure:
- Which database and connector to use
- Default agents, models, and review strategy
- Any project-specific overrides

This file is your source of truth for infrastructure details that vary per machine/developer.

---

## Step 2: Register the Repo

```bash
cd <repo-root>
cobuild setup
```

This auto-detects:
- Language (Go, Node, Rust, Python) and build/test commands
- GitHub remote and default branch
- Project name (from directory or `.cobuild.yaml`)

Verify the output:
- `.cobuild/pipeline.yaml` was created
- `~/.cobuild/repos.yaml` includes this project

If the auto-detected values are wrong, edit `.cobuild/pipeline.yaml` directly.

---

## Step 3: Configure the Connector

Based on the local bootstrap config, set the work-item connector in `.cobuild/pipeline.yaml`:

```yaml
connectors:
    work_items:
        type: context-palace    # or "beads" — from bootstrap.md
```

For Context Palace, verify connectivity:
```bash
cxp status
cxp shard list --project <project-name> --limit 1 -o json
```

For Beads, verify:
```bash
bd list --limit 1 --json
```

---

## Step 4: Configure Storage

CoBuild needs to store its own pipeline data. From the bootstrap config, set:

```yaml
storage:
    backend: postgres
    # DSN is inherited from the cxp/cobuild connection config by default
```

If the bootstrap specifies a different database, set the DSN explicitly:
```yaml
storage:
    backend: postgres
    dsn: "host=<host> dbname=<db> user=<user> sslmode=verify-full"
```

---

## Step 5: Copy Skills

```bash
cobuild init-skills
```

This copies the default skill files into `skills/` organized by phase:

```
skills/
    design/       gate-readiness-review, implementability
    implement/    dispatch-task, stall-check
    review/       gate-review-pr, gate-process-review, merge-and-verify
    done/         gate-retrospective
    shared/       playbook, create-design
```

Review each skill and customize for this project. Key customizations:

- **`shared/create-design.md`** — add project-specific design requirements
- **`design/gate-readiness-review.md`** — adjust readiness criteria for this codebase
- **`shared/playbook.md`** — update agent routing rules if agents have different domains

---

## Step 6: Configure Context Layers

Context layers control what agents see. At minimum, create an architecture doc:

```bash
mkdir -p .cobuild/context
```

Create `.cobuild/context/architecture.md` describing:
- Codebase structure (key directories, packages, modules)
- Build and deploy process
- Key conventions and patterns
- External dependencies

Then add context layers to `.cobuild/pipeline.yaml`:

```yaml
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

---

## Step 7: Configure Agents and Models

From the bootstrap config, set up agents with their domain capabilities:

```yaml
agents:
    agent-steve:
        domains: [cli, migrations]
    agent-mycroft:
        domains: [backend, services, tests]

dispatch:
    max_concurrent: 3
    tmux_session: <project-tmux-session>
    default_model: sonnet

phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            skill: design/gate-readiness-review
            model: haiku
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
            model: haiku
```

---

## Step 8: Configure Review Strategy

From the bootstrap config:

```yaml
review:
    strategy: external              # or "agent"
    external_reviewers: [gemini]    # for external strategy
    process_skill: review/gate-process-review
    ci:
        mode: pr-only
        wait: true
    model: haiku
```

---

## Step 9: Configure Monitoring

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

---

## Step 10: Verify Setup

Run these checks in order:

```bash
# 1. Config loads without errors
cobuild show <any-existing-shard-id> 2>&1 || echo "OK if no shards yet"

# 2. Skills are in place
ls skills/design/ skills/implement/ skills/review/ skills/done/ skills/shared/

# 3. Build and test commands work
# (run whatever is in .cobuild/pipeline.yaml build: and test: sections)

# 4. Connector can reach the work-item system
cxp shard list --project <project> --limit 1 -o json   # for CP
# bd list --limit 1 --json                               # for Beads

# 5. Git worktree creation works
git worktree list
```

---

## Step 11: First Pipeline Run (Optional)

If there's already a design shard to test with:

```bash
# Initialize pipeline on a design
cobuild init <design-shard-id>

# Check pipeline state
cobuild show <design-shard-id>

# Run the poller once in dry-run mode
cobuild poller --once --dry-run
```

---

## Checklist

- [ ] `cobuild version` works
- [ ] `~/.cobuild/bootstrap.md` exists with local infra details
- [ ] `.cobuild/pipeline.yaml` exists with project config
- [ ] Project registered in `~/.cobuild/repos.yaml`
- [ ] Skills copied and customized (`skills/` directory populated)
- [ ] Context layers configured (at minimum: architecture doc)
- [ ] Connector verified (can list work items)
- [ ] Build/test commands verified
- [ ] Review strategy configured
- [ ] Monitoring configured
