package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

type TmuxServer struct {
	SocketPath  string
	SessionName string
}

func newTmuxServer(ctx context.Context, baseDir, sessionName string) (*TmuxServer, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not available in PATH; install tmux before running e2e tests: %w", err)
	}
	if sessionName == "" {
		sessionName = "cobuild-e2e"
	}
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("cbtmux-%d-%d.sock", os.Getpid(), atomic.AddUint64(&tmuxCounter, 1)))
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create tmux dir: %w", err)
	}
	s := &TmuxServer{
		SocketPath:  socketPath,
		SessionName: sessionName,
	}
	if err := s.Run(ctx, "new-session", "-d", "-s", sessionName, "-n", "bootstrap"); err != nil {
		return nil, err
	}
	return s, nil
}

var tmuxCounter uint64

func (s *TmuxServer) Args(args ...string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, "-S", s.SocketPath)
	out = append(out, args...)
	return out
}

func (s *TmuxServer) Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "tmux", s.Args(args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %v: %w\n%s", args, err, string(out))
	}
	return nil
}

func (s *TmuxServer) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", s.Args(args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %v: %w\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *TmuxServer) ListWindows(ctx context.Context) ([]string, error) {
	out, err := s.Output(ctx, "list-windows", "-t", s.SessionName, "-F", "#{window_name}")
	if err != nil {
		if strings.Contains(err.Error(), "can't find session") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func (s *TmuxServer) Teardown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "tmux", s.Args("kill-server")...)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "no server running") {
		return fmt.Errorf("tmux kill-server: %w\n%s", err, string(out))
	}
	return nil
}
