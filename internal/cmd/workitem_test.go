package cmd

import (
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestWICreateInheritsParentRepoMetadata(t *testing.T) {
	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{
		ID:       "cb-parent",
		Title:    "parent",
		Type:     "design",
		Status:   "open",
		Metadata: map[string]any{"repo": "context-palace"},
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

	got := testConn.createRequests[0].Metadata["repo"]
	if got != "context-palace" {
		t.Fatalf("child repo metadata = %v, want context-palace", got)
	}
}

func TestWICreateSkipsAmbiguousParentRepoMetadata(t *testing.T) {
	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{
		ID:       "cb-parent",
		Title:    "parent",
		Type:     "design",
		Status:   "open",
		Metadata: map[string]any{"repo": "context-palace, penfold"},
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
	if testConn.createRequests[0].Metadata != nil {
		t.Fatalf("child metadata = %#v, want nil", testConn.createRequests[0].Metadata)
	}
}
