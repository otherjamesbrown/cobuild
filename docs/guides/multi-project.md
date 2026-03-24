# Multi-Project Support

CoBuild manages multiple repos from a single poller. Each project has its own pipeline config, skills, and context layers. The repo registry maps project names to local paths, and the all-projects poller checks each one every cycle.

## Quick start

```bash
cd ~/github/yourorg/project-a && cobuild setup
cd ~/github/yourorg/project-b && cobuild setup
cobuild poller --all-projects    # poll all registered repos
```

## How it works

### The repo registry

`~/.cobuild/repos.yaml` is the central registry. Each entry maps a project name to a local path and default branch:

```yaml
repos:
    penfold:
        path: /Users/james/github/otherjamesbrown/penfold
        default_branch: main
    context-palace:
        path: /Users/james/github/otherjamesbrown/context-palace
        default_branch: main
```

`cobuild setup` creates or updates this file automatically when you register a repo.

### How the poller works across projects

```bash
cobuild poller --all-projects
```

Each cycle, the poller:

1. Loads all entries from `~/.cobuild/repos.yaml`
2. For each project, loads that repo's `pipeline.yaml` (merged with global config)
3. Runs trigger checks: new designs, needs-review tasks, satisfied wait conditions
4. Runs health checks: stalled agents, crashed sessions, max retries
5. Spawns M sessions as needed, with the working directory set to each repo's path

Each project is independent -- a stalled agent in project A does not block dispatches in project B.

### Per-repo config overrides

Every project gets its own `.cobuild/pipeline.yaml`. The merge order is:

```
built-in defaults  →  ~/.cobuild/pipeline.yaml (global)  →  <repo>/.cobuild/pipeline.yaml
```

This means you can set global defaults (model preferences, monitoring settings) once, then override per-repo where needed.

### How dispatch resolves the correct repo

When the poller finds a shard that needs attention, it resolves the repo from the project name associated with the shard. Dispatch commands use the repo path from the registry to:

- Create worktrees in the correct repo
- Generate CLAUDE.md from that repo's context layers
- Run build/test commands defined in that repo's config
- Spawn tmux windows with the correct working directory

## Configuration

### Registering a repo

```bash
cd /path/to/your/project
cobuild setup
```

This auto-detects:

- **Language** (Go, Node, Rust, Python) from project files
- **Build/test commands** based on language
- **GitHub remote** from git config
- **Default branch** from git config

Flags:

```bash
cobuild setup --project custom-name    # override auto-detected project name
cobuild setup --force                  # overwrite existing config
cobuild setup --dry-run                # show what would be created
```

### Global config

`~/.cobuild/pipeline.yaml` sets defaults for all projects:

```yaml
dispatch:
    max_concurrent: 3
    default_model: sonnet

monitoring:
    stall_timeout: 30m
    crash_check: true
    max_retries: 3
    model: haiku
    actions:
        on_stall: skill:m-stall-check
        on_crash: redispatch
        on_max_retries: escalate

review:
    model: haiku
```

### Repo-level overrides

Each repo's `.cobuild/pipeline.yaml` can override any global setting:

```yaml
# This repo needs more concurrent agents
dispatch:
    max_concurrent: 5
    tmux_session: dev-myproject

# Different review strategy for this repo
review:
    strategy: agent
    review_skill: m-review-pr
```

### Poller flags

```bash
cobuild poller                       # current project only, every 30s
cobuild poller --all-projects        # all registered repos
cobuild poller --once                # single check, then exit
cobuild poller --once --dry-run      # show what would trigger, no action
```

## Examples

### Example 1: Go backend (penfold)

```bash
cd ~/github/otherjamesbrown/penfold
cobuild setup
```

Creates `.cobuild/pipeline.yaml`:

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
    tmux_session: dev-penf-cli
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
          paths: [services/ai/]
          command: ./scripts/deploy.sh ai

github:
    owner_repo: otherjamesbrown/penfold
```

### Example 2: Node.js frontend

```bash
cd ~/github/yourorg/dashboard
cobuild setup
```

Creates config with detected Node.js commands:

```yaml
build:
    - npm run build
test:
    - npm test
    - npm run lint

dispatch:
    tmux_session: dev-dashboard
    default_model: sonnet

review:
    strategy: agent
    review_skill: m-review-pr
    ci:
        mode: all-pass
        wait: true

github:
    owner_repo: yourorg/dashboard
```

### Example 3: Python ML pipeline

```bash
cd ~/github/yourorg/ml-pipeline
cobuild setup
```

```yaml
build:
    - pip install -e .
test:
    - pytest tests/
    - mypy src/

dispatch:
    tmux_session: dev-ml
    default_model: sonnet
    max_concurrent: 2          # ML tasks are resource-heavy

monitoring:
    stall_timeout: 60m         # ML tasks take longer

github:
    owner_repo: yourorg/ml-pipeline
```

### Running all three together

```bash
cobuild poller --all-projects
```

The poller checks penfold, dashboard, and ml-pipeline every 30 seconds. Each uses its own build commands, review strategy, and stall timeout. Agents are dispatched into the correct repo's worktree with that repo's context layers.

## Troubleshooting

**Project not found:** Run `cobuild setup` from the repo root. Check `~/.cobuild/repos.yaml` to verify the project is listed with the correct path.

**Wrong config being used:** CoBuild merges global then repo config. If a repo config defines `phases:`, it replaces the entire global phases list. Run `cobuild show <id>` to see which config path is being loaded.

**Poller not picking up a project:** Verify the project path in `~/.cobuild/repos.yaml` is correct and the directory exists. The poller silently skips projects with invalid paths. Run `cobuild poller --once --dry-run` to see which projects are being polled.
