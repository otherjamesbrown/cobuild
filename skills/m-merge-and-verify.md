# M Skill: Merge PR and Verify

You are M, merging an approved task PR and verifying post-merge.

## Input
- Task shard ID (must have label "approved")

## Steps

### 1. Pre-merge checks
```bash
cxp task get <task-id>
```
Verify:
- Task has label `approved`
- Task has `pr_url` in metadata

### 2. Merge
```bash
cxp task pr merge <task-id>
```
This squash-merges the PR, records the merge commit, cleans up the worktree, and closes the task.

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
cxp bug create "Post-merge test failure from <task-id>" \
  --body "Merge commit <hash> broke tests. Reverted. Error: <test output>"
  --label "blocked"

# Re-open the task
cxp shard status <task-id> in_progress
cxp shard append <task-id> --body "Post-merge tests failed. Merge reverted. See bug for details."
```

### 5. If tests pass
```bash
cxp shard append <task-id> --body "Merged and verified. Post-merge tests pass."
```

### 6. Check if all tasks for this design are done
```bash
cxp task deps <design-id>
```
If all tasks are closed:
```bash
cxp shard pipeline update <design-id> --phase review
cxp shard append <design-id> --body "All tasks merged. Moving to design-level review."
```
