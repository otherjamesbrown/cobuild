//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/e2e/harness"
)

const (
	multiProjectAlpha  = "multi-project-alpha"
	multiProjectBeta   = "multi-project-beta"
	multiDesignAlphaID = "cb-multi-project-alpha"
	multiDesignBetaID  = "cb-multi-project-beta"
	multiTaskAlphaID   = "cb-multi-task-alpha"
	multiTaskBetaID    = "cb-multi-task-beta"
)

func TestE2EMultiProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h := harness.Setup(t, harness.Options{
		Project: multiProjectAlpha,
		Runtime: "stub",
	})
	if _, err := h.AddProject(ctx, multiProjectBeta, nil); err != nil {
		t.Fatalf("add secondary project: %v", err)
	}

	seedAutonomousImplementDesign(t, h, multiDesignAlphaID, multiTaskAlphaID, multiProjectAlpha)
	seedAutonomousImplementDesign(t, h, multiDesignBetaID, multiTaskBetaID, multiProjectBeta)

	pollerOut, err := h.PollerOnce(ctx, multiProjectAlpha)
	if err != nil {
		t.Fatalf("poller failed:\n%s", h.FailureReport("implement", "poller --once", pollerOut, multiDesignAlphaID, multiTaskAlphaID, multiDesignBetaID, multiTaskBetaID))
	}
	waitOut, err := h.RunCobuild(ctx, "wait", multiTaskAlphaID, multiTaskBetaID, "--timeout", "30s", "--interval", "5")
	if err != nil {
		t.Fatalf("wait for task completion failed:\n%s", h.FailureReport("implement", "wait for both tasks needs-review", pollerOut+"\n"+waitOut, multiDesignAlphaID, multiTaskAlphaID, multiDesignBetaID, multiTaskBetaID))
	}

	waitForWorkItemStatus(t, h, multiTaskAlphaID, "needs-review", 20*time.Second)
	waitForWorkItemStatus(t, h, multiTaskBetaID, "needs-review", 20*time.Second)

	alphaTask, err := h.GetWorkItem(multiTaskAlphaID)
	if err != nil {
		t.Fatalf("load alpha task: %v", err)
	}
	betaTask, err := h.GetWorkItem(multiTaskBetaID)
	if err != nil {
		t.Fatalf("load beta task: %v", err)
	}

	assertTaskDispatchProject(t, alphaTask, multiProjectAlpha)
	assertTaskDispatchProject(t, betaTask, multiProjectBeta)

	resetAlphaOut, err := h.Reset(ctx, multiDesignAlphaID, "design")
	if err != nil {
		t.Fatalf("reset alpha design failed:\n%s", h.FailureReport("reset", "alpha cleanup reset", resetAlphaOut, multiDesignAlphaID, multiTaskAlphaID))
	}
	resetBetaOut, err := h.RunCobuildForProject(ctx, multiProjectBeta, "reset", multiDesignBetaID, "--phase", "design")
	if err != nil {
		t.Fatalf("reset beta design failed:\n%s", h.FailureReport("reset", "beta cleanup reset", resetBetaOut, multiDesignBetaID, multiTaskBetaID))
	}
	if _, err := h.Store.CancelRunningSessions(ctx, multiDesignAlphaID); err != nil {
		t.Fatalf("cancel alpha sessions: %v", err)
	}
	if _, err := h.Store.CancelRunningSessions(ctx, multiDesignBetaID); err != nil {
		t.Fatalf("cancel beta sessions: %v", err)
	}
	if _, err := h.Store.CancelRunningSessions(ctx, multiTaskAlphaID); err != nil {
		t.Fatalf("cancel alpha task sessions: %v", err)
	}
	if _, err := h.Store.CancelRunningSessions(ctx, multiTaskBetaID); err != nil {
		t.Fatalf("cancel beta task sessions: %v", err)
	}
	cancelAllRunningSessions(t, ctx, h)

	assertNoZombieState(t, ctx, h, pollerOut+"\n"+waitOut+"\n"+resetAlphaOut+"\n"+resetBetaOut, multiDesignAlphaID, multiTaskAlphaID, multiDesignBetaID, multiTaskBetaID)
}

func seedAutonomousImplementDesign(t *testing.T, h *harness.Harness, designID, taskID, project string) {
	t.Helper()

	if err := h.AddWorkItem(connector.WorkItem{
		ID:      designID,
		Title:   "Autonomous implement design " + project,
		Type:    "design",
		Status:  "open",
		Project: project,
		Content: "Poller should dispatch this design's implementation task.",
	}); err != nil {
		t.Fatalf("seed design %s: %v", designID, err)
	}
	if _, err := h.Store.CreateRunWithMode(context.Background(), designID, project, "implement", "autonomous"); err != nil {
		t.Fatalf("create autonomous run for %s: %v", designID, err)
	}
	if err := h.AddWorkItem(connector.WorkItem{
		ID:      taskID,
		Title:   "Implement " + project,
		Type:    "task",
		Status:  "open",
		Project: project,
		Content: "Apply the canned implementation patch.",
		Metadata: map[string]any{
			"repo": project,
		},
	}); err != nil {
		t.Fatalf("seed task %s: %v", taskID, err)
	}
	if err := h.AddWorkItemEdge(taskID, designID, "child-of"); err != nil {
		t.Fatalf("seed task edge %s -> %s: %v", taskID, designID, err)
	}
}

func assertTaskDispatchProject(t *testing.T, item *connector.WorkItem, project string) {
	t.Helper()

	if item == nil {
		t.Fatal("task item is nil")
	}
	if item.Project != project {
		t.Fatalf("task %s project = %q, want %q", item.ID, item.Project, project)
	}
	prURL, _ := item.Metadata["pr_url"].(string)
	if strings.TrimSpace(prURL) == "" {
		t.Fatalf("task %s missing pr_url metadata", item.ID)
	}
	worktreePath, _ := item.Metadata["worktree_path"].(string)
	wantFragment := filepath.Join("worktrees", project, item.ID)
	if !strings.Contains(worktreePath, wantFragment) {
		t.Fatalf("task %s worktree_path = %q, want fragment %q", item.ID, worktreePath, wantFragment)
	}
}
