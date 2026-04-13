package livestate

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// SessionInfo is a dashboard-oriented view of a running pipeline session.
type SessionInfo struct {
	ID           string `json:"id"`
	DesignID     string `json:"design_id"`
	TaskID       string `json:"task_id"`
	Phase        string `json:"phase"`
	Project      string `json:"project"`
	Runtime      string `json:"runtime"`
	StartedAt    time.Time `json:"started_at"`
	AgeSeconds   int64  `json:"age_seconds"`
	WorktreePath string `json:"worktree_path,omitempty"`
	TmuxSession  string `json:"tmux_session,omitempty"`
	TmuxWindow   string `json:"tmux_window,omitempty"`
}

// RunningSessionLister is the minimal store surface needed for session rows.
type RunningSessionLister interface {
	ListRunningSessions(ctx context.Context, project string) ([]store.SessionRecord, error)
}

// CollectSessions reads running pipeline_sessions across all projects.
func CollectSessions(ctx context.Context, lister RunningSessionLister, now time.Time) ([]SessionInfo, error) {
	rows, err := lister.ListRunningSessions(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list running sessions: %w", err)
	}

	sessions := make([]SessionInfo, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, sessionInfoFromRecord(row, now))
	}
	return sessions, nil
}

func sessionInfoFromRecord(r store.SessionRecord, now time.Time) SessionInfo {
	info := SessionInfo{
		ID:         r.ID,
		DesignID:   r.DesignID,
		TaskID:     r.TaskID,
		Phase:      r.Phase,
		Project:    r.Project,
		Runtime:    r.Runtime,
		StartedAt:  r.StartedAt,
		AgeSeconds: int64(now.Sub(r.StartedAt).Seconds()),
	}
	if r.WorktreePath != nil {
		info.WorktreePath = *r.WorktreePath
	}
	if r.TmuxSession != nil {
		info.TmuxSession = *r.TmuxSession
	}
	if r.TmuxWindow != nil {
		info.TmuxWindow = *r.TmuxWindow
	}
	return info
}
