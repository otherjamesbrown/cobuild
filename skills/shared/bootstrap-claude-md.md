---
name: bootstrap-claude-md
description: Generate .cobuild/AGENTS.md with pipeline instructions and add a CoBuild pointer to CLAUDE.md. Final bootstrap step.
---

# Skill: Generate CoBuild Agent Instructions

Create `.cobuild/AGENTS.md` with full pipeline instructions for agents, and add a pointer to it in the repo's CLAUDE.md. This is the final bootstrap step — all configuration must be complete before running this.

---

## Why Two Files?

- **CLAUDE.md** stays concise — a brief mention that this project uses CoBuild, plus a pointer
- **`.cobuild/AGENTS.md`** has the full instructions — commands, protocols, skills reference
- Agents read both (CLAUDE.md loads first, then follows the pointer)
- When CoBuild config changes, update one file instead of digging through CLAUDE.md

---

## Step 1: Generate .cobuild/AGENTS.md

Using the completed pipeline config (`.cobuild/pipeline.yaml`) and project identity (`.cobuild.yaml`), generate the full agent instructions file.

### Template

```markdown
# CoBuild Pipeline Instructions

This project uses CoBuild for pipeline automation. If you are an agent working on a task dispatched by CoBuild, follow these instructions.

## Project

- **Name:** <project-name>
- **Connector:** <context-palace|beads>
- **Workflows:**
  - design: design → decompose → implement → review → done
  - bug: fix → review → done (default; single-session investigate+implement)
  - bug-complex: investigate → implement → review → done (label `needs-investigation` to escalate)
  - task: implement → review → done

<if multi-repo>
## Multi-Repo

Designs may span these repos: <list of repos>

Tasks are tagged with their target repo during decomposition. Your worktree is already set to the correct repo for your task.
</if>

## Commands

### Pipeline

| Command | When to use |
|---------|------------|
| `cobuild show <id>` | See pipeline state for a design |
| `cobuild complete <task-id>` | **Run as your LAST action** after implementing a task |
| `cobuild gate <id> <gate> --verdict pass\|fail --body "..."` | Record a gate verdict |
| `cobuild audit <id>` | View gate history for a design |

### Work Items

| Command | Purpose |
|---------|---------|
| `cobuild wi show <id>` | Read a design, task, or bug |
| `cobuild wi list --type <type>` | List work items |
| `cobuild wi links <id>` | See relationships (child-of, blocked-by) |
| `cobuild wi status <id> <status>` | Update work item status |
| `cobuild wi append <id> --body "..."` | Append content to a work item |
| `cobuild wi create --type <type> --title "..."` | Create a new work item |

## Task Completion Protocol

When you have completed your implementation:

1. Run tests: `<test commands from pipeline.yaml>`
2. Run build: `<build commands from pipeline.yaml>`
3. **Run `cobuild complete <task-id>`**

This commits remaining changes, pushes your branch, creates a PR, appends evidence to the work item, and marks the task as needs-review.

**Do this as your LAST action. Do not skip it.**

## What CoBuild manages vs what you do directly

Be explicit when reporting status to the developer. State clearly whether an action is:
- **A CoBuild pipeline action** — "CoBuild will handle this: `cobuild merge-design <id>`"
- **A direct action you'll take** — "I'll run `penf deploy gateway` now"
- **A human action needed** — "You need to approve this PR before CoBuild can merge"

Do not say vague things like "ready for deployment" — say exactly who does what:
- "All PRs merged. Running `cobuild merge-design pf-6e38e9 --auto` to merge and test."
- "Merge complete. Deploy is not managed by CoBuild for this project — run `penf deploy all` to deploy."
- "Deploy is configured in pipeline.yaml. Running `cobuild deploy pf-6e38e9` to deploy affected services."

## Skills

Pipeline skills are in `skills/` organized by phase:

| Directory | Skills | Purpose |
|-----------|--------|---------|
| `design/` | gate-readiness-review, implementability | Design evaluation |
| `decompose/` | decompose-design | Break designs into tasks |
| `fix/` | fix-bug | Single-session bug fix (default bug workflow) |
| `investigate/` | bug-investigation | Root cause analysis (needs-investigation escalation) |
| `implement/` | dispatch-task, stall-check | Task dispatch and monitoring |
| `review/` | gate-review-pr, gate-process-review, merge-and-verify | Code review |
| `done/` | gate-retrospective | Post-delivery retrospective |
| `shared/` | playbook, create-design, design-review | Cross-phase reference |

## Context

Architecture and project context is in `.cobuild/context/`:
<list the context files that were created>

These are assembled into your CLAUDE.md at dispatch time via context layers.
```

---

## Step 2: Update CLAUDE.md

Add a CoBuild pointer section to CLAUDE.md. Keep it short — just enough for an agent to know CoBuild exists and where to find the full instructions.

**If CLAUDE.md exists:** Append this section at an appropriate location (near the top, after any project description):

```markdown
## CoBuild

This project uses [CoBuild](https://github.com/otherjamesbrown/cobuild) for pipeline automation — designs flow through structured phases (design → decompose → implement → review → done) with quality gates.

**Read `.cobuild/AGENTS.md` for full pipeline instructions, commands, and task completion protocol.**
```

**If CLAUDE.md doesn't exist:** Create one:

```markdown
# <Project Name>

<brief description from README>

## CoBuild

This project uses [CoBuild](https://github.com/otherjamesbrown/cobuild) for pipeline automation — designs flow through structured phases (design → decompose → implement → review → done) with quality gates.

**Read `.cobuild/AGENTS.md` for full pipeline instructions, commands, and task completion protocol.**
```

---

## Step 3: Review

> I've created `.cobuild/AGENTS.md` with pipeline instructions and added a pointer in CLAUDE.md.
>
> Please review:
> - Are the build/test commands correct in the completion protocol?
> - Is there anything project-specific that agents should know?
> - Should any existing CLAUDE.md content be adjusted?

---

## Verification

- [ ] `.cobuild/AGENTS.md` exists with full pipeline instructions
- [ ] CLAUDE.md contains a CoBuild section pointing to `.cobuild/AGENTS.md`
- [ ] Commands match the configured connector (cxp vs bd)
- [ ] Task completion protocol has correct build/test commands
- [ ] Multi-repo context included if applicable
- [ ] Developer has reviewed both files

## Gotchas

<!-- Add failure patterns here as they're discovered -->

## Final step

When `.cobuild/AGENTS.md` and the CLAUDE.md pointer are in place and verified, stop here. Do not run `cobuild complete` from this bootstrap skill. Exit the session with `/exit`.
