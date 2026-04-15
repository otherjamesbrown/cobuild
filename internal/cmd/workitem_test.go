package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
)

func TestWICreateAutoSetsRepoForSingleRepoProject(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	repoRoot := filepath.Join(tempDir, "penfold")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	writeTestRepoConfig(t, repoRoot, "penfold")

	prevHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
	})

	if err := config.SaveRepoRegistry(&config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			"penfold": {Path: repoRoot},
		},
	}); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}

	testConn := newFakeConnector()
	restore := installTestGlobals(t, testConn, newFakeStore(), "penfold")
	defer restore()

	_ = wiCreateCmd.Flags().Set("title", "Penfold bug")
	_ = wiCreateCmd.Flags().Set("type", "bug")
	t.Cleanup(func() {
		_ = wiCreateCmd.Flags().Set("title", "")
		_ = wiCreateCmd.Flags().Set("type", "")
	})

	out, err := runCommandWithOutputs(t, wiCreateCmd, nil)
	if err != nil {
		t.Fatalf("wi create: %v", err)
	}

	if len(testConn.createRequests) != 1 {
		t.Fatalf("created requests = %d, want 1", len(testConn.createRequests))
	}
	if got := testConn.createRequests[0].Metadata[domain.MetaRepo]; got != "penfold" {
		t.Fatalf("repo metadata = %v, want penfold", got)
	}
	if !strings.Contains(out, "Auto-set repo=penfold from single-repo project penfold.") {
		t.Fatalf("output missing auto-set log:\n%s", out)
	}
}

func TestWICreateDoesNotAutoSetRepoForMultiRepoProject(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	apiRepo := filepath.Join(tempDir, "api")
	webRepo := filepath.Join(tempDir, "web")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	writeTestRepoConfig(t, apiRepo, "multi")
	writeTestRepoConfig(t, webRepo, "multi")

	prevHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
	})

	if err := config.SaveRepoRegistry(&config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			"api": {Path: apiRepo},
			"web": {Path: webRepo},
		},
	}); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}

	testConn := newFakeConnector()
	restore := installTestGlobals(t, testConn, newFakeStore(), "multi")
	defer restore()

	_ = wiCreateCmd.Flags().Set("title", "Multi bug")
	_ = wiCreateCmd.Flags().Set("type", "bug")
	t.Cleanup(func() {
		_ = wiCreateCmd.Flags().Set("title", "")
		_ = wiCreateCmd.Flags().Set("type", "")
	})

	out, err := runCommandWithOutputs(t, wiCreateCmd, nil)
	if err != nil {
		t.Fatalf("wi create: %v", err)
	}

	if len(testConn.createRequests) != 1 {
		t.Fatalf("created requests = %d, want 1", len(testConn.createRequests))
	}
	if _, ok := testConn.createRequests[0].Metadata[domain.MetaRepo]; ok {
		t.Fatalf("did not expect repo metadata, got %#v", testConn.createRequests[0].Metadata)
	}
	if strings.Contains(out, "Auto-set repo=") {
		t.Fatalf("did not expect auto-set log:\n%s", out)
	}
}

func TestWICreateInheritsParentRoutingMetadata(t *testing.T) {
	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{
		ID:     "cb-parent",
		Title:  "parent",
		Type:   "design",
		Status: "open",
		Metadata: map[string]any{
			domain.MetaRepo:            "context-palace",
			domain.MetaDispatchRuntime: domain.RuntimeCodex,
			domain.MetaCompletionMode:  "direct",
		},
	})

	testStore := newFakeStore()
	restore := installTestGlobals(t, testConn, testStore, "context-palace")
	defer restore()

	_ = wiCreateCmd.Flags().Set("title", "Child task")
	_ = wiCreateCmd.Flags().Set("type", "task")
	_ = wiCreateCmd.Flags().Set("body", "Implement the child task.")
	_ = wiCreateCmd.Flags().Set("parent", "cb-parent")
	t.Cleanup(func() {
		_ = wiCreateCmd.Flags().Set("title", "")
		_ = wiCreateCmd.Flags().Set("type", "")
		_ = wiCreateCmd.Flags().Set("body", "")
		_ = wiCreateCmd.Flags().Set("parent", "")
	})

	if err := wiCreateCmd.RunE(wiCreateCmd, nil); err != nil {
		t.Fatalf("wi create: %v", err)
	}

	if len(testConn.createRequests) != 1 {
		t.Fatalf("created requests = %d, want 1", len(testConn.createRequests))
	}

	got := testConn.createRequests[0].Metadata
	if got[domain.MetaRepo] != "context-palace" {
		t.Fatalf("child repo metadata = %v, want context-palace", got[domain.MetaRepo])
	}
	if got[domain.MetaDispatchRuntime] != domain.RuntimeCodex {
		t.Fatalf("child dispatch_runtime = %v, want %s", got[domain.MetaDispatchRuntime], domain.RuntimeCodex)
	}
	if got[domain.MetaCompletionMode] != "direct" {
		t.Fatalf("child completion_mode = %v, want direct", got[domain.MetaCompletionMode])
	}
}

func TestWICreateSkipsAmbiguousParentRepoMetadataButKeepsOtherRoutingMetadata(t *testing.T) {
	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{
		ID:     "cb-parent",
		Title:  "parent",
		Type:   "design",
		Status: "open",
		Metadata: map[string]any{
			domain.MetaRepo:            "context-palace, penfold",
			domain.MetaDispatchRuntime: domain.RuntimeCodex,
			domain.MetaCompletionMode:  "direct",
		},
	})

	testStore := newFakeStore()
	restore := installTestGlobals(t, testConn, testStore, "context-palace")
	defer restore()

	_ = wiCreateCmd.Flags().Set("title", "Child task")
	_ = wiCreateCmd.Flags().Set("type", "task")
	_ = wiCreateCmd.Flags().Set("parent", "cb-parent")
	t.Cleanup(func() {
		_ = wiCreateCmd.Flags().Set("title", "")
		_ = wiCreateCmd.Flags().Set("type", "")
		_ = wiCreateCmd.Flags().Set("parent", "")
	})

	if err := wiCreateCmd.RunE(wiCreateCmd, nil); err != nil {
		t.Fatalf("wi create: %v", err)
	}

	if len(testConn.createRequests) != 1 {
		t.Fatalf("created requests = %d, want 1", len(testConn.createRequests))
	}
	got := testConn.createRequests[0].Metadata
	if _, ok := got[domain.MetaRepo]; ok {
		t.Fatalf("child metadata = %#v, did not expect repo", got)
	}
	if got[domain.MetaDispatchRuntime] != domain.RuntimeCodex {
		t.Fatalf("child dispatch_runtime = %v, want %s", got[domain.MetaDispatchRuntime], domain.RuntimeCodex)
	}
	if got[domain.MetaCompletionMode] != "direct" {
		t.Fatalf("child completion_mode = %v, want direct", got[domain.MetaCompletionMode])
	}
}
