# Phase 4 — Review Gate

Use this file only when `pipeline.phase = review`.

## Choose the review strategy

Read `review.strategy` from pipeline config.

### `strategy: external`

1. Wait for CI completion if `review.ci.wait: true`.
2. Wait for external reviewer comments.
3. Follow `skills/review/gate-process-review.md` to evaluate CI and comments.
4. Record the verdict:

```bash
cobuild task review-verdict <task-id> approve|request-changes|escalate
```

### `strategy: agent`

1. Spawn the configured review agent with `review_skill`.
2. Have the agent evaluate the PR against the task and design.
3. Record the verdict.

## Merge

```bash
gh pr merge <pr-number> --squash
cobuild worktree remove <task-id>
cobuild wi status <task-id> closed
```

This merges the PR, cleans up the worktree, closes the task, and triggers deploy handling from config if applicable.

## Design-level verification

When all tasks are merged:

1. Check the built result against the design success criteria.
2. If gaps remain, create follow-up tasks and route back to implement.
3. If complete, advance to done:

```bash
cobuild update <id> --phase done
```

## Final step

After recording review outcomes and taking the merge or phase-advance action for this pass, stop. This phase guide is not a dispatched task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
