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

When CoBuild resolves a skill name (e.g. `m-readiness-check`):

1. `<repo>/<skills_dir>/m-readiness-check.md` -- repo-level (checked first)
2. `~/.cobuild/skills/m-readiness-check.md` -- global fallback

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
            skill: m-readiness-check       # loads skills/m-readiness-check.md
    - name: done
      gates:
          - name: retrospective
            skill: m-retrospective         # loads skills/m-retrospective.md
```

### Overriding a default skill

To customize the readiness check for your project:

```bash
cobuild init-skills                        # install defaults
# Edit skills/m-readiness-check.md with your project-specific criteria
```

Your repo version takes priority. The global version is untouched.

## Configuration

### Default skills installed by `cobuild init-skills`

| File | Purpose | Used by |
|------|---------|---------|
| `create-design.md` | Design authoring guide | Any agent creating designs |
| `m-playbook.md` | Orchestrator decision trees and phase rules | M (orchestrator) |
| `m-readiness-check.md` | Phase 1 readiness + implementability evaluation | readiness-review gate |
| `m-implementability.md` | Implementability criteria reference | M |
| `m-dispatch-task.md` | Phase 3 task dispatch procedure | M |
| `m-review-pr.md` | Phase 4 PR review (agent strategy) | review gate |
| `m-process-pr-review.md` | Process external reviewer output | review gate (external) |
| `m-merge-and-verify.md` | Merge + post-merge verification | M |
| `m-stall-check.md` | Stall diagnosis for stuck agents | monitoring on_stall |
| `m-retrospective.md` | Post-delivery retrospective | retrospective gate |

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

**Evaluated by:** `skills/m-readiness-check.md`

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

### Example 2: m-playbook.md (orchestrator skill)

The playbook is a decision-tree skill. It defines what M does at each phase:

```markdown
# M Playbook — Pipeline Orchestration

You are **M**, an ephemeral orchestrator. You read a pipeline shard,
take one action, update state, and exit.

## Startup
1. Read the pipeline shard: `cobuild pipeline show <id>`
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

Say you want a security review gate. Create `skills/m-security-check.md`:

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

**Skill not loading during gate:** The gate config must have a `skill` field matching the filename (without `.md`). For example, `skill: m-readiness-check` loads `m-readiness-check.md`.

**Changes not taking effect:** Skills are read fresh each time they are referenced. There is no caching. If your changes are not visible, verify you are editing the correct file (repo vs global). The repo version takes priority.
