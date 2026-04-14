package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestDecomposePassFailsWhenChildTaskRepoResolutionIsMissingOrAmbiguous(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registerProjectRepos(t, "multi", "repo-alpha", "repo-beta")

	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{ID: "design-1", Title: "design", Type: "design", Status: "open", Project: "multi"})
	testConn.addItem(&connector.WorkItem{ID: "cb-missing", Title: "missing", Type: "task", Status: "open", Project: "multi"})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-ambiguous",
		Title:   "ambiguous",
		Type:    "task",
		Status:  "open",
		Project: "multi",
		Metadata: map[string]any{
			"repo": []string{"repo-alpha", "repo-beta"},
		},
	})
	testConn.setChildTasks("design-1", "cb-missing", "cb-ambiguous")

	testStore := newFakeStore()
	now := time.Now()
	testStore.runs["design-1"] = &store.PipelineRun{
		ID:           "run-design-1",
		DesignID:     "design-1",
		CurrentPhase: "decompose",
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	restore := installCommandTestGlobals(t, testConn, testStore, "multi")
	defer restore()

	_, err := runDecomposeCommand(t, "design-1", "pass", "looks good")
	if err == nil {
		t.Fatal("expected decompose pass to fail")
	}
	if !strings.Contains(err.Error(), "cb-missing") {
		t.Fatalf("error = %v, want missing task ID", err)
	}
	if !strings.Contains(err.Error(), "cb-ambiguous") {
		t.Fatalf("error = %v, want ambiguous task ID", err)
	}
	if len(testStore.gates) != 0 {
		t.Fatalf("recorded gates = %d, want 0", len(testStore.gates))
	}
	if got := testStore.runs["design-1"].CurrentPhase; got != "decompose" {
		t.Fatalf("phase = %s, want decompose", got)
	}
}

func TestDecomposePassAdvancesWhenChildTasksResolveToSingleRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registerProjectRepos(t, "single", "repo-solo")

	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{ID: "design-2", Title: "design", Type: "design", Status: "open", Project: "single"})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-task-1",
		Title:   "task 1",
		Type:    "task",
		Status:  "open",
		Project: "single",
		Metadata: map[string]any{
			"repo": "repo-solo",
		},
	})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-task-2",
		Title:   "task 2",
		Type:    "task",
		Status:  "open",
		Project: "single",
		Metadata: map[string]any{
			"repo": "repo-solo",
		},
	})
	testConn.setChildTasks("design-2", "cb-task-1", "cb-task-2")

	testStore := newFakeStore()
	now := time.Now()
	testStore.runs["design-2"] = &store.PipelineRun{
		ID:           "run-design-2",
		DesignID:     "design-2",
		CurrentPhase: "decompose",
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	restore := installCommandTestGlobals(t, testConn, testStore, "single")
	defer restore()

	output, err := runDecomposeCommand(t, "design-2", "pass", "looks good")
	if err != nil {
		t.Fatalf("decompose pass: %v", err)
	}
	if !strings.Contains(output, "Recorded Phase 2 decomposition for design-2") {
		t.Fatalf("output = %q, want success message", output)
	}
	if len(testStore.gates) != 1 {
		t.Fatalf("recorded gates = %d, want 1", len(testStore.gates))
	}
	if got := testStore.gates[0].GateName; got != "decomposition-review" {
		t.Fatalf("gate name = %s, want decomposition-review", got)
	}
	if got := testStore.runs["design-2"].CurrentPhase; got != "implement" {
		t.Fatalf("phase = %s, want implement", got)
	}
}

func TestDecomposePassPrintsFileOverlapWarningsWithoutBlockingGate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registerProjectRepos(t, "single", "repo-solo")

	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{ID: "design-3", Title: "design", Type: "design", Status: "open", Project: "single"})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-task-overlap",
		Title:   "task overlap",
		Type:    "task",
		Status:  "open",
		Project: "single",
		Metadata: map[string]any{
			"repo":  "repo-solo",
			"paths": []string{"internal/cmd"},
		},
	})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-task-clean",
		Title:   "task clean",
		Type:    "task",
		Status:  "open",
		Project: "single",
		Metadata: map[string]any{
			"repo":  "repo-solo",
			"files": []string{"docs/clean.md"},
		},
	})
	testConn.setChildTasks("design-3", "cb-task-overlap", "cb-task-clean")

	testStore := newFakeStore()
	now := time.Now()
	testStore.runs["design-3"] = &store.PipelineRun{
		ID:           "run-design-3",
		DesignID:     "design-3",
		CurrentPhase: "decompose",
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	restore := installCommandTestGlobals(t, testConn, testStore, "single")
	defer restore()

	oldCollector := decomposeMergedTaskCollector
	collectorCalls := 0
	decomposeMergedTaskCollector = func(ctx context.Context, cn connector.Connector, designID, repoRoot string) ([]MergedTask, error) {
		collectorCalls++
		return []MergedTask{
			{TaskID: "cb-merged", FilesChanged: []string{"internal/cmd/pipeline.go", "docs/already-done.md"}},
		}, nil
	}
	defer func() { decomposeMergedTaskCollector = oldCollector }()

	output, err := runDecomposeCommand(t, "design-3", "pass", "looks good")
	if err != nil {
		t.Fatalf("decompose pass: %v", err)
	}
	if collectorCalls != 1 {
		t.Fatalf("collector calls = %d, want 1", collectorCalls)
	}
	if !strings.Contains(output, "⚠️ file-overlap") {
		t.Fatalf("output = %q, want file-overlap section", output)
	}
	if !strings.Contains(output, "task cb-task-overlap overlaps merged task cb-merged: internal/cmd/pipeline.go") {
		t.Fatalf("output = %q, want overlap details", output)
	}
	if strings.Contains(output, "cb-task-clean overlaps") {
		t.Fatalf("output = %q, did not expect clean task warning", output)
	}
	if len(testStore.gates) != 1 {
		t.Fatalf("recorded gates = %d, want 1", len(testStore.gates))
	}
	if got := testStore.runs["design-3"].CurrentPhase; got != "implement" {
		t.Fatalf("phase = %s, want implement", got)
	}
}

func TestDecomposePassOmitsFileOverlapWarningsWhenTasksAreClean(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registerProjectRepos(t, "single", "repo-solo")

	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{ID: "design-4", Title: "design", Type: "design", Status: "open", Project: "single"})
	testConn.addItem(&connector.WorkItem{
		ID:      "cb-task-clean",
		Title:   "task clean",
		Type:    "task",
		Status:  "open",
		Project: "single",
		Metadata: map[string]any{
			"repo":  "repo-solo",
			"files": []string{"docs/clean.md"},
		},
	})
	testConn.setChildTasks("design-4", "cb-task-clean")

	testStore := newFakeStore()
	now := time.Now()
	testStore.runs["design-4"] = &store.PipelineRun{
		ID:           "run-design-4",
		DesignID:     "design-4",
		CurrentPhase: "decompose",
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	restore := installCommandTestGlobals(t, testConn, testStore, "single")
	defer restore()

	oldCollector := decomposeMergedTaskCollector
	decomposeMergedTaskCollector = func(ctx context.Context, cn connector.Connector, designID, repoRoot string) ([]MergedTask, error) {
		return []MergedTask{
			{TaskID: "cb-merged", FilesChanged: []string{"internal/cmd/pipeline.go"}},
		}, nil
	}
	defer func() { decomposeMergedTaskCollector = oldCollector }()

	output, err := runDecomposeCommand(t, "design-4", "pass", "looks good")
	if err != nil {
		t.Fatalf("decompose pass: %v", err)
	}
	if strings.Contains(output, "⚠️ file-overlap") {
		t.Fatalf("output = %q, did not expect file-overlap section", output)
	}
}

func TestMergedFileOverlapIndexMatchesExactFilesAndPathPrefixes(t *testing.T) {
	index := newMergedFileOverlapIndex([]MergedTask{
		{TaskID: "cb-merged-a", FilesChanged: []string{"internal/cmd/pipeline.go", "docs/guide.md"}},
		{TaskID: "cb-merged-b", FilesChanged: []string{"internal/cmd/dispatch.go", "internal/cmd/nested/helper.go"}},
	})

	warnings := index.findTaskOverlaps("cb-task-new", []string{"internal/cmd/pipeline.go", "./internal/cmd", "docs"})

	want := []fileOverlapWarning{
		{
			TaskID:       "cb-task-new",
			MergedTaskID: "cb-merged-a",
			Paths:        []string{"docs/guide.md", "internal/cmd/pipeline.go"},
		},
		{
			TaskID:       "cb-task-new",
			MergedTaskID: "cb-merged-b",
			Paths:        []string{"internal/cmd/dispatch.go", "internal/cmd/nested/helper.go"},
		},
	}
	if !reflect.DeepEqual(warnings, want) {
		t.Fatalf("findTaskOverlaps() = %#v, want %#v", warnings, want)
	}
}

func runDecomposeCommand(t *testing.T, designID, verdict, body string) (string, error) {
	t.Helper()

	if err := decomposeCmd.Flags().Set("verdict", verdict); err != nil {
		t.Fatalf("set verdict flag: %v", err)
	}
	if err := decomposeCmd.Flags().Set("body", body); err != nil {
		t.Fatalf("set body flag: %v", err)
	}
	if err := decomposeCmd.Flags().Set("body-file", ""); err != nil {
		t.Fatalf("clear body-file flag: %v", err)
	}
	defer func() {
		_ = decomposeCmd.Flags().Set("verdict", "")
		_ = decomposeCmd.Flags().Set("body", "")
		_ = decomposeCmd.Flags().Set("body-file", "")
	}()

	return runCommandWithOutputs(t, decomposeCmd, []string{designID})
}

func registerProjectRepos(t *testing.T, project string, repoNames ...string) {
	t.Helper()

	// Isolate HOME so SaveRepoRegistry writes to a tempdir, not the
	// developer's real ~/.cobuild/repos.yaml (cb-3d611c). t.Setenv restores
	// the original value at test cleanup.
	t.Setenv("HOME", t.TempDir())

	reg := &config.RepoRegistry{Repos: map[string]config.RepoEntry{}}
	for _, repoName := range repoNames {
		repoPath := filepath.Join(t.TempDir(), repoName)
		if err := os.MkdirAll(repoPath, 0o755); err != nil {
			t.Fatalf("mkdir repo path: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoPath, ".cobuild.yaml"), []byte("project: "+project+"\n"), 0o644); err != nil {
			t.Fatalf("write .cobuild.yaml: %v", err)
		}
		reg.Repos[repoName] = config.RepoEntry{Path: repoPath}
	}
	if err := config.SaveRepoRegistry(reg); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}
}
