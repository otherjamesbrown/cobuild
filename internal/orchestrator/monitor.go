package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// Blocker describes a concrete reason progress cannot continue automatically.
type Blocker struct {
	Reason          StopReason
	Message         string
	BlockingTaskIDs []string
	Recoverable     bool
}

// ProgressSnapshot captures the current wait state for a shard.
type ProgressSnapshot struct {
	BlockingTaskIDs []string
	Blocker         *Blocker
}

// ProgressMonitor inspects the pipeline while the runner is waiting.
type ProgressMonitor interface {
	Snapshot(ctx context.Context, shardID, phase string) (*ProgressSnapshot, error)
}

// MonitorStore is the narrow store surface used by the default monitor.
type MonitorStore interface {
	GetRun(ctx context.Context, designID string) (*store.PipelineRun, error)
	ListTasks(ctx context.Context, pipelineID string) ([]store.PipelineTaskRecord, error)
	ListSessions(ctx context.Context, designID string) ([]store.SessionRecord, error)
}

// StoreProgressMonitor reports blocking task IDs and detects stale running sessions.
type StoreProgressMonitor struct {
	Store        MonitorStore
	Now          func() time.Time
	StallTimeout time.Duration
}

func (m StoreProgressMonitor) Snapshot(ctx context.Context, shardID, phase string) (*ProgressSnapshot, error) {
	if m.Store == nil {
		return nil, fmt.Errorf("progress monitor store is nil")
	}

	run, err := m.Store.GetRun(ctx, shardID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, store.ErrNotFound
	}

	tasks, err := m.Store.ListTasks(ctx, run.ID)
	if err != nil {
		return nil, err
	}

	blockingTaskIDs := blockingTaskIDsForPhase(tasks, shardID, phase)
	snapshot := &ProgressSnapshot{
		BlockingTaskIDs: blockingTaskIDs,
	}

	if len(blockingTaskIDs) == 0 {
		return snapshot, nil
	}

	if m.StallTimeout <= 0 {
		return snapshot, nil
	}

	sessions, err := m.Store.ListSessions(ctx, shardID)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}

	latestByTask := map[string]store.SessionRecord{}
	for _, session := range sessions {
		prev, ok := latestByTask[session.TaskID]
		if !ok || session.StartedAt.After(prev.StartedAt) {
			latestByTask[session.TaskID] = session
		}
	}

	for _, taskID := range blockingTaskIDs {
		session, ok := latestByTask[taskID]
		if !ok || session.Status != "running" {
			continue
		}
		age := now().Sub(session.StartedAt)
		if age < m.StallTimeout {
			continue
		}

		snapshot.Blocker = &Blocker{
			Reason:          StopReasonStalledAgent,
			Message:         fmt.Sprintf("dispatched agent for %s appears stalled after %s", taskID, age.Round(time.Second)),
			BlockingTaskIDs: []string{taskID},
			Recoverable:     true,
		}
		return snapshot, nil
	}

	return snapshot, nil
}

func blockingTaskIDsForPhase(tasks []store.PipelineTaskRecord, shardID, phase string) []string {
	switch phase {
	case "implement", "review":
		var ids []string
		for _, task := range tasks {
			if isTerminalTaskStatus(task.Status) {
				continue
			}
			ids = append(ids, task.TaskShardID)
		}
		if len(ids) == 0 {
			return nil
		}
		sort.Strings(ids)
		return ids
	default:
		if shardID == "" {
			return nil
		}
		return []string{shardID}
	}
}

func isTerminalTaskStatus(status string) bool {
	switch status {
	case "closed", "completed":
		return true
	default:
		return false
	}
}
