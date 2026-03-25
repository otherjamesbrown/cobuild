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
```bash
cobuild task get <task-id>
```
Verify:
- Task has label `approved`
- Task has `pr_url` in metadata

### 2. Merge
```bash
gh pr merge <pr-number> --squash
```
This squash-merges the PR. Then clean up the worktree and close the task:
```bash
cobuild worktree remove <task-id>
cobuild wi status <task-id> closed
```

### 3. Post-merge verification
```bash
cd ~/github/otherjamesbrown/context-palace
git pull
go test ./...
```

### 4. If post-merge tests fail
```bash
# Revert the merge
git revert <merge-commit> --no-edit
git push

# File a bug
cobuild wi create --type bug \
  --title "Post-merge test failure from <task-id>" \
  --body "Merge commit <hash> broke tests. Reverted. Error: <test output>" \
  --label "blocked"

# Re-open the task
cobuild wi status <task-id> in_progress
cobuild wi append <task-id> --body "Post-merge tests failed. Merge reverted. See bug for details."
```

### 5. If tests pass
```bash
cobuild wi append <task-id> --body "Merged and verified. Post-merge tests pass."
```

### 6. Check if all tasks for this design are done
```bash
cobuild deps <design-id>
```
If all tasks are closed:
```bash
cobuild pipeline update <design-id> --phase review
cobuild wi append <design-id> --body "All tasks merged. Moving to design-level review."
```

## Gotchas

<!-- Add failure patterns here as they're discovered -->
