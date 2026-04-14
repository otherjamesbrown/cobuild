package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestCollectSiblingFileOverlapProblems_DetectsOverlap(t *testing.T) {
	ctx := context.Background()
	designID := "cb-design"

	fc := newFakeConnector()
	fc.items[designID] = &connector.WorkItem{ID: designID, Type: "design", Status: "in_progress"}
	fc.items["cb-a"] = &connector.WorkItem{
		ID: "cb-a", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"internal/cmd/dispatch.go", "internal/config/config.go"}},
	}
	fc.items["cb-b"] = &connector.WorkItem{
		ID: "cb-b", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"internal/cmd/dispatch.go", "skills/x.md"}},
	}
	fc.items["cb-c"] = &connector.WorkItem{
		ID: "cb-c", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"docs/README.md"}},
	}
	fc.parent["cb-a"] = designID
	fc.parent["cb-b"] = designID
	fc.parent["cb-c"] = designID

	problems, err := collectSiblingFileOverlapProblems(ctx, fc, designID)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 {
		t.Fatalf("want 1 overlap, got %d: %+v", len(problems), problems)
	}
	p := problems[0]
	if p.TaskA != "cb-a" || p.TaskB != "cb-b" {
		t.Fatalf("overlap pair = (%s, %s), want (cb-a, cb-b)", p.TaskA, p.TaskB)
	}
	if len(p.Paths) != 1 || p.Paths[0] != "internal/cmd/dispatch.go" {
		t.Fatalf("overlap paths = %v, want [internal/cmd/dispatch.go]", p.Paths)
	}
}

func TestCollectSiblingFileOverlapProblems_NoOverlap(t *testing.T) {
	ctx := context.Background()
	designID := "cb-clean-design"

	fc := newFakeConnector()
	fc.items[designID] = &connector.WorkItem{ID: designID, Type: "design", Status: "in_progress"}
	fc.items["cb-a"] = &connector.WorkItem{
		ID: "cb-a", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"x.go"}},
	}
	fc.items["cb-b"] = &connector.WorkItem{
		ID: "cb-b", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"y.go"}},
	}
	fc.parent["cb-a"] = designID
	fc.parent["cb-b"] = designID

	problems, err := collectSiblingFileOverlapProblems(ctx, fc, designID)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("want no overlap, got %+v", problems)
	}
}

func TestCollectSiblingFileOverlapProblems_IgnoresClosedTasks(t *testing.T) {
	ctx := context.Background()
	designID := "cb-closed-design"

	fc := newFakeConnector()
	fc.items[designID] = &connector.WorkItem{ID: designID, Type: "design", Status: "in_progress"}
	fc.items["cb-closed"] = &connector.WorkItem{
		ID: "cb-closed", Type: "task", Status: "closed",
		Metadata: map[string]any{"files": []string{"shared.go"}},
	}
	fc.items["cb-open"] = &connector.WorkItem{
		ID: "cb-open", Type: "task", Status: "open",
		Metadata: map[string]any{"files": []string{"shared.go"}},
	}
	fc.parent["cb-closed"] = designID
	fc.parent["cb-open"] = designID

	problems, err := collectSiblingFileOverlapProblems(ctx, fc, designID)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("closed tasks should not participate; got %+v", problems)
	}
}

func TestRenderSiblingFileOverlapError(t *testing.T) {
	if err := renderSiblingFileOverlapError(nil); err != nil {
		t.Fatalf("empty list should return nil, got %v", err)
	}
	err := renderSiblingFileOverlapError([]siblingFileOverlap{
		{TaskA: "cb-a", TaskB: "cb-b", Paths: []string{"f.go"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cb-a ↔ cb-b") {
		t.Fatalf("error did not mention both tasks: %v", err)
	}
	if !strings.Contains(err.Error(), "cb-7cda32") {
		t.Fatalf("error should cite shard: %v", err)
	}
}
