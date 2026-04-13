//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/e2e/harness"
)

const (
	happyDesignID = "cb-happy-path"
	happyTaskAID  = "cb-happy-task-a"
	happyTaskBID  = "cb-happy-task-b"
)

func TestE2EHappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h := harness.Setup(t, harness.Options{
		Project: "happy-e2e",
		Runtime: "stub",
	})

	if err := h.AddWorkItem(connector.WorkItem{
		ID:      happyDesignID,
		Title:   "Happy path design",
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

	initOut, err := h.InitPipeline(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("init failed:\n%s", h.FailureReport("design", "init command", initOut))
	}
	if _, err := h.Store.CancelRunningSessions(ctx, happyDesignID); err != nil {
		t.Fatalf("preflight session cleanup failed: %v", err)
	}

	orchestrateOut, err := h.Orchestrate(ctx, happyDesignID, 2*time.Minute)
	if err != nil {
		t.Fatalf("orchestrate failed:\n%s", h.FailureReport("orchestrate", "command exited non-zero", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	run, err := h.GetRun(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("load final run:\n%s", h.FailureReport("done", "load pipeline run", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if run.Status != "completed" || run.CurrentPhase != "done" {
		t.Fatalf("unexpected final run (%s, %s):\n%s", run.CurrentPhase, run.Status, h.FailureReport("done", "pipeline run should be completed/done", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	commitCount, commits, err := h.CountMainCommitsByPrefix(ctx, "[cb-happy-task-")
	if err != nil {
		t.Fatalf("count main commits:\n%s", h.FailureReport("review", "read main git history", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if commitCount < 2 {
		t.Fatalf("expected at least 2 task commits on main, got %d (%v):\n%s", commitCount, commits, h.FailureReport("review", "main should contain two task-prefixed commits", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	running, err := h.ListRunningSessions(ctx)
	if err != nil {
		t.Fatalf("list running sessions:\n%s", h.FailureReport("done", "list running sessions", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if len(running) != 0 {
		t.Fatalf("expected no running sessions, got %d:\n%s", len(running), h.FailureReport("done", "no pipeline sessions should remain running", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	windows, err := h.ListTmuxWindows(ctx)
	if err != nil {
		t.Fatalf("list tmux windows:\n%s", h.FailureReport("done", "list tmux windows", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if len(windows) != 0 {
		t.Fatalf("expected no stale tmux windows, got %v:\n%s", windows, h.FailureReport("done", "no stale tmux windows should remain", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
}
