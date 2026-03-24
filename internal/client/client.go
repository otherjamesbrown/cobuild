package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"gopkg.in/yaml.v3"
)

// ConnectionConfig holds database connection settings.
type ConnectionConfig struct {
	Host     string `yaml:"host"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	SSLMode  string `yaml:"sslmode"`
}

// DefaultsConfig holds default flag values.
type DefaultsConfig struct {
	Output string `yaml:"output"`
}

// ClientConfig holds the cobuild CLI configuration.
type ClientConfig struct {
	Connection ConnectionConfig `yaml:"connection"`
	Agent      string           `yaml:"agent"`
	Project    string           `yaml:"project"`
	Defaults   *DefaultsConfig  `yaml:"defaults,omitempty"`
}

// Client provides database operations against the Context Palace DB.
type Client struct {
	Config *ClientConfig
}

// NewClient creates a new client with the given config.
func NewClient(cfg *ClientConfig) *Client {
	return &Client{Config: cfg}
}

// Connect opens a database connection.
func (c *Client) Connect(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, c.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Context Palace at %s: %v", c.Config.Connection.Host, err)
	}
	_ = pgxvec.RegisterTypes(ctx, conn)
	return conn, nil
}

// ConnectionString returns the PostgreSQL connection string.
func (c *Client) ConnectionString() string {
	cfg := c.Config.Connection
	sslmode := cfg.SSLMode
	if sslmode == "" {
		sslmode = "verify-full"
	}
	return fmt.Sprintf(
		"host=%s dbname=%s user=%s sslmode=%s",
		cfg.Host, cfg.Database, cfg.User, sslmode,
	)
}

const (
	projectConfigName     = ".cobuild.yaml"
	legacyProjectConfig   = ".cxp.yaml"
	globalConfigDirName   = ".cobuild"
	legacyGlobalConfigDir = ".cxp"
)

// LoadClientConfig loads configuration with precedence:
// env vars > .cobuild.yaml (project) > ~/.cobuild/config.yaml (global) > defaults
func LoadClientConfig(configOverride string) (*ClientConfig, error) {
	cfg := &ClientConfig{
		Connection: ConnectionConfig{
			Host:     "dev02.brown.chat",
			Database: "contextpalace",
			SSLMode:  "verify-full",
		},
	}

	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, globalConfigDirName, "config.yaml")
		if configOverride != "" {
			globalPath = configOverride
			if err := loadYAML(globalPath, cfg); err != nil {
				return nil, err
			}
		} else {
			legacyGlobalPath := filepath.Join(home, legacyGlobalConfigDir, "config.yaml")
			_ = loadYAML(legacyGlobalPath, cfg)
			_ = loadYAML(globalPath, cfg)
		}
	}

	if configOverride == "" {
		if projectPath := findProjectConfig(); projectPath != "" {
			if err := loadProjectConfig(projectPath, cfg); err != nil {
				return nil, err
			}
		}
	}

	if v := firstEnv("COBUILD_HOST", "CXP_HOST", "CP_HOST"); v != "" {
		cfg.Connection.Host = v
	}
	if v := firstEnv("COBUILD_DATABASE", "CXP_DATABASE", "CP_DATABASE"); v != "" {
		cfg.Connection.Database = v
	}
	if v := firstEnv("COBUILD_USER", "CXP_USER", "CP_USER"); v != "" {
		cfg.Connection.User = v
	}
	if v := firstEnv("COBUILD_PROJECT", "CXP_PROJECT", "CP_PROJECT"); v != "" {
		cfg.Project = v
	}
	if v := firstEnv("COBUILD_AGENT", "CXP_AGENT", "CP_AGENT"); v != "" {
		cfg.Agent = v
	}

	if cfg.Connection.User == "" {
		return nil, fmt.Errorf("database user is required (set via COBUILD_USER, .cobuild.yaml, or ~/.cobuild/config.yaml)")
	}
	if cfg.Agent == "" {
		return nil, fmt.Errorf("agent identity is required (set via COBUILD_AGENT, .cobuild.yaml, or ~/.cobuild/config.yaml)")
	}

	return cfg, nil
}

type projectConfig struct {
	Project string `yaml:"project"`
	Agent   string `yaml:"agent"`
}

func findProjectConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		path := filepath.Join(dir, projectConfigName)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		legacyPath := filepath.Join(dir, legacyProjectConfig)
		if _, err := os.Stat(legacyPath); err == nil {
			return legacyPath
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func loadYAML(path string, cfg *ClientConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("error reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("error parsing %s: %w", path, err)
	}
	return nil
}

func loadProjectConfig(path string, cfg *ClientConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("error reading %s: %w", path, err)
	}
	var pc projectConfig
	if err := yaml.Unmarshal(data, &pc); err != nil {
		return fmt.Errorf("error parsing %s: %w", path, err)
	}
	if pc.Project != "" {
		cfg.Project = pc.Project
	}
	if pc.Agent != "" {
		cfg.Agent = pc.Agent
	}
	return nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

// GitRepoRoot returns the root of the git repository containing dir.
func GitRepoRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repository")
		}
		dir = parent
	}
}
