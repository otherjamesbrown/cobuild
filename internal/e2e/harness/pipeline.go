package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (h *Harness) InitPipeline(ctx context.Context, designID string) (string, error) {
	return h.RunCobuild(ctx, "init", designID)
}

func (h *Harness) Orchestrate(ctx context.Context, designID string, timeout time.Duration) (string, error) {
	args := []string{"orchestrate", designID}
	if timeout > 0 {
		args = append(args, "--timeout", timeout.String(), "--poll-interval", "1s")
	}
	return h.RunCobuild(ctx, args...)
}

func (h *Harness) FailureReport(phase, assertion, orchestrateOutput string, taskIDs ...string) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("phase=%s", phase))
	parts = append(parts, fmt.Sprintf("assertion=%s", assertion))
	if strings.TrimSpace(orchestrateOutput) != "" {
		parts = append(parts, "orchestrate.log:\n"+tailLines(orchestrateOutput, 20))
	}
	for _, taskID := range taskIDs {
		if strings.TrimSpace(taskID) == "" {
			continue
		}
		if tail := h.SessionLogTail(taskID, 20); strings.TrimSpace(tail) != "" {
			parts = append(parts, fmt.Sprintf("%s logs:\n%s", taskID, tail))
		}
	}
	return strings.Join(parts, "\n\n")
}
