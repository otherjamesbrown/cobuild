# M Skill: Merge PR and Verify

You are M, merging an approved task PR and verifying post-merge.

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
cobuild shard status <task-id> closed
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
cobuild shard create --type bug \
  --title "Post-merge test failure from <task-id>" \
  --body "Merge commit <hash> broke tests. Reverted. Error: <test output>" \
  --label "blocked"

# Re-open the task
cobuild shard status <task-id> in_progress
cobuild shard append <task-id> --body "Post-merge tests failed. Merge reverted. See bug for details."
```

### 5. If tests pass
```bash
cobuild shard append <task-id> --body "Merged and verified. Post-merge tests pass."
```

### 6. Check if all tasks for this design are done
```bash
cobuild deps <design-id>
```
If all tasks are closed:
```bash
cobuild pipeline update <design-id> --phase review
cobuild shard append <design-id> --body "All tasks merged. Moving to design-level review."
```
