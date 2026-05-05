# Subsystem: gates

What a gate is, when one fires, where the verdict is recorded. For escalation behaviour after a fail see `escalation.md`. For review-specific gate logic see `review.md`.

## Concept

A gate is a structured checkpoint between phases. The dispatched agent's skill produces `.cobuild/gate-verdict.json`. The orchestrator reads it, records the gate, and either advances the phase (pass) or holds + maybe escalates (fail).

## Entry points

- Agent writes verdict file at end of skill (`dispatch-review.md`, `gate-review-pr.md`, etc.).
- `cobuild process-review <task-id>` reads the file and records the gate.
- Poller invokes `process-review` when `task.status == "needs-review"`.
- Phase-specific gate skills: decomposition (decompose phase), readiness (design phase), investigation (investigate phase), retrospective (done phase).

## Verdict file shape

`.cobuild/gate-verdict.json`:
```json
{ "gate": "review", "shard_id": "pf-9c18b2", "verdict": "pass" | "fail", "body": "..." }
```

`gate_verdict.go:10-24` (`normalizeGateVerdict`) maps legacy variants:
- `needs-fix` → `fail`
- `approved` / `passed` → `pass`

Internal storage is binary: `pass` or `fail`. There is no separate `request-changes` state — review's `request-changes` becomes `fail` here, and escalation logic decides whether the next action is re-dispatch or block.

## RecordGateVerdict

`internal/cmd/gatelogic.go:32-166`:

1. **Validate phase match.** `expectedPhaseForGate` (`gatelogic.go:368-387`) maps gate name → expected phase:
   - `readiness-review` → `design`
   - `decomposition-review` → `decompose`
   - `investigation` → `investigate`
   - `review` → no strict check (fires from `process-review` for tasks)
   - `retrospective` → `done`
   Mismatch rejects the call to prevent stale or duplicate writes.
2. **Compute round.** Counts existing rows in `pipeline_gates` for this pipeline + gate.
3. **Create review work item.** Connector creates a shard of type `review`, edge `child-of` parent design.
4. **Persist.** `pipeline_gates` row: `pipeline_id, design_id, gate_name, phase, round, verdict, body, findings_hash, review_shard_id`.
5. **On pass.** `advancePipelinePhase()` (`gatelogic.go:147`) updates `pipeline_runs.current_phase`.
6. **On fail.** `computeFindingsHash`; `shouldEscalateReview`; possibly `markPipelineBlocked` (see `escalation.md`).

## Where verdicts are recorded

- `pipeline_gates` table: full audit trail, one row per gate fire.
- `pipeline_runs.current_phase`: advanced on pass.
- Connector: review shard linked `child-of` design.

## Pass vs fail action

| Verdict | Phase advance | Re-dispatch | Escalation check |
|---|---|---|---|
| `pass` | yes | no | no |
| `fail` (round 1) | no | yes (fix phase) | hash recorded for next round |
| `fail` (round 2+, same hash) | no | no | block |
| `fail` (round 5) | no | no | block |

## What gates do NOT do

- **No verdict beyond pass/fail at storage layer.** Skill-level nuance (severity, suggestions vs nits) lives in `body`, not in a structured field.
- **No retry on the same gate without re-dispatch.** A bad verdict file forces a fresh agent run.
- **No gate-level rollback.** A pass that turns out wrong is corrected by filing new work, not by undoing the gate row.
