---
name: decompose-design
version: "0.1"
description: Break a design into implementable tasks with dependency ordering and wave assignment. Trigger after the readiness gate passes and the pipeline advances to the decompose phase.
summary: >-
  Breaks a design into small, independent tasks that agents can complete in a single session. Groups tasks into waves based on dependencies — wave 1 runs first, wave 2 depends on wave 1. Each task gets clear scope, acceptance criteria, and code locations.
---

# Skill: Decompose Design into Tasks

Break a design into discrete, implementable tasks that agents can complete independently in isolated worktrees.

## Input

- Design work item ID (pipeline must be in `decompose` phase)

## Step 1: Read the design

```bash
cobuild wi show <design-id>
```

Understand:
- What is being built
- What components are affected
- What the acceptance criteria are

## Step 2: Identify tasks

Break the design into tasks. Each task should be:

- **Completable in a single agent session** — if it would take multiple context windows, it's too big
- **Independently testable** — the agent can verify it worked without other tasks being done
- **Scoped to 1-5 files** — a task touching 10 files is probably multiple tasks
- **~100-300 lines of new/changed code** — larger tasks risk agent context overflow

### Common decomposition patterns

| Design element | Typical tasks |
|---------------|--------------|
| Database change | 1: Migration. 2: Model/types. 3: Repository methods. |
| New API endpoint | 1: Handler + route. 2: Business logic. 3: Tests. |
| Config change | 1: Schema + migration. 2: Config loading. 3: Wire into usage sites. |
| Refactor | 1: Extract interface/type. 2: Migrate callers. 3: Remove old code. |
| UI feature | 1: Component. 2: State management. 3: Integration + tests. |

### What makes a bad task

- "Implement the feature" — too vague, no clear scope
- "Fix everything" — unbounded
- A task that requires another task's PR to be merged first but doesn't declare the dependency
- A task with no acceptance criteria

## Step 3: Order by dependencies

Determine which tasks depend on which. Common patterns:
- Schema changes before code that uses the schema
- Types/interfaces before implementations
- Backend before frontend
- Config before usage

Create a dependency graph and assign **waves**:
- **Wave 1**: Tasks with no dependencies (can all run in parallel)
- **Wave 2**: Tasks that depend only on wave 1 tasks
- **Wave 3**: Tasks that depend on wave 2, etc.

## Step 4: Create task work items

For each task, create a work item with:

```bash
cobuild wi create --type task --title "<specific, action-oriented title>" --body "<task body>"
```

Each task body should include:

```markdown
## Scope
What files to create/modify and what changes to make.

## Acceptance Criteria
- [ ] Specific, verifiable criteria the agent can check
- [ ] Tests pass: <specific test command>
- [ ] Build passes: <build command>

## Code Locations
- `path/to/file.go` — what to change and why

## Wave
<wave number>

## Notes
Any context the implementing agent needs that isn't in the design.
```

## Step 5: Link tasks and set dependencies

```bash
# Link each task to the parent design
cobuild wi links add <task-id> <design-id> child-of

# Set blocked-by edges for dependencies
cobuild wi links add <task-id> <blocker-task-id> blocked-by
```

## Step 6: Record the decomposition

Append a summary to the design:

```bash
cobuild wi append <design-id> --body "## Decomposition

<N> tasks across <M> waves:

**Wave 1:**
- <task-id>: <title>

**Wave 2:**
- <task-id>: <title> (blocked by <blocker-id>)

..."
```

Then record the decomposition gate:

```bash
cobuild gate <design-id> decomposition-review --verdict pass --body "<summary of decomposition>"
```

## Gotchas

- Do not create tasks that depend on tasks in the same wave — that defeats parallel dispatch
- Every task must have verifiable acceptance criteria — "works correctly" is not verifiable
- If the design is too vague to decompose, fail the gate and report what's missing
- Prefer more smaller tasks over fewer larger ones — agent context is the constraint
- **Migration number collisions:** Parallel tasks in the same wave all branch from the same main. If multiple tasks create database migrations, assign non-colliding migration numbers explicitly in the task spec. Don't let agents pick their own numbers — they'll collide.
- **Hardcoded values:** If the project has a "config in DB" principle, task specs should explicitly state "read from config table" for any thresholds, limits, or timeouts. Agents default to hardcoding if the spec doesn't say otherwise.
<!-- Add failure patterns here as they're discovered -->
