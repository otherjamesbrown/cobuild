---
name: playbook
version: "0.2"
description: Pipeline orchestration hub. Trigger when a pipeline event occurs and route to the phase-specific playbook file for the current symptom or phase.
summary: >-
  The orchestrator hub. Read the pipeline state, match the current phase or symptom
  in the dispatch table, open only the linked spoke file, take one action, update
  state, and exit.
---

# Playbook — Pipeline Orchestration

You are the **pipeline orchestrator**. Read the work item, take one concrete action, update state, and exit. Do not load every playbook file up front. Use the table below to open only the spoke that matches the current situation.

Full system reference: `docs/cobuild.md`

## Startup

1. Read the pipeline shard: `cobuild show <id>`
2. Determine the shard type and current phase
3. Lock the pipeline: `cobuild lock <id>` — if already locked, exit
4. Match the current symptom or phase in the dispatch table
5. Read that one spoke file and follow it
6. Unlock when done: `cobuild unlock <id>`

## Dispatch Table

| Current symptom or phase | Read |
|---|---|
| You are starting fresh and need the common loop, routing, or workflow map | `skills/shared/playbook/startup.md` |
| `pipeline.phase = design` and you need the readiness decision tree | `skills/shared/playbook/phase-design.md` |
| `pipeline.phase = decompose` and you need task creation rules | `skills/shared/playbook/phase-decompose.md` |
| `pipeline.phase = implement`, tasks need dispatching, or a task looks stalled/crashed | `skills/shared/playbook/phase-implement.md` |
| `pipeline.phase = review` and you need the review or merge procedure | `skills/shared/playbook/phase-review.md` |
| `pipeline.phase = done` and you need the retrospective procedure | `skills/shared/playbook/phase-done.md` |
| You hit ambiguity, retry limits, circular deps, or need escalation text/budgets | `skills/shared/playbook/escalation.md` |

## Routing Summary

```
shard.type = ?
  "design" → design → decompose → implement → review → done
  "bug"    → implement → review → done
  "task"   → implement → review → done
```

If the current phase or symptom is unclear after `cobuild show <id>`, read `skills/shared/playbook/startup.md` first.

## Gotchas

- Open one spoke at a time. The point is to keep orchestration context small.
- Use the phase-specific command that records the audit trail. Do not hand-edit phase state if a gate command exists.
- If a spoke tells you to escalate, stop routing and follow `skills/shared/playbook/escalation.md`.

## Final step

After taking the one concrete orchestration action for this run and unlocking the pipeline, stop. This playbook is orchestrator guidance, not a dispatched task-completion skill, so do not run `cobuild complete`. Exit the session with `/exit`.
