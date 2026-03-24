package store

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

// New creates a Store from pipeline config and client connection settings.
// If no storage backend is configured, defaults to postgres using the
// client's existing connection settings.
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
		return NewPostgresStore(ctx, dsn)

	default:
		return nil, fmt.Errorf("unknown storage backend: %q (supported: postgres)", backend)
	}
}
