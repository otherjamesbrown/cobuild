package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestInspectCmd_FullOutput(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	// Set up shard
	fc.addItem(&connector.WorkItem{
		ID:     "cb-abc123",
		Title:  "Implement the widget",
		Type:   "task",
		Status: "open",
	})
	// Parent edge
	fc.edges["cb-abc123"] = map[string][]connector.Edge{
		"outgoing": {
			{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-design1", Type: "design", Status: "open"},
		},
	}

	// Pipeline run
	fs.runs["cb-abc123"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-abc123",
		CurrentPhase: "implement",
		Status:       "active",
	}

	// Sessions
	now := time.Now()
	model := "sonnet"
	endedAt := now.Add(-5 * time.Minute)
	fs.sessions = []store.SessionRecord{
		{
			ID:        "ps-old",
			DesignID:  "cb-abc123",
			StartedAt: now.Add(-20 * time.Minute),
			EndedAt:   timePtr(now.Add(-15 * time.Minute)),
			Runtime:   "codex",
			Model:     strPtr("gpt-5.4"),
		},
		{
			ID:        "ps-mid",
			DesignID:  "cb-abc123",
			StartedAt: now.Add(-10 * time.Minute),
			EndedAt:   &endedAt,
			Runtime:   "claude",
			Model:     &model,
		},
		{
			ID:        "ps-cur",
			DesignID:  "cb-abc123",
			StartedAt: now.Add(-2 * time.Minute),
			Runtime:   "claude",
			Model:     strPtr("sonnet"),
		},
	}

	// Gate history
	fs.gateHistory = []store.PipelineGateRecord{
		{
			ID:            "g1",
			DesignID:      "cb-abc123",
			GateName:      "review",
			Round:         1,
			Verdict:       "pass",
			ReviewShardID: strPtr("cb-367045"),
			CreatedAt:     now.Add(-8 * time.Minute),
		},
		{
			ID:           "g2",
			DesignID:     "cb-abc123",
			GateName:     "review",
			Round:        2,
			Verdict:      "fail",
			ReviewShardID: strPtr("cb-e43018"),
			FindingsHash: strPtr("a1b2c3d4e5f6"),
			CreatedAt:    now.Add(-6 * time.Minute),
		},
	}

	// PR metadata
	fc.SetMetadata(context.Background(), "cb-abc123", "pr_url", "https://github.com/org/repo/pull/80")

	// Stub gh command
	prevExec := execCommandOutput
	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "gh" {
			return []byte(`{"state":"OPEN","mergeable":"MERGEABLE","statusCheckRollup":[{"state":"PENDING","status":"IN_PROGRESS","conclusion":""}]}`), nil
		}
		return nil, fmt.Errorf("unexpected command %s", name)
	}
	t.Cleanup(func() { execCommandOutput = prevExec })

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	// Capture output
	var buf bytes.Buffer
	inspectCmd.SetOut(&buf)
	inspectCmd.SetErr(&buf)

	if err := inspectCmd.RunE(inspectCmd, []string{"cb-abc123"}); err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	// The command prints to stdout via fmt.Print*, not cmd.OutOrStdout(),
	// so output capture via buf won't show content. We verify structure
	// and correctness via gatherInspectData in the test below.
	_ = buf.String()
}

func TestInspectCmd_GatherData(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-test1",
		Title:  "Test shard",
		Type:   "task",
		Status: "in_progress",
	})
	fc.edges["cb-test1"] = map[string][]connector.Edge{
		"outgoing": {
			{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-parent", Type: "design", Status: "open"},
		},
	}

	fs.runs["cb-test1"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-test1",
		CurrentPhase: "review",
		Status:       "active",
	}

	now := time.Now()
	fs.sessions = []store.SessionRecord{
		{
			ID:        "ps-1",
			DesignID:  "cb-test1",
			StartedAt: now.Add(-5 * time.Minute),
			Runtime:   "claude",
			Model:     strPtr("sonnet"),
		},
	}

	fs.gateHistory = []store.PipelineGateRecord{
		{
			ID:       "g1",
			DesignID: "cb-test1",
			GateName: "review",
			Round:    1,
			Verdict:  "pass",
			CreatedAt: now.Add(-3 * time.Minute),
		},
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	data, err := gatherInspectData(context.Background(), "cb-test1")
	if err != nil {
		t.Fatalf("gatherInspectData error: %v", err)
	}

	// Shard section
	if data.Shard.ID != "cb-test1" {
		t.Errorf("shard ID = %q, want cb-test1", data.Shard.ID)
	}
	if data.Shard.Type != "task" {
		t.Errorf("shard type = %q, want task", data.Shard.Type)
	}
	if data.Shard.Status != "in_progress" {
		t.Errorf("shard status = %q, want in_progress", data.Shard.Status)
	}
	if data.Shard.Parent == nil {
		t.Fatal("shard parent is nil, want cb-parent")
	}
	if data.Shard.Parent.ID != "cb-parent" {
		t.Errorf("parent ID = %q, want cb-parent", data.Shard.Parent.ID)
	}

	// Pipeline section
	if data.Pipeline == nil {
		t.Fatal("pipeline is nil")
	}
	if data.Pipeline.Phase != "review" {
		t.Errorf("pipeline phase = %q, want review", data.Pipeline.Phase)
	}
	if data.Pipeline.Status != "active" {
		t.Errorf("pipeline status = %q, want active", data.Pipeline.Status)
	}

	// Sessions section
	if len(data.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(data.Sessions))
	}
	if data.Sessions[0].Runtime != "claude" {
		t.Errorf("session runtime = %q, want claude", data.Sessions[0].Runtime)
	}
	if !data.Sessions[0].Running {
		t.Error("session should be running (no EndedAt)")
	}

	// Gates section
	if len(data.Gates) != 1 {
		t.Fatalf("gates count = %d, want 1", len(data.Gates))
	}
	if data.Gates[0].Verdict != "PASS" {
		t.Errorf("gate verdict = %q, want PASS", data.Gates[0].Verdict)
	}
}

func TestInspectCmd_NoPipeline(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-nopipe",
		Title:  "No pipeline here",
		Type:   "design",
		Status: "open",
	})

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	data, err := gatherInspectData(context.Background(), "cb-nopipe")
	if err != nil {
		t.Fatalf("gatherInspectData error: %v", err)
	}

	if data.Pipeline != nil {
		t.Error("expected nil pipeline for shard with no run")
	}
	if len(data.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(data.Sessions))
	}
	if len(data.Gates) != 0 {
		t.Errorf("expected 0 gates, got %d", len(data.Gates))
	}
}

func TestInspectCmd_SessionsTrimToThree(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-many",
		Title:  "Many sessions",
		Type:   "task",
		Status: "open",
	})
	fs.runs["cb-many"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-many",
		CurrentPhase: "implement",
		Status:       "active",
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		ended := now.Add(time.Duration(-5+i) * time.Minute)
		fs.sessions = append(fs.sessions, store.SessionRecord{
			ID:        fmt.Sprintf("ps-%d", i),
			DesignID:  "cb-many",
			StartedAt: now.Add(time.Duration(-10+i) * time.Minute),
			EndedAt:   &ended,
			Runtime:   "claude",
			Model:     strPtr("sonnet"),
		})
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	data, err := gatherInspectData(context.Background(), "cb-many")
	if err != nil {
		t.Fatalf("gatherInspectData error: %v", err)
	}

	if len(data.Sessions) != 3 {
		t.Errorf("sessions count = %d, want 3", len(data.Sessions))
	}
	// Should be the last 3 (ps-2, ps-3, ps-4)
	if data.Sessions[0].ID != "ps-2" {
		t.Errorf("first session = %q, want ps-2", data.Sessions[0].ID)
	}
}

func TestInspectCmd_GatesTrimToFive(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-gates",
		Title:  "Many gates",
		Type:   "task",
		Status: "open",
	})
	fs.runs["cb-gates"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-gates",
		CurrentPhase: "review",
		Status:       "active",
	}

	now := time.Now()
	for i := 0; i < 8; i++ {
		fs.gateHistory = append(fs.gateHistory, store.PipelineGateRecord{
			ID:        fmt.Sprintf("g%d", i),
			DesignID:  "cb-gates",
			GateName:  "review",
			Round:     i + 1,
			Verdict:   "fail",
			CreatedAt: now.Add(time.Duration(-8+i) * time.Minute),
		})
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	data, err := gatherInspectData(context.Background(), "cb-gates")
	if err != nil {
		t.Fatalf("gatherInspectData error: %v", err)
	}

	if len(data.Gates) != 5 {
		t.Errorf("gates count = %d, want 5", len(data.Gates))
	}
}

func TestInspectCmd_JSONOutput(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-json",
		Title:  "JSON test",
		Type:   "bug",
		Status: "open",
	})
	fs.runs["cb-json"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-json",
		CurrentPhase: "fix",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevFormat := outputFormat
	outputFormat = "json"
	t.Cleanup(func() { outputFormat = prevFormat })

	if err := inspectCmd.RunE(inspectCmd, []string{"cb-json"}); err != nil {
		t.Fatalf("inspect --json returned error: %v", err)
	}
}

func TestInspectCmd_MissingShard(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	err := inspectCmd.RunE(inspectCmd, []string{"cb-missing"})
	if err == nil {
		t.Fatal("expected error for missing shard")
	}
	if !strings.Contains(err.Error(), "cb-missing") {
		t.Errorf("error %q should mention cb-missing", err.Error())
	}
}

func TestInspectCmd_TextOutputSections(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()

	fc.addItem(&connector.WorkItem{
		ID:     "cb-text",
		Title:  "Text output test",
		Type:   "task",
		Status: "open",
	})
	fs.runs["cb-text"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-text",
		CurrentPhase: "implement",
		Status:       "active",
	}

	now := time.Now()
	fs.sessions = []store.SessionRecord{
		{
			ID:        "ps-1",
			DesignID:  "cb-text",
			StartedAt: now.Add(-5 * time.Minute),
			Runtime:   "claude",
			Model:     strPtr("sonnet"),
		},
	}
	fs.gateHistory = []store.PipelineGateRecord{
		{
			ID:        "g1",
			DesignID:  "cb-text",
			GateName:  "review",
			Round:     1,
			Verdict:   "pass",
			CreatedAt: now.Add(-3 * time.Minute),
		},
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	// Gather and print to verify no panics and correct structure
	data, err := gatherInspectData(context.Background(), "cb-text")
	if err != nil {
		t.Fatalf("gatherInspectData error: %v", err)
	}

	// Verify printInspectText doesn't panic
	printInspectText(data)
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/org/repo/pull/80", "80"},
		{"https://github.com/org/repo/pull/123", "123"},
		{"https://github.com/org/repo/issues/42", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractPRNumber(tt.url)
		if got != tt.want {
			t.Errorf("extractPRNumber(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestFormatDuration_Inspect(t *testing.T) {
	// formatDuration lives in review.go; verify it works for inspect's use cases.
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{10 * time.Minute, "10m"},
		{1*time.Hour + 2*time.Minute, "1h2m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestSummarizeCIStatus(t *testing.T) {
	type ciCheck = struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	tests := []struct {
		name   string
		checks []ciCheck
		want   string
	}{
		{
			"all passing",
			[]ciCheck{{State: "SUCCESS", Conclusion: "SUCCESS"}},
			"passing",
		},
		{
			"pending",
			[]ciCheck{{State: "PENDING", Status: "IN_PROGRESS", Conclusion: ""}},
			"pending",
		},
		{
			"failing",
			[]ciCheck{{State: "FAILURE", Conclusion: "FAILURE"}},
			"failing",
		},
		{
			"mixed with failure",
			[]ciCheck{
				{State: "SUCCESS", Conclusion: "SUCCESS"},
				{State: "FAILURE", Conclusion: "FAILURE"},
			},
			"failing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeCIStatus(tt.checks)
			if got != tt.want {
				t.Errorf("summarizeCIStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// strPtr and timePtr are defined in review_test.go and stale_session_flow_test.go respectively.
