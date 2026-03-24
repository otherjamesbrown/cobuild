# Skill: Configure Pipeline Skills

Copy and customize pipeline skills for a CoBuild project. Called from the main bootstrap or run independently.

---

## Step 1: Copy Default Skills

```bash
cobuild init-skills
```

This creates the `skills/` directory organized by phase:

```
skills/
    design/       gate-readiness-review.md, implementability.md
    decompose/    (empty — gate logic is in the playbook)
    implement/    dispatch-task.md, stall-check.md
    review/       gate-review-pr.md, gate-process-review.md, merge-and-verify.md
    done/         gate-retrospective.md
    shared/       playbook.md, create-design.md, bootstrap.md, bootstrap-*.md
```

If skills already exist (from a previous setup), ask:

> Skills directory already exists. Should I overwrite with fresh defaults?
> Use `cobuild init-skills --force` to overwrite, or skip to keep existing customizations.

---

## Step 2: Customize for This Project

Each skill has generic defaults that should be tailored. Walk through the key ones:

### shared/create-design.md

This tells agents how to write designs that pass the readiness review. Customize:
- Add project-specific required sections (e.g., "Migration Plan" for a database project)
- Add examples of good designs from this project
- Reference the project's architecture doc

> Does this project have specific requirements for design documents beyond the defaults?
> For example: migration plans, API compatibility checks, performance budgets.

### design/gate-readiness-review.md

This defines what "ready to implement" means. Customize:
- Adjust readiness criteria for the project's domain
- Add project-specific implementability checks (e.g., "does the design reference actual file paths in this codebase?")

### shared/playbook.md

The orchestrator's decision tree. Customize:
- Update agent routing — which agent handles which domains
- Add project-specific phase rules if needed
- Update command references if the project uses non-standard patterns

> Which agent should handle which types of tasks in this project?
>
> Available agents: (list from bootstrap config)
>
> Common routing:
> - Backend/API changes → agent-mycroft
> - CLI/migrations → agent-steve
> - Tests → agent-mycroft

---

## Step 3: Review Strategy Skills

Based on the review strategy chosen during bootstrap:

**If external (e.g., Gemini):**
- Review `review/gate-process-review.md` — this processes external reviewer output
- Customize: what counts as an approval vs. a request for changes from the external reviewer

**If agent-based:**
- Review `review/gate-review-pr.md` — this is the agent's review procedure
- Customize: what the reviewing agent should focus on (tests, patterns, security, etc.)

---

## Step 4: Verify Skills Load

Check that skill references in the pipeline config resolve correctly:

```bash
# These skill paths should exist:
ls skills/design/gate-readiness-review.md
ls skills/done/gate-retrospective.md
ls skills/shared/playbook.md
ls skills/review/gate-review-pr.md      # if agent review strategy
ls skills/review/gate-process-review.md # if external review strategy
ls skills/implement/stall-check.md
```

---

## Verification Checklist

- [ ] `cobuild init-skills` ran successfully
- [ ] `skills/` directory has all phase subfolders
- [ ] `shared/create-design.md` reviewed and customized
- [ ] `design/gate-readiness-review.md` reviewed and customized
- [ ] `shared/playbook.md` agent routing updated
- [ ] Review strategy skills reviewed
- [ ] All skill paths referenced in pipeline.yaml resolve to existing files
