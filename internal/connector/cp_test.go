package connector

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type stubCommandResponse struct {
	stdout   string
	stderr   string
	exitCode int
}

type recordedCommand struct {
	name string
	args []string
}

func stubConnectorCommands(t *testing.T, responses ...stubCommandResponse) *[]recordedCommand {
	t.Helper()

	old := connectorCommandContext
	calls := make([]recordedCommand, 0, len(responses))
	index := 0
	connectorCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if index >= len(responses) {
			t.Fatalf("unexpected command: %s %s", name, strings.Join(args, " "))
		}

		calls = append(calls, recordedCommand{
			name: name,
			args: append([]string(nil), args...),
		})
		resp := responses[index]
		index++

		cmdArgs := []string{"-test.run=TestConnectorCommandHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_CONNECTOR_COMMAND_HELPER=1",
			"CONNECTOR_HELPER_STDOUT="+base64.StdEncoding.EncodeToString([]byte(resp.stdout)),
			"CONNECTOR_HELPER_STDERR="+base64.StdEncoding.EncodeToString([]byte(resp.stderr)),
			"CONNECTOR_HELPER_EXIT_CODE="+strconv.Itoa(resp.exitCode),
		)
		return cmd
	}

	t.Cleanup(func() {
		connectorCommandContext = old
		if index != len(responses) {
			t.Errorf("consumed %d/%d stubbed responses", index, len(responses))
		}
	})

	return &calls
}

func TestConnectorCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CONNECTOR_COMMAND_HELPER") != "1" {
		return
	}

	stdout, err := base64.StdEncoding.DecodeString(os.Getenv("CONNECTOR_HELPER_STDOUT"))
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
	stderr, err := base64.StdEncoding.DecodeString(os.Getenv("CONNECTOR_HELPER_STDERR"))
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
	exitCode, err := strconv.Atoi(os.Getenv("CONNECTOR_HELPER_EXIT_CODE"))
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}

	_, _ = os.Stdout.Write(stdout)
	_, _ = os.Stderr.Write(stderr)
	os.Exit(exitCode)
}

func assertCommandCalls(t *testing.T, got []recordedCommand, want []recordedCommand) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command calls mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func cpItemJSON(id string) string {
	return fmt.Sprintf(`{
		"id": %q,
		"title": "Connector coverage",
		"content": "Body",
		"type": "task",
		"status": "open",
		"project": "cobuild",
		"creator": "alice",
		"metadata": {"owner":"alice"},
		"edges": [{"direction":"outgoing","edge_type":"blocked-by","shard_id":"cb-456","title":"Dependency","type":"task","status":"open"}]
	}`, id)
}

func TestCPConnectorReadOperations(t *testing.T) {
	ctx := context.Background()
	calls := stubConnectorCommands(t,
		stubCommandResponse{stdout: cpItemJSON("cb-123")},
		stubCommandResponse{stdout: fmt.Sprintf(`{"results":[%s],"total":1}`, cpItemJSON("cb-123"))},
		stubCommandResponse{stdout: `[{"direction":"outgoing","edge_type":"blocked-by","shard_id":"cb-456"}]`},
		stubCommandResponse{stdout: `"alice"`},
	)
	conn := NewCPConnector("cobuild", "agent-smith", false)

	item, err := conn.Get(ctx, "cb-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.ID != "cb-123" || item.Metadata["owner"] != "alice" {
		t.Fatalf("unexpected item: %+v", item)
	}

	list, err := conn.List(ctx, ListFilters{Type: "task", Status: "open", Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != "cb-123" {
		t.Fatalf("unexpected list: %+v", list)
	}

	edges, err := conn.GetEdges(ctx, "cb-123", "outgoing", []string{"blocked-by", "relates-to"})
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].ItemID != "cb-456" {
		t.Fatalf("unexpected edges: %+v", edges)
	}

	value, err := conn.GetMetadata(ctx, "cb-123", "owner")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if value != "alice" {
		t.Fatalf("metadata = %q, want alice", value)
	}

	assertCommandCalls(t, *calls, []recordedCommand{
		{name: "cxp", args: []string{"shard", "show", "cb-123", "-o", "json", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "list", "-o", "json", "--type", "task", "--status", "open", "--limit", "2", "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "edges", "cb-123", "-o", "json", "--direction", "outgoing", "--edge-type", "blocked-by,relates-to", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "metadata", "get", "cb-123", "owner", "--agent", "agent-smith"}},
	})
}

func TestCPConnectorWriteOperations(t *testing.T) {
	ctx := context.Background()
	calls := stubConnectorCommands(t,
		stubCommandResponse{stdout: `{"id":"cb-777"}`},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
		stubCommandResponse{},
	)
	conn := NewCPConnector("cobuild", "agent-smith", false)

	id, err := conn.Create(ctx, CreateRequest{
		Title:    "New shard",
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
		t.Fatalf("UpdateStatus: %v", err)
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
		t.Fatalf("CreateEdge: %v", err)
	}

	assertCommandCalls(t, *calls, []recordedCommand{
		{name: "cxp", args: []string{"shard", "create", "--type", "task", "--title", "New shard", "-o", "json", "--body", "Body", "--parent", "cb-parent", "--label", "urgent,ops", "--meta", `{"source":"test"}`, "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "status", "cb-123", "needs-review", "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "append", "cb-123", "--body", "More detail", "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "metadata", "set", "cb-123", "owner", "alice", "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "label", "add", "cb-123", "urgent", "--project", "cobuild", "--agent", "agent-smith"}},
		{name: "cxp", args: []string{"shard", "link", "cb-123", "--blocked-by", "cb-456", "--project", "cobuild", "--agent", "agent-smith"}},
	})
}

func TestCPConnectorCommandError(t *testing.T) {
	ctx := context.Background()
	stubConnectorCommands(t, stubCommandResponse{stderr: "cxp exploded", exitCode: 2})
	conn := NewCPConnector("cobuild", "agent-smith", false)

	_, err := conn.Get(ctx, "cb-404")
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	if !strings.Contains(err.Error(), "get cb-404:") || !strings.Contains(err.Error(), "stderr: cxp exploded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCPConnectorMalformedJSON(t *testing.T) {
	ctx := context.Background()
	stubConnectorCommands(t, stubCommandResponse{stdout: "not-json"})
	conn := NewCPConnector("cobuild", "agent-smith", false)

	_, err := conn.Get(ctx, "cb-123")
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	if !strings.Contains(err.Error(), "parse work item") {
		t.Fatalf("unexpected error: %v", err)
	}
}
