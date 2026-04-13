//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/e2e/harness"
)

func TestE2EResetRerun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h := harnessSetupReset(t)

	seedHappyPathDesign(t, h, happyDesignID, "Reset and rerun design")

	initOut, err := h.InitPipeline(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("init failed:\n%s", h.FailureReport("design", "init command", initOut, happyDesignID))
	}

	designDispatchOut, err := h.Dispatch(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("design dispatch failed:\n%s", h.FailureReport("design", "dispatch command", designDispatchOut, happyDesignID))
	}
	waitForRunPhase(t, ctx, h, happyDesignID, "decompose", "active", 15*time.Second)

	decomposeDispatchOut, err := h.Dispatch(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("decompose dispatch failed:\n%s", h.FailureReport("decompose", "dispatch command", decomposeDispatchOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	waitForWorkItemStatus(t, h, happyTaskAID, "open", 15*time.Second)
	waitForWorkItemStatus(t, h, happyTaskBID, "open", 15*time.Second)

	resetOut, err := h.Reset(ctx, happyDesignID, "implement")
	if err != nil {
		t.Fatalf("reset failed:\n%s", h.FailureReport("reset", "reset command", resetOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	run, err := h.GetRun(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("load reset run:\n%s", h.FailureReport("reset", "load pipeline run", resetOut, happyDesignID))
	}
	if run.CurrentPhase != "implement" || run.Status != "active" {
		t.Fatalf("unexpected run after reset (%s, %s):\n%s", run.CurrentPhase, run.Status, h.FailureReport("reset", "pipeline should reset to implement/active", resetOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if err := h.SetWorkItemProject(happyTaskAID, h.Project); err != nil {
		t.Fatalf("normalize alpha task project after reset: %v", err)
	}
	if err := h.SetWorkItemProject(happyTaskBID, h.Project); err != nil {
		t.Fatalf("normalize beta task project after reset: %v", err)
	}
	waitForWorkItemStatus(t, h, happyTaskAID, "open", 15*time.Second)
	waitForWorkItemStatus(t, h, happyTaskBID, "open", 15*time.Second)
	assertNoZombieState(t, ctx, h, resetOut, happyDesignID, happyTaskAID, happyTaskBID)

	orchestrateOut, err := h.Orchestrate(ctx, happyDesignID, 2*time.Minute)
	if err != nil {
		t.Fatalf("rerun orchestrate failed:\n%s", h.FailureReport("rerun", "command exited non-zero", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	run, err = h.GetRun(ctx, happyDesignID)
	if err != nil {
		t.Fatalf("load rerun state:\n%s", h.FailureReport("done", "load pipeline run", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if run.Status != "completed" || run.CurrentPhase != "done" {
		t.Fatalf("unexpected rerun final state (%s, %s):\n%s", run.CurrentPhase, run.Status, h.FailureReport("done", "pipeline should complete after rerun", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	commitCount, commits, err := h.CountMainCommitsByPrefix(ctx, "[cb-happy-task-")
	if err != nil {
		t.Fatalf("count rerun commits:\n%s", h.FailureReport("review", "read main git history", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}
	if commitCount < 2 {
		t.Fatalf("expected rerun to merge two task commits, got %d (%v):\n%s", commitCount, commits, h.FailureReport("review", "main should contain two rerun task commits", orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID))
	}

	assertNoZombieState(t, ctx, h, orchestrateOut, happyDesignID, happyTaskAID, happyTaskBID)
}

func harnessSetupReset(t *testing.T) *harness.Harness {
	t.Helper()
	return harness.Setup(t, harness.Options{
		Project: "happy-e2e",
		Runtime: "stub",
	})
}
