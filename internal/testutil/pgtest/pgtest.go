package pgtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"gopkg.in/yaml.v3"
)

type configFile struct {
	Connection struct {
		Host     string `yaml:"host"`
		Database string `yaml:"database"`
		User     string `yaml:"user"`
		SSLMode  string `yaml:"sslmode"`
	} `yaml:"connection"`
}

type Harness struct {
	DSN   string
	Pool  *pgxpool.Pool
	Store *store.PostgresStore
}

func New(tb testing.TB, ctx context.Context) *Harness {
	tb.Helper()

	dsn := DSN(tb)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgxpool.New() error = %v", err)
	}
	tb.Cleanup(pool.Close)

	s, err := store.NewPostgresStore(ctx, dsn)
	if err != nil {
		tb.Fatalf("store.NewPostgresStore() error = %v", err)
	}
	tb.Cleanup(func() {
		_ = s.Close()
	})

	if err := s.Migrate(ctx); err != nil {
		tb.Fatalf("Migrate() error = %v", err)
	}

	return &Harness{
		DSN:   dsn,
		Pool:  pool,
		Store: s,
	}
}

func DSN(tb testing.TB) string {
	tb.Helper()

	if dsn := strings.TrimSpace(os.Getenv("COBUILD_TEST_POSTGRES_DSN")); dsn != "" {
		return dsn
	}

	home, err := os.UserHomeDir()
	if err != nil {
		tb.Skip("set COBUILD_TEST_POSTGRES_DSN for store integration tests")
	}
	cfgPath := filepath.Join(home, ".cobuild", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		tb.Skip("set COBUILD_TEST_POSTGRES_DSN for store integration tests")
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		tb.Skipf("parse %s: %v", cfgPath, err)
	}
	if cfg.Connection.Host == "" || cfg.Connection.Database == "" || cfg.Connection.User == "" {
		tb.Skipf("set COBUILD_TEST_POSTGRES_DSN for store integration tests; incomplete connection config in %s", cfgPath)
	}

	sslMode := cfg.Connection.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	return fmt.Sprintf(
		"postgres://%s@%s/%s?sslmode=%s",
		cfg.Connection.User,
		cfg.Connection.Host,
		cfg.Connection.Database,
		sslMode,
	)
}

func (h *Harness) CleanupDesign(tb testing.TB, ctx context.Context, designID string) {
	tb.Helper()

	if _, err := h.Pool.Exec(ctx, `DELETE FROM pipeline_sessions WHERE design_id = $1`, designID); err != nil {
		tb.Fatalf("cleanup pipeline_sessions for %s: %v", designID, err)
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM pipeline_tasks WHERE design_id = $1`, designID); err != nil {
		tb.Fatalf("cleanup pipeline_tasks for %s: %v", designID, err)
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE design_id = $1`, designID); err != nil {
		tb.Fatalf("cleanup pipeline_runs for %s: %v", designID, err)
	}
}
