---
name: bootstrap
description: Set up CoBuild on a new project. Interactive walkthrough of connector, storage, context layers, skills, and agent instructions. Trigger on "set up cobuild", "bootstrap", "configure pipeline".
---

# Skill: Bootstrap CoBuild on a Project

## What is CoBuild?

CoBuild is a pipeline that takes a **design** (a written specification of what to build) and turns it into **deployed code** by orchestrating AI agents through structured phases with quality gates.

### The Pipeline

A design flows through 5 phases. Each phase transition is enforced by a **gate** — a quality check that must pass before work moves forward:

```
Design  →  Decompose  →  Implement  →  Review  →  Done
  ↑ gate      ↑ gate                     ↑ gate    ↑ gate
```

1. **Design** — An agent evaluates the design for completeness and implementability. Can an implementing agent build this without asking questions? The **readiness-review** gate enforces this.
2. **Decompose** — An agent breaks the design into discrete tasks with dependency ordering. Tasks are grouped into **waves** (wave 1 has no blockers, wave 2 depends on wave 1). The **decomposition-review** gate verifies tasks are complete and dependencies are acyclic.
3. **Implement** — Agents are dispatched into isolated git worktrees, one per task. Each gets a tailored CLAUDE.md with the task spec, parent design context, and project architecture. Waves are processed in order. When done, agents commit, push, and create PRs.
4. **Review** — PRs are reviewed by an external reviewer (e.g., Gemini) or by another agent. Approved PRs get merged.
5. **Done** — A retrospective captures lessons learned and feeds them back into the pipeline configuration.

**Bugs** follow a shorter workflow: investigate → implement → review → done. The investigation phase analyses root cause before any code is changed.

**Tasks** go straight to: implement → review → done.

### Architecture

CoBuild has three layers:

- **Connector** — bridges to an external work-item system where designs, bugs, and tasks live (Context Palace, Beads, or future Jira/Linear). CoBuild reads and writes work items through the connector. It never owns the work items.
- **Store** — where CoBuild keeps its own orchestration state: pipeline runs, gate audit records, task tracking. Currently Postgres, with SQLite and file-based stores planned.
- **Skills** — markdown files organized by phase that tell agents what to do. Gate skills define evaluation criteria. Phase skills define procedures. The pipeline's intelligence lives in skills, not compiled code.

### What You're Setting Up

This bootstrap configures:
1. **Project identity** — name, GitHub remote, work-item prefix
2. **Connector** — how CoBuild reaches the work-item system
3. **Storage** — where CoBuild stores its pipeline data
4. **Pipeline config** — phases, gates, models, review strategy, monitoring
5. **Skills** — copied and customized for this specific project
6. **Context layers** — what information agents see during each session type

The result is a `.cobuild/` directory in the repo with everything the pipeline needs.

---

**Sub-skills** (called during setup, or run independently):
- `shared/bootstrap-connector-cp.md` — Context Palace connector setup
- `shared/bootstrap-connector-beads.md` — Beads connector setup
- `shared/bootstrap-storage-postgres.md` — Postgres storage setup
- `shared/bootstrap-context-layers.md` — context layer configuration
- `shared/bootstrap-skills.md` — skill copying and customization
- `shared/bootstrap-claude-md.md` — generate CoBuild section for CLAUDE.md

---

## Before You Start

1. Read `~/.cobuild/bootstrap.md` for local infrastructure details
2. Confirm you're in the target repo root: `pwd` and `git remote -v`
3. Verify CLIs: `cobuild version` and `cxp version` (or `bd --version`)

If `~/.cobuild/bootstrap.md` doesn't exist, ask the developer to create it from the template at the CoBuild repo's `docs/bootstrap-template.md`.

---

## Step 1: Auto-Detect

Gather what you can without asking:

```bash
# Language and build system
ls go.mod package.json Cargo.toml pyproject.toml 2>/dev/null

# GitHub remote
git remote get-url origin

# Default branch
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null

# Existing CoBuild or legacy config
ls .cobuild/ .cxp/ .cobuild.yaml .cxp.yaml 2>/dev/null

# Existing skills or context
ls skills/ .cobuild/context/ .cxp/context/ 2>/dev/null

# Existing CLAUDE.md
cat CLAUDE.md 2>/dev/null | head -50
```

Build/test command detection:
- **Go**: `go build ./...`, `go test ./...`, `go vet ./...`
- **Node**: `npm run build`, `npm test`
- **Rust**: `cargo build`, `cargo test`
- **Python**: `pytest`

If the project has subdirectories with their own build systems (multi-module), detect those too.

---

## Step 2: Ask the Developer

Present what you detected, then ask the following questions. Present them with full context so the developer understands *why* each question matters. Batch related questions where it makes sense, but don't rush — the developer needs to understand what they're configuring.

### Question 1: Project Name and Work-Item Prefix

> **Project name** — CoBuild groups pipeline runs, gate records, and metrics by project name. This should match the project name in your work-item system (Context Palace or Beads).
>
> Detected from directory/config: `<detected>`
>
> **Work-item prefix** — If your work-item system uses prefixed IDs (e.g., `pf-abc123`), what's the prefix? This helps CoBuild filter work items that belong to this project.

### Question 2: Multi-Repo

> **Does this project span multiple repos?**
>
> Some projects split across repos (e.g., a backend server + CLI tool). If so, a single *design* can produce tasks that target different repos. During the decompose phase, each task gets tagged with its target repo so CoBuild dispatches agents into the correct codebase.
>
> If yes: which repos are involved, and what does each one contain?

### Question 3: Build and Test

> **Build and test commands** — CoBuild agents run these after implementing a task, before marking it complete. Getting these wrong means agents will either skip verification or fail on every task.
>
> I detected:
> - Build: `<detected>`
> - Test: `<detected>`
>
> Are these correct? Are there additional checks (e.g., `go vet`, linting) that should run?

### Question 4: Work-Item System

> **Where do designs, tasks, and bugs live?**
>
> CoBuild doesn't own work items — it reads and writes them through a connector. Which system does this project use?
>
> 1. **Context Palace** — shards via `cxp` CLI (designs, tasks, bugs stored as shards)
> 2. **Beads** — issues via `bd` CLI
>
> CoBuild needs this to know how to read designs, create tasks during decomposition, and update status as work progresses.

### Question 5: Deploy

> **What happens after a PR is merged?**
>
> CoBuild can auto-deploy after the review phase merges a PR. This is optional — some projects just merge and deploy manually.
>
> If you want auto-deploy: list each service with its deploy command and which file paths trigger it (so CoBuild only deploys services affected by the change).

### Question 6: Review Strategy

> **How should PRs be reviewed?**
>
> After an agent implements a task and creates a PR, CoBuild needs to get it reviewed before merging. Two options:
>
> 1. **External reviewer** — an external AI (e.g., Gemini via code review) reviews the PR, and CoBuild processes the verdict
> 2. **Agent reviewer** — a CoBuild agent reviews the PR directly using the review skill
>
> External review gives you a second opinion from a different model. Agent review keeps everything in CoBuild.

---

## Step 3: Configure Connector

Based on the answer to Question 5, follow the appropriate sub-skill:

- **Context Palace** → follow `shared/bootstrap-connector-cp.md`
- **Beads** → follow `shared/bootstrap-connector-beads.md`

This verifies the CLI works, tests connectivity, and writes the connector config.

---

## Step 4: Configure Storage

Follow `shared/bootstrap-storage-postgres.md`.

This verifies Postgres connectivity, checks/creates CoBuild's tables, and writes the storage config.

---

## Step 5: Create Pipeline Config

Using all the gathered information, create `.cobuild/pipeline.yaml`. Include every section — don't leave things to implicit defaults, make the config explicit and self-documenting:

- `build` and `test` commands
- `dispatch` settings (concurrent, model)
- `connectors.work_items` (from step 3)
- `storage` (from step 4)
- `phases` with gates and skills
- `workflows` for design/bug/task
- `review` strategy and settings
- `monitoring` settings
- `deploy` if applicable
- `github.owner_repo`
- `skills_dir: skills`

Also create `.cobuild.yaml` in the repo root:
```yaml
project: <project-name>
prefix: <work-item-prefix>
```

If multi-repo, add a comment documenting the relationship:
```yaml
# Multi-repo: designs may span <related-repos>
# Tasks are tagged with target repo during decomposition
```

---

## Step 6: Set Up Skills

Follow `shared/bootstrap-skills.md`.

This copies default skills, walks through customization, and verifies skill references resolve.

---

## Step 7: Set Up Context Layers

Follow `shared/bootstrap-context-layers.md`.

This creates the architecture doc, migrates existing context files, and configures layers in the pipeline config.

---

## Step 8: Update CLAUDE.md

Follow `shared/bootstrap-claude-md.md`.

This generates a CoBuild section for the repo's CLAUDE.md containing pipeline commands, task completion protocol, work-item commands for the configured connector, and skills reference. Agents working in this repo will read CLAUDE.md — if it doesn't mention CoBuild, they won't know they're part of a pipeline.

---

## Step 9: Register and Verify

```bash
# Register the repo
cobuild setup

# Final connectivity check
cxp shard list --project <project-name> --limit 1 -o json   # for CP
# bd list --limit 1 --json                                    # for Beads

# Verify build and test
<run build commands>
<run test commands>

# Verify file structure
ls .cobuild.yaml .cobuild/pipeline.yaml
ls skills/design/ skills/implement/ skills/review/ skills/done/ skills/shared/
ls .cobuild/context/architecture.md
```

---

## Step 10: Summary

Print what was configured:

```
CoBuild setup complete for <project-name>
================================================

Project:     <name>
Prefix:      <work-item-prefix>
Repo:        <github url>
Connector:   <type>
Storage:     <backend>
Review:      <strategy>
Build:       <commands>
Test:        <commands>
Deploy:      <yes/no + services>
Multi-repo:  <yes/no + related repos>

Files created/updated:
  .cobuild.yaml                      project identity
  .cobuild/pipeline.yaml             pipeline configuration
  .cobuild/context/architecture.md   codebase architecture
  skills/                            pipeline skills (N files)
  CLAUDE.md                          updated with CoBuild instructions

Next steps:
  1. Review and customize skills for this project
  2. Generate project anatomy: cobuild scan
  3. Create a design: cobuild wi create --type design --title "..."
  4. Initialize pipeline: cobuild init <design-id>
  5. Check your pipeline: cobuild explain
```

## Gotchas

<!-- Add failure patterns here as they're discovered -->
