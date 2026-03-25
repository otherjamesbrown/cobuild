---
name: gate-retrospective
description: Review a completed pipeline to extract lessons learned and suggest improvements. Trigger when a design reaches the done phase.
summary: >-
  Reviews the entire pipeline run after completion. Looks at how many gate rounds it took, which tasks needed intervention, what went wrong, and what to improve. Produces suggested changes to skills and config based on real data.
---

# Skill: Pipeline Retrospective

You are the pipeline orchestrator, reviewing a completed design pipeline to extract lessons learned.

Run this after a design reaches `done` phase.

## Input

- Design shard ID (status: closed, phase: done)

## Steps

### 1. Gather execution data

```bash
# Audit trail
cobuild pipeline audit <design-id>

# Task graph
cobuild deps <design-id>

# Pipeline insights
cobuild pipeline insights
```

### 2. Review each gate

For each gate event in the audit trail:
- How many rounds did it take?
- If it failed, what was the reason?
- Was the failure avoidable with better input?

### 3. Review implementation

For each task:
- Did the agent complete without intervention?
- Was `cobuild task complete` needed to create the PR?
- Were there merge conflicts?
- How long did it take?

### 4. Identify patterns

Look for:
- **Repeated failures** — same issue across multiple designs (e.g. "missing code locations")
- **Agent gaps** — things agents consistently forget (e.g. creating PRs)
- **Process friction** — steps that always need manual intervention
- **Model mismatches** — tasks that took too long or failed because wrong model was used

### 5. Generate improvements

Run the improve command to see data-driven suggestions:
```bash
cobuild pipeline improve -o text
```

### 6. Record findings

Create a knowledge shard with the retrospective:
```bash
cobuild wi create --type knowledge \
  --title "Pipeline retrospective: <design-title>" \
  --body "<findings>"
```

Link to the design:
```bash
cobuild wi links add <retro-id> --references <design-id>
```

### Finding format

```markdown
# Pipeline Retrospective: <design title>

## Summary
- Gates: N rounds across M gates
- Tasks: N completed, M needed intervention
- Time: ~X hours end-to-end

## What worked
- <list>

## What failed
- <list with root causes>

## Suggested changes
- [skill] <change to skill file> — because <data>
- [config] <change to pipeline.yaml> — because <data>
- [process] <change to workflow> — because <data>

## Data-driven improvements
<output from cobuild pipeline improve>
```

## Gotchas

<!-- Add failure patterns here as they're discovered -->
