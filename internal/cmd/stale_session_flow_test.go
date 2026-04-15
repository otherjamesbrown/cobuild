package cmd

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
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

	restoreLevel := setLogLevelForTest(slog.LevelInfo)
	defer restoreLevel()

	_, stderr := captureStdoutAndStderr(t, func() {
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
	// cb-e7edc9: redispatch note moved to slog.Info with structured attrs.
	if !strings.Contains(stderr, `msg="marked pending for redispatch"`) ||
		!strings.Contains(stderr, `prior_status=stale-killed`) {
		t.Fatalf("poller output missing redispatch note:\n%s", stderr)
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

func TestDispatchAutoRecoversWhenRecordedTmuxWindowIsGone(t *testing.T) {
	taskID := "cb-task-zombie"
	fc, fs := setupDispatchConflictTask(t, taskID, domain.StatusInProgress, map[string]any{
		domain.MetaTmuxWindow: taskID,
	})

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	prevProbe := dispatchTmuxWindowExists
	dispatchTmuxWindowExists = func(context.Context, *config.Config, string, string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { dispatchTmuxWindowExists = prevProbe })
	setCommandFlag(t, dispatchCmd, "dry-run", "true")

	out, err := runCommandWithOutputs(t, dispatchCmd, []string{taskID})
	if err != nil {
		t.Fatalf("dispatch returned error, want auto-recovery: %v", err)
	}
	if strings.Count(out, "Auto-recovered cb-task-zombie: tmux window cobuild-test-project:cb-task-zombie no longer present; resetting and re-dispatching.") != 1 {
		t.Fatalf("auto-recovery log count != 1:\n%s", out)
	}
	if got := fc.items[taskID].Status; got != "open" {
		t.Fatalf("work item status = %q, want open", got)
	}
	if got := fs.tasks[0].Status; got != domain.StatusPending {
		t.Fatalf("pipeline task status = %q, want pending", got)
	}
	if got := fs.sessions[0].Status; got != domain.StatusCancelled {
		t.Fatalf("session status = %q, want cancelled", got)
	}
}

func TestDispatchRejectsWhenRecordedTmuxWindowStillExists(t *testing.T) {
	taskID := "cb-task-live"
	fc, fs := setupDispatchConflictTask(t, taskID, domain.StatusInProgress, map[string]any{
		domain.MetaTmuxWindow: taskID,
	})

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	prevProbe := dispatchTmuxWindowExists
	dispatchTmuxWindowExists = func(context.Context, *config.Config, string, string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { dispatchTmuxWindowExists = prevProbe })
	setCommandFlag(t, dispatchCmd, "dry-run", "true")

	_, err := runCommandWithOutputs(t, dispatchCmd, []string{taskID})
	if err == nil {
		t.Fatal("dispatch returned nil error, want live-session rejection")
	}
	if !strings.Contains(err.Error(), "task not dispatchable: conflict in pipeline session: session ps-"+taskID+" is still running") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fc.items[taskID].Status; got != domain.StatusInProgress {
		t.Fatalf("work item status = %q, want %q", got, domain.StatusInProgress)
	}
	if got := fs.tasks[0].Status; got != domain.StatusInProgress {
		t.Fatalf("pipeline task status = %q, want %q", got, domain.StatusInProgress)
	}
}

func TestDispatchRejectsWhenTmuxWindowMetadataIsMissing(t *testing.T) {
	taskID := "cb-task-no-window"
	fc, fs := setupDispatchConflictTask(t, taskID, domain.StatusInProgress, nil)

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	prevProbe := dispatchTmuxWindowExists
	dispatchTmuxWindowExists = func(context.Context, *config.Config, string, string) (bool, error) {
		t.Fatal("tmux probe should not run without tmux_window metadata")
		return false, nil
	}
	t.Cleanup(func() { dispatchTmuxWindowExists = prevProbe })
	setCommandFlag(t, dispatchCmd, "dry-run", "true")

	_, err := runCommandWithOutputs(t, dispatchCmd, []string{taskID})
	if err == nil {
		t.Fatal("dispatch returned nil error, want missing-metadata rejection")
	}
	if !strings.Contains(err.Error(), "task not dispatchable: conflict in pipeline session: session ps-"+taskID+" is still running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A review-phase redispatch that dies should recover back to needs-review
// so process-review can re-run against the PR. Signalled by the "review-"
// prefix on the tmux window name (set by dispatchTmuxWindowName for review
// phases).
func TestDispatchAutoRecoverReviewDispatchRecoversToNeedsReview(t *testing.T) {
	taskID := "cb-task-review"
	fc, fs := setupDispatchConflictTask(t, taskID, domain.StatusNeedsReview, map[string]any{
		domain.MetaTmuxWindow: "review-" + taskID,
		domain.MetaPRURL:      "https://github.com/acme/cobuild/pull/42",
	})
	fs.runs[taskID].CurrentPhase = domain.PhaseReview

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	prevProbe := dispatchTmuxWindowExists
	dispatchTmuxWindowExists = func(context.Context, *config.Config, string, string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { dispatchTmuxWindowExists = prevProbe })
	setCommandFlag(t, dispatchCmd, "dry-run", "true")

	out, err := runCommandWithOutputs(t, dispatchCmd, []string{taskID})
	if err != nil {
		t.Fatalf("dispatch returned error, want review redispatch: %v", err)
	}
	if got := fc.items[taskID].Status; got != domain.StatusNeedsReview {
		t.Fatalf("work item status = %q, want %q", got, domain.StatusNeedsReview)
	}
	if got := fs.tasks[0].Status; got != domain.StatusNeedsReview {
		t.Fatalf("pipeline task status = %q, want %q", got, domain.StatusNeedsReview)
	}
	if !strings.Contains(out, "Dispatching cb-task-review for review (status was needs-review).") {
		t.Fatalf("output missing review redispatch notice:\n%s", out)
	}
}

// A fix-cycle redispatch (task was in_progress with a PR already open after
// review requested changes, per review.go:769-777) that dies should recover
// to in_progress so the fix loop continues, NOT back to needs-review.
func TestDispatchAutoRecoverFixDispatchRecoversToInProgress(t *testing.T) {
	taskID := "cb-task-fix"
	fc, fs := setupDispatchConflictTask(t, taskID, domain.StatusInProgress, map[string]any{
		domain.MetaTmuxWindow: taskID, // implement-style window name, no "review-" prefix
		domain.MetaPRURL:      "https://github.com/acme/cobuild/pull/42",
	})
	fs.runs[taskID].CurrentPhase = domain.PhaseReview

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", testResolverExec())
	defer restore()

	prevProbe := dispatchTmuxWindowExists
	dispatchTmuxWindowExists = func(context.Context, *config.Config, string, string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { dispatchTmuxWindowExists = prevProbe })
	setCommandFlag(t, dispatchCmd, "dry-run", "true")

	if _, err := runCommandWithOutputs(t, dispatchCmd, []string{taskID}); err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	if got := fc.items[taskID].Status; got != domain.StatusInProgress {
		t.Fatalf("work item status = %q, want %q (fix cycle should not bounce back to needs-review)", got, domain.StatusInProgress)
	}
	if got := fs.tasks[0].Status; got != domain.StatusInProgress {
		t.Fatalf("pipeline task status = %q, want %q", got, domain.StatusInProgress)
	}
}

func setupDispatchConflictTask(t *testing.T, taskID, status string, metadata map[string]any) (*fakeConnector, *fakeStore) {
	t.Helper()

	now := time.Now().UTC()
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:       taskID,
		Title:    "Dispatch conflict task",
		Type:     "task",
		Status:   status,
		Metadata: metadata,
	})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-" + taskID,
		DesignID:     taskID,
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{
		{
			ID:          "ps-" + taskID,
			PipelineID:  "run-" + taskID,
			DesignID:    taskID,
			TaskID:      taskID,
			Phase:       domain.PhaseImplement,
			Project:     "test-project",
			Status:      "running",
			StartedAt:   now.Add(-10 * time.Minute),
			TmuxSession: stringPtr("cobuild-test-project"),
			TmuxWindow:  stringPtr(taskID),
		},
	}
	fs.tasks = []store.PipelineTaskRecord{{
		PipelineID:  "run-" + taskID,
		TaskShardID: taskID,
		DesignID:    taskID,
		Status:      status,
	}}
	return fc, fs
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func stringPtr(value string) *string {
	return &value
}
