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

// TestE2EDispatchedReviewPath exercises the dispatched review flow end-to-end
// (cb-f3f33f). The stub runtime emits a canned pass verdict from a review-phase
// fixture — this is the path where CoBuild spawns a fresh agent to review the
// PR instead of calling an LLM API inline. Asserts the verdict was recorded via
// the dispatched path (not the legacy CI/external fallback) and the task merged.
func TestE2EDispatchedReviewPath(t *testing.T) {
	ctx := context.Background()
	h := harness.Setup(t, harness.Options{
		Project: "review-e2e",
		Runtime: "stub",
	})
	// Review.Mode defaults to "dispatched" so we don't need to override. The
	// harness sets Review.Provider/Strategy="external" which would fall back
	// to CI-only review in the legacy path; by providing a review-phase
	// fixture, the dispatched path succeeds first and the fallback never
	// runs. If a future harness change disables dispatched by default,
	// explicitly assert it here.
	if mode := h.Config.Review.EffectiveMode(); mode != "dispatched" {
		t.Fatalf("harness review mode = %q, want dispatched for this test", mode)
	}

	const designID = "cb-review-design"
	const taskID = "cb-review-task"

	if err := h.AddWorkItem(connector.WorkItem{
		ID:      designID,
		Title:   "Dispatched-review e2e design",
		Type:    "design",
		Status:  "open",
		Project: h.Project,
		Content: strings.TrimSpace(`
## Scope
Verify the dispatched-review path fires and merges.

## Acceptance Criteria
- [ ] Review dispatches, verdict is recorded via dispatched path.
- [ ] Task's PR merges and the run completes.
`),
	}); err != nil {
		t.Fatalf("seed design: %v", err)
	}

	initOut, err := h.InitPipeline(ctx, designID)
	if err != nil {
		t.Fatalf("init failed:\n%s", h.FailureReport("design", "init command", initOut))
	}
	if _, err := h.Store.CancelRunningSessions(ctx, designID); err != nil {
		t.Fatalf("preflight session cleanup failed: %v", err)
	}

	orchestrateOut, err := h.Orchestrate(ctx, designID, 2*time.Minute)
	if err != nil {
		t.Fatalf("orchestrate failed:\n%s", h.FailureReport("orchestrate", "command exited non-zero", orchestrateOut, designID, taskID))
	}

	// Pipeline-run finished state.
	run, err := h.GetRun(ctx, designID)
	if err != nil {
		t.Fatalf("load final run:\n%s", h.FailureReport("done", "load pipeline run", orchestrateOut, designID, taskID))
	}
	if run.Status != "completed" || run.CurrentPhase != "done" {
		t.Fatalf("unexpected final run (%s, %s):\n%s", run.CurrentPhase, run.Status, h.FailureReport("done", "pipeline run should be completed/done", orchestrateOut, designID, taskID))
	}

	// Task PR landed on main.
	count, commits, err := h.CountMainCommitsByPrefix(ctx, "[cb-review-task")
	if err != nil {
		t.Fatalf("count main commits:\n%s", h.FailureReport("review", "read main git history", orchestrateOut, designID, taskID))
	}
	if count < 1 {
		t.Fatalf("expected >= 1 task commit on main, got %d (%v):\n%s", count, commits, h.FailureReport("review", "review-task commit should be on main", orchestrateOut, designID, taskID))
	}

	// The dispatched path records a review gate row for the TASK (not the
	// design). Absence of this gate would indicate the legacy fallback
	// approved via CI instead — defeats the point of the test.
	gates, err := h.Store.GetGateHistory(ctx, taskID)
	if err != nil {
		t.Fatalf("get gate history for %s: %v", taskID, err)
	}
	foundReviewPass := false
	for _, g := range gates {
		if g.GateName == "review" && g.Verdict == "pass" {
			foundReviewPass = true
			break
		}
	}
	if !foundReviewPass {
		t.Fatalf("no dispatched review gate recorded for %s; gates=%+v", taskID, gates)
	}

	assertNoZombieState(t, ctx, h, orchestrateOut, designID, taskID)
}
