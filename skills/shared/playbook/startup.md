# Startup And Routing

Use this file when you are resuming a pipeline and need the common orchestration loop before dropping into a phase-specific spoke.

## Core loop

1. Read the work item: `cobuild show <id>`
2. Confirm shard type and `pipeline.phase`
3. Lock it: `cobuild lock <id>` — if locking fails, exit
4. Route by type and phase
5. Take exactly one phase-appropriate action
6. Unlock: `cobuild unlock <id>`

## Workflow map

```
design shard → design → decompose → implement → review → done
bug shard    → implement → review → done
task shard   → implement → review → done
```

## Phase map

```
pipeline.phase = ?
  "design"     → skills/shared/playbook/phase-design.md
  "decompose"  → skills/shared/playbook/phase-decompose.md
  "implement"  → skills/shared/playbook/phase-implement.md
  "review"     → skills/shared/playbook/phase-review.md
  "done"       → skills/shared/playbook/phase-done.md
```

## When to stop routing and escalate

Open `skills/shared/playbook/escalation.md` if the shard state is ambiguous, the workflow does not match the shard type, or you cannot identify a valid next command from the current state.

## Final step

After locking, routing, and taking the single next orchestration action, unlock the pipeline and stop. Do not run `cobuild complete` from this startup guide. Exit the session with `/exit`.
