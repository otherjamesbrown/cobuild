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

Shorter workflows exist for **bugs** and **tasks** that skip the design and decompose phases.

### Architecture

CoBuild has three layers:

- **Connector** — bridges to an external work-item system where designs, bugs, and tasks live (Context Palace, Beads, or future Jira/Linear). CoBuild reads and writes work items through the connector. It never owns the work items.
- **Store** — where CoBuild keeps its own orchestration state: pipeline runs, gate audit records, task tracking. Currently Postgres, with SQLite and file-based stores planned.
- **Skills** — markdown files organized by phase that tell agents what to do. Gate skills define evaluation criteria. Phase skills define procedures. The pipeline's intelligence lives in skills, not compiled code.

### What You're Setting Up

This bootstrap configures:
1. **Project identity** — name, agent, GitHub remote
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

Present what you detected, then ask these questions one at a time. Use the defaults from `~/.cobuild/bootstrap.md` where applicable.

### Question 1: Project Name

> What is the project name? This should match the name in your work-item system.
>
> Detected from directory/config: `<detected>`

### Question 2: Multi-Repo

> Do designs for this project ever span multiple repos?
>
> If yes: which repos are involved? Tasks will be tagged with their target repo during decomposition, so CoBuild dispatches agents into the correct worktree.

### Question 3: Agent

> Which agent identity should this project use?
>
> Available from bootstrap config:
> | Agent | Domains |
> |-------|---------|
> (list from ~/.cobuild/bootstrap.md)

### Question 4: Build and Test

> I detected these commands. Correct?
>
> Build: `<detected>`
> Test: `<detected>`
>
> If this is a multi-module project, should build/test run from a subdirectory?

### Question 5: Work-Item System

> Which work-item system does this project use?
>
> 1. **Context Palace** — shards via `cxp` CLI
> 2. **Beads** — issues via `bd` CLI

### Question 6: Deploy

> Does this project have services to auto-deploy after PR merge?
>
> If yes: list each service with its name, path prefix (files that trigger it), and deploy command.

### Question 7: Review Strategy

> How should PRs be reviewed?
>
> 1. **external** — an external reviewer (e.g., Gemini) reviews, CoBuild processes the result
> 2. **agent** — a CoBuild agent reviews the PR directly
>
> Default: `<from bootstrap>`

### Question 8: Tmux Session

> What tmux session should dispatched agents run in?
>
> Check: `tmux list-sessions`
> Convention from bootstrap: `<convention>`

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
- `agents` with domains
- `dispatch` settings (concurrent, tmux, model)
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
agent: <agent-identity>
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

## Step 8: Register and Verify

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

## Step 9: Summary

Print what was configured:

```
CoBuild setup complete for <project-name>
================================================

Project:     <name>
Repo:        <github url>
Agent:       <agent>
Connector:   <type>
Storage:     <backend>
Review:      <strategy>
Build:       <commands>
Test:        <commands>
Deploy:      <yes/no + services>
Multi-repo:  <yes/no + related repos>

Files created:
  .cobuild.yaml                      project identity
  .cobuild/pipeline.yaml             pipeline configuration
  .cobuild/context/architecture.md   codebase architecture
  skills/                            pipeline skills (N files)

Next steps:
  1. Review and customize skills for this project
  2. Create a design: cxp shard create --type design --title "..."
  3. Initialize pipeline: cobuild init <design-id>
  4. Dry-run the poller: cobuild poller --once --dry-run
```
