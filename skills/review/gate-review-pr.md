---
name: gate-review-pr
description: Review a pull request against its task spec and parent design. Trigger when an agent-based review is needed for a task PR.
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
