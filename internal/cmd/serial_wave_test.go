package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestDispatchWaveSerialTwoWaveProgression(t *testing.T) {
	ctx := context.Background()
	fc := newFakeConnector()
	restore := installTestGlobals(t, fc, nil, "")
	defer restore()

	fc.addItem(&connector.WorkItem{ID: "cb-design", Type: "design", Status: "open"})
	fc.addTask("cb-task-1", "open", 1)
	fc.addTask("cb-task-2", "open", 1)
	fc.addTask("cb-task-3", "open", 2)
	fc.addTask("cb-task-4", "open", 2)
	fc.setChildTasks("cb-design", "cb-task-1", "cb-task-2", "cb-task-3", "cb-task-4")
	fc.setBlockedBy("cb-task-3", connector.Edge{ItemID: "cb-task-1", Status: "open", EdgeType: "blocked-by"})
	fc.setBlockedBy("cb-task-4", connector.Edge{ItemID: "cb-task-2", Status: "open", EdgeType: "blocked-by"})

	repoCfg := &config.Config{Dispatch: config.DispatchCfg{WaveStrategy: "serial"}}

	dispatchWaveCmd.Flags().Set("dry-run", "true")
	defer dispatchWaveCmd.Flags().Set("dry-run", "false")

	out1 := captureStdout(t, func() {
		oldFind := findRepoRoot
		_ = oldFind
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"cb-design"}); err != nil {
			t.Fatalf("first serial dispatch-wave failed: %v", err)
		}
	})

	if !strings.Contains(out1, "Dispatching 2 tasks (serial wave) for cb-design") {
		t.Fatalf("first dispatch output missing serial-wave header:\n%s", out1)
	}
	if !strings.Contains(out1, "cb-task-1") || !strings.Contains(out1, "cb-task-2") {
		t.Fatalf("first dispatch output missing wave 1 tasks:\n%s", out1)
	}
	if strings.Contains(out1, "cb-task-3") || strings.Contains(out1, "cb-task-4") {
		t.Fatalf("serial dispatch should not pre-dispatch wave 2 tasks before wave 1 closes:\n%s", out1)
	}
	if resolveWaveStrategy(repoCfg) != "serial" {
		t.Fatalf("expected serial wave strategy")
	}

	fc.mustUpdateStatus(ctx, "cb-task-1", "closed")
	fc.mustUpdateStatus(ctx, "cb-task-2", "closed")
	fc.setBlockedBy("cb-task-3", connector.Edge{ItemID: "cb-task-1", Status: "closed", EdgeType: "blocked-by"})
	fc.setBlockedBy("cb-task-4", connector.Edge{ItemID: "cb-task-2", Status: "closed", EdgeType: "blocked-by"})

	out2 := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"cb-design"}); err != nil {
			t.Fatalf("second serial dispatch-wave failed: %v", err)
		}
	})

	if !strings.Contains(out2, "cb-task-3") || !strings.Contains(out2, "cb-task-4") {
		t.Fatalf("second dispatch output missing wave 2 tasks after wave 1 closed:\n%s", out2)
	}
	if strings.Contains(out2, "cb-task-1") || strings.Contains(out2, "cb-task-2") {
		t.Fatalf("closed wave 1 tasks should not be redispatched:\n%s", out2)
	}
}

func TestPollerSerialKeepsLaterWaveBlockedUntilCurrentWaveCloses(t *testing.T) {
	fc := newFakeConnector()
	restore := installTestGlobals(t, fc, nil, "")
	defer restore()

	fc.addItem(&connector.WorkItem{ID: "cb-design", Type: "design", Status: "open"})
	fc.addTask("cb-wave1-closed", "closed", 1)
	fc.addTask("cb-wave1-open", "open", 1)
	fc.addTask("cb-wave2-ready", "open", 2)
	fc.setChildTasks("cb-design", "cb-wave1-closed", "cb-wave1-open", "cb-wave2-ready")
	fc.setBlockedBy("cb-wave2-ready", connector.Edge{ItemID: "cb-wave1-closed", Status: "closed", EdgeType: "blocked-by"})

	out := captureStdout(t, func() {
		dispatchReadyTasks(context.Background(), "", &config.Config{
			Dispatch: config.DispatchCfg{WaveStrategy: "serial", MaxConcurrent: 3},
		}, "cb-design", true)
	})

	if !strings.Contains(out, "cb-wave1-open — ready to dispatch") {
		t.Fatalf("serial poller output should keep current wave dispatchable:\n%s", out)
	}
	if strings.Contains(out, "cb-wave2-ready — ready to dispatch") {
		t.Fatalf("serial poller should block later waves until the current wave closes:\n%s", out)
	}
	if !strings.Contains(out, "1 ready") {
		t.Fatalf("serial poller summary should show only one ready task:\n%s", out)
	}
}

func TestPollerParallelStillDispatchesAllReadyTasks(t *testing.T) {
	fc := newFakeConnector()
	restore := installTestGlobals(t, fc, nil, "")
	defer restore()

	fc.addItem(&connector.WorkItem{ID: "cb-design", Type: "design", Status: "open"})
	fc.addTask("cb-wave1-closed", "closed", 1)
	fc.addTask("cb-wave1-open", "open", 1)
	fc.addTask("cb-wave2-ready", "open", 2)
	fc.setChildTasks("cb-design", "cb-wave1-closed", "cb-wave1-open", "cb-wave2-ready")
	fc.setBlockedBy("cb-wave2-ready", connector.Edge{ItemID: "cb-wave1-closed", Status: "closed", EdgeType: "blocked-by"})

	out := captureStdout(t, func() {
		dispatchReadyTasks(context.Background(), "", &config.Config{
			Dispatch: config.DispatchCfg{WaveStrategy: "parallel", MaxConcurrent: 3},
		}, "cb-design", true)
	})

	if !strings.Contains(out, "cb-wave1-open — ready to dispatch") || !strings.Contains(out, "cb-wave2-ready — ready to dispatch") {
		t.Fatalf("parallel poller should keep existing all-ready dispatch behavior:\n%s", out)
	}
	if !strings.Contains(out, "2 ready") {
		t.Fatalf("parallel poller summary should show both ready tasks:\n%s", out)
	}
}

func TestNextImplementGuidanceMentionsSerialSafety(t *testing.T) {
	fs := &fakeStore{
		run: &store.PipelineRun{
			ID:           "pr-1",
			DesignID:     "cb-design",
			CurrentPhase: "implement",
			Status:       "active",
		},
		tasks: []store.PipelineTaskRecord{
			{TaskShardID: "cb-task-1", Status: "closed"},
			{TaskShardID: "cb-task-2", Status: "pending"},
		},
	}
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-design", Title: "Serial wave test", Type: "design", Status: "open"})

	restore := installTestGlobals(t, fc, fs, "")
	defer restore()

	waveStrategyOverride = func() string { return "serial" }
	defer func() { waveStrategyOverride = nil }()

	out := captureStdout(t, func() {
		if err := nextCmd.RunE(nextCmd, []string{"cb-design"}); err != nil {
			t.Fatalf("next command failed: %v", err)
		}
	})

	if !strings.Contains(out, "dispatch only the current serial wave after earlier tasks close") {
		t.Fatalf("serial next guidance should describe serial wave progression:\n%s", out)
	}
	if !strings.Contains(out, "avoiding the unsafe dispatch-everything-then-rebase-later path") {
		t.Fatalf("serial next guidance should make the safety rationale explicit:\n%s", out)
	}
}
