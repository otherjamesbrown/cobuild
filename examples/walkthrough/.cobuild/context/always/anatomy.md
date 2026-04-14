# Walkthrough Anatomy

This walkthrough is a miniature repo root kept inside the `cobuild` repo so
the example can ship with tests.

- `.cobuild.yaml` gives the project name and shard prefix.
- `.cobuild/pipeline.yaml` is the small pipeline config.
- `skills/` holds repo-local skill markdown.
- `work-items/` stands in for the shard bodies a connector would normally return.
- `artifacts/` holds parked examples of temporary pipeline files such as
  `.cobuild/gate-verdict.json`.

The fictional project in these shard files is a tiny "demo-service" with one
API surface, one UI banner, and one deploy smoke check.
