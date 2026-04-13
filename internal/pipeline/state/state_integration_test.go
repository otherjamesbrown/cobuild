package state

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/testutil/pgtest"
	"gopkg.in/yaml.v3"
)

func TestResolveIntegrationWithPostgresAndTmux(t *testing.T) {
	ctx := context.Background()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	pg := pgtest.New(t, ctx)
	testStore := pg.Store
	now := time.Now().UTC()
	designID := fmt.Sprintf("cb-%x", now.UnixNano())
	project := "cobuild-state-test"
	socketName := fmt.Sprintf("cb-state-%d", now.UnixNano())
	tmuxSession := "cobuild-" + project
	worktreePath := filepath.Join(t.TempDir(), designID)

	run, err := testStore.CreateRunWithMode(ctx, designID, project, "implement", "manual")
	if err != nil {
		t.Fatalf("CreateRunWithMode() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = testStore.CancelRunningSessions(ctx, designID)
		_ = killTmuxServer(ctx, socketName)
		pg.CleanupDesign(t, ctx, designID)
	})

	if _, err := testStore.CreateSession(ctx, store.SessionInput{
		PipelineID:   run.ID,
		DesignID:     designID,
		TaskID:       designID,
		Phase:        "implement",
		Project:      project,
		Runtime:      "codex",
		WorktreePath: worktreePath,
		TmuxSession:  tmuxSession,
		TmuxWindow:   designID,
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := startTmuxWindow(ctx, socketName, tmuxSession, designID); err != nil {
		t.Fatalf("startTmuxWindow() error = %v", err)
	}

	resolver := NewResolver(Dependencies{
		Connector: &fakeConnector{item: &connector.WorkItem{
			ID: designID, Type: "design", Status: "open", Project: project,
		}},
		Store: testStore,
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name != "tmux" {
				return nil, fmt.Errorf("unexpected command %s", name)
			}
			argv := append([]string{"-L", socketName}, args...)
			return exec.CommandContext(ctx, name, argv...).CombinedOutput()
		},
	})

	got, err := resolver.Resolve(ctx, designID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Health != HealthOK {
		t.Fatalf("Health = %s, want %s", got.Health, HealthOK)
	}
	if got.Run == nil || got.Run.ID != run.ID {
		t.Fatalf("Run = %#v, want run ID %s", got.Run, run.ID)
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(got.Sessions))
	}
	if got.Sessions[0].TmuxSession != tmuxSession || got.Sessions[0].TmuxWindow != designID {
		t.Fatalf("session tmux fields = %#v", got.Sessions[0])
	}
	if len(got.Tmux) != 1 {
		t.Fatalf("len(Tmux) = %d, want 1", len(got.Tmux))
	}
	if got.Tmux[0].SessionName != tmuxSession || got.Tmux[0].WindowName != designID {
		t.Fatalf("tmux window = %#v", got.Tmux[0])
	}
}

func startTmuxWindow(ctx context.Context, socketName, sessionName, windowName string) error {
	if out, err := exec.CommandContext(ctx, "tmux", "-L", socketName, "new-session", "-d", "-s", sessionName).CombinedOutput(); err != nil {
		return fmt.Errorf("new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "tmux", "-L", socketName, "new-window", "-t", sessionName, "-n", windowName).CombinedOutput(); err != nil {
		return fmt.Errorf("new-window: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func killTmuxServer(ctx context.Context, socketName string) error {
	out, err := exec.CommandContext(ctx, "tmux", "-L", socketName, "kill-server").CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.ToLower(string(out))
	if strings.Contains(msg, "no server running") {
		return nil
	}
	return fmt.Errorf("kill-server: %w: %s", err, strings.TrimSpace(string(out)))
}

type integrationTestConfig struct {
	Connection struct {
		Host     string `yaml:"host"`
		Database string `yaml:"database"`
		User     string `yaml:"user"`
		SSLMode  string `yaml:"sslmode"`
	} `yaml:"connection"`
}

func integrationPostgresDSN(t *testing.T) string {
	t.Helper()

	if dsn := strings.TrimSpace(os.Getenv("COBUILD_TEST_POSTGRES_DSN")); dsn != "" {
		return dsn
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("set COBUILD_TEST_POSTGRES_DSN for integration tests")
	}
	cfgPath := filepath.Join(home, ".cobuild", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Skip("set COBUILD_TEST_POSTGRES_DSN for integration tests")
	}

	var cfg integrationTestConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Skipf("parse %s: %v", cfgPath, err)
	}
	if cfg.Connection.Host == "" || cfg.Connection.Database == "" || cfg.Connection.User == "" {
		t.Skipf("set COBUILD_TEST_POSTGRES_DSN for integration tests; incomplete connection config in %s", cfgPath)
	}

	sslMode := cfg.Connection.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	return fmt.Sprintf("postgres://%s@%s/%s?sslmode=%s",
		cfg.Connection.User,
		cfg.Connection.Host,
		cfg.Connection.Database,
		sslMode,
	)
}
