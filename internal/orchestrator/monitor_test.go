package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestStoreProgressMonitorDetectsStalledRunningSession(t *testing.T) {
	now := time.Date(2026, 4, 12, 13, 0, 0, 0, time.UTC)
	wave := 1
	monitor := StoreProgressMonitor{
		Store: fakeMonitorStore{
			run: &store.PipelineRun{ID: "pr-1", DesignID: "cb-design"},
			tasks: []store.PipelineTaskRecord{
				{TaskShardID: "cb-task-1", Status: "in_progress", Wave: &wave},
			},
			sessions: []store.SessionRecord{
				{
					TaskID:    "cb-task-1",
					Status:    "running",
					StartedAt: now.Add(-45 * time.Minute),
				},
			},
		},
		Now:          func() time.Time { return now },
		StallTimeout: 30 * time.Minute,
	}

	snapshot, err := monitor.Snapshot(context.Background(), "cb-design", "implement")
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}
	if snapshot == nil || snapshot.Blocker == nil {
		t.Fatalf("snapshot = %+v, want stalled blocker", snapshot)
	}
	if snapshot.Blocker.Reason != StopReasonStalledAgent {
		t.Fatalf("blocker reason = %s, want %s", snapshot.Blocker.Reason, StopReasonStalledAgent)
	}
	if got := strings.Join(snapshot.Blocker.BlockingTaskIDs, ","); got != "cb-task-1" {
		t.Fatalf("blocking task ids = %q, want cb-task-1", got)
	}
}

type fakeMonitorStore struct {
	run      *store.PipelineRun
	tasks    []store.PipelineTaskRecord
	sessions []store.SessionRecord
}

func (f fakeMonitorStore) GetRun(context.Context, string) (*store.PipelineRun, error) {
	return f.run, nil
}

func (f fakeMonitorStore) ListTasks(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return append([]store.PipelineTaskRecord(nil), f.tasks...), nil
}

func (f fakeMonitorStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	return append([]store.SessionRecord(nil), f.sessions...), nil
}
