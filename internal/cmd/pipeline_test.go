package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestDecomposeCmdPassesForMultiRepoDesignSplitIntoSingleRepoTasks(t *testing.T) {
	ctx := context.Background()
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-design"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-design",
		CurrentPhase: "decompose",
		Status:       "active",
	}

	fc.addItem(&connector.WorkItem{
		ID:      "cb-design",
		Title:   "Split work across repos",
		Type:    "design",
		Status:  "open",
		Content: "Update context-palace dispatch guidance and penfold runtime wiring in separate tasks.",
	})
	fc.addItem(&connector.WorkItem{
		ID:       "cb-task-cp",
		Title:    "Context Palace task",
		Type:     "task",
		Status:   "open",
		Metadata: map[string]any{"repo": "context-palace"},
	})
	fc.addItem(&connector.WorkItem{
		ID:       "cb-task-pf",
		Title:    "Penfold task",
		Type:     "task",
		Status:   "open",
		Metadata: map[string]any{"repo": "penfold"},
	})
	fc.parent["cb-task-cp"] = "cb-design"
	fc.parent["cb-task-pf"] = "cb-design"
	fc.setBlockedBy("cb-task-pf", connector.Edge{ItemID: "cb-task-cp", EdgeType: "blocked-by", Status: "open"})

	restore := installTestGlobals(t, fc, fs, "context-palace")
	defer restore()

	_ = decomposeCmd.Flags().Set("verdict", "pass")
	_ = decomposeCmd.Flags().Set("body", "Split into one task per repo.")
	t.Cleanup(func() {
		_ = decomposeCmd.Flags().Set("verdict", "")
		_ = decomposeCmd.Flags().Set("body", "")
	})

	if err := decomposeCmd.RunE(decomposeCmd, []string{"cb-design"}); err != nil {
		t.Fatalf("decompose returned error: %v", err)
	}

	if got := fs.runs["cb-design"].CurrentPhase; got != "implement" {
		t.Fatalf("design phase = %q, want implement", got)
	}
	if len(fs.gates) != 1 {
		t.Fatalf("recorded %d gates, want 1", len(fs.gates))
	}

	repoCP, _ := fc.GetMetadata(ctx, "cb-task-cp", "repo")
	repoPF, _ := fc.GetMetadata(ctx, "cb-task-pf", "repo")
	if repoCP != "context-palace" || repoPF != "penfold" {
		t.Fatalf("child repo metadata = (%q, %q), want (context-palace, penfold)", repoCP, repoPF)
	}

	blockers, err := fc.GetEdges(ctx, "cb-task-pf", "outgoing", []string{"blocked-by"})
	if err != nil {
		t.Fatalf("get blockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ItemID != "cb-task-cp" {
		t.Fatalf("blocked-by edges = %+v, want cb-task-pf blocked by cb-task-cp", blockers)
	}
}

func TestDecomposeCmdFailsWhenChildTaskRepoTargetingIsMissingOrAmbiguous(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-design"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-design",
		CurrentPhase: "decompose",
		Status:       "active",
	}

	fc.addItem(&connector.WorkItem{
		ID:      "cb-design",
		Title:   "Ambiguous repo targeting",
		Type:    "design",
		Status:  "open",
		Content: "Touch context-palace and penfold, but do not leave repo ownership implicit.",
	})
	fc.addItem(&connector.WorkItem{ID: "cb-task-missing", Title: "Missing repo", Type: "task", Status: "open"})
	fc.addItem(&connector.WorkItem{
		ID:       "cb-task-ambiguous",
		Title:    "Ambiguous repo",
		Type:     "task",
		Status:   "open",
		Metadata: map[string]any{"repo": "context-palace, penfold"},
	})
	fc.parent["cb-task-missing"] = "cb-design"
	fc.parent["cb-task-ambiguous"] = "cb-design"

	restore := installTestGlobals(t, fc, fs, "context-palace")
	defer restore()

	_ = decomposeCmd.Flags().Set("verdict", "pass")
	_ = decomposeCmd.Flags().Set("body", "Should fail until every child task has one repo.")
	t.Cleanup(func() {
		_ = decomposeCmd.Flags().Set("verdict", "")
		_ = decomposeCmd.Flags().Set("body", "")
	})

	err := decomposeCmd.RunE(decomposeCmd, []string{"cb-design"})
	if err == nil {
		t.Fatal("decompose returned nil error, want validation failure")
	}

	msg := err.Error()
	for _, want := range []string{
		"child tasks must target exactly one repo",
		"cb-task-missing (missing `repo` metadata)",
		"cb-task-ambiguous (ambiguous `repo` metadata",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error missing %q:\n%s", want, msg)
		}
	}
	if len(fs.gates) != 0 {
		t.Fatalf("recorded %d gates, want 0 on validation failure", len(fs.gates))
	}
	if got := fs.runs["cb-design"].CurrentPhase; got != "decompose" {
		t.Fatalf("design phase = %q, want decompose after failure", got)
	}
}
