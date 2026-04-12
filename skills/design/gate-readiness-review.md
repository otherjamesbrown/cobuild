---
name: gate-readiness-review
version: "0.1"
description: Evaluate whether a design is ready for decomposition. Trigger on design review, readiness gate, or when a design reaches the design phase.
summary: >-
  Is this design ready to build? Checks 5 criteria: is the problem clear, who's it for, how will we know it works, what's out of scope, and is it linked to an outcome. Also checks whether an agent could implement it without asking questions.
---

# Skill: Design Readiness Check

You are the pipeline orchestrator, checking whether a design shard is ready for decomposition.

**Design criteria reference:** `skills/shared/create-design.md` defines what a well-formed design looks like. This skill evaluates against those same criteria.

## Input
- Design shard ID (from trigger context)

## Steps

1. Read the design: `cobuild show <design-id>`

2. Check each readiness criterion:

| # | Criterion | How to check |
|---|-----------|-------------|
| 1 | Links to outcome | `cobuild wi links <design-id> outgoing child-of` — must have a parent outcome |
| 2 | Problem stated | Design has a "Problem" section with concrete description, file paths, specific behavior |
| 3 | User identified | Design has a "Primary User", "User", or "Consumer" section |
| 4 | Success criteria | Design has measurable acceptance/success criteria (testable by an agent) |
| 5 | Scope boundaries | Design has "Non-Goals", "Scope", or "Out of Scope" section |
| 6 | Test strategy | Design specifies how the work will be tested (see below) |

3. Check test strategy:

   The design must answer: **how will this be tested?**

   | Level | Required? | What to check |
   |-------|-----------|---------------|
   | Unit tests | Always | What gets unit tested? With mocks or real deps? |
   | Integration tests | If DB/API/external services involved | Which integrations need real-connection tests? |
   | End-to-end tests | If new workflow or pipeline | Is there an e2e scenario that validates the full flow? |
   | Test infrastructure | If none exists yet | Does the project have shared fixtures, test DB setup, conftest.py? If not, the design must specify creating them. |

   If the design says nothing about testing: **FAIL this criterion.** Agents without a test strategy write isolated mocked unit tests that don't catch real integration bugs.

   A valid test strategy doesn't need to be long — one paragraph is enough:
   > "Unit tests for each service method (mocked DB). Integration test for the full pipeline hitting the test database. conftest.py with DB fixtures needed — create as Wave 1 task."

4. Run implementability check: "Could an implementing agent write code from this design without asking the developer any questions?"

   Check for:
   - Technical approach specified (not "TBD")
   - Code locations identified (file paths, function names)
   - Data model changes described (schema, types, fields)
   - API surface defined (commands, endpoints, interfaces)
   - Migration / rollout strategy stated
   - Edge cases / error handling mentioned

5. Count readiness score (N out of 6) and determine verdict.

5. **Record the review using the pipeline review command:**

   ```bash
   cobuild review <design-id> \
     --verdict pass|fail \
     --readiness <N> \
     --body "### Readiness (N/6)
   1. Links to outcome: PASS/FAIL — <detail>
   2. Problem stated: PASS/FAIL — <detail>
   3. User identified: PASS/FAIL — <detail>
   4. Success criteria: PASS/FAIL — <detail>
   5. Scope boundaries: PASS/FAIL — <detail>
   6. Test strategy: PASS/FAIL — <detail>

   ### Implementability
   PASS/FAIL — <detail on what's present or missing>

   ### Verdict
   <Ready for decomposition / Needs work: list gaps>"
   ```

   This single command:
   - Creates a review sub-shard with full findings (audit trail)
   - Updates pipeline metadata with structured verdict
   - If pass: automatically advances phase to `decompose`
   - Tracks round number (Round 1, Round 2, etc.)

6. If fail: add blocked label:
   ```bash
   cobuild wi label add <design-id> blocked
   ```

7. Unlock pipeline and exit:
   ```bash
   cobuild pipeline unlock <design-id>
   ```

## Gotchas

- **Always use `cobuild review`** — do NOT manually append findings or update the phase. The command handles all bookkeeping.
- The review is recorded even on pass — this is the audit trail.
- Do not skip any criteria. Every criterion gets a PASS/FAIL with a detail note.
<!-- Add failure patterns here as they're discovered -->

## Final Step

After you have recorded the readiness verdict and unlocked the pipeline, exit the session immediately. Run `/exit` so the dispatched session does not remain at an interactive prompt.
## Final step

After recording the readiness review and unlocking the pipeline, stop. This gate skill is not a task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
