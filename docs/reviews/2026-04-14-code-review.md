# CoBuild Code Review — 2026-04-14

Reviewers: three specialised agents (architecture, code quality, testing). Snapshot at commit `6c9a003`. 168 Go files, 48 test files, 14 internal packages.

## Executive Summary

CoBuild is **well-architected at the top level** — the Connector / Store / Config split follows its stated ontology, configuration is mostly YAML-driven, and the e2e harness is genuinely good. Three things are holding it back:

1. **The `internal/client` → `internal/store`+`internal/connector` migration is half-done.** Two parallel data paths coexist, with silent fallback. This is the single biggest source of architectural and code-quality debt.
2. **Critical logic has zero unit tests.** `internal/merge`, `internal/worktree`, and both real connector implementations (`cp.go`, `beads.go`) are only exercised via e2e. The known "squash merge + dependent branches" bug (cb-7dd0d4) cannot be reproduced in a unit test today.
3. **Silent error suppression is pervasive in the command layer.** 100+ `_ = ...` assignments in audit-critical paths, plus a 592-line `dispatchCmd.RunE` and an 800+ line `processReviewCmd.RunE` that are effectively untestable.

Ranked fix list at the bottom.

---

## 1. Architecture

### Strengths
- **Ontology-to-code alignment is good.** Each of the 7 objects in `research/cobuild-ontology.md` maps to concrete types: `connector.WorkItem`, `store.PipelineRun`, `config.PhaseConfig`, `config.GateConfig`, `config.AgentCfg`, `connector.Connector`.
- **Connector abstraction is genuinely extensible** (`internal/connector/connector.go:76–118`). Three implementations exist (CP, Beads, e2e fake); a Jira or Linear connector can be added without touching pipeline code.
- **Config-driven transitions where it counts.** `config.StartPhaseForType()` and `config.NextPhaseInWorkflow()` (`internal/config/config.go:1012–1029`) route phase advancement through YAML.
- **Package-level doc comments** exist on the core packages (`connector.go:1–10`, `store.go:1–11`).

### Concerns

**High — legacy `internal/client` is a shadow backend.**
- ~2200 lines across 5 files; `pipeline.go` alone is 1171 lines.
- Hardcoded Context Palace defaults in `internal/client/client.go:78–100` (`host="dev02.brown.chat"`, `database="contextpalace"`) — contradicts the "standalone" goal.
- Used by 15+ files in `internal/cmd/`. Same operations (e.g. gate recording) exist in both client and store, with a fallback at `internal/cmd/pipeline.go:324–342`: two sources of truth, chosen silently at runtime.

**High — hardcoded phase ordering fallback.**
- `internal/cmd/phase_transition.go:122–141` contains a switch statement over phase names used when config/connector are unavailable. Directly violates "config over code". If a new phase is added in YAML, this fallback silently disagrees with it.

**High — `root.go` swallows initialisation errors.**
- `internal/cmd/root.go:100, 132`: `config.LoadConfig()` and `connector.New()` errors are discarded with `_`. If the DB is unreachable or config is malformed, `cbStore` ends up nil and the code silently falls through to `cbClient`, which may itself be misconfigured. No warning surfaces.

**Medium — `cmd/` has both orchestration and data-access responsibilities.**
Commands call `cbClient` directly (`pipeline.go:327`, `insights.go:28`, `improve.go:50`) alongside `cbStore` (`pipeline.go:325`). There is no orchestration layer between cmd and the backends. The new `internal/cmd/gatelogic.go` is a small step in the right direction but still lives in `cmd/`.

**Medium — workflow inference has a hardcoded fallback.**
`internal/cmd/dispatch.go:1347–1370` (`inferWorkflowFromType`) checks config first but has a hardcoded type→workflow map as backup.

**Medium — config has no referential-integrity validation.**
Invalid skill paths, phase names that don't exist in a workflow, or missing model references surface only at dispatch time, not at `config.LoadConfig()`.

**Low — magic strings for phase names scattered across dispatch/review/pipeline** (`"design"`, `"implement"`, `"review"`, `"done"`, `"readiness-review"`, `"decomposition-review"`). No constants file.

**Low — some newer packages lack package docs** (`internal/pipeline/state`, `internal/pipeline/livestate`, `internal/runtime`, `internal/orchestrator`).

---

## 2. Code Quality

### Strengths
- Consistent interface patterns for Connector / Store / Runtime.
- Newer code (store/postgres.go, orchestrator/runner.go) wraps errors with `%w`.
- Clear directory layout; `testutil/pgtest` is tidy.

### Critical issues

**Dead code in `internal/client/pipeline.go:390–1160`.**
At least 15 exported functions have no non-test callers:
`AddPipelineTask`, `AddShardLabel`, `AppendShardContent`, `CreateWorktree`, `RemoveWorktree`, `FindNewDesigns`, `FindTasksNeedingReview`, `FindSatisfiedWaits`, `FindInProgressTasks`, `UpdateShardStatus`, `UpdatePipelineRunStatus`, `UpdateMetadata`, `ListPipelineTasks`, `ListPipelineRuns`, `GetTask`.

**Monolithic `RunE` functions.**
- `internal/cmd/dispatch.go:48–639` — 592 lines: state resolution, worktree creation, context assembly, tmux spawning, session recording, cleanup defer, and early-death probes all in one function.
- `internal/cmd/review.go:63–~800` — `processReviewCmd.RunE` (file total 1407 lines): verdict reading, merge, kb-sync, and cleanup paths interleaved.
- `internal/cmd/pipeline.go` — 1484 lines; decompose/status/advance spread across oversize handlers.

These can't be unit-tested meaningfully; they need to be broken up into named functions (`resolveDispatchPrerequisites`, `prepareDispatchContext`, `spawnDispatchSession`, …).

**Silent error suppression in audit-critical paths.**
Over 100 `_ = ...` assignments. Worst offenders:
- `internal/cmd/dispatch.go:87` — state lookup error ignored, fallback is implicit.
- `internal/cmd/dispatch.go:453` — `_ = conn.SetMetadata(ctx, taskID, "session_id", session.ID)`.
- `internal/cmd/review.go:578–595` — `EndSession`, `SetMetadata`, `AppendContent` all discarded in error paths (the exact paths where recording the failure matters most).
- `internal/cmd/pipeline.go:182–183` — `gates, _ = cbStore.GetGateHistory()`; the audit trail lookup can fail silently.
- `internal/cmd/dispatch.go:178–183`, `review.go:33–39`, similar in pipeline.go: `pCfg, _ := config.LoadConfig(repoRoot)` followed by `DefaultConfig()` fallback — a bad YAML file produces a silent fallback to defaults.

**Panics in production code.**
- `internal/runtime/runtime.go:134–143` — `Register()` panics on nil runtime, empty name, or duplicate. These run at `init` time, so a misconfigured runtime takes the entire binary down.

### Significant

**Legacy `.cxp/` fallback is not actually retired** despite CLAUDE.md marking it "DONE".
`internal/client/client.go:71–73` (`legacyGlobalConfigDir = ".cxp"`), `internal/cmd/setup.go:140` (tries `.cobuild.yaml`, `.cxp.yaml`, then even `.cp.yaml` — three generations of legacy). No deprecation warning is emitted when the legacy path is used.

**Duplicated shell-out wrappers.**
`pipelineCommandOutput`, `dispatchCommandOutput`, `reviewCommandOutput` are near-identical `exec.CommandContext` wrappers redefined in three files (`dispatch.go:26–43`, `review.go:27–43`, `pipeline.go:26–31`). Should be one helper in `internal/cmd/exec.go`.

**Brittle string parsing of external CLIs.**
`internal/cmd/review.go:108–126` parses `gh pr view --json state --jq .state` via string trim. If the value is unexpected, the code silently treats the PR as not-merged. Should validate against a known set: `{"MERGED","OPEN","CLOSED"}` and error otherwise.

**Inconsistent error wrapping.**
Old client code uses `%v` (`internal/client/runs.go:119`) or plain strings (`internal/client/pipeline.go:163`). Newer code uses `%w`. Enforce with `errorlint`.

**Mixed logging.**
~777 uses of `fmt.Printf/Println` in `internal/cmd/` alongside occasional `log.Printf`; no `slog`. No structured levels; test assertions catch stdout in places.

### Minor

- No central constants for metadata keys (`"pr_url"`, `"worktree_path"`, `"session_id"`, `"dispatch_runtime"` are string-literal everywhere).
- Test stubs use `panic("not implemented")` (`internal/review/review_test.go:27–63`, `internal/cmd/wave_progression_test.go:15–105`). Returning an error would be safer if the stub leaks outside tests.
- Inconsistent wrapper naming (`dispatchCommandOutput` vs `tmuxRun`/`tmuxCombinedOutput`).

---

## 3. Testing

### Current state

| Package | Coverage |
|---|---|
| `internal/pipeline/state` | 80.1 % |
| `internal/orchestrator` | 65.1 % |
| `internal/review` | 54.7 % |
| `internal/config` | 41.0 % |
| `internal/cmd` | 36.2 % |
| `internal/store` | 16.9 % |
| `internal/connector` | **0 %** |
| `internal/merge` | **0 %** |
| `internal/worktree` | **0 %** |
| `internal/client` | **0 %** |
| `internal/runtime` (except `stub/`) | ~0 % |

189 test functions across 48 files; 329 subtests. E2E tests gated with `//go:build e2e`.

**CI:** `.github/workflows/ci.yml` runs `go test ./...` on every push/PR — unit only, no `-cover`, no threshold. `.github/workflows/e2e.yml` runs the e2e suite on PRs with a real postgres and tmux.

### Strengths

- **E2E harness is the best-engineered part of the test suite** (`internal/e2e/harness/{harness,connector,assertions}.go`). Isolated postgres schemas, idempotent setup/teardown, a fully-featured FakeConnector, happy-path dispatch→complete→gate→advance coverage.
- **`internal/testutil/pgtest`** gracefully skips when postgres is absent — no hard fail.
- **Phase state machine is well covered** (~80 %). Idempotency, wave closure, conflict detection all have tests.
- **Review flow tests assert behaviour, not just no-error** (`internal/cmd/review_test.go`): verdicts, metadata keys, edge creation, PR comment posting.

### Gaps (ranked by risk)

1. **CRITICAL — `internal/merge` has zero unit tests** (675 lines across `analyse.go`, `plan.go`, `execute.go`, `supersede.go`). This is exactly the package CLAUDE.md flags as having a known bug (cb-7dd0d4). `AnalyseBranches`, `BuildMergePlan`, `ExecuteMergePlan`, `DetectSupersession` are all exported and untested.
2. **HIGH — real connector implementations are untested** (`internal/connector/cp.go`, `beads.go`). Only the e2e FakeConnector is exercised. JSON-parsing edge cases, CLI-output shape changes, auth failures only surface in production.
3. **HIGH — worktree lifecycle untested** (`internal/worktree/worktree.go`, 227 lines). `Create`/`Verify`/`Cleanup` shell out to `git`; stale-state cleanup (`worktree.go:42–43`) is exactly the kind of failure that strands branches and blocks waves.
4. **MEDIUM — legacy `internal/client`** (2206 lines) has no tests at all. Low priority because it's slated for removal, but any lingering caller is unverified.
5. **MEDIUM — gate enforcement** in `internal/cmd/complete.go` only has integration-level coverage; no isolated unit test of `evaluateGate`.
6. **MEDIUM — config context assembly** (`internal/config/context.go`). 41 % package coverage; the CLAUDE.md context layering is the piece most likely to silently misbehave in a dispatched session.
7. **MEDIUM — runtimes (`claudecode/`, `codex/`)** are only tested at the script-generation level. `codex_e2e_smoke_test.go` is env-var-gated and doesn't run in CI.

### Quality and determinism

- Many `if err != nil { t.Fatal(err) }` followed by nothing — flagging presence, not correctness. Example: parts of `internal/cmd/dispatch_test.go:20–50` only check that a decompose prompt contains keywords, never the decomposition logic.
- `internal/store/store_test.go:33,35` uses `time.Sleep(5ms)` as synchronisation — benign but a marker.
- Very few `t.Parallel()` calls; concurrency bugs aren't exercised.
- E2E happy-path test has a 2-minute timeout (`happy_path_test.go:55`) — could be tight on slow CI runners.

---

## Prioritised Fix List

**P0 — data integrity / known-bug risk**

1. **Add `internal/merge` unit tests** with local-git fixtures. Cover single-wave clean merge, multi-wave file overlaps, supersession, and the specific dependent-branch scenario in cb-7dd0d4.
2. **Finish the `client → store+connector` migration.** Delete the 15 unused `internal/client/pipeline.go` functions; replace remaining callers (`insights.go`, `improve.go`, pipeline.go:327) with store/connector equivalents; drop the silent fallback at `pipeline.go:324–342`.
3. **Stop swallowing errors in `root.go`** (`internal/cmd/root.go:100, 132`). If config or connector init fails, log and refuse to proceed rather than silently falling through to `cbClient`.

**P1 — auditability / reliability**

4. **Audit every `_ = ...` in audit-critical paths** (`dispatch.go:87, 453`; `review.go:578–595`; `pipeline.go:182–183`). Each one either returns an error or logs a warning. Silent suppression of audit writes is not acceptable given the "audit everything" principle.
5. **Split `dispatchCmd.RunE` and `processReviewCmd.RunE`** into named, testable functions. Even before new tests are added, this makes existing logic legible.
6. **Add unit tests for `internal/connector/cp.go` and `beads.go`** with a stubbed `exec.Cmd` — malformed JSON, missing fields, non-zero exit codes, auth errors.
7. **Add unit tests for `internal/worktree`** — stale cleanup, concurrent-create race, partial-cleanup recovery.

**P2 — hygiene**

8. **Replace `runtime.Register` panics** with returned errors; fail with a clear message from `init` helpers rather than a panic stack.
9. **Validate config at load time** — unknown skill path, phase not in workflow, unknown model → error from `config.LoadConfig`.
10. **Promote phase ordering to config-only** — delete the hardcoded switch at `phase_transition.go:122–141` now that workflows live in YAML.
11. **Centralise metadata key constants** and magic phase strings into `internal/domain/constants.go` (or similar).
12. **Retire the `.cxp/` fallback.** Emit a deprecation warning on first use; remove in 2–3 releases.
13. **Enforce coverage in CI** (`go test -cover ./...` with a floor around 40 %) so these new gaps don't grow.
14. **Consolidate the three `*CommandOutput` wrappers** into `internal/cmd/exec.go`.
15. **Standardise logging** — introduce a small `Logger` interface (or adopt `slog`) and migrate `fmt.Printf` in non-user-facing paths.

---

## Appendix A — Files Worth Reading First

- `internal/client/pipeline.go` — biggest cleanup target.
- `internal/cmd/dispatch.go` and `internal/cmd/review.go` — biggest testability target.
- `internal/merge/*.go` — biggest correctness risk.
- `internal/connector/cp.go`, `beads.go` — biggest untested surface area.
- `internal/cmd/root.go` and `internal/cmd/phase_transition.go` — worst silent-fallback offenders.

---

## Appendix B — Would Tightening CLAUDE.md Help?

Partially. Several findings are pure code drift — the rule already exists in CLAUDE.md and was ignored. Tightening the doc won't fix those; only a code change or a review checkpoint will. But a handful of findings map to genuine gaps in CLAUDE.md, and CLAUDE.md itself contains stale content that is actively misleading future orchestrator sessions.

### Findings mapped to CLAUDE.md

| Finding | Rule in CLAUDE.md today | Tightening helps? |
|---|---|---|
| Hardcoded phase switch (`phase_transition.go:122–141`) | "Don't hardcode phase names or gate logic" | **No** — rule exists, code fix needed. |
| Hardcoded CP DB defaults (`dev02.brown.chat`) | "no features that only work for one repo" | **No** — rule exists, code drift. |
| Silent `_ = err` in audit paths | "Fail visible" (general principle) | **Yes** — needs a concrete rule. |
| 592-line `dispatchCmd.RunE` / 800+ line `processReviewCmd.RunE` | Nothing | **Yes** — add a size ceiling for cobra `RunE`. |
| Zero tests in `internal/merge`, `worktree`, real connectors | Nothing | **Yes** — add "tests ship with the fix". |
| Legacy `.cxp/` fallback still live | Marked "DONE" (incorrect) | **Yes** — fix the false status. |
| Magic phase-name strings | Partially covered | **Yes** — point at a constants file. |
| Half-done client → store migration | Called out as "legacy… being migrated" | **Yes** — add a "finish migrations, don't stack them" rule. |
| No CI coverage floor | Nothing | **Yes** — state the expectation. |
| Mixed logging, duplicated shell-out wrappers | Nothing | Marginal — code review catches these. |

### Proposed additions to CLAUDE.md

Under **Don't** near the end:

- **Don't silently suppress errors.** `_ = err` is forbidden outside deliberate cleanup, and even there, log. Audit writes (`SetMetadata`, `EndSession`, gate records) must never be discarded — the "audit everything" principle requires that failure to audit is itself audited.
- **Don't grow cobra `RunE` handlers past ~150 lines.** Extract named, testable functions; keep `RunE` an orchestrator that wires them together. `dispatchCmd.RunE` (592 lines) and `processReviewCmd.RunE` (800+ lines) are the reference for what not to do.
- **Don't merge behaviour changes without tests.** New or modified logic in `internal/merge`, `internal/worktree`, `internal/connector`, or gate/phase transitions needs a unit test in the same PR. "Covered by e2e" is not sufficient for these packages.
- **Don't stack migrations.** If you touch a half-migrated area (`internal/client` → `internal/store`+`internal/connector`, or `.cxp/` → `.cobuild/`), finish retiring the old path or leave it alone. Never introduce a third parallel approach.

Under **Principles**:

- **No magic phase/gate strings.** Phase names, gate names, and workflow names go through constants in `internal/domain/constants.go` (needs to be created). String literals in Go code are a signal the config-over-code principle has leaked.

### Stale content in CLAUDE.md that should be corrected

- **"Current State §2: Rename `.cxp/` to `.cobuild/` — DONE"** is false. `internal/client/client.go:71–73` still resolves `legacyGlobalConfigDir = ".cxp"`; `internal/cmd/setup.go:140` still reads `.cobuild.yaml`, `.cxp.yaml`, and `.cp.yaml`. Either retire the fallbacks or change the status to "partial — deprecation pending".
- **"Current State §3: Dispatch reliability — DONE"** is mostly true but doesn't acknowledge that silent fallbacks in `internal/cmd/root.go:100, 132` (config and connector init errors discarded) undermine the "agent exits cleanly" goal. Add a caveat or open a follow-up.
- **"Current State §1: Make it standalone"** reads as untouched TODO, but `internal/store` + `internal/connector` largely exist. The real blocker is now *finishing* the `internal/client` migration — reframe §1 as "Finish the client → store+connector migration" and cross-link the relevant shard.
- **`docs/BACKLOG.md`** should carry an explicit entry for finishing the client migration if cb-3f5be6 doesn't already cover it.

### Why some findings are not CLAUDE.md-addressable

CLAUDE.md already forbids hardcoded phase logic, single-repo features, and silent failures at the principle level. The fact that `phase_transition.go:122–141`, hardcoded database defaults, and 100+ `_ = err` assignments exist anyway means the doc is not the leverage point for those — code review and lint rules are. Adding more principles on top of unenforced principles makes the doc longer without changing behaviour. Prefer lint (`errorlint`, a custom `go vet` check for `_ = err`) and CI gates where possible.
