---
name: kb-judge
version: "0.1"
description: Layer 2 semantic verifier for KB article updates. Uses a different model family from kb-sync to judge accuracy, completeness, and gaps in a proposed article update.
summary: >-
  Semantically reviews a proposed KB article update against the source PR diff and old article content. Asks three questions: accuracy (does the new content contradict the diff?), completeness (was anything removed that should still be true?), and gaps (is anything substantive missing?). Returns a JSON verdict that the kb-sync phase handler uses to approve, rollback, or log KB Gaps.

# MODEL FAMILY CONSTRAINT (enforced at startup by the kb-sync phase handler):
# This skill MUST use a different model family from kb-sync.
#   If kb-sync uses gemini  → kb-judge uses claude (claude-haiku-4-5)
#   If kb-sync uses claude  → kb-judge uses gemini (gemini-2.5-flash)
#   If kb-sync uses gpt     → kb-judge uses gemini (gemini-2.5-flash)
# Routing rule: task_type = kb_judge  (see ai_routing_rules)
# The phase handler refuses to run kb-sync if the two rules share a model family.
---

# Skill: KB Judge (Layer 2 — Semantic Verification)

You are the semantic judge in a 3-layer KB verification pipeline. Your job is to
determine whether a proposed KB article update accurately and completely reflects
what a PR introduced — using a **different model family from the writer agent**
to provide an independent second opinion.

You do NOT commit or modify anything. You return a JSON verdict and exit.

---

## Inputs

You need four pieces of information. Locate them via the arguments or environment
variables described below.

| Input | Where to find it |
|-------|-----------------|
| PR diff | File path in `$KB_JUDGE_DIFF` or passed as first argument |
| Work item body | File path in `$KB_JUDGE_WORKITEM` or second argument |
| Old KB article content | File path in `$KB_JUDGE_OLD` or third argument |
| Proposed new KB content | File path in `$KB_JUDGE_NEW` or fourth argument |

If any input is missing, write an error verdict and exit 1:

```json
{
  "verdict": "error",
  "issues": [{"severity": "high", "description": "Missing input: <which>"}],
  "gaps_identified": []
}
```

---

## Step 1: Read all four inputs

```bash
# Read inputs — fail fast if any are missing or empty
cat "$KB_JUDGE_DIFF"       # or the path passed as arg 1
cat "$KB_JUDGE_WORKITEM"   # or arg 2
cat "$KB_JUDGE_OLD"        # or arg 3
cat "$KB_JUDGE_NEW"        # or arg 4
```

Hold all four in memory for the next step.

---

## Step 2: Call the kb_judge prompt template

Invoke the LLM with task_type `kb_judge`. The prompt template (stored in
`prompt_templates` with stage `kb_judge`) already contains the structure below —
do not re-write it. Pass your four inputs as the template variables.

**Prompt template variables:**

```
{{.Diff}}        ← full PR diff text
{{.WorkItemBody}} ← work item body text
{{.OldContent}}  ← old KB article text
{{.NewContent}}  ← proposed new KB article text
```

**What the template asks:**

> You are reviewing a proposed update to a knowledge base article.
>
> SOURCE MATERIAL:
> - PR diff: {{.Diff}}
> - Work item body: {{.WorkItemBody}}
> - Old article content: {{.OldContent}}
> - Proposed new content: {{.NewContent}}
>
> Answer three questions about the proposed new content:
>
> 1. ACCURACY: Does the new content accurately reflect what the PR changed?
>    Specifically, are any statements in the new content contradicted by the
>    diff or the work item body?
>
> 2. COMPLETENESS: Did the update REMOVE anything from the old content that
>    should still be true? Check the diff — if the old content said X, and
>    the PR did not change X, then X should still appear in the new content
>    (unless X was actually wrong before).
>
> 3. GAPS: Is there anything substantive in the PR that the new article
>    should mention but does not? (e.g. a new function, a changed behaviour,
>    a removed field.)
>
> Return JSON:
> ```json
> {
>   "verdict": "consistent" | "inaccurate" | "incomplete" | "gaps_noted",
>   "issues": [
>     {"severity": "high|medium|low", "description": "..."}
>   ],
>   "gaps_identified": ["..."]
> }
> ```

---

## Step 3: Parse and validate the response

The model must return valid JSON matching this schema:

```json
{
  "verdict": "<string: consistent|inaccurate|incomplete|gaps_noted>",
  "issues": [
    {"severity": "<string: high|medium|low>", "description": "<string>"}
  ],
  "gaps_identified": ["<string>"]
}
```

If the model returns malformed JSON or an unexpected verdict value, retry once.
If the second attempt also fails, emit:

```json
{
  "verdict": "error",
  "issues": [{"severity": "high", "description": "Judge LLM returned unparseable response after retry"}],
  "gaps_identified": []
}
```

---

## Step 4: Write the verdict

Write the final JSON to two places:

1. **Timestamped temp file** (permanent record):
   ```bash
   TS=$(date +%s)
   OUTFILE="/tmp/kb-judge-${TS}.json"
   echo '<verdict-json>' > "$OUTFILE"
   echo "Verdict written to $OUTFILE"
   ```

2. **Stdout** — print the JSON so the phase handler can capture it:
   ```bash
   cat "$OUTFILE"
   ```

---

## Verdict meanings (for the phase handler)

You emit the verdict; the **phase handler decides the action**. Do not take any
action beyond writing the verdict.

| Verdict | Highest severity | Phase handler action |
|---------|-----------------|---------------------|
| `consistent` | — | Pass. Commit the update. |
| `inaccurate` | high | Fail. Rollback. Log KB Gap with judge findings. |
| `incomplete` | high | Fail. Rollback. Log KB Gap with judge findings. |
| `inaccurate` | medium/low | Pass. Log KB Gap for follow-up. |
| `incomplete` | medium/low | Pass. Log KB Gap for follow-up. |
| `gaps_noted` | any | Pass. Log gaps to kb-gaps shard for backfill. |
| `error` | — | Phase handler treats as fail-safe: rollback + log. |

---

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Verdict produced (consistent, inaccurate, incomplete, gaps_noted) |
| 1 | Input error or unrecoverable LLM error — verdict `error` written |

---

## Example: contradictory update (should return inaccurate + high)

**Scenario**: PR renames function `AttributeProject` to `ClassifyProject`.
Old KB article says "The `AttributeProject` function handles attribution."
Proposed update says "The `AttributeProject` function now uses LLM-based
classification." — the name is still wrong in the new content.

**Expected verdict:**
```json
{
  "verdict": "inaccurate",
  "issues": [
    {
      "severity": "high",
      "description": "New content still references 'AttributeProject' but the PR renamed it to 'ClassifyProject'. The article contradicts the diff."
    }
  ],
  "gaps_identified": []
}
```

## Example: correct update (should return consistent)

**Scenario**: PR adds a `retry_limit` config key. Old article has no mention of
it. Proposed update adds a section describing `retry_limit` accurately.

**Expected verdict:**
```json
{
  "verdict": "consistent",
  "issues": [],
  "gaps_identified": []
}
```

---

## Gotchas

- **Do not hallucinate file contents.** If a diff shows a file was modified but
  the content is truncated, note the truncation in a low-severity issue rather
  than inferring what the change was.
- **Prose style differences are not issues.** Judge factual accuracy and
  completeness only — not grammar, tone, or formatting choices.
- **Removed sections are only a problem if the removed content was still true.**
  If the old article described a stage that the PR deleted, removing that section
  is correct, not incomplete.
- **gaps_noted is non-blocking.** Use it when the article is accurate and
  complete but the PR introduced something noteworthy that the article omits —
  e.g. a new CLI flag that would help users but isn't mentioned yet.
<!-- Add failure patterns here as they're discovered -->

## Final step

After writing the verdict file and printing the JSON, stop. Do not edit repo files, do not run `cobuild complete`, and do not stay at an interactive prompt. Exit the session with `/exit`.
