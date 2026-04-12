# KB Sync — Automatic Knowledge Base Maintenance

KB sync keeps your project's Knowledge Base articles up to date as code changes land. After `cobuild process-review` merges a PR, it automatically runs `cobuild kb-sync` to find and update any KB articles affected by the change.

## How it works

```
PR merged → process-review → cobuild kb-sync → affected articles updated
```

The sync runs a 3-layer verification pipeline on each affected article:

1. **Layer 0 (Writer)** — An LLM reads the PR diff + task spec and proposes an update to the article content
2. **Layer 1 (Factcheck)** — Deterministic verification: checks claims against the codebase (file paths exist, function signatures match, config keys are real)
3. **Layer 2 (Judge)** — A *different* LLM family reviews the proposed update for semantic correctness and coherence

If all layers pass, the article is updated via `cxp kb update`. If any layer fails, the change is rolled back and a gap is logged to the project's `<prefix>-kb-gaps` shard for manual review.

The gate verdict is **non-blocking** — even if all articles fail verification, the pipeline still advances to done. KB quality is tracked, not enforced.

## Enabling kb-sync for your project

Add to your project's `.cobuild/pipeline.yaml`:

```yaml
kb_sync:
    enabled: true
    root_article: pf-5ae167    # optional — see "Scoping" below
```

That's it. The next `cobuild process-review` that merges a PR will trigger kb-sync.

## Scoping with root_article

By default, kb-sync searches **all** KB articles in your project to find ones affected by the change. This works well for projects with a flat collection of articles.

If your project has a hierarchical KB with a root article that links to children (via `child-of` edges), set `root_article` to scope the search:

```yaml
kb_sync:
    enabled: true
    root_article: cb-5ae167    # only sync articles that are children of this root
```

This prevents kb-sync from touching unrelated knowledge shards (reports, retrospectives, gate records) that happen to share the project namespace.

**When to use root_article:**
- Your project has a dedicated KB root with child articles (like cobuild's cb-5ae167 → 5 reference articles)
- You have many shards of mixed types and want to limit what kb-sync can touch

**When to leave it empty:**
- Your KB articles are a flat collection with no root (like penfold's 20 topic-specific articles)
- All knowledge-type shards in your project are legitimate KB articles

## What you need before enabling

### 1. KB articles must exist

kb-sync updates existing articles — it doesn't create new ones. If your project has no KB articles, there's nothing to sync. Create your foundational articles first:

```bash
cxp shard create --type knowledge --project <your-project> --title "Architecture Overview" --body "..."
```

### 2. Model routing rules (recommended)

kb-sync uses LLM calls for the writer (Layer 0) and judge (Layer 2) steps. For cross-model verification to work, these should use **different model families**:

- Writer (kb_sync task type) → e.g. Gemini
- Judge (kb_judge task type) → e.g. Claude

If both use the same model family, `cobuild kb-sync` will refuse to run (same-model review is blind to its own errors). Configure routing in your AI routing rules or pipeline config.

If you don't have model routing set up, kb-sync will warn but still attempt the sync with whatever model is available.

### 3. A gaps tracker shard (recommended)

When verification fails, kb-sync logs the failure to `<prefix>-kb-gaps` (e.g. `pf-kb-gaps`). Create this shard if you want to track what's failing:

```bash
cxp shard create --type knowledge --project <your-project> --id <prefix>-kb-gaps --title "KB Gaps Tracker" --body "# KB Gaps\n\nAutomatic log of kb-sync verification failures."
```

If the shard doesn't exist, gaps are logged to stdout only.

## Configuration reference

```yaml
kb_sync:
    # Enable automatic kb-sync after PR merges (default: false)
    enabled: true

    # Root KB article — scope article search to children of this shard.
    # Empty = search all KB articles in the project. (default: "")
    root_article: ""
```

## CLI usage

kb-sync also works as a standalone command:

```bash
# Sync KB articles affected by a specific work item's merged PR
cobuild kb-sync <work-item-id>

# Dry run — show what would be updated
cobuild kb-sync <work-item-id> --dry-run

# Override root article from CLI (takes precedence over config)
cobuild kb-sync <work-item-id> --root cb-5ae167
```

## Current project status

| Project | Enabled | Root Article | Notes |
|---------|---------|-------------|-------|
| cobuild | yes | cb-5ae167 | 5 reference articles under root |
| penfold | yes | — | 20 topic-specific articles, flat |
| context-palace | no | — | No KB articles yet |
| penf-cli | no | — | No KB articles yet |
| mycroft | no | — | CoBuild not onboarded yet |

## Troubleshooting

**"no concepts extractable from PR diff"** — The PR changes were too small or too mechanical (formatting, imports) for the concept extractor to find KB-relevant content. This is normal and not a failure.

**"kb_sync and kb_judge use the same model family"** — Both routing rules point to the same LLM family. Change one so writer and judge use different families for cross-model verification.

**"no affected KB articles found"** — The concept search didn't match any KB articles. Either the change genuinely doesn't affect any articles, or the articles' content doesn't contain terms similar to the changed code. Consider adding more specific technical terms to your KB articles.

**kb-sync runs but articles aren't updated** — Check the gaps tracker shard. Layer 1 (factcheck) or Layer 2 (judge) may be rejecting the proposed updates. Common causes: the proposed update references files that don't exist, or the semantic reviewer found inconsistencies with other articles.
