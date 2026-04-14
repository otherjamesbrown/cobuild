package pgtest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"gopkg.in/yaml.v3"
)

const postgresDSNEnv = "COBUILD_TEST_POSTGRES_DSN"

var ErrNoPostgres = errors.New("postgres unavailable for tests")

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

	baseDSN := requireDSN(tb, ctx)
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

type noPostgresError struct {
	reason string
}

func (e *noPostgresError) Error() string {
	return e.reason
}

func (e *noPostgresError) Unwrap() error {
	return ErrNoPostgres
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return requireDSN(tb, ctx)
}

func Skip(tb testing.TB, ctx context.Context) {
	tb.Helper()
	_, err := Probe(tb, ctx)
	if err == nil {
		return
	}
	if errors.Is(err, ErrNoPostgres) {
		tb.Skip(err.Error())
	}
	tb.Fatalf("resolve Postgres-backed test dependency: %v", err)
}

func Probe(tb testing.TB, ctx context.Context) (string, error) {
	tb.Helper()

	dsn, source, err := configuredDSN()
	if err != nil {
		return "", err
	}

	probeCtx, cancel := probeContext(ctx)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		if source == postgresDSNEnv {
			return "", fmt.Errorf("parse %s: %w", postgresDSNEnv, err)
		}
		return "", noPostgresf("postgres test config in %s produced an invalid DSN: %v", source, err)
	}

	pool, err := pgxpool.NewWithConfig(probeCtx, cfg)
	if err != nil {
		return "", noPostgresf("postgres not reachable via %s, set %s or configure ~/.cobuild/config.yaml to enable Postgres-backed tests", source, postgresDSNEnv)
	}
	defer pool.Close()

	if err := pool.Ping(probeCtx); err != nil {
		return "", noPostgresf("postgres not reachable via %s, set %s or configure ~/.cobuild/config.yaml to enable Postgres-backed tests", source, postgresDSNEnv)
	}

	return dsn, nil
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

func configuredDSN() (string, string, error) {
	if dsn := strings.TrimSpace(os.Getenv(postgresDSNEnv)); dsn != "" {
		return dsn, postgresDSNEnv, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", noPostgresf("postgres not configured; set %s or configure ~/.cobuild/config.yaml to enable Postgres-backed tests", postgresDSNEnv)
	}
	cfgPath := filepath.Join(home, ".cobuild", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", "", noPostgresf("postgres not configured; set %s or configure %s to enable Postgres-backed tests", postgresDSNEnv, cfgPath)
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", cfgPath, noPostgresf("postgres test config in %s is invalid: %v", cfgPath, err)
	}
	if cfg.Connection.Host == "" || cfg.Connection.Database == "" || cfg.Connection.User == "" {
		return "", cfgPath, noPostgresf("postgres test config in %s is incomplete; set %s or complete the connection settings to enable Postgres-backed tests", cfgPath, postgresDSNEnv)
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
	), cfgPath, nil
}

func noPostgresf(format string, args ...any) error {
	return &noPostgresError{reason: fmt.Sprintf(format, args...)}
}

func probeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, 2*time.Second)
}

func requireDSN(tb testing.TB, ctx context.Context) string {
	tb.Helper()

	dsn, err := Probe(tb, ctx)
	if err == nil {
		return dsn
	}
	if errors.Is(err, ErrNoPostgres) {
		tb.Skip(err.Error())
	}
	tb.Fatalf("resolve Postgres-backed test dependency: %v", err)
	return ""
}
