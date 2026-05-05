# Subsystem: review

What the review subsystem actually does today. For architectural intent see `docs/cobuild.md`. For in-flight changes see CLAUDE.md "Current State".

## Entry points

- `cobuild process-review <task-id>` — `internal/cmd/review.go:47-64`. Manual or poller-invoked.
- Poller calls process-review when a task's status is `needs-review` (`internal/cmd/poller.go` ~line 465).
- Dispatched review agents run skills `skills/review/gate-process-review.md` + `skills/review/dispatch-review.md`, then write `.cobuild/gate-verdict.json`.

## Skills

- `skills/review/gate-review-pr.md` — instructions for reviewing a PR against task spec + parent design.
- `skills/review/gate-process-review.md` — CI checking modes (ignore | all-pass | pr-only) and PR merge mechanics.
- `skills/review/dispatch-review.md` — verdict file shape and fix-phase re-dispatch flow.
- `skills/review/merge-and-verify.md` — post-merge test runner.

## What runs end-to-end

1. Resolve PR URL from task metadata → branch lookup → task ID as branch fallback (`review.go:94-121`).
2. If PR already MERGED on GitHub, reconcile local state and exit clean (`review.go:137-149`).
3. Pick reviewer via `ResolveReviewer` (`internal/review/cross_model.go:47-87`). With cross-model on, a Claude-written PR gets reviewed by OpenAI and vice versa. Unrecognised provider → external (waits for Gemini PR comments).
4. Fetch PR diff via `gh pr diff`.
5. Build review prompt: task spec + parent design context + diff (`review.go:180-237`).
6. Call reviewer (Claude Messages API or OpenAI), parse JSON verdict, normalise to `approve` | `request-changes`. Findings tagged `critical | suggestion | nit`.
7. CI check honoured per mode in `gate-process-review.md:40-80`.
8. Record gate via `RecordGateVerdict` (`internal/cmd/gatelogic.go:32-166`): creates a review work item linked `child-of` design, writes row to `pipeline_gates` with `findings_hash`.
9. On approve: merge PR, run post-merge tests, clean worktree, close task.
10. On request-changes: revert task to `in_progress`, append feedback, re-dispatch agent to fix phase.

## What review does NOT do

- **No structured claim/diff comparison at platform level.** The LLM may notice "commit message claims X but diff lacks X" (it did this five times on pf-9c18b2) but there is no Go code that extracts claims from the spec/commit message and verifies against the diff. The detection is emergent LLM behaviour, not platform behaviour.
- **No cross-repo reasoning.** Review scope is the single PR's diff. It cannot file or reference work in sibling repos.
- **No diff-content hashing.** Findings hash is computed from review verdict text, not the PR diff.

## Output / artefacts

- `pipeline_gates` row: gate name, phase, round, verdict, body, `findings_hash`, review shard ID.
- Connector review work item: type `review`, edge `child-of` design.
- On pass: PR merged via `gh pr merge`, worktree removed.
- On fail: task feedback appended; agent re-dispatched.

## Models / providers

Configured per project (`pipeline.yaml`: `review.provider`). `auto` selects opposite-family of writer when cross-model enabled. Defaults: Claude → `sonnet`, OpenAI → `gpt-5.4`, external → `gemini-code-assist[bot]`.

## Direct-mode tasks (no PR)

`review.go:113-116`: tasks without PR URL bypass review (recorded as pass, no merge). Used for non-code work items.
