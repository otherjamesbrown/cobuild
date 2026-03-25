---
name: design-review
description: Review a design for pipeline readiness. Pre-flight check before submitting to CoBuild. Trigger on "review design", "design review", "is this ready".
summary: >-
  Pre-flight check before submitting a design to the pipeline. Evaluates the design against readiness criteria, checks project-specific constraints, and reports findings by severity. Offers to submit to CoBuild when everything passes.
---

# Skill: Design Review

Review a design work item to determine if it's ready to submit to the CoBuild pipeline. This is an interactive pre-flight check — it evaluates the design, reports findings, and offers to submit when everything passes.

**This skill does NOT decompose into tasks.** Decomposition happens automatically after the design enters the pipeline and passes the formal readiness gate.

## Input

A design work item ID: `$ARGUMENTS`

If no ID is provided, ask the developer which design to review.

## Step 1: Read the design

```bash
cobuild wi show <design-id>
```

Read the full content. Understand what it's proposing.

## Step 2: Check readiness criteria

Evaluate each criterion. Be specific about what's present and what's missing.

| # | Criterion | What to look for |
|---|-----------|-----------------|
| 1 | **Problem stated** | Concrete description of what's broken, missing, or painful. Specific file paths, behaviors, error messages — not vague complaints. |
| 2 | **User identified** | Who benefits from this change? Could be a person, another system, or an agent. |
| 3 | **Success criteria** | Measurable acceptance criteria that an agent could verify. "Works correctly" is not testable. "Returns 200 with valid JSON matching schema X" is. |
| 4 | **Scope boundaries** | What's explicitly out of scope or deferred. Without this, implementing agents gold-plate. |
| 5 | **Links to parent** | Design should be linked to a parent outcome or initiative. Check edges. |

## Step 3: Check implementability

Could an implementing agent write code from this design without asking any questions?

| Area | Pass if |
|------|---------|
| Technical approach | Specified — not "TBD" or "to be decided" |
| Code locations | File paths or modules identified |
| Data model | Schema changes described with field names and types |
| API surface | Endpoints, commands, or interfaces defined |
| Migration / rollout | Strategy stated, even if "single PR, no migration needed" |
| Error handling | What happens on failure is defined |

## Step 4: Check project-specific constraints

If the project has architectural principles or conventions (e.g., "all config in database", "no hardcoded values"), check the design against them. Look for:

- Violations of documented principles
- Hardcoded values that should be configurable
- New systems that duplicate existing ones
- Missing references to existing infrastructure

## Step 5: Report findings

Present findings as a table grouped by severity:

| Severity | Meaning |
|----------|---------|
| CRITICAL | Blocks pipeline submission — must fix |
| HIGH | Should fix before submission — will likely fail the formal gate |
| MEDIUM | Worth addressing but won't block |
| LOW | Suggestions for improvement |

For each finding, state:
- What's wrong
- Why it matters (which criterion or principle it violates)
- What a fix looks like

## Step 6: Determine verdict

### If there are CRITICAL or HIGH findings:

> **Verdict: NOT READY**
>
> The following must be addressed before submitting to the pipeline:
> 1. [list of CRITICAL/HIGH findings with suggested fixes]
>
> Fix these and run `/design-review <id>` again.

### If all criteria pass (no CRITICAL or HIGH):

> **Verdict: READY FOR PIPELINE**
>
> This design passes all readiness criteria and implementability checks.
>
> **Next step:** Submit to the CoBuild pipeline? This will:
> 1. Initialize the pipeline on this design (`cobuild init <design-id>`)
> 2. Run the formal readiness gate (recorded in the audit trail)
> 3. If it passes, automatically advance to decomposition
> 4. Decompose into tasks with dependency ordering
> 5. Dispatch agents to implement each task
>
> Submit now? (yes/no)

If the developer confirms, run:

```bash
cobuild init <design-id>
```

This starts the pipeline. The formal readiness gate will run automatically.

## What this skill does NOT do

- **Does not decompose into tasks** — that's the decompose phase, after the design enters the pipeline
- **Does not create child work items** — decomposition creates tasks
- **Does not record a formal gate verdict** — `cobuild init` triggers the formal gate
- **Does not modify the design** — it reports findings for the developer to fix

## Gotchas

- Do not flag "no child tasks" — tasks don't exist yet, that's the point of the pipeline
- Do not flag "no PR" or "no branch" — implementation hasn't started
- This is a pre-flight check, not a gate. The formal gate runs after `cobuild init`
<!-- Add failure patterns here as they're discovered -->
