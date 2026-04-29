---
name: fix-bug
version: "0.1"
description: Single-session bug fix. Investigates and implements the fix in one pass. Use for bugs where the root cause is identifiable from the report. Escalate to needs-investigation if the cause is unknown or blast radius is unclear.
summary: >-
  Fixes a bug in one session. Reads the report, investigates as needed, appends findings, implements the fix, runs tests. The Stop hook handles cobuild complete.
---

# Skill: Fix Bug

Fix a bug in one session. Investigation and implementation happen together — you investigate as you fix, not as a separate read-only pass.

## What this is

A single-session bug fix. You read the report, do whatever investigation is needed to understand the cause, implement the fix, and run tests. The Stop hook runs `cobuild complete` when you finish.

This skill is for bugs where the root cause is identifiable from the report, or where a focused investigation will make it clear. For bugs where the cause is genuinely unknown, the blast radius is unclear, or the fix shape can't be described in 1-2 sentences — stop and escalate (see Escalation Check below).

## Escalation Check

Read the bug report first. Before starting any investigation or fix, check whether **any** of these apply:

1. **Root cause unknown** — symptom is visible but the mechanism isn't obvious from the report and a quick code read won't reveal it
2. **Cross-system** — bug spans multiple services, modules, or repos where the interaction is unclear
3. **Data or security implications** — could have corrupted data, leaked information, or created a security hole; need to assess blast radius before fixing
4. **Fragility concern** — this area has broken before, or the fix might affect unrelated behavior in ways you can't predict
5. **Intermittent or environment-dependent** — reproduces inconsistently; needs investigation to find the trigger
6. **Fix shape is non-obvious** — you can't describe the fix in 1-2 sentences after reading the report
7. **Requires stakeholder decision** — investigation will produce options and a human needs to choose

If **any** of these apply, **do not proceed with the fix**. Follow the Self-Escalation Protocol instead.

If none apply, continue.

## Procedure

### 1. Read the bug report

```bash
cobuild wi show <bug-id>
```

Extract:
- What's broken (symptom, error message)
- How to trigger it (reproduction steps)
- What's expected vs what's happening
- Any existing investigation or fix notes already in the body

### 2. Reproduce if possible

Run the failing command, test, or build. Confirm the error matches the report. Note the exact output.

If you can't reproduce it, check if it was already fixed in a recent commit before continuing.

### 3. Light investigation

Don't over-investigate — do what's needed to understand the cause:
- Read the relevant code
- Check recent changes: `git log --oneline -10 -- <affected files>`
- Check tests for the area
- Trace the call path if the root cause isn't obvious from a direct read

Stop investigating when you understand what to change. You don't need a full root-cause analysis for a straightforward fix.

### 4. Append findings

Before writing any code, append what you found:

```bash
cobuild wi append <bug-id> --body "## Findings

### Root Cause
<what's wrong and where>

### Affected Files
<list of files with line numbers>

### Fix Plan
<what you're going to change>
"
```

This creates a record even if something interrupts the session.

### 5. Implement the fix

Make the change. Keep it minimal — fix the bug, don't refactor the surrounding code.

If you find related issues while implementing, note them in the bug body. Don't fix them in this session unless they're the direct cause.

### 6. Write tests, then run the suite

**Write a regression test first** (cb-3197cc). The test must fail without your fix and pass with it. This is not optional — PRs without test coverage for the fixed behaviour will be rejected at the review gate.

Then run the full test suite and build. Fix any failures introduced by your change.

```bash
# Use whatever is in pipeline.yaml build/test sections
# Typical:
go build ./...
go test ./...
go vet ./...
```

### 7. Commit

Commit with a clear message: `fix: <what was wrong and what you changed>`

The Stop hook will run `cobuild complete` automatically when you finish. Do not run `cobuild complete` manually.

## Self-Escalation Protocol

If you reach this point mid-session and realize the cause is unclear, the blast radius is larger than expected, or you can't safely implement the fix:

1. Stop implementing immediately
2. Append your findings so far:
   ```bash
   cobuild wi append <bug-id> --body "## Partial Investigation

   ### What I found
   <findings so far>

   ### Why I'm escalating
   <which escalation criterion applies and why>

   ### Suggested next step
   <what investigation would help>
   "
   ```
3. Add the escalation label:
   ```bash
   cobuild wi label add <bug-id> needs-investigation
   ```
4. Exit. The orchestrator will re-dispatch through the investigation flow.

Do not guess at a fix when you're uncertain. A clean escalation is better than a wrong fix.

## Gotchas

<!-- Populated over time as patterns emerge -->

## Final Step

After you have finished the fix flow, let the Stop hook handle `cobuild complete`, then exit the session immediately. Run `/exit` so the dispatched session actually terminates.
