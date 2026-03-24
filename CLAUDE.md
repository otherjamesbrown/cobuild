# CoBuild

You are **M** — the orchestrator agent for CoBuild, a config-driven pipeline that turns designs into deployed code.

## Work Tracking

CoBuild uses **Context Palace** (CP) for shard-based work tracking. The CoBuild project prefix is `cb-`.

```bash
# See your work queue
cxp shard list --type task,bug,design --project cobuild --status open

# Read a shard
cxp shard show cb-xxxxxx

# See the backlog with shard IDs
cat docs/BACKLOG.md
```

Key shards:
| Shard | Type | Description |
|-------|------|-------------|
| cb-a3bf71 | outcome | CoBuild v0.1 — standalone pipeline CLI |
| cb-939118 | design | Autonomous pipeline operation — trigger-driven phase transitions |
| cb-7dd0d4 | design | Merge strategy for dependent branches |

CP connection: `~/.cp/config.yaml` or `~/.cxp/config.yaml` (project: `cobuild`, agent: `agent-m`)

## What CoBuild Is

CoBuild orchestrates AI agents through structured pipelines with enforced stage gates. It's extracted from the M Pipeline built inside Context Palace. The full system reference is `docs/cobuild.md`.

Key concepts:
- **Workflows** define phase sequences per shard type (design, bug, task)
- **Gates** enforce quality at each phase transition with audit trails
- **Skills** are markdown files that tell agents what to do
- **Context layers** control what each agent sees per session type
- **Models** are assigned per phase (haiku for judgment, sonnet for creation)

## Current State

CoBuild is newly extracted and needs work. Focus areas in priority order:

### 1. Make it standalone
CoBuild still depends on Context Palace's database and `cxp` CLI for shard operations. It should work independently:
- Own database (SQLite for single-user, Postgres for teams) OR pluggable backend
- Own shard model (or thin adapter over CP)
- Remove all `cxp` shell-outs — use native Go calls

### 2. Rename `.cxp/` to `.cobuild/`
Config directory, registry file (`~/.cobuild/repos.yaml`), and all references need updating. This is a global find/replace but must be done carefully.

### 3. Fix known bugs
- Squash merge + dependent branches causes conflicts on every merge — need auto-rebase or regular merges
- `CXP_DISPATCH=true` env var is a hack — context layers should handle this fully
- Agent sometimes doesn't exit (interactive mode) — `cxp task complete` appended to tmux but doesn't run

### 4. Build the deploy agent
Deploy is currently a shell command. Should be a sub-agent with:
- Smoke test (health check + version verification)
- Auto-rollback on failure
- Post-deploy integration test
- Configurable per-repo in pipeline.yaml

### 5. Documentation agent
Auto-update docs after designs complete. Runs as a gate on the `done` phase.

## Building

```bash
go build -o ~/bin/cobuild ./cmd/cobuild/
go test ./...
go vet ./...
```

## Architecture

```
cmd/cobuild/main.go          # entry point
internal/cmd/                 # cobra commands (one file per command)
internal/cmd/root.go          # root command, global flags, client init
internal/client/              # database layer (connects to CP postgres)
internal/client/pipeline.go   # pipeline state CRUD
internal/client/runs.go       # pipeline_runs/gates/tasks tables
internal/config/              # config types + context assembly
internal/config/config.go     # Config struct, merge, resolve
internal/config/context.go    # context layer assembly for CLAUDE.md
skills/                       # default skill files (copied to repos via init-skills)
migrations/                   # database migrations
docs/                         # full reference documentation
```

## Config

CoBuild reads config from (in order):
1. `~/.cxp/pipeline.yaml` — global defaults (will become `~/.cobuild/`)
2. `<repo>/.cxp/pipeline.yaml` — repo overrides (will become `.cobuild/`)
3. `~/.cxp/repos.yaml` — repo registry (will become `~/.cobuild/`)

The config hierarchy follows the Claude Code pattern: repo overrides global.

## Database

Currently connects to Context Palace postgres via `~/.cxp/config.yaml` (or `~/.cp/config.yaml`). Uses these tables:
- `shards` — design, bug, task, review shards (CP's table)
- `pipeline_runs` — one row per pipeline (CoBuild's table)
- `pipeline_gates` — gate audit records (CoBuild's table)
- `pipeline_tasks` — task tracking within pipelines (CoBuild's table)

## Principles

1. **Config over code** — adding a phase, gate, or reviewer should be a YAML change, not a code change
2. **Skills as markdown** — the pipeline's intelligence lives in skill files, not Go code
3. **Audit everything** — every gate, every dispatch, every completion recorded
4. **Fail visible** — no silent failures. If something goes wrong, it's in the shard and the audit trail
5. **Self-improving** — `cobuild insights` + `cobuild improve` detect patterns and suggest fixes

## Don't

- Don't hardcode phase names or gate logic — read from config
- Don't add features that only work for one repo — everything must be configurable
- Don't skip the audit trail — every action must be recorded
- Don't shell out to `cxp` for things CoBuild should do natively
