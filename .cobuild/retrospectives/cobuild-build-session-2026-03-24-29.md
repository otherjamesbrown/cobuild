# CoBuild Build Session Retrospective

**Period:** 2026-03-24 to 2026-03-29 (6 days)
**Participants:** James (developer) + Claude Opus (agent M)
**Scope:** Build CoBuild from extracted pipeline code to a working multi-project pipeline system

## Summary

| Metric | Value |
|--------|-------|
| Commits | 57 |
| Files changed | 68 |
| Lines added | ~6,654 |
| Lines removed | ~856 |
| New Go files | ~25 |
| New skill files | 3 (investigate, decompose, design-review) |
| Research docs | 5 new (competitive landscape, best practices, skills article) |
| Designs completed | 2 (cb-e9562d, cb-3f5be6) |
| Designs created | 10 |
| Pipeline runs | 28 across 3 projects |
| Gate verdicts recorded | 50 |
| Agent sessions tracked | 13 |
| Projects onboarded | 2 (penfold with Context Palace, moneypenny with Beads) |
| Bugs found and fixed | ~25+ |

## What Was Built

### Commands (19 new)
- `cobuild wi` (show, list, links, status, append, create, label) — connector-agnostic work items
- `cobuild dispatch` / `dispatch-wave` / `wait` — phase-aware agent dispatch
- `cobuild merge` / `merge-design` — smart merge with conflict detection and supersession
- `cobuild deploy` — trigger-path matching with smoke tests and rollback
- `cobuild investigate` — bug investigation gate
- `cobuild retro` — pipeline retrospective
- `cobuild status` — active pipelines overview
- `cobuild explain` — human-readable pipeline overview
- `cobuild update-agents` — regenerate AGENTS.md from current config
- `cobuild run` — submit for autonomous processing
- `cobuild admin health/cleanup/db-stats/stuck` — system maintenance

### Packages (4 new)
- `internal/merge/` — conflict analysis, supersession detection, merge plan execution
- `internal/worktree/` — robust worktree lifecycle with stale cleanup
- `internal/cmd/gatelogic.go` — connector+store gate orchestration
- `hooks/` — Claude Code session event tracking

### Database (3 new tables)
- `pipeline_sessions` — per-dispatch: timing, model, prompt, context, results
- `pipeline_session_events` — per-tool-call: events, timestamps
- `pipeline_runs.mode` column — manual vs autonomous

### Skills (3 new, all 20 improved)
- `investigate/bug-investigation.md` — read-only root cause analysis
- `decompose/decompose-design.md` — task decomposition with wave assignment
- `shared/design-review.md` — pre-flight check before pipeline submission
- All 20 skills: added YAML frontmatter (name, description, summary, version), Gotchas sections
- Removed all personal references (M, James, Mycroft, agent-steve)

### Infrastructure
- Poller rewritten: store+connector, phase-aware dispatch, mode filtering
- Context layers: directory-based auto-discovery + phase-aware filtering
- Deploy: pre-deploy step for migrations, trigger-path file detection
- Session tracking: hooks for tool calls, prompt/context stored in DB
- Admin CLI: health checks, cleanup, stuck detection

## What Worked

1. **Eating our own cooking.** Running CoBuild on itself (cb-e9562d) found 9 bugs in the first pipeline run. Every bug was a bug a real user would have hit. The second design (cb-3f5be6) had zero bugs — the fixes from the first run held.

2. **Real project onboarding as testing.** Penfold (Context Palace) found dispatch bugs, merge conflicts, and context gaps. Moneypenny (Beads) found FK constraints, JSON format mismatches, and the interactive vs dispatch confusion. Each project found bugs the other couldn't.

3. **Retrospective feedback loop.** Every issue became either a skill gotcha, a decompose guideline, a review rule, or a code fix. The skills accumulated real lessons:
   - Migration number collisions → decompose gotcha
   - Hardcoded config values → review gotcha
   - Missing context layers → decompose Step 7
   - Agents merging directly → dispatch enforcement rules

4. **Phase-aware dispatch.** One command (`cobuild dispatch`) works for every phase — investigation, design review, decomposition, implementation, review, retrospective. The dispatched agent gets the right prompt automatically.

5. **Connector abstraction proved out.** `cobuild wi` works identically for both Context Palace and Beads. The penfold→moneypenny transition was config-only (change `type: beads`), plus three bug fixes in the Beads connector.

## What Failed

### Dispatch took 4 iterations to get right
1. `--print` flag was the default in config → agents couldn't iterate (single turn)
2. Prompt delivered as shell argument → truncated in tmux
3. Prompt piped via stdin → tmux doesn't support stdin redirect
4. Interactive mode with script + positional argument → works

**Root cause:** No research before iterating. A web search would have found the tmux stdin limitation immediately.

### Agents bypassed the pipeline
- Used `git merge` instead of `cobuild merge` → skipped review gate
- Used `cxp bug create` instead of `cobuild wi create` → inconsistent tracking
- Used inline sub-agents instead of `cobuild dispatch` → ate context window
- Didn't know about investigation phase → dispatched bugs directly

**Root cause:** AGENTS.md was stale or unclear. Fixed by: explicit manual mode workflows, "Next step:" on every command, enforcement rules in dispatch prompt.

### Migration number collisions (3 times)
Parallel tasks and bug fixes created migrations with conflicting numbers.

**Root cause:** Agents pick their own numbers. Fixed by: decompose skill assigns numbers, investigation skill checks current highest.

### PR review comments ignored
Gemini raised a critical finding on PR #76 (missing tenant_id filter). Agent merged without addressing it.

**Root cause:** Agent used raw git merge, bypassing the review gate. Fixed by: dispatch prompt says "NEVER merge PRs yourself", enforcement rules.

### Config parsing silently failed
The global pipeline.yaml used map-format phases but the Go struct expected a list. `LoadConfig` returned an error that was silently swallowed (`pCfg, _ :=`).

**Root cause:** Config schema drifted from docs. Fixed by: changed struct to map, fixed all callers, audit swept all docs.

### Context layers not configured before dispatch
Moneypenny dispatched 4 tasks with zero context configured. Agents didn't know the project architecture.

**Root cause:** No checkpoint between decomposition and dispatch. Fixed by: decompose skill Step 7 (context layer check).

## Key Design Decisions

1. **Skills as markdown, not compiled code.** Every behavioral change was a skill edit, not a Go code change. The gate evaluation criteria, dispatch instructions, and investigation procedure are all markdown files that agents read.

2. **Connector over owner.** CoBuild never owns work items. They live in Context Palace or Beads. CoBuild just orchestrates — reads status, advances phases, dispatches agents.

3. **Store for pipeline state, connector for work items.** Clean separation. Pipeline runs, gates, sessions in Postgres. Work items in whatever system the project uses.

4. **Interactive + dispatch, not just dispatch.** The moneypenny feedback showed that sometimes you've already done the work in conversation. Recording the gate verdict directly (Option A) is as valid as dispatching (Option B). Both advance the phase.

5. **Manual mode by default, autonomous opt-in.** The poller only processes pipelines explicitly marked as autonomous. No surprise dispatches.

6. **Context collections via directories.** Drop `.md` files in `.cobuild/context/<phase>/` — no YAML config needed. Most intuitive for developers.

## Metrics

### Gate efficiency
- Readiness review: 12/16 passed first time (75%) — 4 failures were from early testing
- Decomposition: 19/19 passed first time (100%)
- Investigation: 11/11 passed first time (100%)
- Retrospective: 2/2 passed first time (100%)

### Agent sessions
- 13 sessions tracked in DB
- 7 penfold implementation sessions (all completed)
- 4 moneypenny implementation sessions (running)
- 1 penfold investigation session (completed)
- 1 penfold decomposition session (completed)

### Code velocity
- 57 commits in 6 days (~10/day)
- 6,654 lines added across 68 files
- Average commit: ~115 lines

## What To Do Next

### High priority
1. **Complete legacy client migration (cb-b2f3ac)** — 18 cbClient calls remain, mostly in pipeline.go and poller.go
2. **Build SQLite store** — zero-dependency mode for simple projects
3. **CI/CD for cobuild repo** — build/test on every push

### Medium priority
4. **cobuild merge: check for unaddressed critical review comments** before merging
5. **Poller: handle phase transitions after dispatch completion** — currently only dispatches, doesn't detect completion
6. **Token tracking** — parse transcript JSONL post-session for cost data
7. **Playbook refactor into hub + spoke** — 280 lines is too dense

### Lower priority
8. **Plugin marketplace for skills** — share skills across projects
9. **File-based store** — git-trackable pipeline state
10. **MCP connectors** — Jira, Linear, Slack integration

## Lessons for Other Projects

1. **Run manually first.** The first 3-4 pipeline runs will surface project-specific issues. Each one becomes a skill improvement.

2. **Context layers are critical.** Without them, agents write code that doesn't fit the project. Create a concise architecture reference (~200 lines) before dispatching any implementation agents.

3. **The decompose step is the quality gate.** Good decomposition → good implementation. Bad decomposition → agents fail, merge conflicts, re-work.

4. **Skills accumulate value.** Each gotcha added from a real failure prevents the same failure in every future run. After 5-10 runs, the skills are tuned for your project.

5. **The agent WILL take shortcuts.** If it CAN merge directly with git, it will. Enforcement must be in the prompt, not just the docs.
