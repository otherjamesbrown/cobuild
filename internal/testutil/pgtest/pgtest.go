package pgtest

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
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
	BaseDSN string
	DSN     string
	Schema  string
	Pool    *pgxpool.Pool
	Store   *store.PostgresStore
}

func New(tb testing.TB, ctx context.Context) *Harness {
	tb.Helper()

	baseDSN := DSN(tb)
	schema := SchemaName(tb)
	dsn := DSNWithSchema(tb, baseDSN, schema)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgxpool.New() error = %v", err)
	}
	tb.Cleanup(func() {
		pool.Close()
		dropSchema(tb, ctx, baseDSN, schema)
	})

	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		tb.Fatalf("create schema %s: %v", schema, err)
	}

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
		BaseDSN: baseDSN,
		DSN:     dsn,
		Schema:  schema,
		Pool:    pool,
		Store:   s,
	}
}

func SchemaName(tb testing.TB) string {
	tb.Helper()

	name := strings.ToLower(tb.Name())
	name = schemaNameSanitizer.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "test"
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(tb.Name()))
	return fmt.Sprintf("cbt_%s_%08x", name, h.Sum32())
}

func DSNWithSchema(tb testing.TB, dsn, schema string) string {
	tb.Helper()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("pgxpool.ParseConfig() error = %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	return cfg.ConnString()
}

func DSN(tb testing.TB) string {
	tb.Helper()

	if dsn := strings.TrimSpace(os.Getenv("COBUILD_TEST_POSTGRES_DSN")); dsn != "" {
		return dsn
	}

	home, err := os.UserHomeDir()
	if err != nil {
		tb.Skip("set COBUILD_TEST_POSTGRES_DSN or configure ~/.cobuild/config.yaml before running Postgres-backed tests")
	}
	cfgPath := filepath.Join(home, ".cobuild", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		tb.Skip("set COBUILD_TEST_POSTGRES_DSN or configure ~/.cobuild/config.yaml before running Postgres-backed tests")
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		tb.Skipf("parse %s: %v", cfgPath, err)
	}
	if cfg.Connection.Host == "" || cfg.Connection.Database == "" || cfg.Connection.User == "" {
		tb.Skipf("set COBUILD_TEST_POSTGRES_DSN or complete the Postgres connection in %s before running Postgres-backed tests", cfgPath)
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

var schemaNameSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

func dropSchema(tb testing.TB, ctx context.Context, dsn, schema string) {
	tb.Helper()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgxpool.New() for schema cleanup error = %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		tb.Fatalf("drop schema %s: %v", schema, err)
	}
}
