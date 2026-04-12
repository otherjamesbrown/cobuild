# CoBuild

You are the **orchestrator agent** for CoBuild, a config-driven pipeline that turns designs into deployed code.

## How to Work

**Check before starting something new.** If the user hasn't explicitly asked you to build/run/fix something, confirm what they want first.

**Once approved, execute autonomously start-to-finish.** When the user says "build it", "run it through CoBuild", or "drive them through" — run the full pipeline loop without stopping to ask permission at intermediate steps. Dispatch → poll → process-review → fix conflicts → dispatch next wave → repeat until done. Report the outcome when complete.

**Fix CoBuild bugs inline.** If CoBuild itself breaks during a pipeline run (merge conflicts, sandbox issues, missing features), fix the bug, rebuild the binary, and continue the pipeline. Do not stop to report the bug and wait for instructions.

**Only stop for:** deploy approval, genuine dead-ends needing a human decision, or ambiguous requirements.

## Terminology

Two roles show up throughout CoBuild's docs, skills, and commit messages. Use these terms consistently:

- **orchestrator agent** — whoever invokes `cobuild dispatch`, `cobuild run`, or any other pipeline CLI. Stays lightweight, delegates work. Can be an interactive Claude/Codex session, the `cobuild poller` daemon, a cron job, or a human at a shell prompt — CoBuild doesn't care.
- **dispatched CoBuild agent** — the fresh Claude Code or Codex process CoBuild spawns in a tmux window inside a git worktree to execute a phase's skill. Does all the real reading, editing, and committing. Exits when the skill is done.

Older docs use "M", "parent session", "calling agent", "fresh session", or "implementing agent" for one of these two — they all map onto the canonical terms above. Prefer the canonical terms in new material.

## Work Tracking

CoBuild's own work items live in **Context Palace** under the `cb-` prefix. Use `cxp` directly — this is our project, not a project CoBuild is orchestrating.

```bash
# See work queue
cxp shard list --project cobuild --type task,bug,design --status open -o json

# Read a shard
cxp shard show cb-xxxxxx

# Create a task
cxp shard create --type task --project cobuild --title "..." --body "..."

# See the backlog with shard IDs
cat docs/BACKLOG.md
```

Key shards:
| Shard | Type | Description |
|-------|------|-------------|
| cb-a3bf71 | outcome | CoBuild v0.1 — standalone pipeline CLI |
| cb-939118 | design | Autonomous pipeline operation — trigger-driven phase transitions |
| cb-7dd0d4 | design | Merge strategy for dependent branches |

Connection: `~/.cobuild/config.yaml` (project: `cobuild`, agent: `agent-m`).

### When to use `cxp` vs `cobuild wi`

| Context | Command | Why |
|---------|---------|-----|
| Working on CoBuild itself | `cxp shard ...` | We're the developer, talking to our own Context Palace tenant (`cb-` prefix) |
| CoBuild orchestrating a project | `cobuild wi ...` | CoBuild is acting on behalf of a project, going through the connector. Works with any backend (CP, Beads, Jira). |

Skills use `cobuild wi` because they run on behalf of projects. This CLAUDE.md uses `cxp` because we're developing CoBuild itself.

## Relationship to Context Palace

CoBuild was extracted from `context-palace/cxp`. The pipeline code currently exists in **both** repos:
- `cxp shard pipeline *` / `cxp task dispatch` etc. — the original, still used by penfold
- `cobuild *` — the standalone extraction

CoBuild now has native shard operations (status, labels, worktrees, content append). The `cxp` CLI is no longer required. Pipeline commands will be removed from `cxp` once penfold migrates.

**Do not duplicate work** — new pipeline features go in CoBuild, not context-palace.

## What CoBuild Is

CoBuild orchestrates AI agents through structured pipelines with enforced stage gates. It was extracted from an earlier orchestration pipeline built inside Context Palace. The full system reference is `docs/cobuild.md`.

Key concepts:
- **Workflows** define phase sequences per shard type (design, bug, task)
- **Gates** enforce quality at each phase transition with audit trails
- **Skills** are markdown files that tell agents what to do
- **Context layers** control what each agent sees per session type
- **Models** are assigned per phase (haiku for judgment, sonnet for creation)

## Current State

CoBuild is newly extracted and needs work. Focus areas in priority order:

### 1. Make it standalone
CoBuild still depends on Context Palace's database for storage. It should work independently:
- Own database (SQLite for single-user, Postgres for teams) OR pluggable backend
- Own shard model (or thin adapter over CP)
- ~~Remove all `cxp` shell-outs from pipeline logic~~ **DONE** — all shard operations are now native via CPConnector (which shells out to `cxp` CLI with `-o json`)

### 2. ~~Rename `.cxp/` to `.cobuild/`~~ **DONE**
Config directory, registry file, env vars, and all references updated. Legacy `.cxp/` paths are still supported as fallback.

### 3. ~~Dispatch reliability~~ **DONE** (cb-7aa91d)
Major rework of the dispatch → completion flow, driven by dogfooding on penfold:
- ~~Agent sometimes doesn't exit~~ → **Stop hook** writes `.claude/settings.local.json` into worktrees; `cobuild complete` runs automatically on agent termination
- ~~CLAUDE.md overwritten with context dump~~ → Context now goes to `.cobuild/dispatch-context.md`; CLAUDE.md gets a small pointer section appended (idempotent)
- ~~Dispatch artifacts leak into commits~~ → `cobuild complete` excludes `.cobuild/` and `CLAUDE.md` from auto-commit via pathspec; dispatch writes `.cobuild/.gitignore`
- ~~Workspace trust dialog blocks dispatch~~ → `ensureClaudeTrust()` pre-registers worktrees in `~/.claude.json`
- ~~Direct dispatch fails without `cobuild init`~~ → Auto-creates pipeline run on first dispatch
- ~~Bug workflow forced read-only investigation~~ → Default bug workflow is now `fix → review → done`; label `needs-investigation` escalates to `investigate → implement → review → done`
- ~~`.claude/` edits stall agents~~ → Worktree `.claude/settings.local.json` includes deny list for `.claude/**` edits

### 4. Fix remaining known bugs
- Squash merge + dependent branches causes conflicts on every merge — need auto-rebase or regular merges (see cb-7dd0d4)

### 5. Build the deploy agent
Deploy is currently a shell command. Should be a sub-agent with:
- Smoke test (health check + version verification)
- Auto-rollback on failure
- Post-deploy integration test
- Configurable per-repo in pipeline.yaml

### 6. Documentation agent
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
internal/cmd/root.go          # root command, global flags, client/connector/store init
internal/connector/           # work-item connectors (CP, Beads)
internal/connector/connector.go  # Connector interface + WorkItem types
internal/connector/cp.go      # CPConnector (shells out to cxp CLI)
internal/connector/beads.go   # BeadsConnector (shells out to bd CLI)
internal/store/               # CoBuild's own data persistence
internal/store/store.go       # Store interface
internal/store/postgres.go    # PostgresStore implementation
internal/client/              # legacy database layer (being migrated to connector + store)
internal/config/              # config types + context assembly
internal/config/config.go     # Config struct, merge, resolve
internal/config/context.go    # context layer assembly for CLAUDE.md
internal/merge/               # smart merge: conflict analysis, supersession, wave-aware
internal/worktree/            # git worktree lifecycle (create, verify, cleanup)
hooks/                        # claude code hooks for session event tracking
hooks/cobuild-event.sh        # repeated read detection, token tracking, event logging
hooks/hooks.json              # hook registration for SessionStart, PreToolUse, etc.
.cobuild/context/always/anatomy.md  # auto-generated file index (cobuild scan)
skills/                       # default skill files (copied to repos via init-skills)
examples/                     # example config files (pipeline.yaml, cobuild.yaml)
migrations/                   # database migrations
research/                     # design docs and research
docs/                         # reference documentation + guides
```

## Config

CoBuild reads config from (in order):
1. `~/.cobuild/pipeline.yaml` — global defaults
2. `<repo>/.cobuild/pipeline.yaml` — repo overrides
3. `~/.cobuild/repos.yaml` — repo registry

Legacy `~/.cxp/` paths are still supported as fallback.

The config hierarchy follows the Claude Code pattern: repo overrides global.

## Database

Currently connects to Context Palace postgres via `~/.cobuild/config.yaml` (legacy: `~/.cxp/config.yaml` or `~/.cp/config.yaml`). Uses these tables:
- `shards` — design, bug, task, review shards (CP's table)
- `pipeline_runs` — one row per pipeline, phase, status (CoBuild's table)
- `pipeline_gates` — gate audit records with verdicts and findings (CoBuild's table)
- `pipeline_tasks` — task tracking within pipelines with wave assignments (CoBuild's table)
- `pipeline_sessions` — per-dispatch session records: timing, model, prompt, results, costs (CoBuild's table)
- `pipeline_session_events` — per-tool-call events: file reads, edits, commands, errors (CoBuild's table)

## Design Direction: Connectors + Separated Storage

CoBuild follows **Claude Code / CoWork patterns** for extensibility. See `research/claude-patterns.md` for full analysis.

### Ontology

CoBuild has 7 core objects. See `research/cobuild-ontology.md` for the full Design Ontology Spec.

| Object | What it is | Where it lives |
|--------|-----------|---------------|
| **WorkItem** | A unit of work (design, bug, task) | Connector (external) |
| **Pipeline** | Orchestration of a WorkItem through phases | CoBuild's database |
| **Phase** | A named stage (design, decompose, implement, review, done) | Config |
| **Gate** | Quality check at phase boundaries | Config + CoBuild's database |
| **Skill** | Markdown instructions for an agent | Filesystem |
| **Agent** | Ephemeral AI worker | Config |
| **Connector** | Bridge to external work-item system | Config |

The critical boundary: **WorkItem** lives in the Connector. **Pipeline** lives in CoBuild. Don't mix them.

### Key terms (aligned with Claude ecosystem)
- **Connector** — bridges CoBuild to an external work-item system (CP, Beads, Jira, Linear)
- **Skill** — markdown file with YAML frontmatter + instructions (same as Claude Code skills)
- **Hook** — event handler on lifecycle points (phase transitions, dispatch, completion)
- **Scope** — config hierarchy: global (`~/.cobuild/`) > repo (`.cobuild/`) > local (`.cobuild/*.local.yaml`)

### Architecture split
- **Connector** handles external work items: designs, bugs, tasks, relationships, labels, content
- **CoBuild's own tables** handle orchestration: pipeline runs, gates, dispatch state, audit trail
- Pipeline metadata (phase, locks, review history) lives in `pipeline_runs`, NOT in work-item metadata

### Connector interface
The `Connector` interface (`internal/connector/`) abstracts work-item systems. Config selects which:
```yaml
connectors:
    work_items:
        type: context-palace    # or "beads", "jira"
```
Implementations: `CPConnector` (shells out to `cxp` CLI with `-o json`), `BeadsConnector` (shells out to `bd` CLI with `--json`), future `JiraConnector` (REST API).

## Principles

1. **Config over code** — adding a phase, gate, or reviewer should be a YAML change, not a code change
2. **Skills as markdown** — the pipeline's intelligence lives in skill files, not Go code
3. **Audit everything** — every gate, every dispatch, every completion recorded
4. **Fail visible** — no silent failures. If something goes wrong, it's in the shard and the audit trail
5. **Self-improving** — `cobuild insights` + `cobuild improve` detect patterns and suggest fixes
6. **Claude-native patterns** — use Claude Code/CoWork terminology and patterns (connectors, skills, hooks, scopes)

## Don't

- Don't hardcode phase names or gate logic — read from config
- Don't add features that only work for one repo — everything must be configurable
- Don't skip the audit trail — every action must be recorded
- Don't invent new terms when Claude already has one — use "connector" not "adapter", "skill" not "command"
