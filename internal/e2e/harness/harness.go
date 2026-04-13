package harness

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/testutil/pgtest"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Project         string
	Prefix          string
	Runtime         string
	StubFixturesDir string
	RepoFiles       map[string]string
	Connector       *FakeConnector
}

type Harness struct {
	Project         string
	Prefix          string
	Runtime         string
	StubFixturesDir string
	RootDir         string
	HomeDir         string
	Schema          string
	BaseDSN         string
	DSN             string
	Config          *config.Config
	Connector       *FakeConnector
	Repo            *GitRepo
	Tmux            *TmuxServer
	Store           *store.PostgresStore

	pool *pgxpool.Pool
}

func Setup(t testing.TB, opts Options) *Harness {
	t.Helper()

	ctx := context.Background()
	baseDSN := pgtest.DSN(t)
	project := strings.TrimSpace(opts.Project)
	if project == "" {
		project = "cobuild-e2e"
	}
	prefix := strings.TrimSpace(opts.Prefix)
	if prefix == "" {
		prefix = "cb-"
	}
	runtimeName := strings.TrimSpace(opts.Runtime)
	if runtimeName == "" {
		runtimeName = "stub"
	}
	fixturesDir := strings.TrimSpace(opts.StubFixturesDir)
	if fixturesDir == "" {
		fixturesDir = defaultStubFixturesDir(t)
	}
	conn := opts.Connector
	if conn == nil {
		conn = NewFakeConnector(FakeConnectorOptions{IDPrefix: prefix + "fake"})
	}

	rootDir := t.TempDir()
	homeDir := filepath.Join(rootDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("create harness home: %v", err)
	}

	repo, err := newGitRepo(ctx, GitRepoOptions{
		RootDir:       filepath.Join(rootDir, "repos"),
		Name:          project,
		DefaultBranch: "main",
		Files:         opts.RepoFiles,
	})
	if err != nil {
		t.Fatalf("create disposable git repo: %v", err)
	}

	tmuxServer, err := newTmuxServer(ctx, filepath.Join(rootDir, "tmux"), "cobuild-"+project)
	if err != nil {
		t.Fatalf("create tmux server: %v", err)
	}

	schema := uniqueSchemaName(t)
	dsn, err := dsnWithSchema(baseDSN, schema)
	if err != nil {
		t.Fatalf("build schema dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pg pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}

	st, err := store.NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("create postgres store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Dispatch.TmuxSession = tmuxServer.SessionName
	cfg.Dispatch.TmuxSocket = tmuxServer.SocketPath
	cfg.Dispatch.DefaultRuntime = runtimeName
	cfg.Storage.Backend = "postgres"
	cfg.Storage.DSN = dsn
	cfg.GitHub.OwnerRepo = "acme/" + project

	h := &Harness{
		Project:         project,
		Prefix:          prefix,
		Runtime:         runtimeName,
		StubFixturesDir: fixturesDir,
		RootDir:         rootDir,
		HomeDir:         homeDir,
		Schema:          schema,
		BaseDSN:         baseDSN,
		DSN:             dsn,
		Config:          cfg,
		Connector:       conn,
		Repo:            repo,
		Tmux:            tmuxServer,
		Store:           st,
		pool:            pool,
	}
	if err := h.writeHarnessConfig(); err != nil {
		t.Fatalf("write harness config: %v", err)
	}

	t.Cleanup(func() {
		if err := h.Teardown(); err != nil {
			t.Fatalf("teardown harness: %v", err)
		}
	})
	return h
}

func (h *Harness) Teardown() error {
	ctx := context.Background()
	var errs []string
	if h.Store != nil {
		if err := h.Store.Close(); err != nil {
			errs = append(errs, err.Error())
		}
		h.Store = nil
	}
	if h.pool != nil {
		h.pool.Close()
		if err := dropSchema(ctx, h.BaseDSN, h.Schema); err != nil {
			errs = append(errs, err.Error())
		}
		h.pool = nil
	}
	if h.Tmux != nil {
		if err := h.Tmux.Teardown(ctx); err != nil {
			errs = append(errs, err.Error())
		}
		h.Tmux = nil
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (h *Harness) Env() []string {
	env := append([]string(nil), os.Environ()...)
	env = setEnv(env, "HOME", h.HomeDir)
	env = setEnv(env, "COBUILD_STUB_FIXTURES_DIR", h.StubFixturesDir)
	env = setEnv(env, "COBUILD_TEST_POSTGRES_DSN", h.BaseDSN)
	return env
}

func (h *Harness) CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = h.Repo.Root
	cmd.Env = h.Env()
	return cmd
}

func (h *Harness) writeHarnessConfig() error {
	if err := os.MkdirAll(filepath.Join(h.HomeDir, ".cobuild"), 0o755); err != nil {
		return fmt.Errorf("mkdir ~/.cobuild: %w", err)
	}

	cfgData, err := yaml.Marshal(h.Config)
	if err != nil {
		return fmt.Errorf("marshal pipeline config: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(h.Repo.Root, ".cobuild"), 0o755); err != nil {
		return fmt.Errorf("mkdir repo .cobuild: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.Repo.Root, ".cobuild", "pipeline.yaml"), cfgData, 0o644); err != nil {
		return fmt.Errorf("write repo pipeline config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.HomeDir, ".cobuild", "pipeline.yaml"), []byte("{}\n"), 0o644); err != nil {
		return fmt.Errorf("write global pipeline config: %w", err)
	}

	projectCfg := map[string]string{
		"project": h.Project,
		"prefix":  h.Prefix,
	}
	projectData, err := yaml.Marshal(projectCfg)
	if err != nil {
		return fmt.Errorf("marshal project config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.Repo.Root, ".cobuild.yaml"), projectData, 0o644); err != nil {
		return fmt.Errorf("write .cobuild.yaml: %w", err)
	}

	reg := &config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			h.Project: {Path: h.Repo.Root, DefaultBranch: h.Repo.DefaultBranch},
		},
	}
	if err := config.SaveRepoRegistry(reg); err != nil {
		return fmt.Errorf("save repo registry: %w", err)
	}

	clientCfg, err := clientConfigFromDSN(h.BaseDSN, h.Project, h.Prefix)
	if err != nil {
		return err
	}
	clientData, err := yaml.Marshal(clientCfg)
	if err != nil {
		return fmt.Errorf("marshal client config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.HomeDir, ".cobuild", "config.yaml"), clientData, 0o644); err != nil {
		return fmt.Errorf("write client config: %w", err)
	}
	return nil
}

func clientConfigFromDSN(dsn, project, prefix string) (*client.ClientConfig, error) {
	pgCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse base dsn: %w", err)
	}
	cfg := &client.ClientConfig{
		Connection: client.ConnectionConfig{
			Host:     pgCfg.ConnConfig.Host,
			Database: pgCfg.ConnConfig.Database,
			User:     pgCfg.ConnConfig.User,
			SSLMode:  pgCfg.ConnConfig.RuntimeParams["sslmode"],
		},
		Project: project,
		Prefix:  prefix,
	}
	if cfg.Connection.SSLMode == "" {
		cfg.Connection.SSLMode = "disable"
	}
	return cfg, nil
}

func defaultStubFixturesDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve harness source path")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", "runtime", "stub")
}

var harnessCounter uint64

func uniqueSchemaName(t testing.TB) string {
	t.Helper()
	return fmt.Sprintf("%s_%04d", pgtest.SchemaName(t), atomic.AddUint64(&harnessCounter, 1))
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func dropSchema(ctx context.Context, dsn, schema string) error {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(schema) == "" {
		return nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("cleanup connect: %w", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		return fmt.Errorf("drop schema %s: %w", schema, err)
	}
	return nil
}

func dsnWithSchema(baseDSN, schema string) (string, error) {
	if u, err := url.Parse(baseDSN); err == nil && u.Scheme != "" {
		query := u.Query()
		query.Set("search_path", schema+",public")
		u.RawQuery = query.Encode()
		return u.String(), nil
	}
	if _, err := pgxpool.ParseConfig(baseDSN); err != nil {
		return "", fmt.Errorf("parse base dsn: %w", err)
	}
	return strings.TrimSpace(baseDSN) + " search_path=" + schema + ",public", nil
}
