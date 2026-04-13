package livestate

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// SessionInfo is a raw running-session row from the store for dashboard use.
type SessionInfo struct {
	ID           string    `json:"id"`
	PipelineID   string    `json:"pipeline_id,omitempty"`
	DesignID     string    `json:"design_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	Phase        string    `json:"phase,omitempty"`
	Project      string    `json:"project,omitempty"`
	Runtime      string    `json:"runtime,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	AgeSeconds   int64     `json:"age_seconds,omitempty"`
	Model        *string   `json:"model,omitempty"`
	Status       string    `json:"status,omitempty"`
	WorktreePath *string   `json:"worktree_path,omitempty"`
	TmuxSession  *string   `json:"tmux_session,omitempty"`
	TmuxWindow   *string   `json:"tmux_window,omitempty"`
}

// CollectSessions returns running session rows from the backing store.
func CollectSessions(ctx context.Context, sessionStore SessionStore, now time.Time) ([]SessionInfo, error) {
	if sessionStore == nil {
		return nil, nil
	}

	records, err := sessionStore.ListRunningSessions(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list running sessions: %w", err)
	}

	rows := make([]SessionInfo, 0, len(records))
	for _, record := range records {
		rows = append(rows, mapSessionRecord(record, now))
	}
	return rows, nil
}

func mapSessionRecord(record store.SessionRecord, now time.Time) SessionInfo {
	ageSeconds := maxInt64(0, int64(now.Sub(record.StartedAt).Seconds()))

	return SessionInfo{
		ID:           record.ID,
		PipelineID:   record.PipelineID,
		DesignID:     record.DesignID,
		TaskID:       record.TaskID,
		Phase:        record.Phase,
		Project:      record.Project,
		Runtime:      record.Runtime,
		StartedAt:    record.StartedAt,
		AgeSeconds:   ageSeconds,
		Model:        record.Model,
		Status:       record.Status,
		WorktreePath: record.WorktreePath,
		TmuxSession:  record.TmuxSession,
		TmuxWindow:   record.TmuxWindow,
	}
}
