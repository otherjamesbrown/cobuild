# wx-1001 Task: Add Release Banner API Payload

Parent design: `wx-1000`

## Goal

Expose the release banner fields from the demo service API.

## Acceptance Criteria

- `GET /api/release` returns `banner_text`, `severity`, and `updated_at`.
- Empty release notes return an empty banner payload instead of an error.
- Unit coverage exercises both populated and empty responses.

## Likely Files

- `internal/release/store.go`
- `internal/http/release_handler.go`
- `internal/http/release_handler_test.go`
