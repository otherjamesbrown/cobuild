# Phase 5 — Done

Use this file only when `pipeline.phase = done`.

Run the retrospective gate if it is configured:

```bash
cobuild gate <id> retrospective --verdict pass --body "<findings>"
```

Follow `skills/done/gate-retrospective.md` for the full retrospective procedure.

## Retrospective checklist

1. Review the audit trail: `cobuild audit <id>`
2. Review insights: `cobuild insights`
3. Generate improvements: `cobuild improve`
4. Record findings as a knowledge shard
5. Close the design: `cobuild wi status <id> closed`

Do not skip the knowledge capture step. The point of `done` is to improve the next run, not just close the item.

## Final step

After the retrospective work is recorded and the item is closed, stop. This phase guide is not a dispatched task implementation context, so do not run `cobuild complete`. Exit the session with `/exit`.
