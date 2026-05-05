# CoBuild Changelog

Consumer-facing changes. Written for agents and operators that use CoBuild, not for CoBuild developers. Newest first.

---

## 2026-05-03

### Root cause fix: review sessions no longer leak on CI-pending (842a304)

**What changed:** When a dispatched review agent approves a PR but CI is still pending, the session record is now properly ended. Previously it stayed "running" forever, blocking all future review dispatches for that task.

**Impact:** If you've seen "Review agent already running for X" on a task where no agent is actually running, that's this bug. It won't recur.

**Shards:** cb-0e0482 (closed)

### New command: `cobuild session-end <session-id>`

Ends a session record. You shouldn't need to call this manually — it exists as a backstop in the runner script EXIT trap. But if you see a stuck "running" session in `cobuild inspect` output, you can force-end it:

```bash
cobuild session-end ps-abc123 --note "manually ended: agent exited but session leaked"
```

### `cobuild inspect` now shows dispatch.log (ef25f38)

**What changed:** `cobuild inspect <task-id>` now includes the last 20 lines of `.cobuild/dispatch.log` from the worktree. This shows what the last dispatch actually did — success/failure, exit code, CI status, merge outcome.

**Impact:** When investigating a "stalled" pipeline, `cobuild inspect` now gives you the answer directly instead of requiring manual worktree navigation.

### Poller reconciles missing tmux windows (ef25f38)

**What changed:** If a session is marked "running" but the tmux window no longer exists, the poller ends it immediately on the next cycle (no stall_timeout wait required).

**Impact:** The "session stuck as running after agent exited" class of bugs is now self-healing within one poller cycle (~30s).

---

## 2026-05-02

### Heartbeat protocol for dispatched sessions (e81ab0d)

**What changed:** Both claude-code and codex runner scripts now write `.cobuild/heartbeat` every 30s. The poller reads this as a liveness signal.

**Impact:** A dead agent process is now detected within 2 minutes (heartbeat stops), vs. the previous 30-minute stall_timeout based on session.log mtime. If heartbeat is fresh, the poller won't kill the session even if session.log is stale (long LLM calls are legit).

**Config:** `monitoring.heartbeat_timeout` in pipeline.yaml (default: 2m).

### Stale-session notification (e81ab0d)

**What changed:** When the poller kills a stalled session, it now sends a CXP message to `agent-<project>` and marks the pipeline as blocked.

**Impact:** You'll get a message in your inbox when a session stalls. No more silent 14h waits.

---

## 2026-05-01

### Circuit-breaker notification + blocked status (c149a92)

**What changed:** When the review-fix circuit-breaker fires (same finding repeated, or round cap hit), the pipeline is now:
- Marked `status: blocked` in the database
- Sorted to the top of `cobuild status` with a banner
- Notified via CXP message to `agent-<project>`
- Skipped by `process-review` on subsequent polls (no wasted retries)

**Impact:** You'll know immediately when a pipeline blocks. `cobuild status` shows blocked pipelines first with recovery commands.

**Shards:** cb-d95bcd (closed)

### Review-fix loop hard cap at 5 rounds (f96b402)

**What changed:** `shouldEscalateReview` now blocks the loop after 5 review gate failures regardless of whether the findings hash matches. Previously only identical hashes triggered the circuit-breaker — slight wording variation between rounds let the loop run 33 times.

**Impact:** Worst-case review loop is now 5 rounds, not unbounded.

**Shards:** cb-e20e84, cb-4c9241 (closed)

### Review skill requires evidence for factual claims (f96b402)

**What changed:** The `gate-review-pr` skill now requires reviewers to verify claims before issuing hard blocks. Claims like "X exists in production" must cite file:line from a grep. Unverifiable claims must be softened to questions.

**Impact:** Review verdicts based on wrong assumptions (the cb-91ceff case where a reviewer demanded restoring code that was already deleted) should no longer produce impossible demands.

**Shards:** cb-91ceff (closed)
