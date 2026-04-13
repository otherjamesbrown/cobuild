package livestate

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// PipelineInfo is a dashboard-oriented view of an active pipeline run.
type PipelineInfo struct {
	DesignID     string    `json:"design_id"`
	Project      string    `json:"project"`
	Phase        string    `json:"phase"`
	Status       string    `json:"status"`
	TaskTotal    int       `json:"task_total,omitempty"`
	TaskDone     int       `json:"task_done,omitempty"`
	TaskBlocked  int       `json:"task_blocked,omitempty"`
	LastProgress time.Time `json:"last_progress"`
}

// CollectPipelines reads active pipeline runs from the CoBuild store.
func CollectPipelines(ctx context.Context, runs PipelineRunLister) ([]PipelineInfo, error) {
	rows, err := runs.ListRuns(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}

	pipelines := make([]PipelineInfo, 0, len(rows))
	for _, row := range rows {
		pipelines = append(pipelines, pipelineInfoFromRun(row))
	}
	return pipelines, nil
}

func pipelineInfoFromRun(run store.PipelineRunStatus) PipelineInfo {
	return PipelineInfo{
		DesignID:     run.DesignID,
		Project:      run.Project,
		Phase:        run.Phase,
		Status:       run.Status,
		TaskTotal:    run.TaskTotal,
		TaskDone:     run.TaskDone,
		TaskBlocked:  run.TaskBlocked,
		LastProgress: run.LastProgress,
	}
}
