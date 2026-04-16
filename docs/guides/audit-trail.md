# Audit Trail

Every pipeline decision is recorded with structured data. Gate verdicts, review rounds, dispatch events, and completion evidence create a traceable history. You can reconstruct exactly what happened, when, and why for any design.

## Quick start

```bash
cobuild audit <design-id>       # show full gate timeline
cobuild show <design-id>        # pipeline state + metadata
cobuild status                  # overview across all active designs (ACTIVITY column shows dispatched/awaiting-transition/blocked)
```

## How it works

### What gets recorded

Every gate command creates a **review sub-shard** linked to the design. The sub-shard captures:

- **Timestamp** -- when the gate was evaluated
- **Gate name** -- which gate (e.g. `readiness-review`, `decomposition-review`)
- **Round number** -- iteration count (round 1, round 2, etc.)
- **Verdict** -- `pass` or `fail`
- **Body** -- the reviewer's findings, structured text
- **Structured fields** -- gate-specific data (e.g. readiness score 1-5)
- **Review shard ID** -- link to the full review sub-shard

Additionally, the pipeline metadata on the design shard tracks:

- Current phase and timestamps for each transition
- Task list with dispatch metadata (worktree, branch, PR URL, retries)
- Lock state (who holds it, when acquired)
- Evidence from completed tasks (commit hashes, files changed, PR URLs)

### The pipeline_gates table

Gate records are stored as review sub-shards via the connector. Each record includes:

| Field | Description |
|-------|-------------|
| `gate_name` | Name of the gate (e.g. `readiness-review`) |
| `round` | Iteration number (starts at 1, increments on retry) |
| `verdict` | `pass` or `fail` |
| `body` | Reviewer findings as text |
| `fields` | Structured data (e.g. `{"readiness": 4}`) |
| `created_at` | Timestamp |
| `shard_id` | ID of the review sub-shard |

### Review sub-shards

When a gate runs, `cobuild review`, `cobuild decompose`, or `cobuild gate` creates a child shard of type `review` linked to the design:

```
design-shard (pf-abc123)
  └── review sub-shard (pf-def456)  readiness-review, round 1, fail
  └── review sub-shard (pf-ghi789)  readiness-review, round 2, pass
  └── review sub-shard (pf-jkl012)  decomposition-review, round 1, pass
  └── review sub-shard (pf-mno345)  retrospective, round 1, pass
```

Each sub-shard contains the full review body, making it possible to read the reviewer's detailed reasoning.

### How to trace a decision

To understand why a design is in its current state:

1. **See the timeline:** `cobuild audit <design-id>` shows all gate records chronologically
2. **Read a specific review:** The audit output includes the review shard ID -- fetch it with `cobuild show <review-id>`
3. **Check pipeline state:** `cobuild show <design-id>` shows current phase, task list, and metadata
4. **View task evidence:** Each completed task has evidence in its metadata (commit hash, files changed, PR URL)

### How audit data feeds into insights

The `cobuild insights` command reads gate records to compute:

- **Pass rates** -- how often gates pass on the first try
- **Common failure reasons** -- text analysis of gate review bodies
- **Iteration counts** -- how many rounds each gate typically needs
- **Timing** -- how long each phase takes

This data drives the `cobuild improve` suggestions. For example, if readiness-review has a 40% first-pass rate and the review bodies frequently mention "missing success criteria," the improve command suggests strengthening the design skill.

## Configuration

The audit trail requires no special config -- it is built into every gate command. The only config that affects audit behavior is the gate definition itself:

```yaml
phases:
    design:
        gates:
            - name: readiness-review
              skill: design/gate-readiness-review
              fields:
                  readiness: {type: int, min: 1, max: 5, required: true}
```

The `fields` config determines what structured data is captured alongside the review body.

### Audit command

```bash
cobuild audit <design-id>
```

Shows the gate timeline with: timestamp, gate name, round, verdict, review shard ID, and body preview.

### Show command

```bash
cobuild show <design-id>
```

Shows the full pipeline state: current phase, task list, lock status, and metadata.

## Examples

### Example 1: Audit trail from a design with multiple review rounds

```bash
$ cobuild audit pf-abc123

Pipeline Audit: Config-driven contribution gating
==================================================

2024-11-15 09:12  readiness-review     round 1  FAIL  pf-rev001
  "Missing success criteria for backward compatibility. Technical
   approach does not specify error handling for invalid config."

2024-11-15 10:30  readiness-review     round 2  PASS  pf-rev002
  "All 5 readiness criteria met. Readiness: 4/5. Implementability
   check passed — code locations, schema, and error handling defined."

2024-11-15 11:15  decomposition-review round 1  PASS  pf-rev003
  "3 tasks with clean dependencies. Integration test task present
   (pf-task-int). All tasks have substantive content."

2024-11-16 14:00  retrospective        round 1  PASS  pf-rev004
  "Design completed in 28h. Readiness review caught missing error
   handling — good gate. Suggestion: add error handling checklist
   to create-design.md."
```

This shows:

- The readiness review failed first (round 1) due to missing criteria
- After revision, it passed on round 2 with a readiness score of 4/5
- Decomposition passed first try
- The retrospective captured a lesson about error handling

### Example 2: Tracing a specific review

From the audit output above, dig into the failed readiness review:

```bash
$ cobuild show pf-rev001

Type: review
Parent: pf-abc123
Gate: readiness-review
Round: 1
Verdict: fail

## Readiness Evaluation

### 1. Problem statement: PASS
Clear description of the current switch-statement pain point.

### 2. User/consumer: PASS
Developer experience is well-articulated.

### 3. Success criteria: FAIL
- "The system works correctly" is not testable
- No backward-compatibility criterion
- Missing performance baseline

### 4. Scope boundaries: PASS
Clear exclusions listed.

### 5. Technical approach: PARTIAL
- Code locations specified
- Schema defined
- ERROR: No error handling for invalid config values

### Implementability: FAIL
An implementing agent would need to ask about error handling
strategy and backward-compatibility requirements.

Readiness: 2/5
```

### Example 3: Task-level evidence

After a task completes, its metadata contains evidence:

```bash
$ cobuild show pf-task001

Task: Add StageExecutor interface
Status: closed
Branch: cobuild/pf-task001-stage-executor
PR: https://github.com/otherjamesbrown/penfold/pull/42
Commit: a1b2c3d
Files changed: 4
  - pipeline/executor.go (new)
  - pipeline/registry.go (modified)
  - pipeline/executor_test.go (new)
  - pipeline/registry_test.go (modified)
Dispatch retries: 0
Duration: 47m
```

This evidence links the task back to the exact code changes, PR, and commit. Combined with the gate audit trail on the parent design, you have full traceability from design decision through code delivery.

## Troubleshooting

**Audit shows no records:** Gate records are only created by `cobuild review`, `cobuild decompose`, and `cobuild gate` commands. If the pipeline was advanced manually (e.g. `cobuild update --phase implement`), no gate record exists. Always use gate commands for phase transitions.

**Review shard not found:** The review shard ID in the audit output should be fetchable with `cobuild show <id>`. If it returns not found, the shard may have been deleted. Gate records in pipeline metadata persist even if the review shard is removed.

**Missing evidence on completed tasks:** Evidence is appended by `cobuild complete <task-id>`. If the agent exited abnormally (crash, timeout), the complete command may not have run. Check `cobuild show <task-id>` for dispatch metadata and retry if needed with `cobuild complete <task-id>`.
