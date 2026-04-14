# Consolidated Review — 2026-04-14

Merges findings from two parallel reviews:
- **CR** = `docs/reviews/2026-04-14-code-review.md` (3 specialist agents, snapshot `6c9a003`)
- **PH** = `docs/reviews/project-health-review-2026-04-14.md` (single reviewer, narrative style, filed shards cb-88707a / cb-999bec / cb-d75bd5)

Both reviews agree on the shape of the problem. This doc deduplicates, ranks, and ties each finding to an existing shard (if any) so we can decide what to address first.

---

## Where both reviewers agree

1. **Top-level architecture is sound.** Connector / Store / Orchestrator / Runtime / Review boundaries are real and deliberate. Ontology-to-code mapping is clean. Docs explain the product well.
2. **The weak layer is `internal/cmd`.** Too much state, too much policy, and the biggest files in the repo. CR calls out 1484/1407/1399-line files and 592- and 800-line `RunE` handlers; PH calls it the "concentration" problem.
3. **Migration from `internal/client` to `internal/store` + `internal/connector` is half-done.** Two parallel data paths coexist with silent fallback. Every agent that touches a pipeline command picks between them.
4. **Critical packages have no unit tests.** `internal/merge`, `internal/worktree`, real connector implementations (`cp.go`, `beads.go`), and remaining `internal/client` paths are only covered by e2e (or not at all).
5. **Tests are not hermetic by default.** Running `go test ./...` fails without `dev02.brown.chat` postgres or tmux available.
6. **CLAUDE.md is strong on operational discipline but weak on design discipline.** Both reviews propose additions; neither thinks CLAUDE.md changes alone will fix the code drift.

---

## Findings ranked by risk

Each row: finding · source(s) · where · existing shard (if any).

### P0 — correctness / audit risk

| # | Finding | Source | Where | Shard |
|---|---|---|---|---|
| 1 | `internal/merge` has zero unit tests. cb-7dd0d4 (squash merge + dependent branches) cannot be reproduced in a unit test. | CR, PH | `internal/merge/*.go` (675 lines) | cb-7dd0d4 (bug), cb-d75bd5 (coverage) |
| 2 | `root.go` swallows config + connector init errors; falls through to `cbClient` silently. | CR | `internal/cmd/root.go:100, 132` | none — needs filing |
| 3 | 100+ `_ = err` in audit-critical paths (`EndSession`, `SetMetadata`, `AppendContent`, `GetGateHistory`). "Audit everything" principle violated. | CR | `internal/cmd/dispatch.go:87,453`, `review.go:578–595`, `pipeline.go:182–183` | none — needs filing |
| 4 | `client → store+connector` migration half-done. 15+ unused exported functions in `internal/client/pipeline.go`, silent fallback at `pipeline.go:324–342`, hardcoded CP defaults (`dev02.brown.chat`). | CR, PH | `internal/client/*.go`, consumers in `internal/cmd/` | cb-3f5be6, cb-b2f3ac |

### P1 — structural risk / invisible drift

| # | Finding | Source | Where | Shard |
|---|---|---|---|---|
| 5 | Start-phase / workflow inference duplicated across 5+ entry points. Future workflow changes WILL drift. | PH, CR | `pipeline.go:55`, `run.go:45`, `orchestrate.go:91`, `poller.go:145`, `dispatch.go:210, 1339` | cb-88707a |
| 6 | Monolithic `RunE` handlers. Not meaningfully testable in their current shape. | CR, PH | `dispatchCmd.RunE` 592 lines, `processReviewCmd.RunE` 800+ lines, `pipeline.go` 1484 lines | partially cb-88707a — needs explicit scope |
| 7 | Package-level globals (`projectName`, `projectPrefix`, `conn`, `cbStore`, `cbClient`) consumed implicitly across commands. Forces shared-state resets in tests. | PH | `internal/cmd/root.go:18` | cb-88707a |
| 8 | Real connectors (`cp.go`, `beads.go`) only exercised via e2e FakeConnector. Malformed JSON, CLI-output shape changes, auth failures surface only in production. | CR, PH | `internal/connector/cp.go`, `beads.go` | cb-d75bd5 |
| 9 | `internal/worktree` untested. Stale cleanup, concurrent-create race, partial-cleanup recovery are exactly the failure modes that strand branches. | CR | `internal/worktree/worktree.go` (227 lines) | cb-d75bd5 |

### P2 — hidden contracts & config drift

| # | Finding | Source | Where | Shard |
|---|---|---|---|---|
| 10 | Tests not hermetic. `internal/testutil/pgtest/pgtest.go:104` falls back to user config; `state_integration_test.go:19` requires tmux. | PH, CR | see file refs | cb-999bec |
| 11 | Hardcoded phase ordering fallback violates config-over-code principle. | CR | `internal/cmd/phase_transition.go:122–141` | none — "delete the switch" task |
| 12 | Hardcoded workflow type→phases fallback in `inferWorkflowFromType`. | CR, PH | `internal/cmd/dispatch.go:1339–1370` | none — fold into cb-88707a or new |
| 13 | No referential-integrity validation in `config.LoadConfig`. Invalid skill paths / phase names surface at dispatch time, not load time. | CR | `internal/config/config.go` | none — needs filing |
| 14 | `.cxp/` legacy fallback is live, not retired. CLAUDE.md marks it DONE — false. | CR | `internal/client/client.go:71–73`, `internal/cmd/setup.go:140` | none — needs filing |
| 15 | No CI coverage floor. Gaps can grow silently. | CR | `.github/workflows/ci.yml` | none — needs filing |
| 16 | `runtime.Register` panics on bad input at `init()`. A misconfigured runtime takes the whole binary down. | CR | `internal/runtime/runtime.go:134–143` | none — needs filing |

### P3 — hygiene / readability

| # | Finding | Source | Where |
|---|---|---|---|
| 17 | Three near-identical `exec.CommandContext` wrappers (`pipelineCommandOutput`, `dispatchCommandOutput`, `reviewCommandOutput`). | CR | `dispatch.go:26–43`, `review.go:27–43`, `pipeline.go:26–31` |
| 18 | Brittle parsing of `gh`/`git` CLI output. `review.go:108–126` silently treats unexpected `gh pr view --json state` output as "not merged". | CR | `internal/cmd/review.go:108–126` |
| 19 | Inconsistent error wrapping — `%v` / plain strings in older code, `%w` in newer. Not `errorlint`-clean. | CR | `internal/client/runs.go:119`, `internal/client/pipeline.go:163` |
| 20 | ~777 `fmt.Printf/Println` calls in `internal/cmd/`; no `slog` or levels. Test assertions catch stdout in places. | CR | `internal/cmd/` |
| 21 | Magic phase/gate/metadata strings scattered (`"design"`, `"review"`, `"pr_url"`, `"worktree_path"`, etc.). No constants file. | CR | `internal/cmd/*` |
| 22 | Missing package docs on newer packages. | CR | `internal/pipeline/state`, `livestate`, `runtime`, `orchestrator` |
| 23 | Test stubs `panic("not implemented")` instead of returning errors — unsafe if a stub leaks. | CR | `internal/review/review_test.go:27–63`, `wave_progression_test.go:15–105` |
| 24 | Happy-path e2e has a 2-minute timeout that could be tight on slow CI. | CR | `happy_path_test.go:55` |

---

## Shards audit

### Already filed (covers consolidated findings)

| Shard | Title | Covers rows |
|---|---|---|
| cb-3f5be6 | Migrate pipeline commands from legacy client to connector + store | 4 |
| cb-b2f3ac | Complete legacy client removal — pipeline state, poller, and insights | 4 |
| cb-7dd0d4 | Merge strategy for dependent branches | 1 (bug side; not the test gap) |
| cb-88707a | Refactor CLI bootstrap to reduce global state and centralize pipeline start-phase logic | 5, 6 (partial), 7, 12 (partial) |
| cb-999bec | Make Postgres and tmux integration tests hermetic for default contributor runs | 10 |
| cb-d75bd5 | Document package boundaries and add stronger coverage for real runtime, merge, and client paths | 1 (test side), 8, 9 |

### Not yet tracked — need new shards

- **Finding 2** — `root.go` swallows init errors. Surfacing or failing fast.
- **Finding 3** — Audit of every `_ = err` in audit-critical paths. Either log or return.
- **Finding 6 (explicit)** — Split `dispatchCmd.RunE` / `processReviewCmd.RunE` into named, testable functions. Could be scoped under cb-88707a but deserves its own shard since the refactor is large.
- **Finding 11** — Delete the hardcoded phase ordering switch in `phase_transition.go:122–141`.
- **Finding 13** — Referential-integrity validation in `config.LoadConfig`.
- **Finding 14** — Actually retire the `.cxp/` fallback. Add deprecation warning, remove in 2–3 releases. Correct CLAUDE.md "DONE" status.
- **Finding 15** — Enforce `go test -cover ./...` with a floor (~40%) in CI.
- **Finding 16** — Replace `runtime.Register` panics with returned errors.
- **Hygiene bundle (17–24)** — Could be one "developer ergonomics" shard or split by theme (exec wrapper, logging, constants, package docs).

---

## CLAUDE.md tightening — consolidated recommendations

Both reviews propose similar rules. Merged below.

### New rules (under **Don't** or **Principles**)

1. **Don't silently suppress errors.** `_ = err` is forbidden outside deliberate cleanup, and even there, log. Audit writes (`SetMetadata`, `EndSession`, gate records) must never be discarded. (CR, PH)
2. **Don't grow cobra `RunE` handlers past ~150 lines.** Extract named, testable functions; `RunE` wires them together. `dispatchCmd.RunE` and `processReviewCmd.RunE` are the reference for what not to do. (CR)
3. **Don't merge behaviour changes without tests.** New or modified logic in `internal/merge`, `internal/worktree`, `internal/connector`, `internal/runtime`, or gate/phase transitions needs a unit test in the same PR. "Covered by e2e" is not sufficient. (CR, PH)
4. **Don't stack migrations.** If you touch a half-migrated area, finish retiring the old path or leave it alone. Never introduce a third parallel approach. (CR, PH)
5. **`internal/cmd` is a composition boundary, not the default home for new logic.** New phase inference, workflow routing, bootstrap, or store-vs-legacy decisions belong in reusable packages. (PH)
6. **One canonical implementation of phase/workflow inference.** Any change must audit and update every entry point, not just the one touched. (PH)
7. **Hermetic tests first.** `go test ./...` should pass without live Postgres or tmux. Layer names: hermetic, postgres-backed, tmux-backed, real-runtime. State clearly when a non-hermetic layer was not run. (PH, CR)
8. **No magic phase/gate strings.** Phase names, gate names, workflow names go through constants in `internal/domain/constants.go` (to be created). (CR)

### Stale content to fix in CLAUDE.md

- **"Current State §2: Rename `.cxp/` to `.cobuild/` — DONE"** — false. `client.go:71–73` and `setup.go:140` still resolve `.cxp`. Either retire the fallbacks or mark "partial — deprecation pending". (CR)
- **"Current State §3: Dispatch reliability — DONE"** — mostly true but doesn't acknowledge `root.go:100, 132` silent fallbacks. Add a caveat. (CR)
- **"Current State §1: Make it standalone"** — reads as untouched TODO, but `internal/store` + `internal/connector` largely exist. Reframe as "Finish the `client → store+connector` migration" and link shards. (CR)
- **`docs/BACKLOG.md`** — should carry an explicit entry for finishing the client migration if cb-3f5be6/cb-b2f3ac aren't already surfaced there. (CR)

### What tightening CLAUDE.md won't fix

- Hardcoded phase switches — rule already exists, code drifted anyway.
- Hardcoded DB defaults — principle already covers it.
- Silent `_ = err` — "Fail visible" is already there in spirit.

These need code changes and/or lint rules (`errorlint`, custom `go vet` check for `_ = err`), not doc changes.

---

## Suggested order of operations

Both reviews converge on the same unlock: **fix the CLI bootstrap concentration first, then the client migration, then the test gaps**.

PH's specific recommendation: "The next step I recommend is `cb-88707a`. That one unlocks the rest." — centralising pipeline bootstrap and phase inference makes later cleanup and testing safer.

CR's P0 list reads similarly: migration, root.go errors, merge tests.

Proposed sequence:

1. **cb-88707a** — CLI bootstrap + canonical phase inference. Blocks nothing else, unlocks cleaner split of `RunE` handlers and makes cb-3f5be6 / cb-b2f3ac tractable.
2. **cb-3f5be6 / cb-b2f3ac** — finish the client migration. Removes the silent fallback that makes every other command-layer bug ambiguous.
3. **New shard: merge unit tests** — directly reduces the cb-7dd0d4 risk. Local-git fixtures, scenarios drawn from the known bug.
4. **cb-999bec** — hermetic tests. Removes the "which layer failed" ambiguity that today makes test regressions hard to triage.
5. **New shard: root.go init errors + `_ = err` audit** — cheap, high-audit-value. Can parallelise with the rest.
6. **New shards for P2 items** as capacity allows.
7. **CLAUDE.md tightening** — ship alongside or just after cb-88707a so the new rules describe the state the code is moving into.

---

## Decision points for the user

1. **Do we file new shards for findings 2, 3, 6-explicit, 11, 13, 14, 15, 16, and the hygiene bundle?** Review 2 filed 3 shards already; review 1 didn't file any. I recommend filing the P0/P1 "not yet tracked" ones today (4–5 shards) and deferring the hygiene bundle until we have capacity.
2. **Do we proceed with PH's suggested unlock — start on cb-88707a next?** Or do we want to land the "audit `_ = err`" + "root.go init fail-loud" change first as a cheap pre-cursor that lights up errors the refactor is about to hit?
3. **CLAUDE.md tightening: do it now, or after cb-88707a lands so the rules describe the post-refactor state?**
