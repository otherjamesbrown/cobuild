---
name: kb-sync
version: "0.1"
description: Propose a targeted, patch-style update to a KB article based on a PR diff and work item. Writer component (Layer 0) — produces the proposed update that Layers 1 and 2 will verify.
summary: >-
  Reads a KB article and a PR diff, identifies which sections are affected, and writes a narrow patch-style proposed update to /tmp/kb-sync-<kb-id>-<timestamp>.md. Does NOT call cxp kb update — the phase handler does that after verification passes.
task_type: kb_sync
---

# Skill: KB Sync (Writer)

You are proposing a targeted update to a KB article based on a merged PR. Your job is to produce a narrow, patch-style proposed update — modify only what the PR actually changed. The phase handler will run this output through two verification layers before committing anything.

**Model routing**: `task_type: kb_sync` — routes to Gemini 2.5 Flash by default (configured in `ai_routing_rules`).

## Usage

```
/kb-sync <kb-id> --wi <work-item-id> [--pr <pr-number>] [--diff <diff-file>]
```

Examples:
```
/kb-sync pf-861f0c --wi pf-2091d5 --pr 47
/kb-sync pf-861f0c --wi pf-2091d5 --diff /tmp/my-pr.diff
```

Arguments:
- `<kb-id>` — ID of the KB article to update (e.g. pf-861f0c)
- `--wi <work-item-id>` — Work item (design / bug / task) that drove the PR (e.g. pf-abc123)
- `--pr <pr-number>` — PR number to pull the diff from (uses `gh pr diff`)
- `--diff <file>` — Path to a file containing the diff (alternative to --pr)

One of `--pr` or `--diff` is required.

## Step 1: Read the KB article

```bash
cxp kb show <kb-id>
```

Read the full article. Note its sections and what each section covers. This is the baseline you will patch against.

If the article does not exist, output:

```
ERROR: KB article <kb-id> not found. Cannot propose update.
```

Then stop.

## Step 2: Get the PR diff

If `--pr` was given:
```bash
gh pr diff <pr-number>
```

If `--diff` was given:
```bash
cat <diff-file>
```

Read the diff in full. Note:
- Which files changed (file paths, function names, type names)
- What was added, removed, or renamed
- Any database migrations included
- Any config changes
- Any stage names, model names, or RPC names affected

## Step 3: Read the work item body

```bash
cxp shard show <work-item-id>
```

Read the body. This provides the intent behind the PR — what problem was being solved, what approach was chosen. Use this to understand *why* the code changed, not just *what* changed.

## Step 4: Identify affected sections

Compare the KB article sections against the diff. For each section of the article, ask:

> Does this section describe something the PR changed?

A section is affected if it:
- Names a file, function, type, or stage that was renamed or removed in the diff
- Describes a behavior or flow that the PR changed
- References a DB table, column, or config key modified by a migration in the diff
- Mentions a model name or routing rule changed in the diff

A section is **NOT affected** if the diff did not touch the underlying facts it describes. Leave those sections untouched.

If **no sections are affected**, output:

```
NO-OP: No sections of <kb-id> are affected by this diff. No update proposed.
```

Then stop. The phase handler will treat this as verdict `no-changes-needed`.

## Step 5: Draft the proposed update

Produce the full updated article content, with only the affected sections modified.

Rules for the update:
- **Narrow scope**: only change the sections identified in Step 4. Copy all other sections verbatim from the original.
- **No hallucination**: every claim you write must be grounded in the diff or the work item body. Do not infer facts that are not in the source material.
- **Prefer precision over completeness**: if you are uncertain whether something changed, leave the original text. It is better to be slightly incomplete than to introduce a false claim.
- **Preserve structure**: maintain the same headings, bullet format, and section order as the original unless the diff explicitly warrants a structural change.
- **Prefer patches over rewrites**: if a function was renamed, update the name. If a stage was replaced, update that row. Do not rewrite surrounding context that hasn't changed.

## Step 6: Write a change summary

At the top of the proposed content, prepend a fenced block:

```
<!--kb-sync-summary
Source work item: <work-item-id>
Sections changed: <comma-separated list of section headings>
Reason: <1-2 sentence explanation of what the PR changed and why these sections needed updating>
Diff grounding: <comma-separated list of key facts from the diff that drove the changes>
-->
```

This summary is used by the kb-judge skill (Layer 2) to understand the writer's intent.

## Step 7: Write to temp file

Write the complete proposed new content (summary block + full article) to:

```
/tmp/kb-sync-<kb-id>-<timestamp>.md
```

Where `<timestamp>` is the current Unix timestamp in seconds (e.g. `date +%s`).

Example path: `/tmp/kb-sync-pf-861f0c-1744401600.md`

## Step 8: Output the temp file path

Print the path to stdout on its own line so the phase handler can find it:

```
KB_SYNC_OUTPUT=/tmp/kb-sync-pf-861f0c-1744401600.md
```

If no update was proposed (NO-OP), print:

```
KB_SYNC_OUTPUT=none
```

## Key constraints

1. **Do NOT call `cxp kb update`** — the phase handler runs Layer 1 (kb-factcheck) and Layer 2 (kb-judge) against your output before deciding whether to commit. Calling update directly bypasses verification.
2. **Narrow updates only** — if one section needs to change, the other sections must be copied verbatim. A full article rewrite is almost never the right answer.
3. **No hallucination** — every fact in the proposed update must be traceable to the diff or the work item body. If a fact is not in the source material, omit it.
4. **Output the temp file path** — the phase handler uses `KB_SYNC_OUTPUT=<path>` from your stdout to locate the proposed content.
5. **Stop cleanly on NO-OP** — if no sections are affected, output `KB_SYNC_OUTPUT=none` and stop. Do not produce a trivially identical article just to have output.

## What happens next

After this skill exits, the phase handler will:

1. Run `kb-factcheck` (Layer 1) against your output — deterministic verification of machine-verifiable claims (file paths, function names, DB tables, etc.)
2. Run `kb-judge` (Layer 2) against your output — a different-model LLM reviews accuracy, completeness, and gaps
3. If both pass: commit the update via `cxp kb update`
4. If either fails: discard your output, log a KB Gap, and advance the work item to `done` anyway (non-blocking)

## Final step

After printing `KB_SYNC_OUTPUT=<path>` or `KB_SYNC_OUTPUT=none`, stop. Do not call `cxp kb update` yourself, do not run `cobuild complete`, and do not wait for another turn. Exit the session with `/exit`.
