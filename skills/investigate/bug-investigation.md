---
name: bug-investigation
description: Investigate a bug to identify root cause, affected areas, and produce a fix spec. Trigger when a bug enters the pipeline or when investigation is needed before implementation.
---

# Skill: Bug Investigation

Investigate a bug report to understand the root cause, identify all affected areas, and produce a fix specification that an implementing agent can work from.

**This is a read-only investigation. Do not fix the bug — document findings and create a fix task.**

## Input

- Bug work item ID

## Step 1: Understand the bug report

```bash
cobuild wi show <bug-id>
```

Read the full bug report. Extract:
- **Symptom:** What's broken? (error message, wrong behavior, crash)
- **Reproduction:** How to trigger it (if documented)
- **Impact:** What's blocked by this bug?
- **Reporter context:** Who reported it and what were they doing?

## Step 2: Reproduce and verify

Before investigating, confirm the bug exists:
- Run the failing command/test/build
- Check if the error matches the bug report
- Note the exact error output

If the bug can't be reproduced, document that and check whether it was already fixed.

## Step 3: Investigate root cause

Work through these layers systematically:

### 3a. Direct cause
- What line of code produces the error?
- What changed recently? (`git log --oneline -20 -- <affected files>`)
- Was this introduced by a specific commit or PR?

### 3b. Contributing causes
- Why wasn't this caught by tests?
- Are there related issues in the same area?
- Is this a pattern that could affect other parts of the codebase?

### 3c. Systemic causes
- Is this a design issue or an implementation bug?
- Does the architecture make this class of bug likely?
- Are there similar patterns elsewhere that might have the same issue?

## Step 4: Identify affected areas

Map the blast radius:
- Which files are affected?
- Which other code depends on the broken code?
- Are there tests that should have caught this? Why didn't they?
- Could this bug exist in other similar code paths?

## Step 5: Produce investigation report

Record findings on the bug work item:

```bash
cobuild wi append <bug-id> --body "## Investigation Report

### Symptom
<exact error, reproduction steps>

### Root Cause
<what's wrong and why>

### Affected Files
<list of files with line numbers>

### Related Issues
<other code paths with the same pattern, potential similar bugs>

### Fragility Assessment
<why this area broke, what makes it fragile>
- Coupling: <what depends on what>
- Test coverage: <what's tested, what's not>
- Change frequency: <how often this area is modified>

### Fix Specification
<exactly what to change to fix the bug>
1. File: <path> — Change: <what to do>
2. File: <path> — Change: <what to do>

### Test Requirements
<what tests to add to prevent regression>
1. Test: <description> — Verifies: <what it proves>

### Severity
<CRITICAL|HIGH|MEDIUM|LOW> — <justification>
"
```

## Step 6: Record gate verdict

```bash
cobuild gate <bug-id> investigation --verdict pass --body "<summary of findings and fix spec>"
```

If the bug is not reproducible or is already fixed:
```bash
cobuild gate <bug-id> investigation --verdict pass --body "Bug not reproducible / already fixed. <evidence>"
```

If investigation is incomplete (need more info from reporter):
```bash
cobuild gate <bug-id> investigation --verdict fail --body "Need more information: <what's missing>"
cobuild wi label add <bug-id> blocked
```

## Step 7: Create fix task (if bug is confirmed)

The investigation report's Fix Specification becomes the task:

```bash
cobuild wi create --type task --title "Fix: <concise description>" --body "<fix specification from report>" --parent <bug-id>
```

This task goes through the normal implement → review → done workflow.

## What this skill produces

After investigation, the bug work item should have:
1. **Investigation report** appended to content (root cause, affected files, fix spec)
2. **Fragility assessment** identifying why this area is vulnerable
3. **Related issues** flagging similar patterns that might break
4. **Fix task** as a child work item with clear implementation spec
5. **Test requirements** specifying what tests to add

The implementing agent receives the fix task — it has everything needed to fix the bug without re-investigating.

## Gotchas

- Do NOT modify source code — you are read-only. Create a fix task for the implementer.
- The fragility assessment is as valuable as the fix — it feeds back into architecture decisions and identifies areas that need refactoring or better test coverage.
- Check git blame on the broken code — if it was recently changed by a pipeline task, note which design introduced the bug. This feeds into the retrospective.
- If multiple bugs stem from the same root cause, link them and create one fix task that addresses all of them.
<!-- Add failure patterns here as they're discovered -->
