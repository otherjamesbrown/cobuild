# Phase 2 — Decomposition

Use this file only when `pipeline.phase = decompose`.

## Agent routing

Read the pipeline config for the agent roster. Route decomposition to the agent whose domain matches the design. If the design crosses domains and ownership is unclear, escalate instead of guessing.

## Procedure

1. Structure pass: produce a task tree with titles, scope, and dependencies.
2. Detail review: verify each task is single-session sized, testable, and points at real code locations.
3. Create the implementation tasks:

```bash
cobuild wi create --type task --title "<title>" --parent <design-id> --body "<spec>"
cobuild wi links add <dependent-id> --blocked-by <blocker-id>
```

4. Create the required integration test task:

```bash
cobuild wi create --type task --title "Integration test: <design>" --parent <design-id> --label integration-test --body "<test spec>"
cobuild wi links add <test-id> --blocked-by <all-other-task-ids>
```

5. Register every task on the pipeline:

```bash
cobuild update <id> --add-task <task-id>
```

6. Record the gate result:

```bash
cobuild decompose <id> --verdict pass --body "<rationale>"
```

## Rules

- Do not manually update the phase. `cobuild decompose` validates and advances.
- The integration test task is mandatory. The gate should fail without it.
- If you find circular dependencies or cannot assign ownership cleanly, escalate.

## Final step

After creating the tasks and recording the decomposition verdict, stop. This phase guide is not a dispatched task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
