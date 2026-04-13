---
name: dispatch-review
version: "0.1"
description: Review a task PR in a dispatched agent session and write the gate verdict file for the runtime exit hook.
summary: >-
  Read the task, parent design, and PR diff, run any optional review checks,
  then write `.cobuild/gate-verdict.json` with a pass or fail review verdict.
---

# Skill: Dispatched PR Review

You are a dispatched CoBuild review agent. Review the task's pull request against the task scope and parent design, then write the review verdict file the runtime will record after you exit.

## Inputs

- Task shard ID
- PR number or URL
- Repository from local checkout or task metadata

## Steps

1. Read the task shard so you know the promised scope and acceptance criteria:

   ```bash
   cobuild wi show <task-id>
   ```

2. Read the parent design when one exists. Use the linked design ID from the task if it is present:

   ```bash
   cobuild wi links <task-id>
   cobuild wi show <design-id>
   ```

3. Read the PR diff in full:

   ```bash
   gh pr diff <pr-number> --repo <owner/repo>
   ```

4. Read the changed files locally when the diff points to behavior that needs more context. Check whether the implementation matches the task, the design, and surrounding code patterns.

5. Run optional review checks if the project provides them. If `skills/review/checks.md` exists, follow it. Otherwise run only the smallest targeted checks needed to validate the suspicious areas you found.

6. Decide the verdict:

   - `pass` when the PR satisfies the task scope, matches the design intent, and has no blocking issues.
   - `fail` when you find a real defect, regression risk, missing acceptance coverage, or a clear mismatch with the task/design.

7. Write `.cobuild/gate-verdict.json` in the repo root with this shape:

   ```json
   {
     "gate": "review",
     "shard_id": "<task-id>",
     "verdict": "pass|fail",
     "body": "Concise review findings and rationale"
   }
   ```

   The `body` should be actionable. When failing, include the specific problems and what needs to change.

## Review standard

- Review against the task acceptance criteria first.
- Use the parent design to catch scope drift and architectural mismatches.
- Prefer concrete findings over general commentary.
- Do not edit code in this session unless the dispatch prompt explicitly tells you this is a combined review-and-fix flow.

## Final step

After writing `.cobuild/gate-verdict.json`, stop and exit the session with `/exit`. Do not run `cobuild complete`.
