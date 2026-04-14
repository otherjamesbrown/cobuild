# Project Health Review

Date: 2026-04-14

Tracked in `cb-75a471`.

Follow-up shards created from this review:
- `cb-88707a` Refactor CLI bootstrap to reduce global state and centralize pipeline start-phase logic
- `cb-999bec` Make Postgres and tmux integration tests hermetic for default contributor runs
- `cb-d75bd5` Document package boundaries and add stronger coverage for real runtime, merge, and client paths

## Summary

CoBuild has a strong product shape and a mostly clean core architecture. The connector, store, orchestrator, runtime, and review layers are recognisable subsystems rather than an undifferentiated code blob, and the repo is genuinely more configuration-driven than most internal automation tools. The weak point is the CLI integration layer. Too much orchestration logic, compatibility logic, and process state still collects in `internal/cmd`, which makes the codebase more fragile to change than the high test count first suggests.

The testing story is similar. There is meaningful breadth here, including orchestration tests and tagged end-to-end scenarios, but the default local run is not hermetic enough. In this environment, `go test ./...` failed because parts of the suite expect live tmux and Postgres or fall back to developer-specific Context Palace config.

## What Is Working Well

The core boundaries are directionally good. The connector abstraction in [internal/connector/factory.go](/Users/james/github/otherjamesbrown/cobuild/internal/connector/factory.go:9), the store interfaces in [internal/store/store.go](/Users/james/github/otherjamesbrown/cobuild/internal/store/store.go:1), the orchestrator runner in [internal/orchestrator/runner.go](/Users/james/github/otherjamesbrown/cobuild/internal/orchestrator/runner.go:31), the pipeline state resolver in [internal/pipeline/state/state.go](/Users/james/github/otherjamesbrown/cobuild/internal/pipeline/state/state.go:99), and the review provider resolution in [internal/review/review.go](/Users/james/github/otherjamesbrown/cobuild/internal/review/review.go:36) all show deliberate seams. That gives the project a reusable base to build on.

The docs are also better than average at explaining the product model. [README.md](/Users/james/github/otherjamesbrown/cobuild/README.md:3), [docs/cobuild.md](/Users/james/github/otherjamesbrown/cobuild/docs/cobuild.md:1), [docs/guides/config.md](/Users/james/github/otherjamesbrown/cobuild/docs/guides/config.md:1), and [docs/guides/skills.md](/Users/james/github/otherjamesbrown/cobuild/docs/guides/skills.md:1) make the concepts understandable quickly.

## Main Findings

- High: `internal/cmd` is carrying too much state and too much policy. Package-level globals in [internal/cmd/root.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/root.go:18) initialise `projectName`, `projectPrefix`, `conn`, `cbStore`, and `cbClient`, and those are then consumed implicitly across many commands. That weakens boundaries and is one reason the command tests have to keep resetting shared state.

- High: start-phase and workflow inference are duplicated in multiple entry points. Similar logic appears in [internal/cmd/pipeline.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/pipeline.go:55), [internal/cmd/run.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/run.go:45), [internal/cmd/orchestrate.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/orchestrate.go:91), [internal/cmd/poller.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/poller.go:145), and [internal/cmd/dispatch.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/dispatch.go:210). `dispatch.go` also keeps its own workflow fallback logic in [inferWorkflowFromType](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/dispatch.go:1339). This is the clearest structural risk in the repo because future workflow changes can drift across paths.

- Medium: the project is config-driven in intent, but not fully config-defined in practice. Important baseline behavior still lives in code in [internal/config/config.go](/Users/james/github/otherjamesbrown/cobuild/internal/config/config.go:251), [internal/config/config.go](/Users/james/github/otherjamesbrown/cobuild/internal/config/config.go:405), [internal/connector/factory.go](/Users/james/github/otherjamesbrown/cobuild/internal/connector/factory.go:11), and [internal/client/client.go](/Users/james/github/otherjamesbrown/cobuild/internal/client/client.go:76). That is acceptable for defaults, but it means changing some core behaviors still requires shipping Go changes rather than only editing YAML.

- Medium: legacy compatibility is still woven through the main path instead of being isolated behind a thinner adapter. `root.go` still wires both the newer connector/store path and the older `internal/client` path in [internal/cmd/root.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/root.go:130), and `pipeline.go` retains broad fallback behavior for old metadata-backed pipelines in [internal/cmd/pipeline.go](/Users/james/github/otherjamesbrown/cobuild/internal/cmd/pipeline.go:120). This is not dead code, but it does raise the cognitive load of almost every pipeline command.

- Medium: the repo is logically structured overall, but the command layer is large enough to be a maintenance hotspot. The top files in `internal/` include `internal/cmd/pipeline.go` at 1484 lines, `internal/cmd/review.go` at 1407, and `internal/cmd/dispatch.go` at 1399. The problem is less “spaghetti everywhere” and more “too much of the important logic lives in a single integration package.”

- Low: the docs explain the concepts well, but they do not yet explain the code map well enough for contributors. There is no short architecture guide that says, in one place, how `internal/cmd`, `internal/orchestrator`, `internal/pipeline/state`, `internal/review`, `internal/store`, and `internal/connector` fit together, or how to run the integration suite safely.

## Testing Readout

The repository has real test breadth. I counted 48 `_test.go` files and 189 `Test...` functions. There is good coverage around command behavior, configuration resolution, orchestrator logic, runtime stubs, and pipeline state handling.

The main weakness is trust in the default contributor workflow. The tagged end-to-end tests under [internal/e2e/](/Users/james/github/otherjamesbrown/cobuild/internal/e2e) are meaningful, but the core harness defaults to a stub runtime and fake connector in [internal/e2e/harness/harness.go](/Users/james/github/otherjamesbrown/cobuild/internal/e2e/harness/harness.go:54), so the suite proves the pipeline state machine more strongly than it proves the real runtime boundary. The happy-path e2e itself is also behind a build tag in [internal/e2e/happy_path_test.go](/Users/james/github/otherjamesbrown/cobuild/internal/e2e/happy_path_test.go:1), which means it is not part of the ordinary `go test ./...` loop.

The integration layer is also not hermetic enough. Postgres-backed helpers fall back to user config in [internal/testutil/pgtest/pgtest.go](/Users/james/github/otherjamesbrown/cobuild/internal/testutil/pgtest/pgtest.go:104), and the state integration test requires tmux in [internal/pipeline/state/state_integration_test.go](/Users/james/github/otherjamesbrown/cobuild/internal/pipeline/state/state_integration_test.go:19). In this environment, `go test ./...` failed in `internal/cmd`, `internal/e2e/harness`, and `internal/pipeline/state` because DNS and external infra expectations were not satisfiable.

There are also a few obvious direct-coverage gaps. I could not find dedicated `_test.go` coverage under `internal/merge/` or `internal/client/`, even though those packages handle side effects and configuration that are important to production behavior.

## CLAUDE.md Review

`CLAUDE.md` is already strong on operational discipline. The sections on investigating failures before retrying, cleaning state before runs, and creating shards for bugs in [CLAUDE.md](/Users/james/github/otherjamesbrown/cobuild/CLAUDE.md:37), [CLAUDE.md](/Users/james/github/otherjamesbrown/cobuild/CLAUDE.md:61), and [CLAUDE.md](/Users/james/github/otherjamesbrown/cobuild/CLAUDE.md:69) directly address real failure modes in this repo. Those instructions probably do reduce wasted retries and state corruption in day-to-day use.

That said, the file currently helps more with runtime behaviour than with design behaviour. It tells an agent how to operate, but not strongly enough how to avoid making the codebase worse while changing it. The architecture and testing issues from this review would be helped by tightening `CLAUDE.md`, but only partially.

The most useful additions would be:

- A rule that `internal/cmd` is a composition boundary, not the default home for new orchestration policy. New phase inference, workflow routing, bootstrap, or store-vs-legacy decision logic should be extracted into reusable packages before command handlers grow further.

- A rule that start-phase and workflow inference must have one canonical implementation. If an agent changes phase routing, it must audit and update every entry point that initialises or resumes pipelines rather than patching only the path it touched.

- A testing rule that distinguishes fast hermetic checks from environment-dependent checks. The current [CLAUDE.md](/Users/james/github/otherjamesbrown/cobuild/CLAUDE.md:209) build section says `go test ./...`, but it should also explain that Postgres, tmux, and runtime-smoke coverage are separate layers, and that agents should prefer hermetic coverage first and state clearly when a non-hermetic layer was not run.

- A rule that fake or stub runtime coverage is not enough for changes that touch dispatch/runtime boundaries, merge execution, or client/config loading. Those changes should require either a real-runtime smoke path or an explicit note that only simulated coverage was exercised.

- A stronger migration rule for legacy code. `CLAUDE.md` already notes that `internal/client/` is legacy in the architecture map at [CLAUDE.md](/Users/james/github/otherjamesbrown/cobuild/CLAUDE.md:217), but it should also instruct agents not to spread new behavior across both legacy and new paths unless the migration explicitly requires it.

If tightened that way, `CLAUDE.md` would help reduce recurrence of the exact problems in this review: command-layer sprawl, drift across pipeline entry points, and overconfidence from partial test coverage. It would not eliminate the need for the code refactors themselves, but it would make future agent changes less likely to deepen the same problems.

## Best-Practice Verdict

This is not a messy codebase. It has a credible architecture, clear product concepts, and a better-than-average test investment. The main best-practice gap is concentration: too much coordination logic and too much compatibility logic is concentrated in the CLI layer. If that is reduced, the architecture will feel as clean in the code as it already sounds in the docs.

The next step I recommend is `cb-88707a`. That one unlocks the rest, because centralising pipeline bootstrap and phase inference will make later cleanup and testing work materially safer.
