package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

type postCloseAction int

const (
	postCloseNoop postCloseAction = iota
	postCloseDispatchNextWave
	postCloseCompleteDesign
)

type postCloseDecision struct {
	Action   postCloseAction
	NextWave int
}

var dispatchWaveRunner = func(ctx context.Context, designID string) error {
	out, err := exec.CommandContext(ctx, "cobuild", "dispatch-wave", designID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dispatch-wave %s: %w\n%s", designID, err, strings.TrimSpace(string(out)))
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		fmt.Println(trimmed)
	}
	return nil
}

func syncPipelineTaskStatus(ctx context.Context, taskID, status string) {
	if cbStore == nil {
		return
	}
	if err := cbStore.UpdateTaskStatus(ctx, taskID, status); err != nil && !errors.Is(err, store.ErrNotFound) {
		fmt.Printf("  Warning: failed to sync pipeline task status to %s: %v\n", status, err)
	}
}

func handlePostCloseProgress(ctx context.Context, taskID string) error {
	designID, err := parentDesignID(ctx, taskID)
	if err != nil || designID == "" {
		return err
	}

	syncPipelineTaskStatus(ctx, taskID, "closed")

	if cbStore == nil {
		return advanceDesignIfAllTasksClosed(ctx, designID)
	}

	repoRoot := findRepoRoot()
	pCfg, _ := config.LoadConfig(repoRoot)
	if pCfg == nil {
		pCfg = config.DefaultConfig()
	}

	currentTask, err := cbStore.GetTaskByShardID(ctx, taskID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("get pipeline task for %s: %w", taskID, err)
	}

	allTasks, err := cbStore.ListTasksByDesign(ctx, designID)
	if err != nil {
		return fmt.Errorf("list pipeline tasks for %s: %w", designID, err)
	}

	if len(allTasks) == 0 {
		return advanceDesignIfAllTasksClosed(ctx, designID)
	}

	decision, err := decidePostCloseAction(normalizeWaveStrategy(pCfg.Dispatch.WaveStrategy), currentTask, allTasks, cbStore, ctx, designID)
	if err != nil {
		return err
	}

	switch decision.Action {
	case postCloseDispatchNextWave:
		fmt.Printf("\nWave %d complete for %s. Dispatching wave %d.\n", *currentTask.Wave, designID, decision.NextWave)
		if err := dispatchWaveRunner(ctx, designID); err != nil {
			return fmt.Errorf("wave %d complete for %s, but dispatching wave %d failed: %w", *currentTask.Wave, designID, decision.NextWave, err)
		}
	case postCloseCompleteDesign:
		return completeDesignRun(ctx, designID)
	}

	return nil
}

func decidePostCloseAction(strategy string, currentTask *store.PipelineTaskRecord, allTasks []store.PipelineTaskRecord, st store.Store, ctx context.Context, designID string) (postCloseDecision, error) {
	if allTasksClosed(allTasks) {
		return postCloseDecision{Action: postCloseCompleteDesign}, nil
	}

	if strategy != "serial" || currentTask == nil || currentTask.Wave == nil || st == nil {
		return postCloseDecision{}, nil
	}

	waveTasks, err := st.GetTasksByWave(ctx, designID, *currentTask.Wave)
	if err != nil {
		return postCloseDecision{}, fmt.Errorf("list wave %d tasks for %s: %w", *currentTask.Wave, designID, err)
	}
	if len(waveTasks) == 0 || !allTasksClosed(waveTasks) {
		return postCloseDecision{}, nil
	}

	if nextWave, ok := nextOpenWave(allTasks, *currentTask.Wave); ok {
		return postCloseDecision{Action: postCloseDispatchNextWave, NextWave: nextWave}, nil
	}

	return postCloseDecision{}, nil
}

func normalizeWaveStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "serial":
		return "serial"
	case "parallel":
		return "parallel"
	default:
		return "parallel"
	}
}

func nextOpenWave(tasks []store.PipelineTaskRecord, currentWave int) (int, bool) {
	next := 0
	found := false
	for _, task := range tasks {
		if task.Wave == nil || *task.Wave <= currentWave || task.Status == "closed" {
			continue
		}
		if !found || *task.Wave < next {
			next = *task.Wave
			found = true
		}
	}
	return next, found
}

func allTasksClosed(tasks []store.PipelineTaskRecord) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if task.Status != "closed" {
			return false
		}
	}
	return true
}

func parentDesignID(ctx context.Context, taskID string) (string, error) {
	if conn == nil {
		return "", nil
	}
	edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil {
		return "", fmt.Errorf("get parent design for %s: %w", taskID, err)
	}
	if len(edges) == 0 {
		return "", nil
	}
	return edges[0].ItemID, nil
}

func advanceDesignIfAllTasksClosed(ctx context.Context, designID string) error {
	if conn == nil {
		return nil
	}
	edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil
	}
	for _, edge := range edges {
		item, itemErr := conn.Get(ctx, edge.ItemID)
		if itemErr != nil || item == nil || item.Type != "task" {
			continue
		}
		if item.Status != "closed" {
			return nil
		}
	}
	return completeDesignRun(ctx, designID)
}

func completeDesignRun(ctx context.Context, designID string) error {
	fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
	if cbStore == nil {
		return nil
	}
	if err := cbStore.UpdateRunPhase(ctx, designID, "done"); err != nil {
		return fmt.Errorf("advance %s to done: %w", designID, err)
	}
	if err := cbStore.UpdateRunStatus(ctx, designID, "completed"); err != nil {
		return fmt.Errorf("mark %s completed: %w", designID, err)
	}
	return nil
}
