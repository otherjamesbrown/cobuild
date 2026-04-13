package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestStatusShowsResolverHealthAndInconsistencies(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-status", Type: "design", Status: "open"})

	fs := newFakeStore()
	fs.runStatuses = []store.PipelineRunStatus{
		{
			DesignID:     "cb-status",
			Project:      "test-project",
			Phase:        "implement",
			Status:       "active",
			TaskTotal:    3,
			TaskDone:     1,
			LastProgress: time.Now().Add(-5 * time.Minute),
		},
	}
	fs.runs["cb-status"] = &store.PipelineRun{
		ID:           "run-status",
		DesignID:     "cb-status",
		Project:      "test-project",
		CurrentPhase: "implement",
		Status:       "active",
	}
	fs.sessionsByDesign["cb-status"] = []store.SessionRecord{
		{
			ID:          "ps-zombie",
			DesignID:    "cb-status",
			Status:      "running",
			StartedAt:   time.Now().Add(-15 * time.Minute),
			TmuxSession: strPtr("cobuild-test"),
			TmuxWindow:  strPtr("cb-status"),
		},
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	out, err := runCommandWithOutputs(t, statusCmd, nil)
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}

	for _, want := range []string{
		"ID",
		"HEALTH",
		"cb-status",
		"ZOMBIE",
		"1/3",
		"! session ps-zombie is running but no tmux window exists",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusPrefersResolvedPhaseAndSurfacesSourceErrors(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{ID: "cb-status-2", Type: "design", Status: "open"})

	fs := newFakeStore()
	fs.runStatuses = []store.PipelineRunStatus{
		{
			DesignID:     "cb-status-2",
			Project:      "test-project",
			Phase:        "design",
			Status:       "active",
			LastProgress: time.Now().Add(-2 * time.Minute),
		},
	}
	fs.runs["cb-status-2"] = &store.PipelineRun{
		ID:           "run-status-2",
		DesignID:     "cb-status-2",
		Project:      "test-project",
		CurrentPhase: "review",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	out, err := runCommandWithOutputs(t, statusCmd, nil)
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "cb-status-2") || !strings.Contains(out, "review") {
		t.Fatalf("status output missing resolved phase:\n%s", out)
	}
	if strings.Contains(out, "resolver error") {
		t.Fatalf("status should not print resolver errors for healthy rows:\n%s", out)
	}
}
