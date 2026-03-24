---
name: gate-process-review
description: Process external review feedback (Gemini, CI) on a task PR and decide approve, request-changes, or escalate. Trigger when a PR has external review results.
---

# Skill: Process PR Review

You are the pipeline orchestrator, processing review feedback on a task PR. External reviewers (Gemini, CI) have already provided feedback. Your job is to evaluate it and decide the next action.

## Input

- Task shard ID (status: needs-review)
- PR URL from task metadata

## Step 1: Gather feedback

### CI status
```bash
gh pr checks <pr-number> --repo <owner/repo>
```

Wait until all checks have completed (no `pending`). If checks are still running, exit — the poller will respawn you when they finish.

### Gemini review comments
```bash
gh api repos/<owner/repo>/pulls/<pr-number>/comments
gh api repos/<owner/repo>/pulls/<pr-number>/reviews
```

### PR diff
```bash
gh pr diff <pr-number> --repo <owner/repo>
```

## Step 2: Evaluate CI

Read the CI mode from `.cobuild/pipeline.yaml` → `review.ci.mode`:

### mode: ignore
Skip CI evaluation entirely. Proceed to review.

### mode: all-pass
```
All checks pass?
  YES → proceed to review
  NO  → request-changes with failure details
```

### mode: pr-only (default)
Only fail on checks that this PR broke — not pre-existing failures.

```bash
# Get PR check results
gh pr checks <pr-number> --repo <owner/repo>

# Get main branch CI status for comparison
gh api repos/<owner/repo>/actions/runs?branch=main&status=completed&per_page=1 \
  --jq '.workflow_runs[0].id' | xargs -I{} gh api repos/<owner/repo>/actions/runs/{}/jobs \
  --jq '.jobs[] | .name + ":" + .conclusion'
```

Compare each failed check against main:
```
Check fails on PR AND passes on main? → PR introduced this failure → BLOCK
Check fails on PR AND fails on main?  → Pre-existing failure → NOTE but don't block
Check passes on PR?                   → Fine
```

Record in your review:
```
CI: 3/8 checks failed
  - Lint: FAIL (pre-existing — also fails on main)
  - Proto Lint: FAIL (pre-existing)
  - Unit Tests (services/worker): FAIL — NEW failure, caused by this PR
```

Only new failures block the PR.

## Step 3: Evaluate Gemini review

For each Gemini comment, classify:

| Type | Action |
|------|--------|
| Security issue (injection, auth, secrets) | Must fix — send back |
| Bug (logic error, null pointer, race) | Must fix — send back |
| Style/naming suggestion | Ignore unless egregious |
| Documentation suggestion | Ignore |
| Performance suggestion | Note for follow-up, don't block |
| Missing error handling | Evaluate: is this a real gap or defensive noise? |

## Step 4: Decide

```
CI fails (not pre-existing) + real issues?
  → request-changes: list CI failures + real Gemini issues

CI passes + real Gemini issues?
  → request-changes: list only the real issues

CI passes + no real issues (or style-only)?
  → approve

CI fails (pre-existing only) + no real issues?
  → approve with note about pre-existing CI failures
```

## Step 5: Record verdict

```bash
# Approve
cobuild task review-verdict <task-id> approve --body "CI: pass. Gemini: N comments, none blocking. Approved."

# Request changes
cobuild task review-verdict <task-id> request-changes --body "CI: <failures>. Gemini: <real issues>. Fix needed: <list>"

# Escalate (design-level problem found)
cobuild task review-verdict <task-id> escalate --body "Review found design-level issue: <description>"
```

## Step 6: If request-changes

The implementer needs to fix and push. The poller will detect the task back in `in_progress` and re-check when it returns to `needs-review`.

Add the specific feedback to the task shard so the implementer knows what to fix:
```bash
cobuild wi append <task-id> --body "## Review Feedback — Round N
### CI Failures
<list>
### Gemini Issues (actionable)
<list with file:line references>
### What to fix
<specific instructions>"
```

## Gotchas

<!-- Add failure patterns here as they're discovered -->
