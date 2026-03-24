# Skill: Create a Design Shard

When creating a design shard, follow this structure. Designs that don't meet these criteria will be sent back by the pipeline readiness check.

**Evaluated by:** `skills/m-readiness-check.md` — M runs this check before advancing a design to decomposition. The 5 readiness criteria and implementability check map directly to the required sections below.

---

## Required Sections

### 1. Problem

What is broken, missing, or painful? Be concrete:
- Reference specific files, functions, or line numbers where the problem lives
- Show the current behavior vs desired behavior
- If there's an incident or bug that triggered this, reference it

**Bad:** "The notification system is inflexible."
**Good:** "Notification sender detection is hardcoded in `pipeline.go:412-445` with a switch statement over 8 known senders. Adding a new sender requires a code change and redeploy."

### 2. User / Consumer

Who benefits from this change? This can be:
- End users ("newsletter subscribers see duplicates")
- Developers ("adding a pipeline stage requires touching 4 files")
- Operators ("no visibility into why a message was dropped")
- Other agents ("the implementing agent can't find the entry point")

### 3. Success Criteria

Measurable, verifiable conditions. An agent should be able to write a test for each one.

**Bad:** "The system works correctly."
**Good:**
- "New pipeline stages can be added by creating a single file implementing the `StageExecutor` interface"
- "`go test ./pipeline/...` passes with the new stage registered"
- "Existing stages produce identical output (verified by diff against golden files)"

Aim for 3-5 criteria. Each should be independently testable.

### 4. Scope Boundaries

What is explicitly **not** included. This prevents gold-plating and scope creep.

**Example:**
- "UI changes are out of scope — this is backend only"
- "Migration of existing data is deferred to a follow-up design"
- "Performance optimization is not a goal; correctness first"

If there are things that look related but are deliberately excluded, call them out.

### 5. Technical Approach

Pick one approach and describe it. If you considered alternatives, briefly note why you chose this one.

Include:
- **Architecture:** How the pieces fit together
- **Code locations:** File paths, package names, function names where changes happen
- **Data model:** Schema changes, new types, modified interfaces (with field names and types)
- **API surface:** New or changed commands, endpoints, function signatures
- **Dependencies:** What this integrates with, what it requires to exist first

### 6. Migration / Rollout

How does this get deployed without breaking things?
- Can it be done in one PR or does it need phasing?
- Is it backward-compatible?
- Does existing data need migrating?
- What happens to in-flight work during the rollout?

If the answer is "it's a single small change, no migration needed" — say that explicitly.

---

## Optional Sections

### Edge Cases / Error Handling

What happens when things go wrong? Think about:
- Invalid input / missing config
- Partial failures (half-migrated state)
- Concurrent access
- Backward compatibility with existing data

### Dependencies

Other designs or tasks that must complete first. Reference shard IDs.

### Non-Goals

Stronger than scope boundaries — things this design will **never** do, even in future iterations.

---

## Implementability Test

Before submitting, ask yourself:

> Could an implementing agent write code from this design without asking me any questions?

If the answer is no, the design isn't ready. Common gaps:

| Gap | Fix |
|-----|-----|
| "Somewhere in the pipeline code" | Specific file path and line numbers |
| "We need a new config format" | Show the schema with field names and types |
| "Option A or B" | Pick one and state why |
| "Handle errors appropriately" | Define what "appropriately" means (retry? fail-open? log and skip?) |
| "Should be backward-compatible" | Describe the specific compatibility constraint |

---

## Creating the Shard

```bash
cxp shard create --type design \
  --title "<concise title — what changes, not why>" \
  --body-file design.md

# Link to parent outcome
cxp shard edge create <design-id> <outcome-id> child-of

# If this depends on another design
cxp shard edge create <design-id> <other-design-id> blocked-by
```

### Title Convention

Format: `<What changes> — <one-line summary>`

**Good:**
- "Config-driven contribution gating — move skip thresholds to pipeline_definitions"
- "Stage-kind-driven workflow execution — replace sequential branching with config-driven dispatch"

**Bad:**
- "Fix the pipeline"
- "Improvements to notification handling"
- "Phase 2 of the refactor"
