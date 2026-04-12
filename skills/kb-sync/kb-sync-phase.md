---
name: kb-sync-phase
version: "0.1"
description: Orchestrate KB article updates for a merged work item. Runs between review and done. Dispatches kb-sync (writer), kb-factcheck (Layer 1), and kb-judge (Layer 2) in sequence for each affected KB article.
summary: >-
  Finds KB articles affected by the merged PR for a work item, proposes targeted updates via
  the kb-sync writer skill, verifies them with kb-factcheck (deterministic) and kb-judge
  (cross-family LLM semantic review), and commits or rolls back based on both layers passing.
---

# Skill: kb-sync Phase

You are the CoBuild orchestrator running the **kb-sync phase** for work item `{{.WorkItemID}}`.

This phase runs between `review` and `done`. It keeps the knowledge base current after code changes merge.

## What you are doing

For each KB article affected by this work item's merged PR:
1. Propose a targeted update (narrow patch, not full rewrite)
2. Verify claims deterministically (Layer 1: kb-factcheck)
3. Verify semantically with a different model family (Layer 2: kb-judge)
4. Commit or roll back based on both layers

Gate verdict is **non-blocking**: all-rolled-back still advances to `done`, but failures are logged.

## Input

- Work item ID: `{{.WorkItemID}}`
- PR diff and body from the merged PR
- Current KB article content from `cxp kb show <id>`

## Step 1: Verify model-family constraint

Before doing anything, confirm that `kb_sync` and `kb_judge` routing rules use different model families:

```bash
# Query ai_routing_rules for both task types
# If same family → log CRITICAL warning and exit non-zero
cobuild kb-sync {{.WorkItemID}} --dry-run
```

If the constraint check fails, do not proceed. Append a warning to the work item explaining the issue.

## Step 2: Find affected KB articles

```bash
# Semantic search for concepts from the diff
cxp kb search "<file_paths and function_names from diff>" -o json --limit 10

# Full-text scan for file paths from diff
cxp kb list -o json | grep -l "<path from diff>"
```

If no articles are found → record gate verdict `no-changes-needed` and exit 0.

## Step 3: For each affected article

### 3a. Propose update (kb-sync writer)

```bash
# Read current article
cxp kb show <article-id>

# Write proposed update to temp file
# Rules:
# - Narrow, patch-style updates only — don't rewrite untouched sections
# - Include a short change summary explaining what changed and why
# - Output to /tmp/kb-sync-<article-id>-<timestamp>.md
```

The proposed update should:
- Only modify sections directly affected by the PR diff
- Preserve all information that is still accurate
- Include a `## Change Summary` section at the end

### 3b. Run kb-factcheck (Layer 1)

```bash
/kb-factcheck input=/tmp/kb-sync-<article-id>-<timestamp>.md output=/tmp/kb-factcheck-<article-id>-<timestamp>.json
```

Parse the JSON output:
- `"verdict": "pass"` → proceed to Layer 2
- `"verdict": "fail"` → rollback, log KB Gap with each failed claim

### 3c. Run kb-judge (Layer 2)

```bash
/kb-judge diff=<diff-file> wi-body=<wi-body-file> old=<old-content-file> new=/tmp/kb-sync-<article-id>-<timestamp>.md output=/tmp/kb-judge-<article-id>-<timestamp>.json
```

Apply output handling rules:
- `consistent` → pass, commit the update
- `inaccurate` or `incomplete` with `high` severity → rollback, log KB Gap
- `inaccurate` or `incomplete` with `medium`/`low` severity → pass, log KB Gap for follow-up
- `gaps_noted` → pass, log each gap to `pf-kb-gaps` for backfill

### 3d. Commit or rollback

On both layers passing:
```bash
cxp kb update <article-id> --file /tmp/kb-sync-<article-id>-<timestamp>.md \
    --summary "Updated by kb-sync for {{.WorkItemID}} (factcheck=N/M, judge=consistent)"
```

On any layer failing:
- Do NOT call `cxp kb update`
- Log the failure to `pf-kb-gaps`:
```bash
cxp shard append pf-kb-gaps --project penfold \
    --body "**Date:** <timestamp>\n**Article:** <article-id>\n**Work item:** {{.WorkItemID}}\n**Reason:** <failure details>"
```

## Step 4: Record gate verdict

After processing all articles, record the gate verdict:

| Outcome | Verdict |
|---------|---------|
| No affected articles | `no-changes-needed` |
| All articles updated | `updated` |
| Some updated, some rolled back | `partial-update` |
| All rolled back | `all-rolled-back` |

```bash
cobuild gate {{.WorkItemID}} kb-sync pass --body "verdict=<verdict> updated=N rolled_back=M"
```

All verdict values advance the work item to `done`. This phase is non-blocking.

## Key rules

- **Never rewrite a whole article** if only one section changed
- **Never commit without both layers passing** (unless verdict is medium/low for Layer 2)
- **Every failure is a data point** — log it to `pf-kb-gaps` with enough context to debug
- **The gate is always pass** — failures are logged, not blocking
- This skill runs as the orchestrator; it dispatches other skills via `cobuild dispatch`, it does not implement the writer/factcheck/judge logic itself

## On completion

When all articles are processed and the gate verdict is recorded, this phase is complete. CoBuild will advance the work item to `done`.

Do not run `cobuild complete` from this phase skill. Once the gate verdict is recorded and any required follow-up shards are logged, exit the session with `/exit`.
