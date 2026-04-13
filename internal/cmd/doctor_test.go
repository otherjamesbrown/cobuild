package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestDoctorTableOutputIncludesColumnsAndScopedPipeline(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-healthy"] = &store.PipelineRun{ID: "run-1", DesignID: "cb-healthy", Project: "proj-a", CurrentPhase: "implement", Status: "active"}
	fs.runs["cb-other"] = &store.PipelineRun{ID: "run-2", DesignID: "cb-other", Project: "proj-b", CurrentPhase: "review", Status: "active"}

	fc.addItem(&connector.WorkItem{ID: "cb-healthy", Type: "task", Status: "open", Project: "proj-a"})
	fc.addItem(&connector.WorkItem{ID: "cb-other", Type: "task", Status: "open", Project: "proj-b"})

	restore := installTestGlobals(t, fc, fs, "proj-a")
	defer restore()

	buf := &bytes.Buffer{}
	resetDoctorCommandState(t, buf)

	outputFormat = "text"
	if err := doctorCmd.Flags().Set("pipeline", "cb-healthy"); err != nil {
		t.Fatalf("set pipeline flag: %v", err)
	}

	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"DESIGN", "PROJECT", "HEALTH", "INCONSISTENCIES", "RECOMMENDED", "cb-healthy", "All pipelines healthy."} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cb-other") {
		t.Fatalf("scoped output should not include cb-other:\n%s", out)
	}
}

func TestDoctorJSONOutputIncludesRecommendedFields(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-broken"] = &store.PipelineRun{ID: "run-1", DesignID: "cb-broken", Project: "proj-a", CurrentPhase: "implement", Status: "active"}

	fc.addItem(&connector.WorkItem{ID: "cb-broken", Type: "task", Status: "closed", Project: "proj-a"})

	restore := installTestGlobals(t, fc, fs, "proj-a")
	defer restore()

	buf := &bytes.Buffer{}
	resetDoctorCommandState(t, buf)
	outputFormat = "json"

	err := doctorCmd.RunE(doctorCmd, nil)
	if commandExitCode(err) != 1 {
		t.Fatalf("commandExitCode = %d, want 1 (err=%v)", commandExitCode(err), err)
	}

	var payload doctorReport
	if unmarshalErr := json.Unmarshal(buf.Bytes(), &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal JSON output: %v\n%s", unmarshalErr, buf.String())
	}
	if len(payload.Pipelines) != 1 {
		t.Fatalf("len(payload.Pipelines) = %d, want 1", len(payload.Pipelines))
	}

	got := payload.Pipelines[0]
	if got.Health != "INCONSISTENT" {
		t.Fatalf("health = %q, want INCONSISTENT", got.Health)
	}
	if got.Recommended != "complete run" {
		t.Fatalf("recommended = %q, want complete run", got.Recommended)
	}
	if len(got.Inconsistencies) != 1 || got.Inconsistencies[0] != "pipeline run is active but work item is closed" {
		t.Fatalf("inconsistencies = %#v", got.Inconsistencies)
	}
}

func TestDoctorFixAppliesRecoveryActions(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-zombie"] = &store.PipelineRun{ID: "run-1", DesignID: "cb-zombie", Project: "proj-a", CurrentPhase: "implement", Status: "active"}
	fs.sessions = []store.SessionRecord{{
		ID:         "ps-1",
		PipelineID: "run-1",
		DesignID:   "cb-zombie",
		TaskID:     "cb-zombie",
		Project:    "proj-a",
		Status:     "running",
	}}

	fc.addItem(&connector.WorkItem{ID: "cb-zombie", Type: "task", Status: "open", Project: "proj-a"})

	restore := installTestGlobals(t, fc, fs, "proj-a")
	defer restore()

	buf := &bytes.Buffer{}
	resetDoctorCommandState(t, buf)
	outputFormat = "text"
	if err := doctorCmd.Flags().Set("fix", "true"); err != nil {
		t.Fatalf("set fix flag: %v", err)
	}

	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor returned error: %v", err)
	}

	result, ok := fs.ended["ps-1"]
	if !ok {
		t.Fatalf("expected orphaned session to be ended")
	}
	if result.Status != "orphaned" {
		t.Fatalf("session status = %q, want orphaned", result.Status)
	}

	out := buf.String()
	for _, want := range []string{"cb-zombie", "cancel session", "Applied 1 reconciliation change(s)."} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func resetDoctorCommandState(t *testing.T, out *bytes.Buffer) {
	t.Helper()

	oldFormat := outputFormat
	oldOut := doctorCmd.OutOrStdout()
	oldErr := doctorCmd.ErrOrStderr()
	outputFormat = "text"
	doctorCmd.SetOut(out)
	doctorCmd.SetErr(out)
	_ = doctorCmd.Flags().Set("pipeline", "")
	_ = doctorCmd.Flags().Set("fix", "false")

	t.Cleanup(func() {
		outputFormat = oldFormat
		doctorCmd.SetOut(oldOut)
		doctorCmd.SetErr(oldErr)
		_ = doctorCmd.Flags().Set("pipeline", "")
		_ = doctorCmd.Flags().Set("fix", "false")
	})
}
