# Task: Detect existing investigation content, skip read-only prompt

**Task ID:** cb-c34085
**Agent:** 

## Task Content

## Task E: Detect existing investigation content in bug body, skip investigate-phase prompts

**Parent design:** cb-7aa91d
**Wave:** 2
**Depends on:** Task C, Task D
**Repo:** cobuild

## Scope

Belt-and-braces for RC1. Even if a bug is routed to the investigate phase (via the `needs-investigation` label), if its body already contains investigation content from a prior conversation, do not inject contradictory read-only instructions. Instead, transparently downgrade to the `fix` phase and log a notice.

## Changes

1. `internal/cmd/dispatch.go` — after phase inference, before writing the prompt:
   ```go
   if currentPhase == "investigate" && hasInvestigationContent(task.Content) {
       fmt.Printf("Notice: bug %s already has investigation content — routing to fix phase instead\n", task.ID)
       currentPhase = "fix"
   }
   ```
2. Add helper `hasInvestigationContent(content string) bool` in `internal/cmd/dispatch.go`:
   - Returns true if the body contains any of: `## Investigation Report`, `## Root Cause`, `## Fix Applied`, `## Fix` (at heading level 2)
   - Case-insensitive match
   - Simple substring check is fine; does not need full markdown parsing

## Acceptance criteria

- [ ] `hasInvestigationContent` helper detects all listed heading variants (case-insensitive)
- [ ] A bug labeled `needs-investigation` with a `## Investigation Report` in its body dispatches to `fix` phase, not `investigate`
- [ ] A notice is printed when this downgrade happens
- [ ] A bug labeled `needs-investigation` without prior investigation content still dispatches to `investigate` phase
- [ ] A bug without the label but with investigation content dispatches to `fix` phase (no change, default behavior)
- [ ] Unit test covers all 4 combinations (label × investigation content)
- [ ] `go build ./...` and `go vet ./...` pass

## Out of scope

- Changing the investigate skill behavior
- Adding new heading formats beyond the 4 listed (can be extended later via gotchas)


## Design Context (from cb-7aa91d)

**Dispatch reliability: Stop hook, fix-phase for bugs, auto pipeline runs**

## Problem

Dispatched agents are failing to complete their work reliably. Of 4 recent dispatches (pf-70df40, pf-326239, pf-09df7a, pf-21779f), none ran `cobuild complete`, three had CLAUDE.md corruption (already fixed), and the remaining three each stalled for a different reason.

Post-mortem interviews with the stalled agents (they're still sitting at idle tmux prompts) produced precise root-cause testimony rather than guesswork.

## Root Causes (agent-reported)

### RC1 — Conflicting signals in the dispatch prompt

Agents saw three contradictory signals:

1. Task body had `## Investigation Report` (from prior conversation investigation)
2. Task body had `## Fix` with checkbox acceptance criteria
3. Dispatch-injected prompt said "READ-ONLY investigation, do not modify source code"

pf-326239: *"I read the acceptance criteria as the authoritative signal of what this task wanted, and treated the investigation report as already done. The ## Instructions block was the actual directive for this session."*

Both agents resolved the contradiction by using judgment — "the investigation is done, the fix is obvious, just do it." That was actually the right call. **The pipeline state was wrong, not the agent reasoning.**

### RC2 — Agents reliably forget `cobuild complete`

pf-326239: *"I simply forgot. There was nothing blocking it."*
pf-09df7a: *"I didn't read AGENTS.md before finishing. I treated 'appended notes' as equivalent to 'done.'"*

Running a follow-up CLI command after a clean commit doesn't match the natural developer "done" gesture. We've already tried making the prompt instructions stronger — it doesn't work. This is a reliability problem that can't be solved by lecturing agents harder.

### RC3 — `cobuild investigate` fails on directly-dispatched bugs

pf-09df7a: *"cobuild investigate failed with 'no pipeline run for design pf-09df7a' — meaning this task wasn't entered through the pipeline via cobuild init."*

Direct `cobuild dispatch <bug-id>` skips `cobuild init`, so no `pipeline_runs` row exists. Gate commands that look up the pipeline run fail. The agent correctly inferred "investigation gate doesn't apply," but then conflated that with "pipeline commands don't apply" and skipped `cobuild complete` too.

### RC4 — `--dangerously-skip-permissions` doesn't cover `.claude/` files

pf-21779f is stuck on a permission prompt for editing `.claude/commands/test.integration.md`. Claude Code apparently gates edits to its own config files regardless of the skip-permissions flag. Dispatch hangs indefinitely waiting for human approval.

### RC5 — Phase inference mismatches actual workflow

Current code:
```go
case "bug":
    currentPhase = "investigate"
```

But in practice, investigation for most bugs happens in conversation (orchestrator + human) before dispatch. By the time `cobuild dispatch` runs, the bug already has investigation findings and a fix spec in its body. Injecting investigate-phase instructions for an already-investigated bug creates the RC1 contradiction.

## Rethink: Simplify the Bug Workflow

The current bug workflow (investigate → implement → review → done) is theater for small fixes. Agents naturally investigate as they fix; forcing a separate read-only phase fights the flow.

**New default:** bugs go straight to a single `fix` phase that does investigate+implement together:
1. Read the bug report
2. If cause isn't obvious, investigate (read code, git blame, trace)
3. Append findings to the bug body
4. Implement the fix, run tests
5. Stop hook runs `cobuild complete`

**Escalation path for complex bugs:** label the bug `needs-investigation` and the dispatcher uses a separate investigate phase (read-only, produces report, creates child fix task). This is the exception, not the default.

## Design

### Change 1 — Stop hook for reliable completion

The dispatch script writes `.claude/settings.local.json` into the worktree with a Stop hook:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cobuild complete $COBUILD_TASK_ID --auto"
      }]
    }]
  }
}
```

Environment variables needed in the worktree session:
- `COBUILD_TASK_ID` — set by dispatch script before launching claude
- `COBUILD_SESSION_ID` — already set

The `--auto` flag on `cobuild complete` signals it was triggered by the Stop hook (for logging/telemetry).

**Open question:** What happens if the agent genuinely isn't done (e.g., stopped for a question)? The Stop hook would fire prematurely. Options:
- A) Only trigger if a sentinel file exists (`.cobuild/agent-done`) — agent writes it as its last action, hook checks before running complete
- B) `cobuild complete` detects incomplete state (no commits, uncommitted changes, failing tests) and aborts with a warning
- C) Trust the Stop event — Claude Code already distinguishes "stopped for input" from "finished"

Recommended: B + C. Claude Code's Stop hook only fires on genuine termination, and `cobuild complete` should already fail loudly on incomplete state.

### Change 2 — Collapse investigate+implement for bugs

Kill the standalone investigate phase for bugs by default. Changes:

1. `internal/cmd/dispatch.go` — phase inference:
   ```go
   case "bug":
       // Default: go straight to fix (investigate+implement combined)
       // Escalate only if labeled needs-investigation
       if hasLabel(task, "needs-investigation") {
           currentPhase = "investigate"
       } else {
           currentPhase = "fix"
       }
   ```

2. `pipeline.yaml` — add `fix` phase to workflows:
   ```yaml
   workflows:
     bug:
       phases: [fix, review, done]
     bug-complex:
       phases: [investigate, implement, review, done]
   ```

3. New skill `skills/fix/fix-bug.md` — single-session bug fix skill. Includes inline investigation guidance ("if cause isn't obvious, trace these things first") without the read-only constraint.

4. Deprecate `skills/investigate/bug-investigation.md`? Keep it for the escalation path but mark it as exceptional-use only.

### Change 3 — Auto-create pipeline run on direct dispatch

`cobuild dispatch <id>` should work even if `cobuild init` was never called. Implementation:

```go
// In dispatch.go, after loading the work item:
run, err := cbStore.GetRun(ctx, task.ID)
if err != nil || run == nil {
    // No pipeline run — create one on the fly
    workflow := inferWorkflow(task.Type)  // bug→bug, design→design, task→task
    run, err = cbStore.CreateRunWithMode(ctx, store.RunInput{
        DesignID: task.ID,
        Project:  projectName,
        Workflow: workflow,
        CurrentPhase: firstPhaseOf(workflow),
        Status: "active",
        Mode: "manual",
    })
}
```

This makes all gate commands work regardless of entry path. `cobuild init` becomes an optimization (batching, explicit workflow choice) rather than a requirement.

### Change 4 — Prompt cleanup for already-investigated bugs

In `writePhasePrompt`, before injecting investigate-phase instructions, check if the bug body already contains investigation content:

```go
if phase == "investigate" {
    if hasInvestigationContent(task.Content) {
        // Body already has investigation — treat as fix phase
        phase = "fix"
    }
}
```

`hasInvestigationContent` looks for `## Investigation Report`, `## Root Cause`, or `## Fix` sections. This is belt-and-braces for RC1 — even if the workflow assigns the wrong phase, the prompt won't contradict the body.

### Change 5 — Deny `.claude/` edits in worktrees

Add to the worktree `.claude/settings.local.json`:

```json
{
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)"
    ]
  }
}
```

Agents editing Claude config files is rarely the right action during a task dispatch. If an agent genuinely needs to update `.claude/commands/*.md` for the project, that should be a separate manual task. Blocking it prevents the silent hang pattern.

## Implementation Plan

Suggested wave ordering (can dispatch in parallel within a wave):

**Wave 1 — unblocks everything**
- Task A: Stop hook infrastructure (Change 1) — write `.claude/settings.local.json` during dispatch, set `COBUILD_TASK_ID` env, add `--auto` flag to `cobuild complete`
- Task B: Auto-create pipeline run on dispatch (Change 3) — makes gate commands work everywhere

**Wave 2 — depends on Wave 1**
- Task C: Add `fix` phase + `fix/fix-bug.md` skill (Change 2) — single-session bug fix
- Task D: Update `dispatch.go` phase inference to route bugs to `fix` by default, escalate on label (Change 2)
- Task E: Prompt cleanup — detect existing investigation content in bug body (Change 4)

**Wave 3 — polish**
- Task F: Worktree `.claude/settings.local.json` deny list for `.claude/**` edits (Change 5)
- Task G: Update `examples/pipeline.yaml` and `skills/shared/bootstrap.md` to reflect new bug workflow default
- Task H: Verification — re-dispatch the 3 stalled bugs end-to-end and confirm Stop hook + auto-pipeline + fix phase all work

## Acceptance Criteria

- [ ] Direct `cobuild dispatch <bug-id>` succeeds even without prior `cobuild init`
- [ ] Agents no longer need to run `cobuild complete` manually — Stop hook handles it
- [ ] Bugs dispatch to a single `fix` phase by default
- [ ] Bugs labeled `needs-investigation` dispatch to the read-only investigate phase
- [ ] `cobuild investigate <id>` works without a prior `cobuild init`
- [ ] Worktree agents cannot edit `.claude/**` files (silent deny, no permission prompt hang)
- [ ] The 3 currently-stalled bugs (pf-326239, pf-09df7a, pf-21779f) complete successfully under the new flow
- [ ] No regression in the existing design-workflow pipeline (design → decompose → implement → review → done)

## Out of Scope

- Full redesign of the context-layer system (separate concern)
- SQLite store (tracked in cb-b2f3ac)
- Changing the review/merge flow
- Beads-specific adjustments (apply the same changes; connector-agnostic)

## Risks

- **Stop hook firing prematurely** — if Claude Code's Stop event fires when the agent is waiting for input rather than finished. Mitigation: `cobuild complete` validates state (commits exist, changes staged, tests pass) before acting; aborts loudly if not ready.
- **Collapsing investigate+implement loses the audit trail for bugs that genuinely need investigation first** — Mitigation: the `needs-investigation` label preserves the full two-phase flow for complex cases. Retrospectives can surface bugs where the single-phase fix produced poor quality.
- **Auto-creating pipeline runs hides user intent** — users who ran `cobuild dispatch` without `cobuild init` might not realize a pipeline run was created. Mitigation: print a one-line notice when auto-creating.

---
*Appended by agent-mycroft at 2026-04-05 10:09 UTC*

## Problem

Dispatched agents are failing to complete their work reliably. Of 4 recent dispatches (pf-70df40, pf-326239, pf-09df7a, pf-21779f), none ran `cobuild complete`, three had CLAUDE.md corruption (already fixed), and the remaining three each stalled for a different reason.

Post-mortem interviews with the stalled agents (they're still sitting at idle tmux prompts) produced precise root-cause testimony rather than guesswork.

## Root Causes (agent-reported)

### RC1 — Conflicting signals in the dispatch prompt

Agents saw three contradictory signals:

1. Task body had `## Investigation Report` (from prior conversation investigation)
2. Task body had `## Fix` with checkbox acceptance criteria
3. Dispatch-injected prompt said "READ-ONLY investigation, do not modify source code"

pf-326239: *"I read the acceptance criteria as the authoritative signal of what this task wanted, and treated the investigation report as already done. The ## Instructions block was the actual directive for this session."*

Both agents resolved the contradiction by using judgment — "the investigation is done, the fix is obvious, just do it." That was actually the right call. **The pipeline state was wrong, not the agent reasoning.**

### RC2 — Agents reliably forget `cobuild complete`

pf-326239: *"I simply forgot. There was nothing blocking it."*
pf-09df7a: *"I didn't read AGENTS.md before finishing. I treated 'appended notes' as equivalent to 'done.'"*

Running a follow-up CLI command after a clean commit doesn't match the natural developer "done" gesture. We've already tried making the prompt instructions stronger — it doesn't work. This is a reliability problem that can't be solved by lecturing agents harder.

### RC3 — `cobuild investigate` fails on directly-dispatched bugs

pf-09df7a: *"cobuild investigate failed with 'no pipeline run for design pf-09df7a' — meaning this task wasn't entered through the pipeline via cobuild init."*

Direct `cobuild dispatch <bug-id>` skips `cobuild init`, so no `pipeline_runs` row exists. Gate commands that look up the pipeline run fail. The agent correctly inferred "investigation gate doesn't apply," but then conflated that with "pipeline commands don't apply" and skipped `cobuild complete` too.

### RC4 — `--dangerously-skip-permissions` doesn't cover `.claude/` files

pf-21779f is stuck on a permission prompt for editing `.claude/commands/test.integration.md`. Claude Code apparently gates edits to its own config files regardless of the skip-permissions flag. Dispatch hangs indefinitely waiting for human approval.

### RC5 — Phase inference mismatches actual workflow

Current code:
```go
case "bug":
    currentPhase = "investigate"
```

But in practice, investigation for most bugs happens in conversation (orchestrator + human) before dispatch. By the time `cobuild dispatch` runs, the bug already has investigation findings and a fix spec in its body. Injecting investigate-phase instructions for an already-investigated bug creates the RC1 contradiction.

## Rethink: Simplify the Bug Workflow

The current bug workflow (investigate → implement → review → done) is theater for small fixes. Agents naturally investigate as they fix; forcing a separate read-only phase fights the flow.

**New default:** bugs go straight to a single `fix` phase that does investigate+implement together:
1. Read the bug report
2. If cause isn't obvious, investigate (read code, git blame, trace)
3. Append findings to the bug body
4. Implement the fix, run tests
5. Stop hook runs `cobuild complete`

**Escalation path for complex bugs:** label the bug `needs-investigation` and the dispatcher uses a separate investigate phase (read-only, produces report, creates child fix task). This is the exception, not the default.

## Design

### Change 1 — Stop hook for reliable completion

The dispatch script writes `.claude/settings.local.json` into the worktree with a Stop hook:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cobuild complete $COBUILD_TASK_ID --auto"
      }]
    }]
  }
}
```

Environment variables needed in the worktree session:
- `COBUILD_TASK_ID` — set by dispatch script before launching claude
- `COBUILD_SESSION_ID` — already set

The `--auto` flag on `cobuild complete` signals it was triggered by the Stop hook (for logging/telemetry).

**Open question:** What happens if the agent genuinely isn't done (e.g., stopped for a question)? The Stop hook would fire prematurely. Options:
- A) Only trigger if a sentinel file exists (`.cobuild/agent-done`) — agent writes it as its last action, hook checks before running complete
- B) `cobuild complete` detects incomplete state (no commits, uncommitted changes, failing tests) and aborts with a warning
- C) Trust the Stop event — Claude Code already distinguishes "stopped for input" from "finished"

Recommended: B + C. Claude Code's Stop hook only fires on genuine termination, and `cobuild complete` should already fail loudly on incomplete state.

### Change 2 — Collapse investigate+implement for bugs

Kill the standalone investigate phase for bugs by default. Changes:

1. `internal/cmd/dispatch.go` — phase inference:
   ```go
   case "bug":
       // Default: go straight to fix (investigate+implement combined)
       // Escalate only if labeled needs-investigation
       if hasLabel(task, "needs-investigation") {
           currentPhase = "investigate"
       } else {
           currentPhase = "fix"
       }
   ```

2. `pipeline.yaml` — add `fix` phase to workflows:
   ```yaml
   workflows:
     bug:
       phases: [fix, review, done]
     bug-complex:
       phases: [investigate, implement, review, done]
   ```

3. New skill `skills/fix/fix-bug.md` — single-session bug fix skill. Includes inline investigation guidance ("if cause isn't obvious, trace these things first") without the read-only constraint.

4. Deprecate `skills/investigate/bug-investigation.md`? Keep it for the escalation path but mark it as exceptional-use only.

### Change 3 — Auto-create pipeline run on direct dispatch

`cobuild dispatch <id>` should work even if `cobuild init` was never called. Implementation:

```go
// In dispatch.go, after loading the work item:
run, err := cbStore.GetRun(ctx, task.ID)
if err != nil || run == nil {
    // No pipeline run — create one on the fly
    workflow := inferWorkflow(task.Type)  // bug→bug, design→design, task→task
    run, err = cbStore.CreateRunWithMode(ctx, store.RunInput{
        DesignID: task.ID,
        Project:  projectName,
        Workflow: workflow,
        CurrentPhase: firstPhaseOf(workflow),
        Status: "active",
        Mode: "manual",
    })
}
```

This makes all gate commands work regardless of entry path. `cobuild init` becomes an optimization (batching, explicit workflow choice) rather than a requirement.

### Change 4 — Prompt cleanup for already-investigated bugs

In `writePhasePrompt`, before injecting investigate-phase instructions, check if the bug body already contains investigation content:

```go
if phase == "investigate" {
    if hasInvestigationContent(task.Content) {
        // Body already has investigation — treat as fix phase
        phase = "fix"
    }
}
```

`hasInvestigationContent` looks for `## Investigation Report`, `## Root Cause`, or `## Fix` sections. This is belt-and-braces for RC1 — even if the workflow assigns the wrong phase, the prompt won't contradict the body.

### Change 5 — Deny `.claude/` edits in worktrees

Add to the worktree `.claude/settings.local.json`:

```json
{
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)"
    ]
  }
}
```

Agents editing Claude config files is rarely the right action during a task dispatch. If an agent genuinely needs to update `.claude/commands/*.md` for the project, that should be a separate manual task. Blocking it prevents the silent hang pattern.

## Implementation Plan

Suggested wave ordering (can dispatch in parallel within a wave):

**Wave 1 — unblocks everything**
- Task A: Stop hook infrastructure (Change 1) — write `.claude/settings.local.json` during dispatch, set `COBUILD_TASK_ID` env, add `--auto` flag to `cobuild complete`
- Task B: Auto-create pipeline run on dispatch (Change 3) — makes gate commands work everywhere

**Wave 2 — depends on Wave 1**
- Task C: Add `fix` phase + `fix/fix-bug.md` skill (Change 2) — single-session bug fix
- Task D: Update `dispatch.go` phase inference to route bugs to `fix` by default, escalate on label (Change 2)
- Task E: Prompt cleanup — detect existing investigation content in bug body (Change 4)

**Wave 3 — polish**
- Task F: Worktree `.claude/settings.local.json` deny list for `.claude/**` edits (Change 5)
- Task G: Update `examples/pipeline.yaml` and `skills/shared/bootstrap.md` to reflect new bug workflow default
- Task H: Verification — re-dispatch the 3 stalled bugs end-to-end and confirm Stop hook + auto-pipeline + fix phase all work

## Acceptance Criteria

- [ ] Direct `cobuild dispatch <bug-id>` succeeds even without prior `cobuild init`
- [ ] Agents no longer need to run `cobuild complete` manually — Stop hook handles it
- [ ] Bugs dispatch to a single `fix` phase by default
- [ ] Bugs labeled `needs-investigation` dispatch to the read-only investigate phase
- [ ] `cobuild investigate <id>` works without a prior `cobuild init`
- [ ] Worktree agents cannot edit `.claude/**` files (silent deny, no permission prompt hang)
- [ ] The 3 currently-stalled bugs (pf-326239, pf-09df7a, pf-21779f) complete successfully under the new flow
- [ ] No regression in the existing design-workflow pipeline (design → decompose → implement → review → done)

## Out of Scope

- Full redesign of the context-layer system (separate concern)
- SQLite store (tracked in cb-b2f3ac)
- Changing the review/merge flow
- Beads-specific adjustments (apply the same changes; connector-agnostic)

## Risks

- **Stop hook firing prematurely** — if Claude Code's Stop event fires when the agent is waiting for input rather than finished. Mitigation: `cobuild complete` validates state (commits exist, changes staged, tests pass) before acting; aborts loudly if not ready.
- **Collapsing investigate+implement loses the audit trail for bugs that genuinely need investigation first** — Mitigation: the `needs-investigation` label preserves the full two-phase flow for complex cases. Retrospectives can surface bugs where the single-phase fix produced poor quality.
- **Auto-creating pipeline runs hides user intent** — users who ran `cobuild dispatch` without `cobuild init` might not realize a pipeline run was created. Mitigation: print a one-line notice when auto-creating.

---
*Appended by agent-mycroft at 2026-04-05 10:13 UTC*


---

## Addendum: Making `needs-investigation` Discoverable

Changing the default bug flow silently is a trap. If callers don't know the escalation exists, they'll never use it — and we'll lose the two-phase investigation flow for bugs that genuinely need it. Every place that describes the bug workflow must be updated.

### Escalation criteria (when to label `needs-investigation`)

A bug should be labeled `needs-investigation` when **any** of the following are true:

1. **Root cause is unknown** — symptom is visible but the mechanism isn't obvious from the report
2. **Cross-system** — bug spans multiple services, modules, or repos where the interaction is unclear
3. **Data or security implications** — bug could have corrupted data, leaked information, or created a security hole; need to assess blast radius before fixing
4. **Fragility concern** — this area has broken before, or the fix might affect unrelated behavior
5. **Intermittent or environment-dependent** — bug reproduces inconsistently; needs investigation to find the trigger
6. **Fix shape is non-obvious** — you can't describe the fix in 1-2 sentences when creating the bug
7. **Requires stakeholder decision** — investigation will produce options (A/B/C) and a human needs to choose

If **none** of these apply, the default `fix` phase is the right path — agent investigates as it fixes, in one session.

### Docs and code that must be updated

| Location | Current content | Update needed |
|----------|-----------------|---------------|
| `README.md` | Workflows table shows `bug: investigate → implement → review → done` | Update to `bug: fix → review → done` (default), `bug-complex: investigate → implement → review → done` (escalation). Add escalation criteria summary. |
| `examples/pipeline.yaml` | Workflow definitions | Add `fix` phase, update `bug` workflow, add `bug-complex` workflow |
| `examples/pipeline-minimal.yaml` | Minimal config | Ensure minimal still works with new default |
| `skills/shared/bootstrap.md` | Bootstrap walkthrough mentions bug workflow | Mention the default fix flow and the `needs-investigation` escalation |
| `skills/shared/create-design.md` (or equivalent bug-creation guidance) | How to create work items | Add section: "When creating a bug, decide if it needs investigation. Apply the `needs-investigation` label if any of [criteria list] apply." |
| `skills/investigate/bug-investigation.md` | Current investigation skill | Add header: "This skill is only used for bugs labeled `needs-investigation`. For the default fix flow, see `skills/fix/fix-bug.md`." |
| `skills/fix/fix-bug.md` | (new skill) | Include the escalation criteria in the "if cause isn't obvious" section, with instructions to stop and escalate rather than guessing |
| `AGENTS.md` (generated by `cobuild update-agents`) | Agent instructions | Generated from skills, should automatically reflect the new flow. Verify `cobuild update-agents` picks up the new `fix` skill and updated workflows. |
| `docs/guides/` (any workflow guide) | Workflow descriptions | Update to reflect new default |
| `cobuild explain` output | Pipeline explanation | Already reads from config/skills, but verify it renders the new workflows correctly and mentions the escalation |

### Discoverability checks for the orchestrator agent (M)

The orchestrator agent (the one calling `cobuild dispatch`) is the most likely caller to need this. Before creating a bug work item, M should:

1. Read the bug symptoms from the user
2. Mentally run through the 7 escalation criteria
3. If any apply, add `--label needs-investigation` when creating the work item
4. If creating via skill, the skill should prompt M to make this decision explicitly

This belongs in the CLAUDE.md / AGENTS.md that the orchestrator reads, in the "creating work items" section. It should be a small decision tree, not buried prose.

### Fail-loud signal

If dispatch ever routes a bug to the `fix` phase and the fix agent realises mid-session that the cause is genuinely unknown or the blast radius is unclear, it should:

1. Stop implementing
2. Append findings so far to the bug
3. Add the `needs-investigation` label
4. Exit and let the orchestrator re-dispatch through the investigation flow

This self-escalation path prevents agents from flailing on bugs they can't fix safely.

### New Task for Wave 3

**Task I: Escalation documentation and self-escalation**
- Update all docs/skills listed in the table above
- Add escalation criteria to `skills/fix/fix-bug.md` with inline decision checklist
- Add bug-creation guidance to CLAUDE.md / generated AGENTS.md: "when creating a bug, apply these criteria to decide labeling"
- Add self-escalation protocol to `skills/fix/fix-bug.md`
- Verify `cobuild explain` renders the new workflows cleanly

Dependencies: blocked by Task C (needs `skills/fix/fix-bug.md` to exist first)

## Instructions

Implement this task following the acceptance criteria above.

### On completion

1. **Run `cobuild complete cb-c34085`** -- this commits remaining changes, pushes, creates the PR, appends evidence, and marks the task needs-review. Do this as your LAST action.

**IMPORTANT RULES:**
- NEVER use raw `git merge` or `git push` to main — always use `cobuild complete` which creates a PR
- NEVER merge PRs yourself — the orchestrating agent handles merge via `cobuild merge` after review
- If a reviewer (Gemini, human) leaves a critical comment on your PR, you MUST address it before the PR can merge
- Check review comments: `gh pr view <pr-number> --comments`


---

## Design Context (from cb-7aa91d)

**Dispatch reliability: Stop hook, fix-phase for bugs, auto pipeline runs**

## Problem

Dispatched agents are failing to complete their work reliably. Of 4 recent dispatches (pf-70df40, pf-326239, pf-09df7a, pf-21779f), none ran `cobuild complete`, three had CLAUDE.md corruption (already fixed), and the remaining three each stalled for a different reason.

Post-mortem interviews with the stalled agents (they're still sitting at idle tmux prompts) produced precise root-cause testimony rather than guesswork.

## Root Causes (agent-reported)

### RC1 — Conflicting signals in the dispatch prompt

Agents saw three contradictory signals:

1. Task body had `## Investigation Report` (from prior conversation investigation)
2. Task body had `## Fix` with checkbox acceptance criteria
3. Dispatch-injected prompt said "READ-ONLY investigation, do not modify source code"

pf-326239: *"I read the acceptance criteria as the authoritative signal of what this task wanted, and treated the investigation report as already done. The ## Instructions block was the actual directive for this session."*

Both agents resolved the contradiction by using judgment — "the investigation is done, the fix is obvious, just do it." That was actually the right call. **The pipeline state was wrong, not the agent reasoning.**

### RC2 — Agents reliably forget `cobuild complete`

pf-326239: *"I simply forgot. There was nothing blocking it."*
pf-09df7a: *"I didn't read AGENTS.md before finishing. I treated 'appended notes' as equivalent to 'done.'"*

Running a follow-up CLI command after a clean commit doesn't match the natural developer "done" gesture. We've already tried making the prompt instructions stronger — it doesn't work. This is a reliability problem that can't be solved by lecturing agents harder.

### RC3 — `cobuild investigate` fails on directly-dispatched bugs

pf-09df7a: *"cobuild investigate failed with 'no pipeline run for design pf-09df7a' — meaning this task wasn't entered through the pipeline via cobuild init."*

Direct `cobuild dispatch <bug-id>` skips `cobuild init`, so no `pipeline_runs` row exists. Gate commands that look up the pipeline run fail. The agent correctly inferred "investigation gate doesn't apply," but then conflated that with "pipeline commands don't apply" and skipped `cobuild complete` too.

### RC4 — `--dangerously-skip-permissions` doesn't cover `.claude/` files

pf-21779f is stuck on a permission prompt for editing `.claude/commands/test.integration.md`. Claude Code apparently gates edits to its own config files regardless of the skip-permissions flag. Dispatch hangs indefinitely waiting for human approval.

### RC5 — Phase inference mismatches actual workflow

Current code:
```go
case "bug":
    currentPhase = "investigate"
```

But in practice, investigation for most bugs happens in conversation (orchestrator + human) before dispatch. By the time `cobuild dispatch` runs, the bug already has investigation findings and a fix spec in its body. Injecting investigate-phase instructions for an already-investigated bug creates the RC1 contradiction.

## Rethink: Simplify the Bug Workflow

The current bug workflow (investigate → implement → review → done) is theater for small fixes. Agents naturally investigate as they fix; forcing a separate read-only phase fights the flow.

**New default:** bugs go straight to a single `fix` phase that does investigate+implement together:
1. Read the bug report
2. If cause isn't obvious, investigate (read code, git blame, trace)
3. Append findings to the bug body
4. Implement the fix, run tests
5. Stop hook runs `cobuild complete`

**Escalation path for complex bugs:** label the bug `needs-investigation` and the dispatcher uses a separate investigate phase (read-only, produces report, creates child fix task). This is the exception, not the default.

## Design

### Change 1 — Stop hook for reliable completion

The dispatch script writes `.claude/settings.local.json` into the worktree with a Stop hook:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cobuild complete $COBUILD_TASK_ID --auto"
      }]
    }]
  }
}
```

Environment variables needed in the worktree session:
- `COBUILD_TASK_ID` — set by dispatch script before launching claude
- `COBUILD_SESSION_ID` — already set

The `--auto` flag on `cobuild complete` signals it was triggered by the Stop hook (for logging/telemetry).

**Open question:** What happens if the agent genuinely isn't done (e.g., stopped for a question)? The Stop hook would fire prematurely. Options:
- A) Only trigger if a sentinel file exists (`.cobuild/agent-done`) — agent writes it as its last action, hook checks before running complete
- B) `cobuild complete` detects incomplete state (no commits, uncommitted changes, failing tests) and aborts with a warning
- C) Trust the Stop event — Claude Code already distinguishes "stopped for input" from "finished"

Recommended: B + C. Claude Code's Stop hook only fires on genuine termination, and `cobuild complete` should already fail loudly on incomplete state.

### Change 2 — Collapse investigate+implement for bugs

Kill the standalone investigate phase for bugs by default. Changes:

1. `internal/cmd/dispatch.go` — phase inference:
   ```go
   case "bug":
       // Default: go straight to fix (investigate+implement combined)
       // Escalate only if labeled needs-investigation
       if hasLabel(task, "needs-investigation") {
           currentPhase = "investigate"
       } else {
           currentPhase = "fix"
       }
   ```

2. `pipeline.yaml` — add `fix` phase to workflows:
   ```yaml
   workflows:
     bug:
       phases: [fix, review, done]
     bug-complex:
       phases: [investigate, implement, review, done]
   ```

3. New skill `skills/fix/fix-bug.md` — single-session bug fix skill. Includes inline investigation guidance ("if cause isn't obvious, trace these things first") without the read-only constraint.

4. Deprecate `skills/investigate/bug-investigation.md`? Keep it for the escalation path but mark it as exceptional-use only.

### Change 3 — Auto-create pipeline run on direct dispatch

`cobuild dispatch <id>` should work even if `cobuild init` was never called. Implementation:

```go
// In dispatch.go, after loading the work item:
run, err := cbStore.GetRun(ctx, task.ID)
if err != nil || run == nil {
    // No pipeline run — create one on the fly
    workflow := inferWorkflow(task.Type)  // bug→bug, design→design, task→task
    run, err = cbStore.CreateRunWithMode(ctx, store.RunInput{
        DesignID: task.ID,
        Project:  projectName,
        Workflow: workflow,
        CurrentPhase: firstPhaseOf(workflow),
        Status: "active",
        Mode: "manual",
    })
}
```

This makes all gate commands work regardless of entry path. `cobuild init` becomes an optimization (batching, explicit workflow choice) rather than a requirement.

### Change 4 — Prompt cleanup for already-investigated bugs

In `writePhasePrompt`, before injecting investigate-phase instructions, check if the bug body already contains investigation content:

```go
if phase == "investigate" {
    if hasInvestigationContent(task.Content) {
        // Body already has investigation — treat as fix phase
        phase = "fix"
    }
}
```

`hasInvestigationContent` looks for `## Investigation Report`, `## Root Cause`, or `## Fix` sections. This is belt-and-braces for RC1 — even if the workflow assigns the wrong phase, the prompt won't contradict the body.

### Change 5 — Deny `.claude/` edits in worktrees

Add to the worktree `.claude/settings.local.json`:

```json
{
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)"
    ]
  }
}
```

Agents editing Claude config files is rarely the right action during a task dispatch. If an agent genuinely needs to update `.claude/commands/*.md` for the project, that should be a separate manual task. Blocking it prevents the silent hang pattern.

## Implementation Plan

Suggested wave ordering (can dispatch in parallel within a wave):

**Wave 1 — unblocks everything**
- Task A: Stop hook infrastructure (Change 1) — write `.claude/settings.local.json` during dispatch, set `COBUILD_TASK_ID` env, add `--auto` flag to `cobuild complete`
- Task B: Auto-create pipeline run on dispatch (Change 3) — makes gate commands work everywhere

**Wave 2 — depends on Wave 1**
- Task C: Add `fix` phase + `fix/fix-bug.md` skill (Change 2) — single-session bug fix
- Task D: Update `dispatch.go` phase inference to route bugs to `fix` by default, escalate on label (Change 2)
- Task E: Prompt cleanup — detect existing investigation content in bug body (Change 4)

**Wave 3 — polish**
- Task F: Worktree `.claude/settings.local.json` deny list for `.claude/**` edits (Change 5)
- Task G: Update `examples/pipeline.yaml` and `skills/shared/bootstrap.md` to reflect new bug workflow default
- Task H: Verification — re-dispatch the 3 stalled bugs end-to-end and confirm Stop hook + auto-pipeline + fix phase all work

## Acceptance Criteria

- [ ] Direct `cobuild dispatch <bug-id>` succeeds even without prior `cobuild init`
- [ ] Agents no longer need to run `cobuild complete` manually — Stop hook handles it
- [ ] Bugs dispatch to a single `fix` phase by default
- [ ] Bugs labeled `needs-investigation` dispatch to the read-only investigate phase
- [ ] `cobuild investigate <id>` works without a prior `cobuild init`
- [ ] Worktree agents cannot edit `.claude/**` files (silent deny, no permission prompt hang)
- [ ] The 3 currently-stalled bugs (pf-326239, pf-09df7a, pf-21779f) complete successfully under the new flow
- [ ] No regression in the existing design-workflow pipeline (design → decompose → implement → review → done)

## Out of Scope

- Full redesign of the context-layer system (separate concern)
- SQLite store (tracked in cb-b2f3ac)
- Changing the review/merge flow
- Beads-specific adjustments (apply the same changes; connector-agnostic)

## Risks

- **Stop hook firing prematurely** — if Claude Code's Stop event fires when the agent is waiting for input rather than finished. Mitigation: `cobuild complete` validates state (commits exist, changes staged, tests pass) before acting; aborts loudly if not ready.
- **Collapsing investigate+implement loses the audit trail for bugs that genuinely need investigation first** — Mitigation: the `needs-investigation` label preserves the full two-phase flow for complex cases. Retrospectives can surface bugs where the single-phase fix produced poor quality.
- **Auto-creating pipeline runs hides user intent** — users who ran `cobuild dispatch` without `cobuild init` might not realize a pipeline run was created. Mitigation: print a one-line notice when auto-creating.

---
*Appended by agent-mycroft at 2026-04-05 10:09 UTC*

## Problem

Dispatched agents are failing to complete their work reliably. Of 4 recent dispatches (pf-70df40, pf-326239, pf-09df7a, pf-21779f), none ran `cobuild complete`, three had CLAUDE.md corruption (already fixed), and the remaining three each stalled for a different reason.

Post-mortem interviews with the stalled agents (they're still sitting at idle tmux prompts) produced precise root-cause testimony rather than guesswork.

## Root Causes (agent-reported)

### RC1 — Conflicting signals in the dispatch prompt

Agents saw three contradictory signals:

1. Task body had `## Investigation Report` (from prior conversation investigation)
2. Task body had `## Fix` with checkbox acceptance criteria
3. Dispatch-injected prompt said "READ-ONLY investigation, do not modify source code"

pf-326239: *"I read the acceptance criteria as the authoritative signal of what this task wanted, and treated the investigation report as already done. The ## Instructions block was the actual directive for this session."*

Both agents resolved the contradiction by using judgment — "the investigation is done, the fix is obvious, just do it." That was actually the right call. **The pipeline state was wrong, not the agent reasoning.**

### RC2 — Agents reliably forget `cobuild complete`

pf-326239: *"I simply forgot. There was nothing blocking it."*
pf-09df7a: *"I didn't read AGENTS.md before finishing. I treated 'appended notes' as equivalent to 'done.'"*

Running a follow-up CLI command after a clean commit doesn't match the natural developer "done" gesture. We've already tried making the prompt instructions stronger — it doesn't work. This is a reliability problem that can't be solved by lecturing agents harder.

### RC3 — `cobuild investigate` fails on directly-dispatched bugs

pf-09df7a: *"cobuild investigate failed with 'no pipeline run for design pf-09df7a' — meaning this task wasn't entered through the pipeline via cobuild init."*

Direct `cobuild dispatch <bug-id>` skips `cobuild init`, so no `pipeline_runs` row exists. Gate commands that look up the pipeline run fail. The agent correctly inferred "investigation gate doesn't apply," but then conflated that with "pipeline commands don't apply" and skipped `cobuild complete` too.

### RC4 — `--dangerously-skip-permissions` doesn't cover `.claude/` files

pf-21779f is stuck on a permission prompt for editing `.claude/commands/test.integration.md`. Claude Code apparently gates edits to its own config files regardless of the skip-permissions flag. Dispatch hangs indefinitely waiting for human approval.

### RC5 — Phase inference mismatches actual workflow

Current code:
```go
case "bug":
    currentPhase = "investigate"
```

But in practice, investigation for most bugs happens in conversation (orchestrator + human) before dispatch. By the time `cobuild dispatch` runs, the bug already has investigation findings and a fix spec in its body. Injecting investigate-phase instructions for an already-investigated bug creates the RC1 contradiction.

## Rethink: Simplify the Bug Workflow

The current bug workflow (investigate → implement → review → done) is theater for small fixes. Agents naturally investigate as they fix; forcing a separate read-only phase fights the flow.

**New default:** bugs go straight to a single `fix` phase that does investigate+implement together:
1. Read the bug report
2. If cause isn't obvious, investigate (read code, git blame, trace)
3. Append findings to the bug body
4. Implement the fix, run tests
5. Stop hook runs `cobuild complete`

**Escalation path for complex bugs:** label the bug `needs-investigation` and the dispatcher uses a separate investigate phase (read-only, produces report, creates child fix task). This is the exception, not the default.

## Design

### Change 1 — Stop hook for reliable completion

The dispatch script writes `.claude/settings.local.json` into the worktree with a Stop hook:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cobuild complete $COBUILD_TASK_ID --auto"
      }]
    }]
  }
}
```

Environment variables needed in the worktree session:
- `COBUILD_TASK_ID` — set by dispatch script before launching claude
- `COBUILD_SESSION_ID` — already set

The `--auto` flag on `cobuild complete` signals it was triggered by the Stop hook (for logging/telemetry).

**Open question:** What happens if the agent genuinely isn't done (e.g., stopped for a question)? The Stop hook would fire prematurely. Options:
- A) Only trigger if a sentinel file exists (`.cobuild/agent-done`) — agent writes it as its last action, hook checks before running complete
- B) `cobuild complete` detects incomplete state (no commits, uncommitted changes, failing tests) and aborts with a warning
- C) Trust the Stop event — Claude Code already distinguishes "stopped for input" from "finished"

Recommended: B + C. Claude Code's Stop hook only fires on genuine termination, and `cobuild complete` should already fail loudly on incomplete state.

### Change 2 — Collapse investigate+implement for bugs

Kill the standalone investigate phase for bugs by default. Changes:

1. `internal/cmd/dispatch.go` — phase inference:
   ```go
   case "bug":
       // Default: go straight to fix (investigate+implement combined)
       // Escalate only if labeled needs-investigation
       if hasLabel(task, "needs-investigation") {
           currentPhase = "investigate"
       } else {
           currentPhase = "fix"
       }
   ```

2. `pipeline.yaml` — add `fix` phase to workflows:
   ```yaml
   workflows:
     bug:
       phases: [fix, review, done]
     bug-complex:
       phases: [investigate, implement, review, done]
   ```

3. New skill `skills/fix/fix-bug.md` — single-session bug fix skill. Includes inline investigation guidance ("if cause isn't obvious, trace these things first") without the read-only constraint.

4. Deprecate `skills/investigate/bug-investigation.md`? Keep it for the escalation path but mark it as exceptional-use only.

### Change 3 — Auto-create pipeline run on direct dispatch

`cobuild dispatch <id>` should work even if `cobuild init` was never called. Implementation:

```go
// In dispatch.go, after loading the work item:
run, err := cbStore.GetRun(ctx, task.ID)
if err != nil || run == nil {
    // No pipeline run — create one on the fly
    workflow := inferWorkflow(task.Type)  // bug→bug, design→design, task→task
    run, err = cbStore.CreateRunWithMode(ctx, store.RunInput{
        DesignID: task.ID,
        Project:  projectName,
        Workflow: workflow,
        CurrentPhase: firstPhaseOf(workflow),
        Status: "active",
        Mode: "manual",
    })
}
```

This makes all gate commands work regardless of entry path. `cobuild init` becomes an optimization (batching, explicit workflow choice) rather than a requirement.

### Change 4 — Prompt cleanup for already-investigated bugs

In `writePhasePrompt`, before injecting investigate-phase instructions, check if the bug body already contains investigation content:

```go
if phase == "investigate" {
    if hasInvestigationContent(task.Content) {
        // Body already has investigation — treat as fix phase
        phase = "fix"
    }
}
```

`hasInvestigationContent` looks for `## Investigation Report`, `## Root Cause`, or `## Fix` sections. This is belt-and-braces for RC1 — even if the workflow assigns the wrong phase, the prompt won't contradict the body.

### Change 5 — Deny `.claude/` edits in worktrees

Add to the worktree `.claude/settings.local.json`:

```json
{
  "permissions": {
    "deny": [
      "Edit(.claude/**)",
      "Write(.claude/**)"
    ]
  }
}
```

Agents editing Claude config files is rarely the right action during a task dispatch. If an agent genuinely needs to update `.claude/commands/*.md` for the project, that should be a separate manual task. Blocking it prevents the silent hang pattern.

## Implementation Plan

Suggested wave ordering (can dispatch in parallel within a wave):

**Wave 1 — unblocks everything**
- Task A: Stop hook infrastructure (Change 1) — write `.claude/settings.local.json` during dispatch, set `COBUILD_TASK_ID` env, add `--auto` flag to `cobuild complete`
- Task B: Auto-create pipeline run on dispatch (Change 3) — makes gate commands work everywhere

**Wave 2 — depends on Wave 1**
- Task C: Add `fix` phase + `fix/fix-bug.md` skill (Change 2) — single-session bug fix
- Task D: Update `dispatch.go` phase inference to route bugs to `fix` by default, escalate on label (Change 2)
- Task E: Prompt cleanup — detect existing investigation content in bug body (Change 4)

**Wave 3 — polish**
- Task F: Worktree `.claude/settings.local.json` deny list for `.claude/**` edits (Change 5)
- Task G: Update `examples/pipeline.yaml` and `skills/shared/bootstrap.md` to reflect new bug workflow default
- Task H: Verification — re-dispatch the 3 stalled bugs end-to-end and confirm Stop hook + auto-pipeline + fix phase all work

## Acceptance Criteria

- [ ] Direct `cobuild dispatch <bug-id>` succeeds even without prior `cobuild init`
- [ ] Agents no longer need to run `cobuild complete` manually — Stop hook handles it
- [ ] Bugs dispatch to a single `fix` phase by default
- [ ] Bugs labeled `needs-investigation` dispatch to the read-only investigate phase
- [ ] `cobuild investigate <id>` works without a prior `cobuild init`
- [ ] Worktree agents cannot edit `.claude/**` files (silent deny, no permission prompt hang)
- [ ] The 3 currently-stalled bugs (pf-326239, pf-09df7a, pf-21779f) complete successfully under the new flow
- [ ] No regression in the existing design-workflow pipeline (design → decompose → implement → review → done)

## Out of Scope

- Full redesign of the context-layer system (separate concern)
- SQLite store (tracked in cb-b2f3ac)
- Changing the review/merge flow
- Beads-specific adjustments (apply the same changes; connector-agnostic)

## Risks

- **Stop hook firing prematurely** — if Claude Code's Stop event fires when the agent is waiting for input rather than finished. Mitigation: `cobuild complete` validates state (commits exist, changes staged, tests pass) before acting; aborts loudly if not ready.
- **Collapsing investigate+implement loses the audit trail for bugs that genuinely need investigation first** — Mitigation: the `needs-investigation` label preserves the full two-phase flow for complex cases. Retrospectives can surface bugs where the single-phase fix produced poor quality.
- **Auto-creating pipeline runs hides user intent** — users who ran `cobuild dispatch` without `cobuild init` might not realize a pipeline run was created. Mitigation: print a one-line notice when auto-creating.

---
*Appended by agent-mycroft at 2026-04-05 10:13 UTC*


---

## Addendum: Making `needs-investigation` Discoverable

Changing the default bug flow silently is a trap. If callers don't know the escalation exists, they'll never use it — and we'll lose the two-phase investigation flow for bugs that genuinely need it. Every place that describes the bug workflow must be updated.

### Escalation criteria (when to label `needs-investigation`)

A bug should be labeled `needs-investigation` when **any** of the following are true:

1. **Root cause is unknown** — symptom is visible but the mechanism isn't obvious from the report
2. **Cross-system** — bug spans multiple services, modules, or repos where the interaction is unclear
3. **Data or security implications** — bug could have corrupted data, leaked information, or created a security hole; need to assess blast radius before fixing
4. **Fragility concern** — this area has broken before, or the fix might affect unrelated behavior
5. **Intermittent or environment-dependent** — bug reproduces inconsistently; needs investigation to find the trigger
6. **Fix shape is non-obvious** — you can't describe the fix in 1-2 sentences when creating the bug
7. **Requires stakeholder decision** — investigation will produce options (A/B/C) and a human needs to choose

If **none** of these apply, the default `fix` phase is the right path — agent investigates as it fixes, in one session.

### Docs and code that must be updated

| Location | Current content | Update needed |
|----------|-----------------|---------------|
| `README.md` | Workflows table shows `bug: investigate → implement → review → done` | Update to `bug: fix → review → done` (default), `bug-complex: investigate → implement → review → done` (escalation). Add escalation criteria summary. |
| `examples/pipeline.yaml` | Workflow definitions | Add `fix` phase, update `bug` workflow, add `bug-complex` workflow |
| `examples/pipeline-minimal.yaml` | Minimal config | Ensure minimal still works with new default |
| `skills/shared/bootstrap.md` | Bootstrap walkthrough mentions bug workflow | Mention the default fix flow and the `needs-investigation` escalation |
| `skills/shared/create-design.md` (or equivalent bug-creation guidance) | How to create work items | Add section: "When creating a bug, decide if it needs investigation. Apply the `needs-investigation` label if any of [criteria list] apply." |
| `skills/investigate/bug-investigation.md` | Current investigation skill | Add header: "This skill is only used for bugs labeled `needs-investigation`. For the default fix flow, see `skills/fix/fix-bug.md`." |
| `skills/fix/fix-bug.md` | (new skill) | Include the escalation criteria in the "if cause isn't obvious" section, with instructions to stop and escalate rather than guessing |
| `AGENTS.md` (generated by `cobuild update-agents`) | Agent instructions | Generated from skills, should automatically reflect the new flow. Verify `cobuild update-agents` picks up the new `fix` skill and updated workflows. |
| `docs/guides/` (any workflow guide) | Workflow descriptions | Update to reflect new default |
| `cobuild explain` output | Pipeline explanation | Already reads from config/skills, but verify it renders the new workflows correctly and mentions the escalation |

### Discoverability checks for the orchestrator agent (M)

The orchestrator agent (the one calling `cobuild dispatch`) is the most likely caller to need this. Before creating a bug work item, M should:

1. Read the bug symptoms from the user
2. Mentally run through the 7 escalation criteria
3. If any apply, add `--label needs-investigation` when creating the work item
4. If creating via skill, the skill should prompt M to make this decision explicitly

This belongs in the CLAUDE.md / AGENTS.md that the orchestrator reads, in the "creating work items" section. It should be a small decision tree, not buried prose.

### Fail-loud signal

If dispatch ever routes a bug to the `fix` phase and the fix agent realises mid-session that the cause is genuinely unknown or the blast radius is unclear, it should:

1. Stop implementing
2. Append findings so far to the bug
3. Add the `needs-investigation` label
4. Exit and let the orchestrator re-dispatch through the investigation flow

This self-escalation path prevents agents from flailing on bugs they can't fix safely.

### New Task for Wave 3

**Task I: Escalation documentation and self-escalation**
- Update all docs/skills listed in the table above
- Add escalation criteria to `skills/fix/fix-bug.md` with inline decision checklist
- Add bug-creation guidance to CLAUDE.md / generated AGENTS.md: "when creating a bug, apply these criteria to decide labeling"
- Add self-escalation protocol to `skills/fix/fix-bug.md`
- Verify `cobuild explain` renders the new workflows cleanly

Dependencies: blocked by Task C (needs `skills/fix/fix-bug.md` to exist first)