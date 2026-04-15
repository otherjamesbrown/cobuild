package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestReconcileExitedSessionsNoPRSkips(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-no-pr"
	exitTime := time.Date(2026, 4, 15, 13, 30, 0, 0, time.UTC)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:       taskID,
		Title:    "No PR task",
		Type:     domain.WorkItemTypeTask,
		Status:   domain.StatusInProgress,
		Metadata: map[string]any{domain.MetaRepo: "acme/cobuild"},
	})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-no-pr",
		DesignID:     taskID,
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:       "ps-no-pr",
		TaskID:   taskID,
		DesignID: taskID,
		Status:   "completed",
		EndedAt:  &exitTime,
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevBranch := reconcileBranchExists
	prevPR := reconcilePRForBranch
	prevWriter := reconcileExitedSessionsWriter
	t.Cleanup(func() {
		reconcileBranchExists = prevBranch
		reconcilePRForBranch = prevPR
		reconcileExitedSessionsWriter = prevWriter
	})
	reconcileBranchExists = func(context.Context, string, string) (bool, error) { return true, nil }
	reconcilePRForBranch = func(context.Context, string, string) (*reconcilePRMatch, error) { return nil, nil }
	reconcileExitedSessionsWriter = &bytes.Buffer{}

	reconcileExitedSessions(ctx)

	if got := fc.items[taskID].Status; got != domain.StatusInProgress {
		t.Fatalf("status = %q, want %q", got, domain.StatusInProgress)
	}
	if len(fc.appended[taskID]) != 0 {
		t.Fatalf("appended notes = %d, want 0", len(fc.appended[taskID]))
	}
}

func TestReconcileExitedSessionsOpenPRAdvances(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-open-pr"
	exitTime := time.Date(2026, 4, 15, 13, 45, 0, 0, time.UTC)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:       taskID,
		Title:    "Open PR task",
		Type:     domain.WorkItemTypeTask,
		Status:   domain.StatusInProgress,
		Metadata: map[string]any{domain.MetaRepo: "acme/cobuild"},
	})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-open-pr",
		DesignID:     taskID,
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:       "ps-open-pr",
		TaskID:   taskID,
		DesignID: taskID,
		Status:   "completed",
		EndedAt:  &exitTime,
	}}
	fs.tasks = []store.PipelineTaskRecord{{
		PipelineID:  "parent-run",
		TaskShardID: taskID,
		DesignID:    "cb-design",
		Status:      domain.StatusInProgress,
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevBranch := reconcileBranchExists
	prevPR := reconcilePRForBranch
	prevWriter := reconcileExitedSessionsWriter
	t.Cleanup(func() {
		reconcileBranchExists = prevBranch
		reconcilePRForBranch = prevPR
		reconcileExitedSessionsWriter = prevWriter
	})
	reconcileBranchExists = func(context.Context, string, string) (bool, error) { return true, nil }
	reconcilePRForBranch = func(context.Context, string, string) (*reconcilePRMatch, error) {
		return &reconcilePRMatch{
			Number: 42,
			URL:    "https://github.com/acme/cobuild/pull/42",
		}, nil
	}
	var stderr bytes.Buffer
	reconcileExitedSessionsWriter = &stderr

	reconcileExitedSessions(ctx)

	if got := fc.items[taskID].Status; got != domain.StatusNeedsReview {
		t.Fatalf("status = %q, want %q", got, domain.StatusNeedsReview)
	}
	if got := fc.metadata[taskID][domain.MetaPRURL]; got != "https://github.com/acme/cobuild/pull/42" {
		t.Fatalf("pr metadata = %q, want recovered URL", got)
	}
	if len(fc.appended[taskID]) != 1 {
		t.Fatalf("appended notes = %d, want 1", len(fc.appended[taskID]))
	}
	note := fc.appended[taskID][0]
	if !strings.Contains(note, "ps-open-pr") {
		t.Fatalf("note missing session id:\n%s", note)
	}
	if !strings.Contains(note, "https://github.com/acme/cobuild/pull/42") {
		t.Fatalf("note missing PR URL:\n%s", note)
	}
	if got := fs.tasks[0].Status; got != domain.StatusNeedsReview {
		t.Fatalf("pipeline task status = %q, want %q", got, domain.StatusNeedsReview)
	}
	if !strings.Contains(stderr.String(), "Reconciled: cb-open-pr advanced to needs-review (PR #42 exists, session exited at 2026-04-15T13:45:00Z)") {
		t.Fatalf("stderr missing reconciliation log:\n%s", stderr.String())
	}
}

func TestReconcileExitedSessionsMergedPRSkips(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-merged-pr"
	exitTime := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:       taskID,
		Title:    "Merged PR task",
		Type:     domain.WorkItemTypeTask,
		Status:   domain.StatusInProgress,
		Metadata: map[string]any{domain.MetaRepo: "acme/cobuild"},
	})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-merged-pr",
		DesignID:     taskID,
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:       "ps-merged-pr",
		TaskID:   taskID,
		DesignID: taskID,
		Status:   "completed",
		EndedAt:  &exitTime,
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevBranch := reconcileBranchExists
	prevPR := reconcilePRForBranch
	prevWriter := reconcileExitedSessionsWriter
	t.Cleanup(func() {
		reconcileBranchExists = prevBranch
		reconcilePRForBranch = prevPR
		reconcileExitedSessionsWriter = prevWriter
	})
	reconcileBranchExists = func(context.Context, string, string) (bool, error) { return true, nil }
	reconcilePRForBranch = func(context.Context, string, string) (*reconcilePRMatch, error) {
		return &reconcilePRMatch{
			Number: 17,
			URL:    "https://github.com/acme/cobuild/pull/17",
			Merged: true,
		}, nil
	}
	reconcileExitedSessionsWriter = &bytes.Buffer{}

	reconcileExitedSessions(ctx)

	if got := fc.items[taskID].Status; got != domain.StatusInProgress {
		t.Fatalf("status = %q, want %q", got, domain.StatusInProgress)
	}
	if len(fc.appended[taskID]) != 0 {
		t.Fatalf("appended notes = %d, want 0", len(fc.appended[taskID]))
	}
}

func TestReconcileExitedSessionsAlreadyNeedsReviewSkips(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-already-needs-review"
	exitTime := time.Date(2026, 4, 15, 14, 5, 0, 0, time.UTC)

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:       taskID,
		Title:    "Already advanced task",
		Type:     domain.WorkItemTypeTask,
		Status:   domain.StatusNeedsReview,
		Metadata: map[string]any{domain.MetaRepo: "acme/cobuild"},
	})

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{
		ID:           "run-already-needs-review",
		DesignID:     taskID,
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}
	fs.sessions = []store.SessionRecord{{
		ID:       "ps-already-needs-review",
		TaskID:   taskID,
		DesignID: taskID,
		Status:   "completed",
		EndedAt:  &exitTime,
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevBranch := reconcileBranchExists
	prevPR := reconcilePRForBranch
	prevWriter := reconcileExitedSessionsWriter
	t.Cleanup(func() {
		reconcileBranchExists = prevBranch
		reconcilePRForBranch = prevPR
		reconcileExitedSessionsWriter = prevWriter
	})
	reconcileBranchExists = func(context.Context, string, string) (bool, error) {
		t.Fatal("branch check should not run when status is already needs-review")
		return false, nil
	}
	reconcilePRForBranch = func(context.Context, string, string) (*reconcilePRMatch, error) {
		t.Fatal("PR lookup should not run when status is already needs-review")
		return nil, nil
	}
	reconcileExitedSessionsWriter = &bytes.Buffer{}

	reconcileExitedSessions(ctx)

	if len(fc.statusUpdates) != 0 {
		t.Fatalf("status updates = %d, want 0", len(fc.statusUpdates))
	}
	if len(fc.appended[taskID]) != 0 {
		t.Fatalf("appended notes = %d, want 0", len(fc.appended[taskID]))
	}
}

func TestReconcileExitedSessionsStatusCommandCallsReconciler(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-status"] = &store.PipelineRun{
		ID:           "run-status",
		DesignID:     "cb-status",
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevRun := reconcileExitedSessionsRun
	t.Cleanup(func() { reconcileExitedSessionsRun = prevRun })

	called := 0
	reconcileExitedSessionsRun = func(context.Context) { called++ }

	_ = captureStdout(t, func() {
		if err := statusCmd.RunE(statusCmd, nil); err != nil {
			t.Fatalf("status command error = %v", err)
		}
	})

	if called != 1 {
		t.Fatalf("reconciler calls = %d, want 1", called)
	}
}

func TestReconcileExitedSessionsAuditCommandCallsReconciler(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-audit",
		Title:  "Audit task",
		Type:   domain.WorkItemTypeTask,
		Status: domain.StatusInProgress,
	})

	fs := newFakeStore()
	fs.runs["cb-audit"] = &store.PipelineRun{
		ID:           "run-audit",
		DesignID:     "cb-audit",
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevRun := reconcileExitedSessionsRun
	t.Cleanup(func() { reconcileExitedSessionsRun = prevRun })

	called := 0
	reconcileExitedSessionsRun = func(context.Context) { called++ }

	_ = captureStdout(t, func() {
		if err := auditCmd.RunE(auditCmd, []string{"cb-audit"}); err != nil {
			t.Fatalf("audit command error = %v", err)
		}
	})

	if called != 1 {
		t.Fatalf("reconciler calls = %d, want 1", called)
	}
}

func TestReconcileStaleStateCallsExitedSessionReconciler(t *testing.T) {
	ctx := context.Background()
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-poller"] = &store.PipelineRun{
		ID:           "run-poller",
		DesignID:     "cb-poller",
		Project:      "test-project",
		CurrentPhase: domain.PhaseImplement,
		Status:       "active",
	}

	restore := installTestGlobalsWithResolverExec(t, fc, fs, "test-project", func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		if len(args) >= 2 && args[0] == "list-sessions" {
			return []byte(""), nil
		}
		if len(args) >= 3 && args[0] == "list-windows" {
			return []byte(""), nil
		}
		return nil, fmt.Errorf("unexpected tmux args %v", args)
	})
	defer restore()

	prevRun := reconcileExitedSessionsRun
	t.Cleanup(func() { reconcileExitedSessionsRun = prevRun })

	called := 0
	reconcileExitedSessionsRun = func(context.Context) { called++ }

	reconcileStaleState(ctx, false)

	if called != 1 {
		t.Fatalf("reconciler calls = %d, want 1", called)
	}
}
