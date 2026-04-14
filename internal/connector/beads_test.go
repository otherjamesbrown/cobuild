package connector

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func beadsItemJSON(id string) string {
	return fmt.Sprintf(`{
		"id": %q,
		"title": "Beads task",
		"description": "Plan",
		"notes": "Implementation notes",
		"issue_type": "task",
		"status": "hooked",
		"labels": ["ops"],
		"metadata": {"estimate":2,"owner":"alice"},
		"parent_id": "cb-parent",
		"dependencies": [
			{"id":"dep-1","from_id":%q,"to_id":"cb-456","type":"depends-on"},
			{"id":"dep-2","from_id":"cb-999","to_id":%q,"type":"blocks"}
		],
		"dependents": [
			{"id":"cb-child","title":"Child","status":"open","issue_type":"task","dependency_type":"parent-child"}
		]
	}`, id, id, id)
}

func TestBeadsConnectorReadOperations(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	calls := stubConnectorCommands(t,
		stubCommandResponse{stdout: "[" + beadsItemJSON("cb-123") + "]"},
		stubCommandResponse{stdout: "[" + beadsItemJSON("cb-123") + "]"},
		stubCommandResponse{stdout: "[" + beadsItemJSON("cb-123") + "]"},
		stubCommandResponse{stdout: "[" + beadsItemJSON("cb-123") + "]"},
	)
	conn := NewBeadsConnector("cb", repo, false)

	item, err := conn.Get(ctx, "cb-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.Content != "Plan\n\nImplementation notes" || item.Status != "needs-review" {
		t.Fatalf("unexpected item: %+v", item)
	}

	list, err := conn.List(ctx, ListFilters{Type: "task", Status: "hooked", Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != "cb-123" {
		t.Fatalf("unexpected list: %+v", list)
	}

	edges, err := conn.GetEdges(ctx, "cb-123", "outgoing", []string{"blocked-by"})
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].ItemID != "cb-456" {
		t.Fatalf("unexpected edges: %+v", edges)
	}

	value, err := conn.GetMetadata(ctx, "cb-123", "estimate")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if value != "2" {
		t.Fatalf("metadata = %q, want 2", value)
	}

	assertCommandCalls(t, *calls, []recordedCommand{
		{name: "bd", args: []string{"show", "cb-123", "--json"}},
		{name: "bd", args: []string{"list", "--json", "--type", "task", "--status", "hooked", "--limit", "1"}},
		{name: "bd", args: []string{"show", "cb-123", "--json"}},
		{name: "bd", args: []string{"show", "cb-123", "--json"}},
	})
}

func TestBeadsConnectorWriteOperations(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	calls := stubConnectorCommands(t,
		stubCommandResponse{stdout: `{"id":"cb-777"}`},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
	)
	conn := NewBeadsConnector("cb", repo, false)

	id, err := conn.Create(ctx, CreateRequest{
		Title:    "New bead",
		Content:  "Body",
		Type:     "task",
		Labels:   []string{"urgent", "ops"},
		Metadata: map[string]any{"source": "test"},
		ParentID: "cb-parent",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "cb-777" {
		t.Fatalf("Create returned %q, want cb-777", id)
	}
	if err := conn.UpdateStatus(ctx, "cb-123", "needs-review"); err != nil {
		t.Fatalf("UpdateStatus needs-review: %v", err)
	}
	if err := conn.UpdateStatus(ctx, "cb-123", "closed"); err != nil {
		t.Fatalf("UpdateStatus closed: %v", err)
	}
	if err := conn.UpdateStatus(ctx, "cb-123", "open"); err != nil {
		t.Fatalf("UpdateStatus open: %v", err)
	}
	if err := conn.AppendContent(ctx, "cb-123", "More detail"); err != nil {
		t.Fatalf("AppendContent: %v", err)
	}
	if err := conn.SetMetadata(ctx, "cb-123", "owner", "alice"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if err := conn.AddLabel(ctx, "cb-123", "urgent"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if err := conn.CreateEdge(ctx, "cb-123", "cb-456", "blocked-by"); err != nil {
		t.Fatalf("CreateEdge blocked-by: %v", err)
	}
	if err := conn.CreateEdge(ctx, "cb-123", "cb-456", "relates-to"); err != nil {
		t.Fatalf("CreateEdge relates-to: %v", err)
	}
	if err := conn.CreateEdge(ctx, "cb-123", "cb-parent", "child-of"); err != nil {
		t.Fatalf("CreateEdge child-of: %v", err)
	}

	assertCommandCalls(t, *calls, []recordedCommand{
		{name: "bd", args: []string{"create", "New bead", "--type", "task", "--json", "--description", "Body", "--parent", "cb-parent", "--labels", "urgent,ops", "--metadata", `{"source":"test"}`}},
		{name: "bd", args: []string{"update", "cb-123", "--status", "hooked"}},
		{name: "bd", args: []string{"close", "cb-123"}},
		{name: "bd", args: []string{"reopen", "cb-123"}},
		{name: "bd", args: []string{"update", "cb-123", "--append-notes", "More detail"}},
		{name: "bd", args: []string{"update", "cb-123", "--set-metadata", "owner=alice"}},
		{name: "bd", args: []string{"label", "add", "cb-123", "urgent"}},
		{name: "bd", args: []string{"dep", "add", "cb-123", "--blocked-by", "cb-456"}},
		{name: "bd", args: []string{"relate", "cb-123", "cb-456"}},
		{name: "bd", args: []string{"update", "cb-123", "--parent", "cb-parent"}},
	})
}

func TestBeadsConnectorCommandError(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	stubConnectorCommands(t, stubCommandResponse{stderr: "bd exploded", exitCode: 2})
	conn := NewBeadsConnector("cb", repo, false)

	_, err := conn.Get(ctx, "cb-404")
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	if !strings.Contains(err.Error(), "get cb-404:") || !strings.Contains(err.Error(), "stderr: bd exploded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBeadsConnectorMalformedJSON(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	stubConnectorCommands(t, stubCommandResponse{stdout: "["})
	conn := NewBeadsConnector("cb", repo, false)

	_, err := conn.Get(ctx, "cb-123")
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	if !strings.Contains(err.Error(), "parse beads show array") {
		t.Fatalf("unexpected error: %v", err)
	}
}
