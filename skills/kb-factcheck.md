---
name: kb-factcheck
version: "0.1"
description: Layer 1 deterministic verifier for proposed KB article updates. Extracts machine-verifiable claims via LLM, then verifies each claim with grep/SQL. Fails if any claim is not found.
summary: >-
  Verifies a proposed KB article update by extracting claims (file paths, function names, DB tables, prompt stages, etc.) then checking each one deterministically against the penfold repo and database. Outputs a JSON verdict file.
---

# Skill: KB Fact-Check (Layer 1 — Deterministic Verifier)

You are the Layer 1 deterministic verifier for a proposed KB article update. Your job is to extract machine-verifiable claims from the article and verify each one against the real system. No human is in the loop — your output is the gate verdict.

## Input

A path to the proposed KB article content file, e.g.:

```
/tmp/kb-sync-pf-861f0c-1234567890.md
```

This file contains the proposed new content for a KB article. It has NOT been committed to CP yet.

## Environment

- **Penfold repo**: `~/github/otherjamesbrown/penfold`
- **DB DSN**: read from `~/.cp/config.yaml` key `database.url`, or use `$PENFOLD_DB_DSN` if set
- **Output file**: `/tmp/kb-factcheck-<timestamp>.json`

Read the DSN at the start:

```bash
# Try env var first
DSN="${PENFOLD_DB_DSN:-}"

# Fall back to config file
if [ -z "$DSN" ]; then
  DSN=$(grep -A2 'database:' ~/.cp/config.yaml | grep 'url:' | awk '{print $2}' | tr -d '"')
fi

echo "Using DSN: $DSN"
```

---

## Step A — Claim Extraction (LLM call, task_type: kb_factcheck_extract)

Read the proposed article content, then call the LLM with the following extraction prompt. Use the cheapest available model (task_type `kb_factcheck_extract` routes to `gemini/gemini-2.0-flash`).

**Extraction prompt** (template stage: `kb_factcheck_extract`):

> Extract every machine-verifiable factual claim from the article below as a JSON array.
>
> Each element must have:
> - `type` — one of: file_path, function_name, type_name, db_table, db_column, migration_number, model_name, prompt_stage, config_key, shard_id, rpc_name
> - `value` — the exact string to check (e.g. the file path, function name, table name)
> - `subject` — (optional) context string, e.g. for db_column the table name goes here
>
> Claim type guide:
> - `file_path` — any file path in the repo (e.g. `services/worker/activities/classify_project.go`)
> - `function_name` — any Go or other function/method name (e.g. `ClassifyProject`, `NewWorker`)
> - `type_name` — any Go type or struct name (e.g. `AutomationRule`, `TenantContext`)
> - `db_table` — any database table name (e.g. `ai_routing_rules`, `prompt_templates`)
> - `db_column` — a column on a specific table; set `subject` to the table name (e.g. value `preferred_models`, subject `ai_routing_rules`)
> - `migration_number` — a numeric migration number (e.g. `165`)
> - `model_name` — an AI model name referenced as used in the system (e.g. `gemini-2.5-flash`)
> - `prompt_stage` — a stage name from `prompt_templates` (e.g. `classify_project`)
> - `config_key` — a key from `pipeline_operational_config` (e.g. `attribution.method`)
> - `shard_id` — a CP shard ID (e.g. `pf-2091d5`)
> - `rpc_name` — a proto RPC name (e.g. `ClassifyProject`)
>
> Only extract claims that are verifiable by looking something up. Skip opinions, descriptions, and high-level summaries. Extract all instances you find.
>
> Return ONLY the JSON array with no other text.
>
> Article content:
> ```
> {{ARTICLE_CONTENT}}
> ```

**In practice**: you are the agent running this skill. Read the article file, then use your own LLM capability to extract the claims by applying the prompt above. Output the result as a JSON array to a variable.

```bash
ARTICLE_CONTENT=$(cat "$INPUT_FILE")
```

Then reason through the article content and produce the JSON array yourself — you ARE the LLM doing the extraction. Do not shell out to another LLM for this step; just extract the claims directly.

Expected output structure:

```json
[
  { "type": "file_path", "value": "services/worker/activities/classify_project.go" },
  { "type": "function_name", "value": "ClassifyProject" },
  { "type": "db_table", "value": "ai_routing_rules" },
  { "type": "prompt_stage", "value": "classify_project" },
  { "type": "db_column", "value": "preferred_models", "subject": "ai_routing_rules" },
  { "type": "shard_id", "value": "pf-2091d5" }
]
```

Save to `/tmp/kb-factcheck-claims-<timestamp>.json`.

---

## Step B — Deterministic Verification

For each claim, run the appropriate check. Record the result as `verified`, `not_found`, or `ambiguous`.

**PENFOLD_REPO=`~/github/otherjamesbrown/penfold`**

### file_path

```bash
git -C "$PENFOLD_REPO" ls-files | grep -q "$VALUE"
# exit 0 → verified, exit 1 → not_found
```

If the path matches multiple files (partial match), mark `ambiguous` with a note.

### function_name

```bash
grep -rq "func ${VALUE}(" "$PENFOLD_REPO" || grep -rq "func (.*) ${VALUE}(" "$PENFOLD_REPO"
# exit 0 → verified, exit 1 → not_found
```

### type_name

```bash
grep -rq "type ${VALUE} " "$PENFOLD_REPO"
# exit 0 → verified, exit 1 → not_found
```

### db_table

```bash
psql "$DSN" -tAc "SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = '${VALUE}'" | grep -q 1
# found → verified, empty → not_found
```

### db_column

`VALUE` is the column name, `SUBJECT` is the table name.

```bash
psql "$DSN" -tAc "SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = '${SUBJECT}' AND column_name = '${VALUE}'" | grep -q 1
# found → verified, empty → not_found
```

If `SUBJECT` is empty, mark `ambiguous` — can't verify a column without a table name.

### migration_number

```bash
ls "${PENFOLD_REPO}/migrations/${VALUE}_"*.sql 2>/dev/null | grep -q .
# exit 0 → verified, exit 1 → not_found
```

Also check without leading zeros:

```bash
ls "${PENFOLD_REPO}/migrations/"*"${VALUE}"*.sql 2>/dev/null | grep -q .
```

### model_name

Check `ai_routing_rules` preferred_models arrays:

```bash
psql "$DSN" -tAc "SELECT 1 FROM ai_routing_rules WHERE '${VALUE}' = ANY(preferred_models) OR '${VALUE}' = ANY(fallback_models) LIMIT 1" | grep -q 1
```

Also check with provider prefix removed (e.g. `gemini-2.5-flash` might be stored as `gemini/gemini-2.5-flash`):

```bash
psql "$DSN" -tAc "SELECT 1 FROM ai_routing_rules WHERE preferred_models::text ILIKE '%${VALUE}%' OR fallback_models::text ILIKE '%${VALUE}%' LIMIT 1" | grep -q 1
# found → verified, empty → not_found
```

### prompt_stage

```bash
psql "$DSN" -tAc "SELECT 1 FROM prompt_templates WHERE stage = '${VALUE}' LIMIT 1" | grep -q 1
# found → verified, empty → not_found
```

### config_key

```bash
psql "$DSN" -tAc "SELECT 1 FROM pipeline_operational_config WHERE key = '${VALUE}' LIMIT 1" | grep -q 1
# found → verified, empty → not_found
# If table doesn't exist, mark ambiguous (config may be stored differently)
```

If the query fails because the table doesn't exist, mark `ambiguous` with reason "pipeline_operational_config table not found".

### shard_id

```bash
cxp shard show "${VALUE}" > /dev/null 2>&1
# exit 0 → verified, non-zero → not_found
```

### rpc_name

```bash
grep -rq "rpc ${VALUE}" "${PENFOLD_REPO}/api/proto/" || grep -rq "rpc ${VALUE}" "${PENFOLD_REPO}/"**"/*.proto"
# exit 0 → verified, exit 1 → not_found
```

If no `.proto` files exist in the repo, mark `ambiguous` with reason "no proto files found in repo".

---

## Step C — Result

Tally the results and produce the verdict.

**Verdict logic**:
- `fail` — if ANY claim has status `not_found`

## Final step

After writing the verdict JSON and printing it to stdout, stop. Do not modify repo files, do not run `cobuild complete`, and do not wait for more interaction in this session. Exit the session with `/exit`.
- `pass` — if all claims are `verified` or `ambiguous` (no `not_found`)

Produce the output JSON:

```json
{
  "verdict": "pass",
  "input_file": "/tmp/kb-sync-pf-861f0c-1234567890.md",
  "claims_checked": 6,
  "claims_verified": 5,
  "claims_ambiguous": 1,
  "claims_failed": [],
  "claims_ambiguous_list": [
    {
      "type": "config_key",
      "value": "attribution.method",
      "reason": "not in pipeline_operational_config, may be stored elsewhere"
    }
  ],
  "timestamp": "2026-04-11T03:00:00Z"
}
```

On failure:

```json
{
  "verdict": "fail",
  "input_file": "/tmp/kb-sync-pf-861f0c-1234567890.md",
  "claims_checked": 6,
  "claims_verified": 4,
  "claims_ambiguous": 0,
  "claims_failed": [
    {
      "type": "function_name",
      "value": "OldAttributeProject",
      "reason": "not found in penfold repo — func OldAttributeProject( not present"
    }
  ],
  "claims_ambiguous_list": [],
  "timestamp": "2026-04-11T03:00:00Z"
}
```

Write the output to `/tmp/kb-factcheck-<timestamp>.json` and print the path to stdout:

```bash
TIMESTAMP=$(date +%s)
OUTPUT_FILE="/tmp/kb-factcheck-${TIMESTAMP}.json"
# ... build JSON ...
echo "$JSON" > "$OUTPUT_FILE"
echo "kb-factcheck result: $OUTPUT_FILE"
cat "$OUTPUT_FILE"
```

---

## Handling edge cases

- **Empty article**: if the article has no extractable claims, output `{ "verdict": "pass", "claims_checked": 0, ... }` with a note that no claims were found. Don't fail on empty claims.
- **DB unreachable**: if the DB connection fails, mark all SQL-based claims as `ambiguous` with reason "DB unreachable: <error>". Do not fail the verdict on DB errors — only on `not_found`.
- **Partial file path match**: if `git ls-files` returns multiple matches for a path, mark `ambiguous`.
- **No proto files**: if the penfold repo has no `.proto` files, mark all `rpc_name` claims as `ambiguous`.

---

## Full execution sequence

```
1. Read input file path from $1 (or prompt)
2. Read penfold repo path and DB DSN from config/env
3. Read article content from input file
4. Extract claims (Step A — LLM extraction, do this yourself)
5. For each claim, run the deterministic check (Step B)
6. Tally results and compute verdict (Step C)
7. Write JSON to /tmp/kb-factcheck-<timestamp>.json
8. Print output file path and the full JSON to stdout
9. Exit 0 if verdict=pass, exit 1 if verdict=fail
```

Exit codes:
- `0` — verdict `pass`
- `1` — verdict `fail` (one or more claims not found)
- `2` — input error (file not found, bad args)

---

## Example run

```bash
# Run with a specific proposed article
/kb-factcheck /tmp/kb-sync-pf-861f0c-1234567890.md

# Output:
# kb-factcheck result: /tmp/kb-factcheck-1744336800.json
# {
#   "verdict": "fail",
#   "claims_checked": 8,
#   "claims_verified": 7,
#   "claims_failed": [
#     {"type": "function_name", "value": "AttributeProject", "reason": "not found in penfold repo"}
#   ],
#   ...
# }
```

<!-- Add failure patterns here as they are discovered -->
