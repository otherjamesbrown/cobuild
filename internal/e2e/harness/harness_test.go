package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestSetupProvidesIsolatedHarnessState(t *testing.T) {
	ctx := context.Background()
	h1 := Setup(t, Options{Project: "alpha"})
	h2 := Setup(t, Options{Project: "beta"})
	defer func() {
		_ = h2.Teardown()
		_ = h1.Teardown()
	}()

	if h1.Repo.Root == h2.Repo.Root {
		t.Fatal("repo roots should be distinct")
	}
	if h1.Tmux.SocketPath == h2.Tmux.SocketPath {
		t.Fatal("tmux sockets should be distinct")
	}
	if h1.Schema == h2.Schema {
		t.Fatal("schemas should be distinct")
	}
	if h1.HomeDir == h2.HomeDir {
		t.Fatal("home dirs should be distinct")
	}

	alphaID := mustCreateItem(t, h1, connector.CreateRequest{
		Title:   "Alpha design",
		Type:    "design",
		Content: "alpha",
	})
	if _, err := h2.Connector.Get(ctx, alphaID); err == nil {
		t.Fatal("connector state leaked between harness instances")
	}

	run1, err := h1.Store.CreateRun(ctx, "cb-alpha", h1.Project, "design")
	if err != nil {
		t.Fatalf("alpha CreateRun() error = %v", err)
	}
	if _, err := h2.Store.GetRun(ctx, "cb-alpha"); err == nil {
		t.Fatal("store state leaked between harness instances")
	}
	if _, err := h2.Store.CreateRun(ctx, "cb-alpha", h2.Project, "design"); err != nil {
		t.Fatalf("beta CreateRun() error = %v", err)
	}
	if run1.Project != "alpha" {
		t.Fatalf("alpha run project = %q, want alpha", run1.Project)
	}

	if err := os.WriteFile(filepath.Join(h1.Repo.Root, "alpha.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write alpha repo file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h2.Repo.Root, "alpha.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected alpha repo file to stay isolated, stat err = %v", err)
	}

	if err := h1.Tmux.Run(ctx, "new-window", "-t", h1.Tmux.SessionName, "-n", "alpha-window"); err != nil {
		t.Fatalf("alpha new-window error = %v", err)
	}
	windows1, err := h1.Tmux.ListWindows(ctx)
	if err != nil {
		t.Fatalf("alpha ListWindows() error = %v", err)
	}
	windows2, err := h2.Tmux.ListWindows(ctx)
	if err != nil {
		t.Fatalf("beta ListWindows() error = %v", err)
	}
	if !slices.Contains(windows1, "alpha-window") {
		t.Fatalf("alpha tmux windows = %v, want alpha-window", windows1)
	}
	if slices.Contains(windows2, "alpha-window") {
		t.Fatalf("beta tmux windows = %v, should not include alpha-window", windows2)
	}
}

func TestSetupConfiguresStubRuntimeFixtures(t *testing.T) {
	h := Setup(t, Options{
		Project:         "fixtures",
		Runtime:         "stub",
		StubFixturesDir: filepath.Join("..", "testdata", "runtime", "stub"),
	})
	defer func() { _ = h.Teardown() }()

	if h.Runtime != "stub" {
		t.Fatalf("runtime = %q, want stub", h.Runtime)
	}
	if h.Config.Dispatch.DefaultRuntime != "stub" {
		t.Fatalf("default runtime = %q, want stub", h.Config.Dispatch.DefaultRuntime)
	}
	if got := h.StubFixturesDir; got != filepath.Join("..", "testdata", "runtime", "stub") {
		t.Fatalf("fixtures dir = %q", got)
	}
	env := h.Env()
	if !slices.Contains(env, "COBUILD_STUB_FIXTURES_DIR="+h.StubFixturesDir) {
		t.Fatalf("env missing stub fixtures dir: %v", env)
	}
	if _, err := os.Stat(filepath.Join(h.Repo.Root, ".cobuild", "pipeline.yaml")); err != nil {
		t.Fatalf("pipeline config missing: %v", err)
	}
}

func TestFakeConnectorSupportsDispatchFlowOperations(t *testing.T) {
	ctx := context.Background()
	fc := NewFakeConnector(FakeConnectorOptions{IDPrefix: "cb-test"})
	fc.AddItem(connector.WorkItem{ID: "cb-design", Title: "Design", Type: "design", Status: "open", Project: "demo"})

	taskID := mustCreateItem(t, &Harness{Connector: fc}, connector.CreateRequest{
		Title:    "Implement harness",
		Type:     "task",
		Content:  "task body",
		ParentID: "cb-design",
		Metadata: map[string]any{"repo": "demo"},
	})
	if err := fc.SetMetadata(ctx, taskID, "worktree_path", "/tmp/worktree"); err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}
	if err := fc.UpdateMetadataMap(ctx, taskID, map[string]any{"session_id": "ps-1", "pr_url": "https://example/pr"}); err != nil {
		t.Fatalf("UpdateMetadataMap() error = %v", err)
	}
	if err := fc.AppendContent(ctx, taskID, "\nmore"); err != nil {
		t.Fatalf("AppendContent() error = %v", err)
	}
	if err := fc.AddLabel(ctx, taskID, "wave-2"); err != nil {
		t.Fatalf("AddLabel() error = %v", err)
	}
	if err := fc.UpdateStatus(ctx, taskID, "needs-review"); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	item, err := fc.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if item.Status != "needs-review" {
		t.Fatalf("status = %q, want needs-review", item.Status)
	}
	if item.Content != "task body\nmore" {
		t.Fatalf("content = %q", item.Content)
	}

	edges, err := fc.GetEdges(ctx, "cb-design", "incoming", []string{"child-of"})
	if err != nil {
		t.Fatalf("GetEdges() error = %v", err)
	}
	if len(edges) != 1 || edges[0].ItemID != taskID || edges[0].Status != "needs-review" {
		t.Fatalf("incoming edges = %#v", edges)
	}

	listed, err := fc.List(ctx, connector.ListFilters{Type: "task", Status: "needs-review", Project: "demo"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != taskID {
		t.Fatalf("List() = %#v", listed)
	}

	if got, err := fc.GetMetadata(ctx, taskID, "pr_url"); err != nil || got != "https://example/pr" {
		t.Fatalf("GetMetadata(pr_url) = %q, %v", got, err)
	}
}

func mustCreateItem(t *testing.T, h *Harness, req connector.CreateRequest) string {
	t.Helper()
	id, err := h.Connector.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return id
}
