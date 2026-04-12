# Phase 1 — Design Readiness

Use this file only when `pipeline.phase = design`.

Follow `skills/design/gate-readiness-review.md` for the detailed evaluation procedure.

## Decision

```
Does the design:
  - touch multiple subsystems? → full C/D/S is required; escalate in v1
  - have label "needs-cds"?    → full C/D/S is required; escalate in v1
  - otherwise                  → fast path
```

## Fast path

1. Read the design and evaluate the readiness criteria plus implementability.
2. Record the verdict:

```bash
cobuild review <id> --verdict pass|fail --readiness <N> --body "<findings>"
```

3. If the verdict is `fail`, label the item blocked and exit:

```bash
cobuild wi label add <id> blocked
```

4. If the verdict is `pass`, the phase advances to `decompose`.

## Rules

- Use `cobuild review`. It creates the audit trail, review sub-shard, and phase transition.
- Do not manually update the phase from `design`.
- If the design fails implementability and you cannot state what is missing, escalate.

## Final step

After recording the design verdict and applying any required label, stop. This phase guide is not a dispatched task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
