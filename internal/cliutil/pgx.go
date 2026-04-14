package cliutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"gopkg.in/yaml.v3"
)

// StoreConfig holds the pieces of ~/.cobuild/config.yaml that cobuild
// needs to stand up its own store and connector. No dev02.brown.chat
// fallback — if the user hasn't put connection details in the config
// file or env vars, we refuse to connect rather than silently guess
// (cb-3f5be6 / cb-b2f3ac big-bang migration).
type StoreConfig struct {
	Connection ConnectionConfig `yaml:"connection"`
	Agent      string           `yaml:"agent"`
	Project    string           `yaml:"project"`
	Prefix     string           `yaml:"prefix"`
	Defaults   *DefaultsConfig  `yaml:"defaults,omitempty"`
}

// ConnectionConfig holds database connection settings.
type ConnectionConfig struct {
	Host     string `yaml:"host"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	SSLMode  string `yaml:"sslmode"`
}

// DefaultsConfig holds default flag values read from config.
type DefaultsConfig struct {
	Output string `yaml:"output"`
}

// LoadStoreConfig reads ~/.cobuild/config.yaml (or the override path) and
// applies COBUILD_* env var overrides. Returns an error if the resulting
// config is missing any required connection field (host / database / user)
// — the legacy .cxp fallback and the dev02.brown.chat default host are
// both gone after cb-3f5be6. Callers that don't need a store (e.g. local
// `cobuild --help`) should not call this.
func LoadStoreConfig(configOverride string) (*StoreConfig, error) {
	cfg := &StoreConfig{
		Connection: ConnectionConfig{SSLMode: "verify-full"},
	}

	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".cobuild", "config.yaml")
		if configOverride != "" {
			path = configOverride
		}
		if err := loadYAMLInto(path, cfg); err != nil {
			return nil, err
		}
	}

	if v := os.Getenv("COBUILD_HOST"); v != "" {
		cfg.Connection.Host = v
	}
	if v := os.Getenv("COBUILD_DATABASE"); v != "" {
		cfg.Connection.Database = v
	}
	if v := os.Getenv("COBUILD_USER"); v != "" {
		cfg.Connection.User = v
	}
	if v := os.Getenv("COBUILD_SSLMODE"); v != "" {
		cfg.Connection.SSLMode = v
	}
	if v := os.Getenv("COBUILD_PROJECT"); v != "" {
		cfg.Project = v
	}
	if v := os.Getenv("COBUILD_AGENT"); v != "" {
		cfg.Agent = v
	}

	if cfg.Connection.Host == "" {
		return nil, fmt.Errorf("database host is required (set via COBUILD_HOST or ~/.cobuild/config.yaml)")
	}
	if cfg.Connection.Database == "" {
		return nil, fmt.Errorf("database name is required (set via COBUILD_DATABASE or ~/.cobuild/config.yaml)")
	}
	if cfg.Connection.User == "" {
		return nil, fmt.Errorf("database user is required (set via COBUILD_USER or ~/.cobuild/config.yaml)")
	}

	return cfg, nil
}

// DSN returns the PostgreSQL connection string built from cfg.
func (cfg *StoreConfig) DSN() string {
	sslmode := cfg.Connection.SSLMode
	if sslmode == "" {
		sslmode = "verify-full"
	}
	return fmt.Sprintf(
		"host=%s dbname=%s user=%s sslmode=%s",
		cfg.Connection.Host, cfg.Connection.Database, cfg.Connection.User, sslmode,
	)
}

// ConnectPostgres opens a pgx connection to dsn and registers the
// pgvector type codecs. Callers own the returned connection's lifecycle.
func ConnectPostgres(ctx context.Context, dsn string) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	_ = pgxvec.RegisterTypes(ctx, conn)
	return conn, nil
}

func loadYAMLInto(path string, cfg *StoreConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
