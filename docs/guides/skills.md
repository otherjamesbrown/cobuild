# Skills as Markdown

Skills are markdown files that tell agents what to do. They define procedures, evaluation criteria, and decision trees. You extend the pipeline by writing a `.md` file and referencing it in config.

## Quick start

```bash
cobuild init-skills              # copy default skills into your repo
ls skills/                       # see what was installed
# Edit any skill, or create a new one in skills/
```

## How it works

A skill is a markdown file in your repo's `skills/` directory (or `~/.cobuild/skills/` for global defaults). When a gate references a skill, the orchestrator loads that file as instructions for the evaluation.

Skills are referenced in two places:

1. **Gate config** -- the `skill` field tells the gate which procedure to follow
2. **Context layers** -- the `skills:<name>` source injects a skill into agent context

### Skill resolution chain

When CoBuild resolves a skill path (e.g. `design/gate-readiness-review`):

1. `<repo>/<skills_dir>/design/gate-readiness-review.md` -- repo-level (checked first)
2. `~/.cobuild/skills/design/gate-readiness-review.md` -- global fallback

The `skills_dir` defaults to `skills` but is configurable in `pipeline.yaml`:

```yaml
skills_dir: skills    # relative to repo root
```

This means you can override any default skill by placing a file with the same name in your repo's skills directory.

### How skills are referenced in config

```yaml
phases:
    - name: design
      gates:
          - name: readiness-review
            skill: design/gate-readiness-review
    - name: done
      gates:
          - name: retrospective
            skill: done/gate-retrospective
```

### Overriding a default skill

To customize the readiness check for your project:

```bash
cobuild init-skills                        # install defaults
# Edit skills/design/gate-readiness-review.md with your project-specific criteria
```

Your repo version takes priority. The global version is untouched.

## Configuration

### Default skills installed by `cobuild init-skills`

Skills are organized by phase. Gate skills are prefixed with `gate-`.

| File | Phase | Purpose |
|------|-------|---------|
| `shared/create-design.md` | — | Design authoring guide |
| `shared/playbook.md` | — | Orchestrator decision trees and phase rules |
| `design/gate-readiness-review.md` | design | Gate: readiness + implementability evaluation |
| `design/implementability.md` | design | Implementability criteria reference |
| `implement/dispatch-task.md` | implement | Task dispatch procedure |
| `implement/stall-check.md` | implement | Stall diagnosis for stuck agents |
| `review/gate-review-pr.md` | review | Gate: PR review (agent strategy) |
| `review/gate-process-review.md` | review | Gate: process external reviewer output |
| `review/merge-and-verify.md` | review | Merge + post-merge verification |
| `done/gate-retrospective.md` | done | Gate: post-delivery retrospective |

### Flags

```bash
cobuild init-skills --force      # overwrite existing skills
cobuild init-skills --dry-run    # show what would be copied
```

## Examples

### Example 1: create-design.md (agent-facing skill)

This skill tells agents how to author designs that pass the readiness review. Key structural elements:

```markdown
# Skill: Create a Design Shard

When creating a design shard, follow this structure. Designs that don't
meet these criteria will be sent back by the pipeline readiness check.

**Evaluated by:** `skills/design/gate-readiness-review.md`

---

## Required Sections

### 1. Problem
What is broken, missing, or painful? Be concrete:
- Reference specific files, functions, or line numbers
- Show current behavior vs desired behavior

### 2. User / Consumer
Who benefits from this change?

### 3. Success Criteria
Measurable, verifiable conditions. An agent should be able to write
a test for each one.

### 4. Scope Boundaries
What is explicitly not included.

### 5. Technical Approach
Architecture, code locations, data model, API surface, dependencies.

### 6. Migration / Rollout
How does this get deployed without breaking things?

---

## Implementability Test

> Could an implementing agent write code from this design without
> asking me any questions?
```

Notice the structure: a clear title, a cross-reference to the evaluating skill, required sections with good/bad examples, and a self-test at the end.

### Example 2: shared/playbook.md (orchestrator skill)

The playbook is a decision-tree skill. It defines what M does at each phase:

```markdown
# M Playbook — Pipeline Orchestration

You are **M**, an ephemeral orchestrator. You read a pipeline shard,
take one action, update state, and exit.

## Startup
1. Read the pipeline: `cobuild show <id>`
2. Determine the shard type and current phase
3. Lock the pipeline: `cobuild pipeline lock <id>`
4. Follow the decision tree for the current phase
5. Unlock when done

## Phase Routing
pipeline.phase = ?
  "design"     → Phase 1 (Design Readiness)
  "decompose"  → Phase 2 (Decomposition)
  "implement"  → Phase 3 (Dispatch & Monitor)
  ...
```

The pattern: identity statement, startup sequence, then branching logic per phase with exact commands to run.

### Example 3: Writing a custom skill

Say you want a security review gate. Create `skills/security/gate-security-check.md`:

```markdown
# Skill: Security Review

Evaluate the PR for security concerns before merging.

## Procedure

1. Read the PR diff: `gh pr diff <number>`
2. Check for:
   - Hardcoded secrets or credentials
   - SQL injection vectors (string concatenation in queries)
   - Missing input validation on API endpoints
   - Exposed internal error details in responses
3. Record verdict:
   ```bash
   cobuild gate <design-id> security-review \
     --verdict pass|fail --body "<findings>"
   ```

## Pass criteria
- No hardcoded secrets
- All database queries use parameterized statements
- API inputs are validated before use
```

Then reference it in config:

```yaml
phases:
    - name: security
      gates:
          - name: security-review
            skill: m-security-check
            model: haiku
```

## Troubleshooting

**Skill not found:** CoBuild checks `<repo>/skills/<name>.md` first, then `~/.cobuild/skills/<name>.md`. Verify the file exists at one of these paths. Check that `skills_dir` in your config matches your actual directory name.

**Skill not loading during gate:** The gate config must have a `skill` field matching the path (without `.md`). For example, `skill: design/gate-readiness-review` loads `skills/design/gate-readiness-review.md`.

**Changes not taking effect:** Skills are read fresh each time they are referenced. There is no caching. If your changes are not visible, verify you are editing the correct file (repo vs global). The repo version takes priority.
