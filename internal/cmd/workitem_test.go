package cmd

import (
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
)

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
