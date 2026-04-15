package state

import (
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
)

// Bootstrap describes the workflow and starting phase for a work item.
type Bootstrap struct {
	Workflow   string
	StartPhase string
}

// ResolveBootstrap returns the canonical workflow and start phase for a work
// item using pipeline config as the source of truth for workflow phases.
func ResolveBootstrap(item *connector.WorkItem, cfg *config.Config) (Bootstrap, error) {
	if item == nil {
		return Bootstrap{}, fmt.Errorf("nil work item")
	}

	workflow, err := workflowForItem(item)
	if err != nil {
		return Bootstrap{}, err
	}

	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	wf, ok := cfg.Workflows[workflow]
	if !ok {
		return Bootstrap{}, fmt.Errorf("workflow %q not declared (Try: add workflow %q to pipeline.yaml)", workflow, workflow)
	}
	if len(wf.Phases) == 0 {
		return Bootstrap{}, fmt.Errorf("workflow %q has no phases (Try: add phases under workflows.%s in pipeline.yaml)", workflow, workflow)
	}

	return Bootstrap{
		Workflow:   workflow,
		StartPhase: wf.Phases[0],
	}, nil
}

func workflowForItem(item *connector.WorkItem) (string, error) {
	switch item.Type {
	case "design", "task":
		return item.Type, nil
	case "bug":
		if containsLabel(item.Labels, "needs-investigation") {
			return "bug-complex", nil
		}
		return "bug", nil
	default:
		return "", fmt.Errorf("unknown shard type %q", item.Type)
	}
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if strings.EqualFold(label, target) {
			return true
		}
	}
	return false
}
