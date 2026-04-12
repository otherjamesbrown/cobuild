---
name: merge-and-verify
version: "0.1"
description: Merge an approved PR, run post-merge tests, and auto-revert on failure. Trigger after a task PR is approved.
summary: >-
  Merges an approved PR, runs post-merge tests on main, and auto-reverts if tests fail. After merging, checks whether all tasks for the design are done — if so, advances the pipeline to the done phase.
---

# Skill: Merge PR and Verify

You are the pipeline orchestrator, merging an approved task PR and verifying post-merge.

## Input
- Task shard ID (must have label "approved")

## Steps

### 1. Pre-merge checks
Inspect the task before merging.

Verify:
- Task has label `approved`
- Task has `pr_url` in metadata

### 2. Merge
Merge the approved PR using the repository's normal protected-branch workflow. After the merge lands, remove the task worktree and close the task shard.

### 3. Post-merge verification
Update the post-merge verification checkout to the latest mainline state and run the project's standard verification suite there.

### 4. If post-merge tests fail
Revert the merge through the normal safe path for this repo, create a blocked bug shard that captures the failing commit and test output, then reopen the original task and append a note linking the failure back to that bug.

### 5. If tests pass
Append evidence that the merge landed and post-merge verification passed.

### 6. Check if all tasks for this design are done
Inspect the parent design's task graph. If every child task is now closed, advance the design pipeline to the review phase and append a note explaining that implementation is complete and design-level review can begin.

## Gotchas

<!-- Add failure patterns here as they're discovered -->

## Final Step

After you have finished the merge verification work and recorded the outcome, exit the session immediately. Run `/exit` so the dispatched session terminates cleanly.
## Final step

After merge handling and post-merge verification are recorded, stop. This merge skill is not a task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
