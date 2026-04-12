---
name: implementability
version: "0.1"
description: Check whether a design is specific enough for an agent to implement without asking questions. Called as part of the readiness review gate.
summary: >-
  Can an agent build this without asking questions? Checks that the design specifies the technical approach, code locations, data model, API surface, migration strategy, and error handling — everything an implementing agent needs.
---

# Skill: Implementability Check

You are the pipeline orchestrator, checking whether a design can be implemented without further input from the developer.

This skill is called as part of the readiness check (`skills/design/gate-readiness-review.md`). You do not need to call this separately — the readiness check includes implementability.

## The Question

> Could an implementing agent write code from this design without asking the developer any questions?

## Check each area

| Area | Pass if |
|------|---------|
| Architecture | Technical approach is specified (not "TBD" or "to be decided") |
| Code locations | File paths or modules are identified |
| Data model | Schema changes or new types are described with field names and types |
| API surface | Endpoints, commands, or interfaces are defined |
| Migration | Rollout strategy stated (even if "single PR, no migration needed") |
| Error handling | What happens on failure is defined |

## Common gaps

| Gap | What's needed |
|-----|--------------|
| "Somewhere in the pipeline code" | Specific file path and line numbers |
| "We need a new config format" | Schema with field names and types |
| "Option A or B" | Pick one and state why |
| "Handle errors appropriately" | Define what "appropriately" means |
| "Should be backward-compatible" | Describe the specific constraint |

## Gotchas

<!-- Add failure patterns here as they're discovered -->

## Recording

Do NOT record implementability separately. Include it in the `--body` of `cobuild pipeline review` as part of the readiness check. See `skills/design/gate-readiness-review.md` for the full recording flow.

## Final Step

This check is part of the readiness-review flow. Once you have finished the readiness review work, exit the session immediately with `/exit`.
