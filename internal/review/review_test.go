package review

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

type fakeConnector struct {
	items map[string]*connector.WorkItem
	edges map[string][]connector.Edge
}

func (f *fakeConnector) Name() string { return "fake" }

func (f *fakeConnector) Get(_ context.Context, id string) (*connector.WorkItem, error) {
	item := f.items[id]
	cp := *item
	return &cp, nil
}

func (f *fakeConnector) List(context.Context, connector.ListFilters) (*connector.ListResult, error) {
	panic("not implemented")
}

func (f *fakeConnector) GetEdges(_ context.Context, id string, direction string, _ []string) ([]connector.Edge, error) {
	return append([]connector.Edge(nil), f.edges[id+"|"+direction]...), nil
}

func (f *fakeConnector) GetMetadata(context.Context, string, string) (string, error) {
	panic("not implemented")
}

func (f *fakeConnector) Create(context.Context, connector.CreateRequest) (string, error) {
	panic("not implemented")
}

func (f *fakeConnector) UpdateStatus(context.Context, string, string) error {
	panic("not implemented")
}

func (f *fakeConnector) AppendContent(context.Context, string, string) error {
	panic("not implemented")
}

func (f *fakeConnector) SetMetadata(context.Context, string, string, any) error {
	panic("not implemented")
}

func (f *fakeConnector) UpdateMetadataMap(context.Context, string, map[string]any) error {
	panic("not implemented")
}

func (f *fakeConnector) AddLabel(context.Context, string, string) error {
	panic("not implemented")
}

func (f *fakeConnector) CreateEdge(context.Context, string, string, string) error {
	panic("not implemented")
}

func TestExtractAcceptanceCriteria(t *testing.T) {
	content := strings.TrimSpace(`
# Task

## Scope
Build the thing.

## Acceptance Criteria
- [ ] First criterion
* [x] Second criterion
1. Third criterion

## Notes
Ignore me.
`)

	got := ExtractAcceptanceCriteria(content)
	want := []string{"First criterion", "Second criterion", "Third criterion"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("criteria mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildInputIncludesTaskDesignAndDiff(t *testing.T) {
	conn := &fakeConnector{
		items: map[string]*connector.WorkItem{
			"cb-task": {
				ID:        "cb-task",
				Title:     "Assemble review input",
				Type:      "task",
				Status:    "in_progress",
				Content:   "## Scope\nTask spec body.\n\n## Acceptance Criteria\n- [ ] Include diff\n- [ ] Include design\n",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			"cb-design": {
				ID:        "cb-design",
				Title:     "Built-in review design",
				Type:      "design",
				Status:    "ready",
				Content:   "Parent design context body.",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
		edges: map[string][]connector.Edge{
			"cb-task|outgoing": {
				{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-design"},
			},
		},
	}

	input, err := BuildInput(context.Background(), conn, "cb-task", "diff --git a/x b/x\n+new line")
	if err != nil {
		t.Fatalf("BuildInput error: %v", err)
	}

	if input.TaskSpec != "## Scope\nTask spec body.\n\n## Acceptance Criteria\n- [ ] Include diff\n- [ ] Include design" {
		t.Fatalf("task spec missing from input: %#v", input.TaskSpec)
	}
	if input.ParentDesignContext != "Parent design context body." {
		t.Fatalf("design context missing from input: %#v", input.ParentDesignContext)
	}
	if input.PRDiff != "diff --git a/x b/x\n+new line" {
		t.Fatalf("diff missing from input: %#v", input.PRDiff)
	}
	wantCriteria := []string{"Include diff", "Include design"}
	if !reflect.DeepEqual(input.AcceptanceCriteria, wantCriteria) {
		t.Fatalf("criteria mismatch\n got: %#v\nwant: %#v", input.AcceptanceCriteria, wantCriteria)
	}
}

func TestPromptRequestsStructuredJSON(t *testing.T) {
	prompt := Prompt(ReviewInput{
		TaskID:              "cb-task",
		TaskTitle:           "Review input assembly",
		TaskSpec:            "Task body",
		ParentDesignID:      "cb-design",
		ParentDesignTitle:   "Built-in review design",
		ParentDesignContext: "Design body",
		AcceptanceCriteria:  []string{"Include diff", "Include design"},
		PRDiff:              "diff --git a/x b/x",
	})

	for _, needle := range []string{
		`"verdict":"approve"|"request-changes"`,
		`"findings":[{"file":"path/to/file","line":123,"severity":"critical"|"suggestion"|"nit","body":"issue and fix"}]`,
		`"summary":"short summary"`,
		"## Task",
		"## Acceptance Criteria",
		"## Parent Design",
		"## PR Diff",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt missing %q\n%s", needle, prompt)
		}
	}
}
