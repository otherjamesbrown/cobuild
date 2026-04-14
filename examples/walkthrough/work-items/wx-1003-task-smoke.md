# wx-1003 Task: Add Smoke Coverage For Deploy

Parent design: `wx-1000`

Depends on: `wx-1001`, `wx-1002`

## Goal

Make deploy verification assert both service health and banner availability.

## Acceptance Criteria

- The smoke check hits `/healthz`.
- The smoke check hits `/api/release`.
- The deploy notes explain the rollback trigger when either check fails.

## Likely Files

- `scripts/smoke-release.sh`
- `docs/runbooks/deploy.md`
