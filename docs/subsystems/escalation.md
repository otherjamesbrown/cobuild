# Subsystem: escalation

What causes a pipeline to be marked `blocked`, and how it gets unblocked. For the gate-recording flow that calls escalation, see `gates.md`.

## Entry points

- Automatic — fires inside `RecordGateVerdict` (`internal/cmd/gatelogic.go`) when a review gate fails.
- Manual reset — `cobuild reset <task-id>` clears `blocked` status to allow re-dispatch.

## Escalation triggers

`shouldEscalateReview()` in `internal/cmd/findings_hash.go:34-57`. Two independent conditions, either fires:

### 1. Same findings hash for 2 consecutive rounds

- Threshold: `reviewEscalationThreshold = 2` (line 19).
- First fail → re-dispatch to fix.
- Second fail with **identical** `findings_hash` → mark blocked.
- Hash computed by `computeFindingsHash()` (`findings_hash.go:72-85`):
  - Extract lines matching `^-\s*\[(high|critical|must[- ]fix|blocking)\]`.
  - Sort, normalise whitespace, SHA-256, take 8 hex bytes.
  - Fallback: first 500 chars of body if no structured findings (constant `findingsHashMaxBody = 500`).
- Previous hash from `st.GetPreviousGateHash(ctx, pipelineID, gateName, round)`.

### 2. Hard round cap

- `reviewMaxRounds = 5` (line 26).
- If `round >= 5`, escalate regardless of hash variance.
- This guard exists for the case where the LLM reformats the same finding each round, defeating the hash check. (Background: cb-e20e84, cb-4c9241.)

## What "blocked" does

`markPipelineBlocked()` (`findings_hash.go:103-`):
1. Sets `pipeline_runs.status = "blocked"` in the store.
2. Calls `notifyReviewBlocked()` — shells out to `cxp message send agent-<project>` with subject `"Pipeline blocked: <task-id> (round N)"`.
3. Poller checks `run.Status == "blocked"` and skips `process-review` next cycle (`review.go:81-84`).

Notification is best-effort — `cxp` failure does not block the escalation logic.

## Recovery

`cobuild reset <task-id>` clears the blocked flag. The operator should investigate why review couldn't converge before resetting — repeated resets without root-cause analysis violate "Never restart a failure without root cause" (project CLAUDE.md).

## What escalation does NOT do

- **Does not re-prompt the agent with new context.** Reset just unblocks; next dispatch re-runs the same skill against the same context.
- **Does not detect *kind* of stuck.** A pipeline blocked because the work is cross-repo (review correctly says "this needs penf-cli") is indistinguishable from one blocked because the agent's logic is buggy.
- **Hash check ignores the diff.** Computed from review verdict body only. A review that flagged different problems each round but with similar wording could still hash-match and escalate prematurely.

## Known issues

- **5-round hard cap may not fire reliably.** pf-9c18b2 reached 5 review rounds while remaining `status=in_progress` rather than transitioning to `blocked`. Either the cap path isn't taken, or the round count seen by `shouldEscalateReview` differs from the round count visible on the shard. Worth a separate investigation.
- **Notifications go to `agent-<project>`.** No paging path for the orchestrator agent watching multiple projects.
