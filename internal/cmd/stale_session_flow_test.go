package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
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

func TestResetUsesResolvedStateToReopenAndCleanStaleSessions(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-reset", Title: "Reset design", Type: "design", Status: "in_progress"})

	fs := newFakeStore()
	fs.runs["cb-reset"] = &store.PipelineRun{
		ID:           "run-reset",
		DesignID:     "cb-reset",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.sessionsByDesign["cb-reset"] = []store.SessionRecord{
		{ID: "ps-1", DesignID: "cb-reset", Status: "running", StartedAt: time.Now().Add(-10 * time.Minute)},
	}

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	prevTmuxOutput := tmuxOutput
	prevTmuxRun := tmuxRun
	tmuxOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list-sessions":
			return []byte("cobuild-test\n"), nil
		case len(args) >= 3 && args[0] == "list-windows":
			return []byte("@1\tcb-reset\n"), nil
		default:
			return nil, fmt.Errorf("unexpected tmux output args %v", args)
		}
	}
	var killed []string
	tmuxRun = func(_ context.Context, args ...string) error {
		killed = append(killed, strings.Join(args, " "))
		return nil
	}
	defer func() {
		tmuxOutput = prevTmuxOutput
		tmuxRun = prevTmuxRun
	}()
	pipelinestate.ConfigureDefault(pipelinestate.Dependencies{
		Connector: fc,
		Store:     fs,
		Exec: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "tmux" {
				return nil, fmt.Errorf("unexpected command %s", name)
			}
			switch {
			case len(args) >= 2 && args[0] == "list-sessions":
				return []byte("cobuild-test\n"), nil
			case len(args) >= 3 && args[0] == "list-windows":
				return []byte("@1\tcb-reset\n"), nil
			default:
				return nil, fmt.Errorf("unexpected tmux args %v", args)
			}
		},
	})

	out, err := runCommandWithOutputs(t, resetCmd, []string{"cb-reset", "--phase", "design"})
	if err != nil {
		t.Fatalf("reset failed: %v\n%s", err, out)
	}

	if got := fc.items["cb-reset"].Status; got != "open" {
		t.Fatalf("work item status = %q, want open", got)
	}
	if len(fs.resetCalls) != 1 || fs.resetCalls[0].Phase != "design" {
		t.Fatalf("reset calls = %+v, want one reset to design", fs.resetCalls)
	}
	if len(killed) != 1 || killed[0] != "kill-window -t @1" {
		t.Fatalf("tmux kills = %+v, want kill-window -t @1", killed)
	}
	for _, want := range []string{
		"Health: OK",
		"Run: forced implement/active",
		"Sessions: cancelled 1 running session record(s): ps-1",
		"Tmux: killed cobuild-test:cb-reset",
		"Work item: in_progress → open",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("reset output missing %q:\n%s", want, out)
		}
	}
}

func TestResetFallsBackToTmuxScanWhenResolverTmuxReadFails(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-reset-fallback", Title: "Reset design", Type: "design", Status: "closed"})

	fs := newFakeStore()
	fs.runs["cb-reset-fallback"] = &store.PipelineRun{
		ID:           "run-reset-fallback",
		DesignID:     "cb-reset-fallback",
		CurrentPhase: "review",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	prevTmuxOutput := tmuxOutput
	prevTmuxRun := tmuxRun
	tmuxOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list-sessions":
			return []byte("cobuild-test\n"), nil
		case len(args) >= 3 && args[0] == "list-windows":
			return []byte("@2 cb-reset-fallback-extra\n"), nil
		default:
			return nil, fmt.Errorf("unexpected tmux output args %v", args)
		}
	}
	var killed []string
	tmuxRun = func(_ context.Context, args ...string) error {
		killed = append(killed, strings.Join(args, " "))
		return nil
	}
	defer func() {
		tmuxOutput = prevTmuxOutput
		tmuxRun = prevTmuxRun
	}()

	pipelinestate.ConfigureDefault(pipelinestate.Dependencies{
		Connector: fc,
		Store:     fs,
		Exec: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "tmux" && len(args) > 0 && args[0] == "list-sessions" {
				return nil, fmt.Errorf("tmux unavailable")
			}
			return []byte(""), nil
		},
	})

	out, err := runCommandWithOutputs(t, resetCmd, []string{"cb-reset-fallback", "--phase", "design"})
	if err != nil {
		t.Fatalf("reset failed: %v\n%s", err, out)
	}

	if len(killed) != 1 || killed[0] != "kill-window -t @2" {
		t.Fatalf("tmux kills = %+v, want kill-window -t @2", killed)
	}
	for _, want := range []string{
		"? tmux:",
		"Tmux: resolver tmux view unavailable; falling back to tmux scan",
		"Tmux: killed cobuild-test:cb-reset-fallback-extra",
		"Work item: closed → open",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("reset output missing %q:\n%s", want, out)
		}
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
