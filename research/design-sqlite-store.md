# Design: SQLite Store

## Summary

A `SQLiteStore` implementation of the `Store` interface for single-user / local-first CoBuild usage. No external database required — just a single file.

## Motivation

Postgres is the right choice for teams but overkill for a solo developer running CoBuild on their laptop. SQLite gives:
- Zero setup (no server to install or connect to)
- Single file, easy to backup/move (`cp cobuild.db cobuild.db.bak`)
- Fast for the access patterns CoBuild uses (low concurrency, small datasets)
- Battle-tested in production (SQLite handles billions of deployments via D1, Litestream, etc.)

## Config

```yaml
storage:
    backend: sqlite
    path: .cobuild/cobuild.db    # relative to repo root, or absolute
```

Default path when `path` is empty: `.cobuild/cobuild.db`

Global store (shared across projects): `~/.cobuild/cobuild.db`

## Schema

The same tables as Postgres, with SQLite-compatible DDL:

```sql
CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY,
    design_id TEXT NOT NULL UNIQUE,
    project TEXT NOT NULL,
    current_phase TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS pipeline_gates (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL,
    design_id TEXT NOT NULL,
    gate_name TEXT NOT NULL,
    phase TEXT NOT NULL,
    round INTEGER NOT NULL,
    verdict TEXT NOT NULL,
    reviewer TEXT,
    readiness_score INTEGER,
    task_count INTEGER,
    body TEXT,
    review_shard_id TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(pipeline_id, gate_name, round)
);

CREATE TABLE IF NOT EXISTS pipeline_tasks (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL,
    task_shard_id TEXT NOT NULL,
    design_id TEXT NOT NULL,
    wave INTEGER,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Key differences from Postgres:
- `TIMESTAMPTZ` → `TEXT` (ISO8601 strings)
- `NOW()` → `datetime('now')`
- No `REFERENCES` (SQLite supports them but they're off by default)
- No `EXTRACT(EPOCH FROM ...)` — use `julianday()` for duration calculations

## Implementation

### Go library

Use `modernc.org/sqlite` (pure Go, no CGO) or `github.com/mattn/go-sqlite3` (CGO, faster). Pure Go is preferred for zero-dependency builds.

```go
type SQLiteStore struct {
    db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
    db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
    if err != nil {
        return nil, err
    }
    // WAL mode for better concurrent read performance
    db.Exec("PRAGMA journal_mode=WAL")
    db.Exec("PRAGMA foreign_keys=ON")
    return &SQLiteStore{db: db}, nil
}
```

### Query differences from Postgres

| Postgres | SQLite | Used in |
|----------|--------|---------|
| `$1, $2` | `?, ?` | All queries (use positional `?` params) |
| `NOW()` | `datetime('now')` | INSERT defaults |
| `EXTRACT(EPOCH FROM (a - b)) / 60` | `(julianday(a) - julianday(b)) * 1440` | GetAvgTaskDuration |
| `COUNT(*) FILTER (WHERE ...)` | `SUM(CASE WHEN ... THEN 1 ELSE 0 END)` | GetGatePassRates |
| `COUNT(DISTINCT (a, b))` | `COUNT(DISTINCT a \|\| '::' \|\| b)` | GetGatePassRates |

### Concurrency

SQLite uses file-level locking. CoBuild's access pattern is mostly:
- One writer at a time (the poller or a single command)
- Multiple concurrent readers (insights, show, audit)

WAL mode handles this well. The `_timeout=5000` busy timeout prevents "database locked" errors during brief write contention.

### Migration

Same `Migrate(ctx)` method, just runs the SQLite DDL instead of Postgres DDL. The `CREATE TABLE IF NOT EXISTS` pattern works identically.

## Testing

The SQLiteStore should pass the same test suite as PostgresStore. Define a `StoreTestSuite` that runs against the `Store` interface, then instantiate it with both backends:

```go
func TestPostgresStore(t *testing.T) { runStoreSuite(t, newTestPostgresStore()) }
func TestSQLiteStore(t *testing.T)   { runStoreSuite(t, newTestSQLiteStore()) }
```

## Open Questions

- Should SQLite be the default for new `cobuild setup` installations? (Postgres only if configured)
- Should we support in-memory SQLite (`:memory:`) for tests?
- Should the migration auto-run on first use, or require `cobuild migrate`?
