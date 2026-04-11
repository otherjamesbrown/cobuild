package store

import (
	"context"
	"fmt"
	"os"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

// New creates a Store from pipeline config and client connection settings.
// If no storage backend is configured, defaults to postgres using the
// client's existing connection settings.
//
// Runs the store's Migrate() on successful connect so schema changes added
// to Migrate() actually land on the database — previously Migrate() was a
// write-only bucket that never ran anywhere, and schema drift (like the
// missing pipeline_sessions.runtime column that caused 42703 errors after
// the codex runtime refactor) was invisible until the next INSERT failed.
// Migration errors are surfaced as a stderr warning, not a hard failure —
// the store is still usable with its current schema, the only cost is
// that new columns/tables won't be present.
func New(ctx context.Context, cfg *config.Config, dsn string) (Store, error) {
	backend := "postgres"
	if cfg != nil && cfg.Storage.Backend != "" {
		backend = cfg.Storage.Backend
	}

	// Allow storage config to override the DSN
	if cfg != nil && cfg.Storage.DSN != "" {
		dsn = cfg.Storage.DSN
	}

	switch backend {
	case "postgres", "pg":
		if dsn == "" {
			return nil, fmt.Errorf("postgres store requires a DSN (set storage.dsn or configure connection)")
		}
		s, err := NewPostgresStore(ctx, dsn)
		if err != nil {
			return nil, err
		}
		if err := s.Migrate(ctx); err != nil {
			// Non-fatal: schema may be current or the user may lack DDL
			// permissions. Warn loudly so drift is visible, but keep the
			// store usable for read paths + any queries that match the
			// current schema.
			fmt.Fprintf(os.Stderr, "Warning: store migrate failed (schema may be stale): %v\n", err)
		}
		return s, nil

	default:
		return nil, fmt.Errorf("unknown storage backend: %q (supported: postgres)", backend)
	}
}
