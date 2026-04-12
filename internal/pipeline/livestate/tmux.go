package livestate

import (
	"context"
	"fmt"
	"strings"
)

// CollectTmux returns windows from all cobuild-* tmux sessions.
func CollectTmux(ctx context.Context, execFn CommandRunner) ([]TmuxWindow, error) {
	if execFn == nil {
		execFn = defaultCommandRunner
	}

	sessionsOut, err := execFn(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}

	var rows []TmuxWindow
	for _, sessionName := range parseTmuxSessions(string(sessionsOut)) {
		windowOut, err := execFn(ctx, "tmux", "list-windows", "-t", sessionName, "-F", "#{window_id}\t#{window_name}")
		if err != nil {
			return nil, fmt.Errorf("tmux list-windows %s: %w", sessionName, err)
		}
		rows = append(rows, parseTmuxWindows(sessionName, string(windowOut))...)
	}

	return rows, nil
}

func parseTmuxSessions(raw string) []string {
	lines := strings.Split(raw, "\n")
	sessions := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(name, "cobuild-") {
			continue
		}
		sessions = append(sessions, name)
	}
	return sessions
}

func parseTmuxWindows(sessionName, raw string) []TmuxWindow {
	lines := strings.Split(raw, "\n")
	rows := make([]TmuxWindow, 0, len(lines))
	project := strings.TrimPrefix(sessionName, "cobuild-")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 2)
		windowID := ""
		windowName := line
		if len(parts) == 2 {
			windowID = strings.TrimSpace(parts[0])
			windowName = strings.TrimSpace(parts[1])
		}

		rows = append(rows, TmuxWindow{
			SessionName: sessionName,
			Project:     project,
			WindowID:    windowID,
			WindowName:  windowName,
			TargetID:    extractTargetID(windowName),
		})
	}

	return rows
}
