package state

import (
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestResolveBootstrapDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	tests := []struct {
		name           string
		item           *connector.WorkItem
		wantWorkflow   string
		wantStartPhase string
	}{
		{
			name:           "design",
			item:           &connector.WorkItem{ID: "cb-design", Type: "design"},
			wantWorkflow:   "design",
			wantStartPhase: "design",
		},
		{
			name:           "task",
			item:           &connector.WorkItem{ID: "cb-task", Type: "task"},
			wantWorkflow:   "task",
			wantStartPhase: "implement",
		},
		{
			name:           "bug",
			item:           &connector.WorkItem{ID: "cb-bug", Type: "bug"},
			wantWorkflow:   "bug",
			wantStartPhase: "fix",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveBootstrap(test.item, cfg)
			if err != nil {
				t.Fatalf("ResolveBootstrap() error = %v", err)
			}
			if got.Workflow != test.wantWorkflow {
				t.Fatalf("Workflow = %q, want %q", got.Workflow, test.wantWorkflow)
			}
			if got.StartPhase != test.wantStartPhase {
				t.Fatalf("StartPhase = %q, want %q", got.StartPhase, test.wantStartPhase)
			}
		})
	}
}

func TestResolveBootstrapNeedsInvestigationBugUsesBugComplexWorkflow(t *testing.T) {
	t.Parallel()

	got, err := ResolveBootstrap(&connector.WorkItem{
		ID:     "cb-bug",
		Type:   "bug",
		Labels: []string{"needs-investigation"},
	}, config.DefaultConfig())
	if err != nil {
		t.Fatalf("ResolveBootstrap() error = %v", err)
	}
	if got.Workflow != "bug-complex" {
		t.Fatalf("Workflow = %q, want bug-complex", got.Workflow)
	}
	if got.StartPhase != "investigate" {
		t.Fatalf("StartPhase = %q, want investigate", got.StartPhase)
	}
}

func TestResolveBootstrapUnknownTypeReturnsError(t *testing.T) {
	t.Parallel()

	_, err := ResolveBootstrap(&connector.WorkItem{ID: "cb-review", Type: "review"}, config.DefaultConfig())
	if err == nil {
		t.Fatal("ResolveBootstrap() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `unknown shard type "review"`) {
		t.Fatalf("error = %v, want unknown shard type", err)
	}
}

func TestResolveBootstrapTaskUsesConfiguredTaskWorkflow(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Workflows: map[string]config.WorkflowConfig{
			"task": {
				Phases: []string{"implement", "review", "done"},
			},
		},
	}

	got, err := ResolveBootstrap(&connector.WorkItem{ID: "cb-task", Type: "task"}, cfg)
	if err != nil {
		t.Fatalf("ResolveBootstrap() error = %v", err)
	}
	if got.Workflow != "task" {
		t.Fatalf("Workflow = %q, want task", got.Workflow)
	}
	if got.StartPhase != "implement" {
		t.Fatalf("StartPhase = %q, want implement", got.StartPhase)
	}
	if phases := cfg.Workflows[got.Workflow].Phases; strings.Join(phases, ",") != "implement,review,done" {
		t.Fatalf("task phases = %v, want [implement review done]", phases)
	}
}

func TestResolveBootstrapMissingWorkflowReturnsError(t *testing.T) {
	t.Parallel()

	_, err := ResolveBootstrap(&connector.WorkItem{
		ID:     "cb-bug",
		Type:   "bug",
		Labels: []string{"needs-investigation"},
	}, &config.Config{
		Workflows: map[string]config.WorkflowConfig{
			"bug": {Phases: []string{"fix", "review", "done"}},
		},
	})
	if err == nil {
		t.Fatal("ResolveBootstrap() error = nil, want error")
	}
	for _, want := range []string{
		`workflow "bug-complex" not declared`,
		`Try: add workflow "bug-complex" to pipeline.yaml`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func TestResolveBootstrapWorkflowWithoutPhasesReturnsHint(t *testing.T) {
	t.Parallel()

	_, err := ResolveBootstrap(&connector.WorkItem{
		ID:   "cb-task",
		Type: "task",
	}, &config.Config{
		Workflows: map[string]config.WorkflowConfig{
			"task": {},
		},
	})
	if err == nil {
		t.Fatal("ResolveBootstrap() error = nil, want error")
	}
	for _, want := range []string{
		`workflow "task" has no phases`,
		`Try: add phases under workflows.task in pipeline.yaml`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}
