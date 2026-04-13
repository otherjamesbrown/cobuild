//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/e2e/harness"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func seedHappyPathDesign(t *testing.T, h *harness.Harness, designID, title string) {
	t.Helper()

	if err := h.AddWorkItem(connector.WorkItem{
		ID:      designID,
		Title:   title,
		Type:    "design",
		Status:  "open",
		Project: h.Project,
		Content: strings.TrimSpace(`
## Scope
Drive the first end-to-end happy path through CoBuild.

## Acceptance Criteria
- [ ] Review passes.
- [ ] Decomposition creates two implementation tasks.
- [ ] Both tasks land on main and the design finishes done.
`),
	}); err != nil {
		t.Fatalf("seed design: %v", err)
	}
}

func waitForNoRunningSessions(t *testing.T, ctx context.Context, h *harness.Harness, timeout time.Duration, output string, taskIDs ...string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := h.ListRunningSessions(ctx)
		if err == nil && len(running) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	running, err := h.ListRunningSessions(ctx)
	if err != nil {
		t.Fatalf("list running sessions:\n%s", h.FailureReport("wait", "list running sessions", output, taskIDs...))
	}
	t.Fatalf("expected no running sessions, got %d:\n%s", len(running), h.FailureReport("wait", "running sessions should drain", output, taskIDs...))
}

func waitForWorkItemStatus(t *testing.T, h *harness.Harness, id, want string, timeout time.Duration) *connector.WorkItem {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		item, err := h.GetWorkItem(id)
		if err == nil && item.Status == want {
			return item
		}
		time.Sleep(100 * time.Millisecond)
	}

	item, err := h.GetWorkItem(id)
	if err != nil {
		t.Fatalf("get work item %s: %v", id, err)
	}
	t.Fatalf("work item %s status = %q, want %q", id, item.Status, want)
	return nil
}

func waitForRunPhase(t *testing.T, ctx context.Context, h *harness.Harness, designID, wantPhase, wantStatus string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := h.GetRun(ctx, designID)
		if err == nil && run.CurrentPhase == wantPhase {
			if wantStatus == "" || run.Status == wantStatus {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	run, err := h.GetRun(ctx, designID)
	if err != nil {
		t.Fatalf("get run %s: %v", designID, err)
	}
	t.Fatalf("run %s = (%s, %s), want (%s, %s)", designID, run.CurrentPhase, run.Status, wantPhase, wantStatus)
}

func assertNoZombieState(t *testing.T, ctx context.Context, h *harness.Harness, output string, taskIDs ...string) {
	t.Helper()

	running, err := h.ListRunningSessions(ctx)
	if err != nil {
		t.Fatalf("list running sessions:\n%s", h.FailureReport("cleanup", "list running sessions", output, taskIDs...))
	}
	if len(running) != 0 {
		t.Fatalf("expected no running sessions, got %d:\n%s", len(running), h.FailureReport("cleanup", "no pipeline sessions should remain running", output, taskIDs...))
	}

	windows, err := h.ListTmuxWindows(ctx)
	if err != nil {
		t.Fatalf("list tmux windows:\n%s", h.FailureReport("cleanup", "list tmux windows", output, taskIDs...))
	}
	if len(windows) != 0 {
		t.Fatalf("expected no stale tmux windows, got %v:\n%s", windows, h.FailureReport("cleanup", "no stale tmux windows should remain", output, taskIDs...))
	}
}

func requireMissingWorkItem(t *testing.T, h *harness.Harness, id string) {
	t.Helper()

	_, err := h.GetWorkItem(id)
	if err == nil {
		t.Fatalf("expected %s to be removed by reset", id)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected lookup error for %s: %v", id, err)
	}
}

func cancelAllRunningSessions(t *testing.T, ctx context.Context, h *harness.Harness) {
	t.Helper()

	running, err := h.ListRunningSessions(ctx)
	if err != nil {
		t.Fatalf("list running sessions: %v", err)
	}
	for _, session := range running {
		if err := h.Store.EndSession(ctx, session.ID, store.SessionResult{
			ExitCode:       -1,
			Status:         "cancelled",
			CompletionNote: "Cancelled by e2e cleanup",
		}); err != nil {
			t.Fatalf("end running session %s: %v", session.ID, err)
		}
	}
}
