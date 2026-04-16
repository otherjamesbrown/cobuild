package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestRedispatchKillsRunningSessionAndDispatches(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-redispatch",
		Title:  "Test redispatch",
		Type:   "task",
		Status: "in_progress",
	})

	fs := newFakeStore()
	fs.runs["cb-redispatch"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-redispatch",
		CurrentPhase: "implement",
		Status:       "active",
	}
	now := time.Now()
	fs.sessions = []store.SessionRecord{{
		ID:          "ps-running",
		DesignID:    "cb-redispatch",
		TaskID:      "cb-redispatch",
		Phase:       "implement",
		Status:      "running",
		Runtime:     "claude-code",
		StartedAt:   now.Add(-10 * time.Minute),
		TmuxSession: ptr("cobuild-test"),
		TmuxWindow:  ptr("cb-redispatch"),
	}}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevExec := execCommandCombinedOutput
	t.Cleanup(func() { execCommandCombinedOutput = prevExec })
	var dispatched bool
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "cobuild" && len(args) > 0 && args[0] == "dispatch" {
			dispatched = true
			return []byte("Dispatched cb-redispatch"), nil
		}
		return nil, nil
	}

	err := redispatchCmd.RunE(redispatchCmd, []string{"cb-redispatch"})
	if err != nil {
		t.Fatalf("redispatch returned error: %v", err)
	}

	if result, ok := fs.ended["ps-running"]; !ok {
		t.Fatal("running session should have been ended")
	} else if result.Status != "cancelled" {
		t.Fatalf("session status = %q, want cancelled", result.Status)
	}

	if !dispatched {
		t.Fatal("dispatch should have been called")
	}
}

func TestRedispatchNoRunningSessionStillDispatches(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-norun",
		Title:  "No running session",
		Type:   "task",
		Status: "in_progress",
	})

	fs := newFakeStore()
	fs.runs["cb-norun"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-norun",
		CurrentPhase: "implement",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevExec := execCommandCombinedOutput
	t.Cleanup(func() { execCommandCombinedOutput = prevExec })
	var dispatched bool
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "cobuild" && len(args) > 0 && args[0] == "dispatch" {
			dispatched = true
			return []byte("Dispatched"), nil
		}
		return nil, nil
	}

	err := redispatchCmd.RunE(redispatchCmd, []string{"cb-norun"})
	if err != nil {
		t.Fatalf("redispatch returned error: %v", err)
	}
	if !dispatched {
		t.Fatal("should still dispatch even with no running session")
	}
}
