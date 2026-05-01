---
name: gate-review-pr
version: "0.1"
description: Review a pull request against its task spec and parent design. Trigger when an agent-based review is needed for a task PR.
summary: >-
  Reviews a pull request by comparing it against the task spec and parent design. Checks three things: does it match the spec, does it fit the design, and will it break anything. Produces an approve, request-changes, or escalate verdict.
---

# Skill: Review PR

You are reviewing a pull request for a pipeline task.

## Input
- Task shard ID

## Setup

Read the task and its PR:
```bash
cobuild task get <task-id>
```
Get the PR URL from task metadata, then fetch the diff:
```bash
gh pr diff <pr-url>
```
Get the parent design for context:
```bash
cobuild wi links <task-id> outgoing child-of
cobuild show <parent-design-id>
```

## Evidence Requirement

Before issuing any demand based on a factual claim about the codebase, verify it:

- **"X exists in production"** → `grep -rn 'X' <relevant dirs>` and cite file:line where X is defined (not just referenced)
- **"X is missing"** → `grep -rn 'X'` confirming zero results, cite the command
- **"X was removed/added in commit Y"** → `git log --oneline --all -- <file>` or `git log --grep='X'` and cite the commit hash
- **"This test covers production code path Z"** → trace the call chain and cite the production file:line

Include a brief summary of your verification in the verdict body (e.g. "Verified: `BuildContext` is defined at `activities.go:42`").

If a claim **cannot be verified** (grep returns nothing, git log is ambiguous), soften the demand to a question: "Please confirm whether X is still in use — I could not find it at the expected location" rather than issuing a hard block.

**Never issue a hard block based on an unverified assumption about what exists or doesn't exist in the codebase.**

## Three Review Questions

### 1. Does it match the task spec?
- Compare the PR diff against the acceptance criteria in the task shard
- Every acceptance criterion should be addressed
- Nothing extra that wasn't asked for (no gold-plating)

### 2. Does it fit the overall design?
- Changes are consistent with the design's architecture
- No contradictions with other tasks in the same design
- Naming, patterns, and conventions match the codebase

### 3. Will it break anything?
- No obvious regressions
- Tests cover the changes
- Schema changes are backward-compatible or have migrations
- No hardcoded values that should be configurable

## Test Diagnosis

If tests fail, determine fault:
- **Implementation diverges from spec** → implementation bug → request changes
- **Test expects wrong thing** → test bug → note in verdict
- **Spec is ambiguous** → escalate

## Write Your Verdict

```bash
# If approved:
cobuild task review-verdict <task-id> approve --body "All acceptance criteria met. Tests pass. Clean implementation."

# If changes needed:
cobuild task review-verdict <task-id> request-changes --body "Issue: <description>. Fix: <suggestion>."

# If design problem:
cobuild task review-verdict <task-id> escalate --body "Design ambiguity: <description>. The developer needs to clarify."
```

## Gotchas

<!-- Add failure patterns here as they're discovered -->

## Final Step

After you have written the review verdict, exit the session immediately. Run `/exit` so the dispatched session does not sit idle.
## Final step

After recording the PR review verdict, stop. This review skill is not a task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
