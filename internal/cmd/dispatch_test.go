package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestWritePhasePromptDecomposeMentionsCompletionModeDirect(t *testing.T) {
	var b strings.Builder
	writePhasePrompt(&b, "decompose", "cb-parent", "cb-parent", nil)
	got := b.String()

	for _, want := range []string{
		"set `completion_mode: direct` only for non-code tasks",
		"leave it unset and let `cobuild complete` auto-detect",
		"`cxp shard metadata set <task-id> completion_mode direct`",
		"tasks tagged `completion_mode: direct`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decompose prompt missing %q\nprompt:\n%s", want, got)
		}
	}
}

func TestHasInvestigationContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty body",
			content: "",
			want:    false,
		},
		{
			name:    "plain bug report, no investigation",
			content: "## Description\n\nServer crashes on startup.\n\n## Steps to Reproduce\n\n1. Run server\n2. Observe crash",
			want:    false,
		},
		{
			name:    "has investigation report heading",
			content: "## Description\n\nServer crashes.\n\n## Investigation Report\n\nFound null pointer in auth middleware.",
			want:    true,
		},
		{
			name:    "has root cause heading",
			content: "## Description\n\nServer crashes.\n\n## Root Cause\n\nMissing nil check in auth.go:42.",
			want:    true,
		},
		{
			name:    "has fix applied heading",
			content: "## Description\n\nServer crashes.\n\n## Fix Applied\n\nAdded nil check.",
			want:    true,
		},
		{
			name:    "has fix heading",
			content: "## Description\n\nServer crashes.\n\n## Fix\n\n- [ ] Add nil check in auth.go",
			want:    true,
		},
		{
			name:    "case insensitive - uppercase",
			content: "## INVESTIGATION REPORT\n\nFound the issue.",
			want:    true,
		},
		{
			name:    "case insensitive - mixed",
			content: "## Root Cause\n\nThe bug is here.",
			want:    true,
		},
		{
			name:    "heading in prose, not heading level",
			content: "The investigation report showed nothing useful here.",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasInvestigationContent(tt.content)
			if got != tt.want {
				t.Errorf("hasInvestigationContent(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestInvestigationContentDowngrade(t *testing.T) {
	// These test the 4 combinations of label × investigation content.
	// The dispatch logic is: if label=needs-investigation → investigate, else → fix.
	// Then: if phase=investigate AND hasInvestigationContent → downgrade to fix.

	type input struct {
		hasNeedsInvestigationLabel bool
		hasInvestigationBody       bool
	}
	type want struct {
		phase string
	}

	tests := []struct {
		name  string
		input input
		want  want
	}{
		{
			name:  "label=false, investigation=false → fix (normal bug)",
			input: input{false, false},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=false → investigate (escalation path)",
			input: input{true, false},
			want:  want{"investigate"},
		},
		{
			name:  "label=false, investigation=true → fix (already investigated, default path)",
			input: input{false, true},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=true → fix (downgrade: body overrides label)",
			input: input{true, true},
			want:  want{"fix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the dispatch.go phase inference + downgrade logic
			labels := []string{}
			if tt.input.hasNeedsInvestigationLabel {
				labels = []string{"needs-investigation"}
			}

			content := "## Description\n\nSome bug."
			if tt.input.hasInvestigationBody {
				content += "\n\n## Investigation Report\n\nFound the root cause."
			}

			// Phase inference (mirrors dispatch.go fallback logic)
			phase := "fix"
			if hasLabel(labels, "needs-investigation") {
				phase = "investigate"
			}

			// Downgrade (mirrors dispatch.go post-inference check)
			if phase == "investigate" && hasInvestigationContent(content) {
				phase = "fix"
			}

			if phase != tt.want.phase {
				t.Errorf("phase = %q, want %q", phase, tt.want.phase)
			}
		})
	}
}

func TestDispatchWaveSerialOnlyDispatchesLowestEligibleWave(t *testing.T) {
	testDir := setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: serial\n  max_concurrent: 3\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 2}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "[dry-run] task-1")
	assertContains(t, output, "[dry-run] task-2")
	assertNotContains(t, output, "[dry-run] task-3")
	_ = testDir
}

func TestDispatchWaveParallelKeepsMultiWaveDispatch(t *testing.T) {
	setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: parallel\n  max_concurrent: 3\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 2}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 3}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "[dry-run] task-1")
	assertContains(t, output, "[dry-run] task-2")
	assertContains(t, output, "[dry-run] task-3")
}

func TestDispatchWaveAppliesConcurrencyAfterWaveSelection(t *testing.T) {
	setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: serial\n  max_concurrent: 1\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 2}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "Dispatching 1 tasks")
	assertContains(t, output, "[dry-run] task-1")
	assertNotContains(t, output, "[dry-run] task-2")
	assertNotContains(t, output, "[dry-run] task-3")
}

type dispatchWaveTestConnector struct {
	items       map[string]*connector.WorkItem
	edgesByItem map[string][]connector.Edge
}

func newDispatchWaveTestConnector(items ...*connector.WorkItem) *dispatchWaveTestConnector {
	index := make(map[string]*connector.WorkItem, len(items))
	for _, item := range items {
		index[item.ID] = item
	}
	return &dispatchWaveTestConnector{
		items:       index,
		edgesByItem: make(map[string][]connector.Edge),
	}
}

func (c *dispatchWaveTestConnector) Name() string { return "test" }

func (c *dispatchWaveTestConnector) Get(ctx context.Context, id string) (*connector.WorkItem, error) {
	item, ok := c.items[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return item, nil
}

func (c *dispatchWaveTestConnector) List(ctx context.Context, filters connector.ListFilters) (*connector.ListResult, error) {
	return nil, nil
}

func (c *dispatchWaveTestConnector) GetEdges(ctx context.Context, id string, direction string, types []string) ([]connector.Edge, error) {
	edges := c.edgesByItem[id]
	if len(edges) == 0 {
		return nil, nil
	}
	filtered := make([]connector.Edge, 0, len(edges))
	for _, edge := range edges {
		if direction != "" && edge.Direction != direction {
			continue
		}
		if len(types) > 0 && !containsString(types, edge.EdgeType) {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered, nil
}

func (c *dispatchWaveTestConnector) GetMetadata(ctx context.Context, id string, key string) (string, error) {
	return "", nil
}

func (c *dispatchWaveTestConnector) Create(ctx context.Context, req connector.CreateRequest) (string, error) {
	return "", nil
}

func (c *dispatchWaveTestConnector) UpdateStatus(ctx context.Context, id string, status string) error {
	return nil
}

func (c *dispatchWaveTestConnector) AppendContent(ctx context.Context, id string, content string) error {
	return nil
}

func (c *dispatchWaveTestConnector) SetMetadata(ctx context.Context, id string, key string, value any) error {
	return nil
}

func (c *dispatchWaveTestConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	return nil
}

func (c *dispatchWaveTestConnector) AddLabel(ctx context.Context, id string, label string) error {
	return nil
}

func (c *dispatchWaveTestConnector) CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error {
	return nil
}

func dispatchWaveTestItem(id, itemType, status string, metadata map[string]any) *connector.WorkItem {
	return &connector.WorkItem{
		ID:       id,
		Type:     itemType,
		Status:   status,
		Metadata: metadata,
	}
}

func setupDispatchWaveTestRepo(t *testing.T, pipelineConfig string) string {
	t.Helper()

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir repo config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".cobuild.yaml"), []byte("project: cb-test\nprefix: cb-\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".cobuild", "pipeline.yaml"), []byte(pipelineConfig), 0o644); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}
	if err := exec.Command("git", "init", "-q", tempDir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	prevHome := os.Getenv("HOME")
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	dispatchWaveCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchWaveCmd.Flags().Set("dry-run", "false")
		_ = os.Chdir(prevWD)
		_ = os.Setenv("HOME", prevHome)
	})

	return tempDir
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = origStdout
	return <-done
}

func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, output)
	}
}

func assertNotContains(t *testing.T, output, want string) {
	t.Helper()
	if strings.Contains(output, want) {
		t.Fatalf("expected output not to contain %q, got:\n%s", want, output)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
