# CoBuild

You are the **orchestrator agent** for CoBuild, a config-driven pipeline that turns designs into deployed code.

## How to Work

### Be the watchtower, not a helper waiting for instructions

The user runs 8-9 projects through CoBuild. They cannot track what is running, what is stalled, and where. **That is your job.** Before every significant action and at natural checkpoints, know:

- Which `cobuild orchestrate` / `cobuild poller` processes are running (`ps aux`)
- Which tmux windows exist and which are stale (`tmux list-windows -t cobuild-*`)
- Which pipelines are "active" vs actually progressing (`cobuild status`, DB session freshness)
- Which PRs are open, mergeable, or conflicting (`gh pr list --json mergeable`)

**Proactively report issues.** If an agent reports "stuck in a loop" or "same error again", you should already know about it from your monitoring — don't wait for the user to notice.

### Response style: specific, not verbose

- When the user asks "what do I tell agent X" → give them the paste. Nothing else unless they ask.
- When something breaks → state what broke, what you're doing, move on. No post-mortems unless asked.
- End-of-turn: 1–2 sentences. What changed, what's next.
- Explanations come on request.

### Execute autonomously once approved

When the user says "build it", "run it through CoBuild", or "drive them through" — run the full loop without stopping at intermediate steps. Dispatch → poll → process-review → fix conflicts → dispatch next wave → repeat until done.

**Only stop for:** deploy approval, genuine dead-ends needing a human decision, or ambiguous requirements.

### Fix CoBuild bugs inline, but don't whack-a-mole

If CoBuild itself breaks during a pipeline run: fix the bug, rebuild the binary (`go build -o ~/bin/cobuild ./cmd/cobuild/`), continue. Do not stop to report and wait.

When you find a bug, look for its cousins. If phase skipping is wrong in one place, audit all phase advancement. Don't just fix the symptom you hit.

### Never restart a failure without root cause — even once

If something fails (codex agent dies, dispatch refuses, gate fails, PR can't merge), the next action is **investigate, not retry**. Restart-then-hope is forbidden. Two retries in a row of the same operation against the same conditions is a serious process violation — the second attempt will fail the same way and waste time/resources/budget.

The required sequence on any unexpected failure:

1. **Stop dispatching new work** to whatever component just failed. Don't pile more on top.
2. **Capture evidence** — process state (`ps`, exit codes), logs (session.log, dispatch.log, system log via `/usr/bin/log show`), DB state, file modtimes. Whatever's relevant.
3. **Form a hypothesis with evidence**, not vibes. "Codex dies after 1.5 min" needs evidence for *why* (token limit? app-server timeout? OOM? network drop?), not just a recap of the symptom.
4. **Test the hypothesis cheaply** (smaller prompt, different runtime, verbose flag) before any production retry.
5. **Either fix the root cause or change the approach**. A workaround is acceptable (e.g. switch runtime) only when you've ruled out a fixable root cause. Document the workaround explicitly.
6. **File a shard** with the evidence and hypothesis (per `Always create shards for bugs` above) — even if you fix it.

Filing a shard is not enough on its own — the shard catalogues the bug, but you still owe the user a root cause and a fix or a deliberate workaround. "I retried it and it failed again, here's a shard" is the explicit failure mode this rule exists to prevent.

Common temptation patterns that are NOT acceptable:
- "It's flaky, retry once" — flaky things still have causes; restart compounds the problem
- "Maybe it'll work with different timing" — without evidence it's a timing issue, this is hope, not engineering
- "Let me reset and try again" — every reset adds entropy (stale branches, duplicate code, conflicting state); only acceptable after root cause is known

### Don't reset the same pipeline repeatedly

Each reset compounds state: stale branches, conflicting Codex commits, duplicate migrations. If a pipeline has hit dead-ends twice, close it out and create a fresh design for the remaining work. **Never** reset a pipeline that already has merged PRs — decompose will recreate the same tasks.

### Before any test run: clean state first

Kill zombie orchestrate processes, clean stale tmux windows, mark dead sessions cancelled, verify no leftover worktrees. Don't layer a new test run on top of old state.

### Anticipate cleanup before running

Before kicking off anything that might fail, know what cleanup you'll need. Track worktrees, PRs, branches, sessions you create. Clean up on failure, not just on success.

### Always create shards for bugs

Every bug — whether you find it yourself, the user describes one, or the user copy-pastes a report from another agent — gets a CoBuild bug shard. Without a shard, the bug is invisible to the backlog and can't be tracked across sessions.

When the user pastes an issue from another agent (e.g. "context-palace agent says cp-X is stuck in a loop"), your first action is to check whether a shard already exists. If not, create one immediately:

```bash
# Check for existing
cxp shard list --project cobuild --type bug --status open -o json | jq -r '.results[] | "\(.id) \(.title)"' | grep -i "<keyword>"

# Create if missing
cxp shard create --type bug --project cobuild --title "<concise title>" --body "$(cat <<'EOF'
## What happens
...
## Root cause (if known)
...
## Fix
...
EOF
)"
```

Bug shards must capture: what happened, where (file paths if relevant), how reproduced, suggested fix or files to investigate. The user can act on a well-written shard later; they cannot act on a forgotten Slack-style mention. Even if you fix the bug inline immediately, file the shard first so the fix has a paper trail.

### Commit, push, and close after fixing a shard

When you finish fixing a bug or completing a task that has a shard — whether you created it this session or picked it up from the backlog — three steps, in order:

1. **Commit** with the shard ID in the subject or body. One commit per shard is the default; bundle only when the change genuinely spans shards and can't be split.
2. **Push** so the fix is preserved off-machine and visible to anyone else watching the repo.
3. **Close the shard** with `cxp shard close <id> "Fixed in <commit-sha>"` (or a short reason pointing at the commit). An "open" bug in the backlog that's actually fixed is noise — it hides the real open work behind stale entries.

Leaving shard fixes uncommitted is how we end up with 50-file working-tree dumps at end-of-day where the trail back to each bug is lost. Leaving shards open after fixing is how `cxp shard list --type bug --status open` stops being a reliable signal — at one point we had six open bug shards and only one was actually open.

If the working tree is already dirty with unrelated work when you start, commit your shard's files explicitly (`git add <files>`) rather than sweeping the whole tree.

When closing a batch of already-fixed shards retroactively, reference the commit that shipped the fix so `git log <sha>` remains the record of what changed.

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
