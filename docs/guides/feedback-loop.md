# Self-Improving Pipeline

CoBuild includes a feedback loop that analyzes pipeline execution, detects patterns, and suggests improvements. The vision: every design that runs through the pipeline makes the pipeline better for the next one.

## Quick start

```bash
cobuild insights                 # see execution analysis
cobuild improve                  # get improvement suggestions
cobuild improve --apply          # auto-apply non-skill changes
```

## How it works

The feedback loop has three components that build on each other:

### 1. Insights -- execution analysis

`cobuild insights` reads pipeline execution data (gate records, task metadata, timing) and produces a structured report.

```bash
cobuild insights
cobuild insights --project penfold
cobuild insights -o json
```

The report includes:

- **Overview** -- total designs, tasks, PRs processed
- **Gate pass rates** -- first-try pass percentage per gate (e.g. "readiness-review: 60% first-pass")
- **Common failure reasons** -- extracted from gate review bodies (e.g. "missing success criteria", "vague technical approach")
- **Agent performance** -- task completion rates, PR creation, timing per agent
- **Friction points** -- detected patterns that slow the pipeline
- **Suggested improvements** -- data-driven recommendations

Example output:

```
Pipeline Insights (penfold)
===========================

Overview: 4 designs, 18 tasks, 15 PRs merged

Gate Pass Rates:
  readiness-review:     60% first-pass (3/5)
  decomposition-review: 80% first-pass (4/5)
  retrospective:        100% (3/3)

Common Failure Reasons:
  readiness-review:
    - Missing success criteria (2 occurrences)
    - Vague technical approach (1 occurrence)

Friction Points:
  - 3 tasks stalled > 30m (agent-steve, implement phase)
  - 2 PRs required 3+ review rounds

Suggestions:
  - Strengthen create-design.md with success criteria examples
  - Add architecture context layer for implement phase
```

### 2. Improve -- pattern-based suggestions

`cobuild improve` takes the insights data and proposes specific changes to skills, config, and process files.

```bash
cobuild improve                  # print suggestions
cobuild improve --apply          # auto-apply config/process changes
cobuild improve -o json          # machine-readable output
```

Detected patterns and their suggestions:

| Pattern | Detection | Suggestion |
|---------|-----------|------------|
| Low readiness-review pass rate | < 70% first-pass | Strengthen `shared/create-design.md` with common failure examples |
| Missing model configuration | No per-phase models | Add haiku for judgment, sonnet for creation |
| No monitoring configured | Empty `monitoring:` section | Add health check config with defaults |
| Missing integration test gate | No `requires_label` on decompose gate | Add `requires_label: integration-test` |
| Skills not initialized | No skills directory | Suggest `cobuild init-skills` |

The `--apply` flag auto-applies config and process changes. Skill changes always require human review because they affect agent behavior.

### 3. Retrospective -- post-delivery lessons

The `done` phase has a `retrospective` gate (skill `done/gate-retrospective`, model `haiku`) that runs after a design is fully delivered.

The retrospective:

1. Reviews the full audit trail: `cobuild audit <id>`
2. Analyzes insights data: `cobuild insights`
3. Identifies what went well and what caused friction
4. Records findings as a knowledge shard for future reference
5. Runs `cobuild improve` to generate actionable changes

Example retrospective findings:

```
Retrospective: Config-driven contribution gating
=================================================

What went well:
- Decomposition produced clean task boundaries (0 circular deps)
- All 5 tasks completed in < 2 hours each
- CI caught a test regression before merge

What caused friction:
- Readiness review failed first attempt (missing migration section)
- Agent-steve stalled on task 3 (missing context about deploy paths)

Actions:
- create-design.md: add migration section to required checklist
- Context layer: add deploy.md to dispatch mode
```

## Configuration

The feedback loop requires no special config -- it reads from existing pipeline data. The retrospective gate is configured as part of the `done` phase:

```yaml
phases:
    done:
        gates:
            - name: retrospective
              skill: done/gate-retrospective
              model: haiku
```

### Project filtering

```bash
cobuild insights --project penfold    # single project
cobuild insights                      # current project (from working directory)
```

### Output formats

```bash
cobuild insights -o json              # JSON for scripting
cobuild improve -o json               # JSON improvement proposals
```

## Examples

### Example 1: Detecting a weak design skill

After 5 designs, insights shows readiness-review has a 40% first-pass rate. The most common failure is "missing success criteria."

```bash
$ cobuild improve

Improvement suggestions:
1. [skill] create-design.md: Add explicit success criteria examples
   Pattern: 3/5 readiness failures cite "missing success criteria"
   Action: Add 2-3 concrete examples to the Success Criteria section
   (requires manual review)

2. [config] monitoring.stall_timeout: Reduce from 30m to 20m
   Pattern: 3 stalls detected, all resolved within 20m
   Action: Update .cobuild/pipeline.yaml
   (auto-applicable with --apply)
```

Run `cobuild improve --apply` to apply the config change. Then manually edit `skills/shared/create-design.md` to add the success criteria examples.

### Example 2: Full feedback cycle

```bash
# 1. Run insights after a few designs complete
cobuild insights --project penfold

# 2. Get improvement suggestions
cobuild improve

# 3. Apply safe changes automatically
cobuild improve --apply

# 4. Review and manually apply skill changes
# Edit skills/shared/create-design.md based on suggestions

# 5. Next design benefits from the improvements
cobuild init <new-design-id>
```

### Example 3: Retrospective as part of pipeline

When the last task in a design merges and the pipeline reaches `done`:

```bash
# M runs the retrospective gate automatically
cobuild gate <design-id> retrospective --verdict pass \
  --body "## Retrospective

  Readiness: passed first try (score 4/5)
  Decomposition: 3 tasks, clean deps
  Implementation: all tasks < 90min, 0 stalls
  Review: 1 PR needed revision (missing test for edge case)

  Improvement: add edge-case testing reminder to dispatch-completion.md"
```

The retrospective verdict is recorded in the audit trail. The suggestion feeds into the next `cobuild improve` run.

## Troubleshooting

**Insights shows no data:** The pipeline needs completed gate records to analyze. Run at least one design through the full pipeline before expecting meaningful insights.

**Improve suggests changes already applied:** The improve command analyzes current config against patterns. If you applied a change manually but the pattern still matches (e.g. pass rate has not improved yet), it may suggest the same change. Run more designs to update the data.

**Retrospective gate not running:** Check that the `done` phase has a `retrospective` gate configured. The gate must be triggered explicitly with `cobuild gate <id> retrospective` or by M following the playbook.
