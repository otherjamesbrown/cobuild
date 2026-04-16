# Escalation And Budgets

Use this file when a phase spoke tells you to escalate, or when retry/iteration limits are in play.

## Escalate when

- A design fails implementability and you cannot identify what is missing
- A task stalls for more than 10 iterations
- Review round 3 still requests changes
- You find a circular dependency in the task graph
- An agent crashes repeatedly on the same task and exceeds `max_retries`
- The same review finding repeats in 2 consecutive rounds (auto-blocked — fix the code, then `cobuild reset`)
- Post-merge tests fail
- Any ambiguity remains that you cannot resolve from the shard and config

## Escalation format

```bash
cobuild wi append <id> --body "## Escalation
**Issue:** <one sentence>
**Context:** <what you tried>
**Decision needed:** <specific question for the developer>"
cobuild wi label add <id> blocked
```

## Iteration budgets

| Limit | Default | Action when exceeded |
|---|---:|---|
| Review rounds per gate | 5 | Close the loop and proceed |
| Review rounds per PR | 3 | Re-scope or escalate |
| Max concurrent agents | 3 | Queue dispatches |
| Stall timeout | 30m | Run the configured health action |
| Max retries per task | 3 | Escalate |

## Final step

After appending the escalation note and applying any blocking label, stop routing work in this session. Do not run `cobuild complete` from this escalation guide. Exit the session with `/exit`.
