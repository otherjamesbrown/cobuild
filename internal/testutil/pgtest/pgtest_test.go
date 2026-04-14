package pgtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredDSNWithoutEnvOrConfigReturnsNoPostgres(t *testing.T) {
	t.Setenv(postgresDSNEnv, "")
	t.Setenv("HOME", t.TempDir())

	_, _, err := configuredDSN()
	if !errors.Is(err, ErrNoPostgres) {
		t.Fatalf("configuredDSN() error = %v, want ErrNoPostgres", err)
	}
	if !strings.Contains(err.Error(), postgresDSNEnv) {
		t.Fatalf("configuredDSN() error = %q, want %s hint", err, postgresDSNEnv)
	}
}

func TestConfiguredDSNBuildsFromConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(postgresDSNEnv, "")
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".cobuild")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := []byte("connection:\n  host: db.example\n  database: cobuild\n  user: tester\n  sslmode: require\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), cfg, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dsn, source, err := configuredDSN()
	if err != nil {
		t.Fatalf("configuredDSN() error = %v", err)
	}
	if source != filepath.Join(cfgDir, "config.yaml") {
		t.Fatalf("configuredDSN() source = %q", source)
	}
	want := "postgres://tester@db.example/cobuild?sslmode=require"
	if dsn != want {
		t.Fatalf("configuredDSN() dsn = %q, want %q", dsn, want)
	}
}

func TestProbeReturnsNoPostgresWhenServerIsUnreachable(t *testing.T) {
	t.Setenv(postgresDSNEnv, "postgres://tester@127.0.0.1:1/cobuild?sslmode=disable")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := Probe(t, ctx)
	if !errors.Is(err, ErrNoPostgres) {
		t.Fatalf("Probe() error = %v, want ErrNoPostgres", err)
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("Probe() error = %q, want reachability hint", err)
	}
}
