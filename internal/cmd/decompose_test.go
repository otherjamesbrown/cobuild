package cmd

import (
	"os"
	"path/filepath"
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
