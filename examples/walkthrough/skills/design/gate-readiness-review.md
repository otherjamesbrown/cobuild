---
name: walkthrough-readiness-review
version: "0.1"
description: Minimal repo-local readiness skill used by the walkthrough example.
summary: >-
  Read the design, check that it is concrete enough to split into tasks, then
  write `.cobuild/gate-verdict.json`.
---

# Skill: Walkthrough Readiness Review

Use this file as the design-phase rubric for the walkthrough example.

## What to check

1. The problem is concrete and names the user.
2. The success criteria are testable.
3. Scope boundaries are explicit.
4. The technical approach names likely files or components.
5. Rollout or deploy behavior is described.

## Output

Write `.cobuild/gate-verdict.json` with this shape:

```json
{
  "gate": "readiness-review",
  "shard_id": "<design-id>",
  "verdict": "pass|fail",
  "readiness": 1,
  "body": "Short findings"
}
```

When the design passes, the body should explain the task split you expect the
decompose step to create.
