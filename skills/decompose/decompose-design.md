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
- **Target exactly one repo** — one task maps to one worktree and one PR; if the design spans multiple repos, split it into separate tasks and link them with `blocked-by` when sequencing matters

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
- A task that edits files in more than one repo
- A task with no acceptance criteria

### Mandatory: Test infrastructure task (Wave 1)

If the project doesn't already have shared test infrastructure for the area being changed, create a **Wave 1 task** for it. This runs before any implementing agent starts writing tests.

The test infrastructure task should create:
- **Shared fixtures** (conftest.py, test helpers, factory functions)
- **Test database setup/teardown** (if integration tests need a real DB)
- **Mock vs live flags** (for tests that can run with or without external services)
- **E2E test skeleton** (if the design's test strategy includes e2e tests)

Check: `ls tests/ conftest.py test_*.py *_test.go` — if test infrastructure already exists for this area, skip this task.

Without this: each agent independently invents its own mocks, fixtures, and patterns. The result is 8 test files that all mock differently and catch zero integration bugs.

## Step 3: Order by dependencies

Determine which tasks depend on which. Common patterns:
- **Test infrastructure** before any implementation tasks (Wave 1)
- Schema changes before code that uses the schema
- Types/interfaces before implementations
- Backend before frontend
- Config before usage

Create a dependency graph and assign **waves**:
- **Wave 1**: Tasks with no dependencies (can all run in parallel)
- **Wave 2**: Tasks that depend only on wave 1 tasks
- **Wave 3**: Tasks that depend on wave 2, etc.

## Multi-repo designs

Designs may span multiple repos. Tasks must not.

Use this rule: first map each spec or file reference in the design to a target `(project, repo)`, then create one task per repo-sized unit of work. If one repo's change depends on another repo landing first, express that with a `blocked-by` edge instead of combining both repos into one task.

Be explicit about ownership:

- **`--project <name>`** sets the shard's home project. Use it when the task should live in a different project's backlog or use a different shard namespace than the parent design.
- **`repo` metadata** sets the git repo `cobuild dispatch` will open a worktree in. Use it whenever the task's code changes belong in a repo that is not the current repo default.

A task may need both. A penfold-owned task that edits the `penfold` repo should be created with `--project penfold` and `repo=penfold`. A context-palace-owned coordination task that edits the `penf-cli` repo would usually keep the default project but set `repo=penf-cli`.

Worked example:

If a design says:

```text
Update penfold/X.go to emit the new payload, then update context-palace/Y.go to consume it.
```

The correct decomposition is two single-repo tasks, not one combined task:

```bash
# Task 1: penfold repo change
cobuild wi create --project penfold --type task --title "Emit new payload from X.go" --body "<scope for penfold/X.go>" --parent <design-id>
cxp shard metadata set <task-penfold> repo penfold

# Task 2: context-palace repo change
cobuild wi create --type task --title "Consume new payload in Y.go" --body "<scope for context-palace/Y.go>" --parent <design-id>
cxp shard metadata set <task-context-palace> repo context-palace

# Sequence them if Y.go depends on X.go shipping first
cobuild wi links add <task-context-palace> <task-penfold> blocked-by
```

The second task only gets the `blocked-by` edge when the dependency is real. If both repo changes can be implemented and verified independently, keep them in the same wave with no dependency edge.

## Step 4: Create task work items

For each task, create a work item with:

```bash
cobuild wi create --project <target-project-if-needed> --type task --title "<specific, action-oriented title>" --body "<task body>"
cxp shard metadata set <task-id> repo <target-repo>
```

Set `--project` when the task belongs to a different project's backlog than the parent design. Set `repo` metadata for every task whose code lands in a different repo than the current repo default. For cross-project, cross-repo work, set both.

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

## Step 7: Verify context layers and anatomy before dispatching

Before recording the decomposition gate, check that dispatched agents will have the context they need. Agents in worktrees only see what's configured — they don't have your CLAUDE.md or conversation history.

**Refresh the project anatomy:**
```bash
cobuild scan
```
This generates `.cobuild/context/always/anatomy.md` — a file index with descriptions and token estimates. Agents use it to understand the codebase structure without reading every file. Run this before dispatching so agents have a current map.

**Check always-on context:**
```bash
ls .cobuild/context/always/ 2>/dev/null
```

If this directory is empty or doesn't exist, dispatched agents will only see their task prompt and parent design — no architecture reference, no coding conventions, no project constraints.

**Minimum context for most projects:**

| File | What it contains | Lines |
|------|-----------------|-------|
| `.cobuild/context/always/architecture.md` | Core objects, relationships, data flow, hard constraints, project layout | ~200 |
| `.cobuild/context/implement/coding-patterns.md` | DB patterns, CLI conventions, error handling, naming | ~80 |

**Check for oversized context:**

If the project has a large spec or architecture doc (500+ lines), do NOT point agents at it directly. Create a concise reference (~200-300 lines) that covers what an implementing agent needs. Full specs waste context window and degrade agent performance.

```bash
# Check if architecture doc exists and its size
wc -l ARCHITECTURE.md .cobuild/context/always/*.md 2>/dev/null
```

**If context is missing, create it now** — before recording the decomposition gate. Tasks will be dispatched after decomposition passes, and agents need context to implement correctly.

**Report in the gate verdict:**
```
Context check:
  ✓ .cobuild/context/always/architecture.md (185 lines)
  ✓ .cobuild/context/implement/coding-patterns.md (82 lines)
  ⚠ No investigate-phase context — bug investigators won't have operational context
```

## Step 8: Record the decomposition gate

```bash
cobuild gate <design-id> decomposition-review --verdict pass --body "<summary of decomposition + context check>"
```

## Gotchas

- Do not create tasks that depend on tasks in the same wave — that defeats parallel dispatch
- Every task must have verifiable acceptance criteria — "works correctly" is not verifiable
- If the design is too vague to decompose, fail the gate and report what's missing
- Prefer more smaller tasks over fewer larger ones — agent context is the constraint
- **Migration number collisions:** Parallel tasks in the same wave all branch from the same main. If multiple tasks create database migrations, assign non-colliding migration numbers explicitly in the task spec. Don't let agents pick their own numbers — they'll collide.
- **Hardcoded values:** If the project has a "config in DB" principle, task specs should explicitly state "read from config table" for any thresholds, limits, or timeouts. Agents default to hardcoding if the spec doesn't say otherwise.
- **Missing context layers:** If `.cobuild/context/always/` is empty, stop and create an architecture reference before dispatching. Agents without context produce incorrect code that doesn't fit the project. A 200-line architecture doc saves hours of re-work.
- **Oversized context:** If pointing at a 1000+ line spec, agents waste context window and produce shallow work. Create a concise reference. The full spec is for humans — the agent reference is for agents.

## Final Step

After you have created the tasks, recorded the gate verdict, and finished all required notes, exit the session immediately. Run `/exit` so the dispatched session terminates cleanly and the runner can continue.
## Final step

After creating the child tasks and recording the decomposition gate verdict, stop. This gate skill is not a task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
