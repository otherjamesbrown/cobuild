package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

type lifecycleStore struct {
	*fakeStore
	gateHistory       map[string][]store.PipelineGateRecord
	sessionsByDesign  map[string][]store.SessionRecord
	sessionByTask     map[string]*store.SessionRecord
	taskStatusUpdates map[string]string
}

func newLifecycleStore() *lifecycleStore {
	return &lifecycleStore{
		fakeStore:         newFakeStore(),
		gateHistory:       map[string][]store.PipelineGateRecord{},
		sessionsByDesign:  map[string][]store.SessionRecord{},
		sessionByTask:     map[string]*store.SessionRecord{},
		taskStatusUpdates: map[string]string{},
	}
}

func (s *lifecycleStore) GetGateHistory(_ context.Context, designID string) ([]store.PipelineGateRecord, error) {
	return append([]store.PipelineGateRecord(nil), s.gateHistory[designID]...), nil
}

func (s *lifecycleStore) ListSessions(_ context.Context, designID string) ([]store.SessionRecord, error) {
	return append([]store.SessionRecord(nil), s.sessionsByDesign[designID]...), nil
}

func (s *lifecycleStore) GetSession(_ context.Context, taskID string) (*store.SessionRecord, error) {
	return s.sessionByTask[taskID], nil
}

func (s *lifecycleStore) UpdateTaskStatus(_ context.Context, taskShardID, status string) error {
	s.taskStatusUpdates[taskShardID] = status
	return nil
}

func TestAuditShowsSessionLifecycleEvents(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-design", Title: "Lifecycle design", Type: "design", Status: "in_progress"})

	fs := newLifecycleStore()
	fs.runs["cb-design"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-design",
		CurrentPhase: "implement",
		Status:       "active",
	}

	t1 := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(10 * time.Minute)
	t3 := t2.Add(5 * time.Minute)
	body := "Gate passed"
	note1 := "Killed by poller: session.log mtime > stall_timeout (12m idle)"
	note2 := "Worktree missing when poller inspected the session"

	fs.gateHistory["cb-design"] = []store.PipelineGateRecord{
		{ID: "pg-1", DesignID: "cb-design", GateName: "design", Round: 1, Verdict: "pass", Body: &body, CreatedAt: t1},
	}
	fs.sessionsByDesign["cb-design"] = []store.SessionRecord{
		{ID: "ps-1", DesignID: "cb-design", TaskID: "cb-task-1", Phase: "implement", Status: "stale-killed", StartedAt: t1, EndedAt: &t2, CompletionNote: &note1},
		{ID: "ps-2", DesignID: "cb-design", TaskID: "cb-task-2", Phase: "implement", Status: "orphaned", StartedAt: t2, EndedAt: &t3, CompletionNote: &note2},
	}

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	out, err := runCommandWithOutputs(t, auditCmd, []string{"cb-design"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}

	if !strings.Contains(out, "session stale-killed") || !strings.Contains(out, "cb-task-1") {
		t.Fatalf("audit output missing stale-killed session event:\n%s", out)
	}
	if !strings.Contains(out, note1) {
		t.Fatalf("audit output missing stale-killed completion note:\n%s", out)
	}
	if !strings.Contains(out, "session orphaned") || !strings.Contains(out, "cb-task-2") {
		t.Fatalf("audit output missing orphaned session event:\n%s", out)
	}
	if !strings.Contains(out, note2) {
		t.Fatalf("audit output missing orphaned completion note:\n%s", out)
	}
}

func TestPollerMarksStaleKilledTaskPendingForRedispatch(t *testing.T) {
	ctx := context.Background()
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-design", Type: "design", Status: "open"})
	fc.addTask("cb-task-stuck", "in_progress", 1)
	fc.setChildTasks("cb-design", "cb-task-stuck")

	fs := newLifecycleStore()
	note := "Killed by poller: session.log mtime > stall_timeout (15m idle)"
	fs.sessionByTask["cb-task-stuck"] = &store.SessionRecord{
		ID:             "ps-stuck",
		TaskID:         "cb-task-stuck",
		Phase:          "implement",
		Status:         "stale-killed",
		CompletionNote: &note,
	}

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	out := captureStdout(t, func() {
		dispatchReadyTasks(ctx, "", &config.Config{
			Dispatch: config.DispatchCfg{WaveStrategy: "parallel", MaxConcurrent: 3},
		}, "cb-design", false)
	})

	if got := fc.items["cb-task-stuck"].Status; got != "open" {
		t.Fatalf("connector task status = %q, want open", got)
	}
	if got := fs.taskStatusUpdates["cb-task-stuck"]; got != "pending" {
		t.Fatalf("pipeline task status = %q, want pending", got)
	}
	if !strings.Contains(out, "marked pending for redispatch after stale-killed") {
		t.Fatalf("poller output missing redispatch note:\n%s", out)
	}
}

func TestNextMentionsRedispatchAfterLifecycleRecovery(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-design", Title: "Redispatch design", Type: "design", Status: "in_progress"})

	fs := newLifecycleStore()
	fs.runs["cb-design"] = &store.PipelineRun{
		ID:           "pr-1",
		DesignID:     "cb-design",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.tasks = []store.PipelineTaskRecord{
		{TaskShardID: "cb-task-stuck", Status: "pending"},
	}
	note := "Killed by poller: session.log mtime > stall_timeout (15m idle)"
	fs.sessionsByDesign["cb-design"] = []store.SessionRecord{
		{
			ID:             "ps-stuck",
			DesignID:       "cb-design",
			TaskID:         "cb-task-stuck",
			Phase:          "implement",
			Status:         "stale-killed",
			CompletionNote: &note,
			StartedAt:      time.Now().Add(-20 * time.Minute),
			EndedAt:        timePtr(time.Now().Add(-5 * time.Minute)),
		},
	}

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	out, err := runCommandWithOutputs(t, nextCmd, []string{"cb-design"})
	if err != nil {
		t.Fatalf("next failed: %v", err)
	}

	if !strings.Contains(out, "pending redispatch after a stale-killed/orphaned session") {
		t.Fatalf("next output missing redispatch guidance:\n%s", out)
	}
}

// Self-heal: a "running" session record with no live tmux window is stale
// state from a prior orchestrator crash. Dispatch should recover it in
// place instead of refusing (cb-d5e1dd #4). The zombie session row must be
// cancelled and the task must be re-dispatched.
func TestDispatchSelfHealsZombieSessionConflict(t *testing.T) {
	taskID := "cb-task-zombie"
	now := time.Now().UTC()

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: taskID, Title: "Zombie task", Type: "task", Status: "in_progress"})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-zombie",
		DesignID:     taskID,
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{
		{
			ID:          "ps-zombie",
			PipelineID:  "run-zombie",
			DesignID:    taskID,
			TaskID:      taskID,
			Phase:       "implement",
			Project:     "test-project",
			Status:      "running",
			StartedAt:   now.Add(-10 * time.Minute),
			TmuxSession: stringPtr("cobuild-test-project"),
			TmuxWindow:  stringPtr(taskID),
		},
	}

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	_ = dispatchCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchCmd.Flags().Set("dry-run", "false")
	})

	if err := dispatchCmd.RunE(dispatchCmd, []string{taskID}); err != nil {
		t.Fatalf("dispatch returned error, want self-heal: %v", err)
	}
	// The zombie session row must be cancelled, not left as 'running'.
	var zombie *store.SessionRecord
	for i := range fs.sessions {
		if fs.sessions[i].ID == "ps-zombie" {
			zombie = &fs.sessions[i]
			break
		}
	}
	if zombie == nil {
		t.Fatal("zombie session record disappeared")
	}
	if zombie.Status == "running" {
		t.Fatalf("zombie session status still running; self-heal did not cancel it")
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func stringPtr(value string) *string {
	return &value
}
