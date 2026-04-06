# CoBuild Backlog

Work items tracked in Context Palace under project `cobuild` (prefix: `cb-`).

Outcome: `cb-a3bf71` — CoBuild v0.1 standalone pipeline CLI

## Priority 1: Make it standalone

| ID | Type | Title |
|----|------|-------|
| ~~cb-379d4f~~ | ~~task~~ | ~~Remove cxp CLI dependency — native shard operations~~ **DONE** |
| ~~cb-70f1b4~~ | ~~task~~ | ~~Rename .cxp/ to .cobuild/ everywhere~~ **DONE** |
| cb-98bc92 | task | SQLite backend for single-user mode |

## ~~Priority 2: Dispatch reliability~~ **DONE** (cb-7aa91d)

Design: cb-7aa91d — "Dispatch reliability: Stop hook, fix-phase for bugs, auto pipeline runs"
Dogfooded on penfold — 11 tasks, 10 PRs merged. Verified end-to-end on 3 previously-stalled bugs.

| ID | Type | Title |
|----|------|-------|
| ~~cb-75b0bf~~ | ~~task~~ | ~~Stop hook for reliable dispatch completion~~ **DONE** |
| ~~cb-332869~~ | ~~task~~ | ~~Auto-create pipeline run on direct dispatch~~ **DONE** |
| ~~cb-611655~~ | ~~task~~ | ~~Pre-accept Claude Code workspace trust for worktrees~~ **DONE** |
| ~~cb-12dd55~~ | ~~task~~ | ~~Add fix phase and fix-bug skill~~ **DONE** |
| ~~cb-b6a8e2~~ | ~~task~~ | ~~Route bugs to fix phase, escalate on needs-investigation~~ **DONE** |
| ~~cb-c34085~~ | ~~task~~ | ~~Detect existing investigation content, skip read-only prompt~~ **DONE** |
| ~~cb-ec5858~~ | ~~task~~ | ~~Deny .claude/** edits in worktrees~~ **DONE** |
| ~~cb-0b09d0~~ | ~~task~~ | ~~Fix .cobuild/ dispatch artifacts leaking into commits~~ **DONE** |
| ~~cb-3be0b5~~ | ~~task~~ | ~~Update README, examples, bootstrap for new bug workflow~~ **DONE** |
| ~~cb-c6a083~~ | ~~task~~ | ~~needs-investigation discoverability and self-escalation~~ **DONE** |
| ~~cb-a8d5f0~~ | ~~task~~ | ~~E2E verify dispatch reliability with 3 stalled bugs~~ **DONE** |

Token reduction impact (measured on penfold):
- Context injection: ~2900 lines → ~5 lines per session (context moves to dispatch-context.md)
- Stalled dispatches: eliminated (Stop hook; previously ~$5-15 wasted per stall)
- Bug dispatch overhead: halved (single fix session vs separate investigate + implement)

## Priority 3: Production reliability

| ID | Type | Title |
|----|------|-------|
| cb-9bf8f2 | task | Deploy agent: sub-agent with smoke test + rollback |
| cb-79f856 | task | Post-deploy integration test runner |
| cb-bdd60c | task | Automate external review processing in poller |

## Priority 4: Self-improvement

| ID | Type | Title |
|----|------|-------|
| cb-3ac5b9 | task | Documentation agent: auto-update docs as done-phase gate |

## Priority 5: Distribution

| ID | Type | Title |
|----|------|-------|
| cb-3c0c3c | task | CI/CD pipeline for cobuild repo |
| cb-e36a81 | task | Example repo with full CoBuild pipeline config |

## Priority 6: Future designs

| ID | Type | Title |
|----|------|-------|
| cb-06ff6a | design | MCP connectors for external systems (Jira, Linear, Slack) |
| cb-46522a | design | CoWork-style plugin model for skills and integrations |

## Related shards (in penfold project)

| ID | Type | Title | Relevance |
|----|------|-------|-----------|
| pf-ca054d | outcome | futures - CoWork Style | Plugin model research |
| pf-2b22a3 | knowledge | Cross-design review: 6 penfold pipeline designs | Lessons from first run |
| pf-d2e770 | knowledge | Documentation audit: project md files | Docs status |
