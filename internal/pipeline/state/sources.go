package state

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// CommandRunner executes a command and returns combined output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// WorkItemGetter is the connector surface required by the resolver.
type WorkItemGetter interface {
	Get(ctx context.Context, id string) (*connector.WorkItem, error)
}

// Store is the minimal read-only store surface required by the resolver.
type Store interface {
	GetRun(ctx context.Context, designID string) (*store.PipelineRun, error)
	ListSessions(ctx context.Context, designID string) ([]store.SessionRecord, error)
}

func (r *Resolver) collectWorkItem(ctx context.Context, designID string) (*WorkItemState, error) {
	if r.connector == nil {
		return nil, nil
	}
	item, err := r.connector.Get(ctx, designID)
	if err != nil {
		return nil, fmt.Errorf("get work item %s: %w", designID, err)
	}
	if item == nil {
		return nil, nil
	}
	return &WorkItemState{
		ID:        item.ID,
		Type:      item.Type,
		Status:    item.Status,
		Project:   item.Project,
		Labels:    append([]string(nil), item.Labels...),
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

func (r *Resolver) collectRun(ctx context.Context, designID string) (*RunState, error) {
	if r.store == nil {
		return nil, nil
	}
	run, err := r.store.GetRun(ctx, designID)
	if err != nil {
		return nil, fmt.Errorf("get run %s: %w", designID, err)
	}
	if run == nil {
		return nil, nil
	}
	return &RunState{
		ID:        run.ID,
		Phase:     run.CurrentPhase,
		Status:    run.Status,
		Mode:      run.Mode,
		Project:   run.Project,
		CreatedAt: run.CreatedAt,
		UpdatedAt: run.UpdatedAt,
	}, nil
}

func (r *Resolver) collectSessions(ctx context.Context, designID string, now time.Time) ([]SessionState, error) {
	if r.store == nil {
		return nil, nil
	}
	rows, err := r.store.ListSessions(ctx, designID)
	if err != nil {
		return nil, fmt.Errorf("list sessions %s: %w", designID, err)
	}
	sessions := make([]SessionState, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, sessionFromRecord(row, now))
	}
	return sessions, nil
}

func sessionFromRecord(row store.SessionRecord, now time.Time) SessionState {
	referenceTime := row.StartedAt
	if row.EndedAt != nil {
		referenceTime = *row.EndedAt
	}
	session := SessionState{
		ID:         row.ID,
		PipelineID: row.PipelineID,
		DesignID:   row.DesignID,
		TaskID:     row.TaskID,
		Phase:      row.Phase,
		Project:    row.Project,
		Runtime:    row.Runtime,
		Status:     row.Status,
		StartedAt:  row.StartedAt,
		EndedAt:    row.EndedAt,
		AgeSeconds: maxInt64(0, int64(now.Sub(referenceTime).Seconds())),
	}
	if row.WorktreePath != nil {
		session.WorktreePath = *row.WorktreePath
	}
	if row.TmuxSession != nil {
		session.TmuxSession = *row.TmuxSession
	}
	if row.TmuxWindow != nil {
		session.TmuxWindow = *row.TmuxWindow
	}
	return session
}

func (r *Resolver) collectTmux(ctx context.Context, designID string) ([]TmuxWindow, error) {
	if r.exec == nil {
		return nil, nil
	}
	sessionsOut, err := r.exec(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var rows []TmuxWindow
	for _, sessionName := range parseTmuxSessions(string(sessionsOut)) {
		windowOut, err := r.exec(ctx, "tmux", "list-windows", "-t", sessionName, "-F", "#{window_id}\t#{window_name}")
		if err != nil {
			return nil, fmt.Errorf("tmux list-windows %s: %w", sessionName, err)
		}
		for _, row := range parseTmuxWindows(sessionName, string(windowOut)) {
			if row.TargetID == designID {
				rows = append(rows, row)
			}
		}
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

var targetIDPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*-[a-z0-9]+\b`)

func extractTargetID(value string) string {
	return targetIDPattern.FindString(strings.ToLower(value))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
