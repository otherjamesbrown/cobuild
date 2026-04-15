# CoBuild — INTEROP

How agents in **other projects** interact with CoBuild. Load this when you need to file work items against a project, drive a shard through a pipeline, check pipeline status, or request changes to CoBuild itself from outside the cobuild repo. For working **inside** CoBuild, read `CLAUDE.md` instead.

## What CoBuild produces

- **Pipeline runs** `[Live]` — drive a shard through `design → decompose → implement → review → done`. Every phase transition is gated and audited. `cobuild orchestrate <shard-id>`.
- **Dispatched agent sessions** `[Live]` — CoBuild spawns a fresh Claude Code or Codex agent in a tmux window inside an isolated git worktree, runs a skill, commits, opens a PR, and merges. One session per task.
- **Audit trail** `[Live]` — `pipeline_runs`, `pipeline_gates`, `pipeline_sessions`, `pipeline_session_events`, `pipeline_tasks` capture every phase, verdict, dispatch, tool call, and cost. Surfaced via `cobuild status` and `cobuild audit`.
- **Work-item connector** `[Partial]` — `cobuild wi create|list|show|close` is backend-neutral. Context Palace and Beads connectors are Live; Jira is Planned. Prefer `cobuild wi` to direct `cxp`/`bd` calls for portability.
- **Deploy agent** `[Planned]` — deploy is a shell command today. Planned as a sub-agent with smoke-test, auto-rollback, and post-deploy integration test.
- **Documentation agent** `[Planned]` — auto-updates project docs after designs complete; will run as a gate on the `done` phase.

## What CoBuild consumes from other projects

| From | What | Mechanism | Status |
|------|------|-----------|--------|
| Any project | Work items (design, task, bug) | Connector backend (CP `cxp`, Beads `bd`) | Live |
| Any project | Pipeline overrides (build, test, deploy, context layers) | `<repo>/.cobuild/pipeline.yaml` | Live |
| Any project | Phase-specific skills | `<repo>/skills/*.md` | Live |
| Any project | Registry entry mapping project name → repo path | `cobuild setup --project <name>` writes `~/.cobuild/repos.yaml` | Live |

## How to interact with us

```bash
cobuild wi create --type <design|task|bug> --project <name> \
    --title "..." --body "..."                 # file work into any project
cobuild orchestrate <shard-id>                 # drive a shard end-to-end
cobuild status [--project <name>]              # running pipelines + current phase
cobuild audit <shard-id>                       # gate verdicts + session history
cobuild doctor                                 # diagnose a stalled pipeline
```

`--project <name>` is load-bearing on `cobuild wi create` — without it, the shard lands in whichever project's pool the current repo resolves to. Always pass it explicitly when filing cross-project.

Full reference: `cobuild --help` and `docs/cobuild.md`.

## Where to look

| Need | Location |
|------|----------|
| Full system reference (phases, gates, skills, context layers) | `docs/cobuild.md` |
| Object model (WorkItem, Pipeline, Phase, Gate, Skill, Agent, Connector) | `research/cobuild-ontology.md` |
| Default skill files copied into dispatched worktrees | `skills/` |
| Example pipeline and repo config | `examples/pipeline.yaml`, `examples/cobuild.yaml` |

## How to cite CoBuild

Format is defined in `~/decisions/citation-format.md`. CoBuild's `<ref>` convention: `<kind> <id>` where kind is one of `pipeline_run`, `gate`, `session`, `task`. IDs are primary keys in CoBuild's tables and are stable.

Default tier: **T1** for raw records (the session ran, the gate fired — the row exists); **T3** for analytic rollups produced by `cobuild insights` or `cobuild improve`.

Examples:

> ✓ "Dispatch consistently dies ~90s into codex runs on wave-2 tasks (CoBuild, session 4821, 2026-04-14) [T1]."
> ✓ "Review gate verdicts trended toward deny after the v0.1 cut (CoBuild, pipeline_run 192, 2026-04-10) [T3]."
> ✗ "The pipeline looked stuck" — no ID, not re-resolvable.

## Don't modify from outside CoBuild

- `pipeline_runs`, `pipeline_gates`, `pipeline_sessions`, `pipeline_session_events`, `pipeline_tasks` — CoBuild's canonical tables. Read freely; never write. Direct writes desync the dispatch state machine.
- Git worktrees under `~/worktrees/<project>/` — active dispatch state. Deleting one mid-pipeline corrupts the session record and leaks the branch.
- `.cobuild/` inside a dispatched worktree (`dispatch-context.md`, `complete.done`, `sessions/`) — owned by the dispatched-agent lifecycle.
- `~/.cobuild/repos.yaml` — shared registry. Edit via `cobuild setup`, not by hand.

Reading any of these for investigation is encouraged. Writing from outside CoBuild is not.

## Requesting changes

```bash
cobuild wi create --type <bug|task|design> --project cobuild \
    --title "..." --body "..."
```

Good bug reports include: the `cobuild` subcommand that failed, exit code, relevant shard ID, session ID (cite as `(CoBuild, session <id>, <date>) [T1]`), and any `dispatch-error.log` excerpt. CoBuild's CLAUDE.md requires a bug shard for every issue — if you've hit a CoBuild problem and no shard exists, filing one is the single most useful thing you can do.

## Critical gotchas

1. **CoBuild is infrastructure — the work item lives in your project, not ours.** Designs, tasks, and bugs you file belong to *your* project's pool; `--project <name>` is what selects that. CoBuild's own tables track pipeline state, not your work. Conflating the two leads to shards in the wrong pool and citations that don't resolve.
2. **Never restart a failing pipeline without a root cause.** If `cobuild orchestrate` or a dispatched agent fails, investigate with `cobuild audit <shard>` and `cobuild doctor` first. Blind retry compounds state — stale branches, duplicate commits, conflicting migrations. This rule is in CoBuild's CLAUDE.md for our own agents; it applies equally to external callers.
