# M Skill: Design Readiness Check

You are M, checking whether a design shard is ready for decomposition.

**Design criteria reference:** `skills/create-design.md` defines what a well-formed design looks like. This skill evaluates against those same criteria.

## Input
- Design shard ID (from trigger context)

## Steps

1. Read the design: `cobuild show <design-id>`

2. Check each readiness criterion:

| # | Criterion | How to check |
|---|-----------|-------------|
| 1 | Links to outcome | `cobuild shard edges <design-id> outgoing child-of` — must have a parent outcome |
| 2 | Problem stated | Design has a "Problem" section with concrete description, file paths, specific behavior |
| 3 | User identified | Design has a "Primary User", "User", or "Consumer" section |
| 4 | Success criteria | Design has measurable acceptance/success criteria (testable by an agent) |
| 5 | Scope boundaries | Design has "Non-Goals", "Scope", or "Out of Scope" section |

3. Run implementability check: "Could an implementing agent write code from this design without asking James any questions?"

   Check for:
   - Technical approach specified (not "TBD")
   - Code locations identified (file paths, function names)
   - Data model changes described (schema, types, fields)
   - API surface defined (commands, endpoints, interfaces)
   - Migration / rollout strategy stated
   - Edge cases / error handling mentioned

4. Count readiness score (N out of 5) and determine verdict.

5. **Record the review using the pipeline review command:**

   ```bash
   cobuild pipeline review <design-id> \
     --verdict pass|fail \
     --readiness <N> \
     --body "### Readiness (N/5)
   1. Links to outcome: PASS/FAIL — <detail>
   2. Problem stated: PASS/FAIL — <detail>
   3. User identified: PASS/FAIL — <detail>
   4. Success criteria: PASS/FAIL — <detail>
   5. Scope boundaries: PASS/FAIL — <detail>

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
   cobuild shard label add <design-id> blocked
   ```

7. Unlock pipeline and exit:
   ```bash
   cobuild pipeline unlock <design-id>
   ```

## Important

- **Always use `cobuild pipeline review`** — do NOT manually append findings or update the phase. The command handles all bookkeeping.
- The review is recorded even on pass — this is the audit trail.
- Do not skip any criteria. Every criterion gets a PASS/FAIL with a detail note.
