# CoBuild Backlog

Work items tracked in Context Palace under project `cobuild` (prefix: `cb-`).

Outcome: `cb-a3bf71` — CoBuild v0.1 standalone pipeline CLI

## Priority 1: Make it standalone

| ID | Type | Title |
|----|------|-------|
| ~~cb-379d4f~~ | ~~task~~ | ~~Remove cxp CLI dependency — native shard operations~~ **DONE** |
| ~~cb-70f1b4~~ | ~~task~~ | ~~Rename .cxp/ to .cobuild/ everywhere~~ **DONE** |
| cb-98bc92 | task | SQLite backend for single-user mode |

## Priority 2: Production reliability

| ID | Type | Title |
|----|------|-------|
| cb-9bf8f2 | task | Deploy agent: sub-agent with smoke test + rollback |
| cb-79f856 | task | Post-deploy integration test runner |
| cb-bdd60c | task | Automate external review processing in poller |

## Priority 3: Self-improvement

| ID | Type | Title |
|----|------|-------|
| cb-3ac5b9 | task | Documentation agent: auto-update docs as done-phase gate |

## Priority 4: Distribution

| ID | Type | Title |
|----|------|-------|
| cb-3c0c3c | task | CI/CD pipeline for cobuild repo |
| cb-e36a81 | task | Example repo with full CoBuild pipeline config |

## Priority 5: Future designs

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
