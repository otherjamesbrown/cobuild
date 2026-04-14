# wx-1000 Design: Release Banner For Demo Service

## Problem

Operators deploy the demo service, but they have no obvious confirmation that a
release contains a customer-facing note. The deploy is technically healthy, yet
support still has to inspect logs or source to confirm what changed.

## User

Primary user: the on-call operator who needs a fast "did the release note land?"
answer after deploy.

## Success Criteria

- `GET /api/release` returns `banner_text`, `severity`, and `updated_at`.
- The home page renders a release banner when `banner_text` is non-empty.
- The smoke path checks both `/healthz` and `/api/release`.
- A rollback removes the banner cleanly by restoring the previous deploy.

## Out Of Scope

- Rich-text editing for release notes.
- Auth or role management.
- Historical banner archives.

## Technical Approach

- Add a small release payload in `internal/release/store.go`.
- Expose it from `internal/http/release_handler.go`.
- Render the banner in `web/src/components/release-banner.tsx`.
- Update the smoke test so deploy validation exercises the new endpoint.

## Rollout

Split the work into API, UI, and smoke/deploy tasks. Merge them through normal
review, then approve the deploy once the smoke test path is in place.
