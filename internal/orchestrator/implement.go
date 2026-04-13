package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// WaveDispatcher triggers dispatch-wave for implement-phase designs.
type WaveDispatcher interface {
	DispatchWave(ctx context.Context, designID string) error
}

// WaveDispatchFunc adapts a function into a WaveDispatcher.
type WaveDispatchFunc func(ctx context.Context, designID string) error

func (f WaveDispatchFunc) DispatchWave(ctx context.Context, designID string) error {
	return f(ctx, designID)
}

// ReviewProcessor runs process-review for a task or single-item review shard.
type ReviewProcessor interface {
	ProcessReview(ctx context.Context, shardID string) (ReviewResult, error)
}

// ReviewProcessFunc adapts a function into a ReviewProcessor.
type ReviewProcessFunc func(ctx context.Context, shardID string) (ReviewResult, error)

func (f ReviewProcessFunc) ProcessReview(ctx context.Context, shardID string) (ReviewResult, error) {
	return f(ctx, shardID)
}

// ReviewResult captures the single-line outcome the runner should emit.
type ReviewResult struct {
	Outcome string
	Message string
}

func (r *Runner) runImplement(ctx context.Context, shardID string) error {
	if r.opts.Tasks == nil {
		return fmt.Errorf("task source is nil")
	}
	if r.opts.WaveDispatcher == nil {
		return fmt.Errorf("wave dispatcher is nil")
	}
	if r.opts.Reviewer == nil {
		return fmt.Errorf("review processor is nil")
	}

	if r.opts.StepMode && r.opts.BeforeStep != nil {
		if err := r.opts.BeforeStep(ctx, shardID, "implement"); err != nil {
			return err
		}
	}

	deadline := r.opts.Now().Add(r.opts.PhaseTimeout)
	if err := r.dispatchWave(ctx, shardID); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return normalizeContextError(ctx, err)
		}
		if !deadline.IsZero() && !r.opts.Now().Before(deadline) {
			return &TimeoutError{ShardID: shardID, Phase: "implement", Timeout: r.opts.PhaseTimeout}
		}

		currentPhase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return err
		}
		if currentPhase != "implement" {
			r.emit(shardID, "implement", EventTransition, fmt.Sprintf("Phase: implement -> %s", currentPhase))
			return nil
		}

		tasks, err := r.opts.Tasks.ListTasksByDesign(ctx, shardID)
		if err != nil {
			return err
		}

		reviewable := reviewableTaskIDs(tasks)
		merged := false
		for _, taskID := range reviewable {
			result, err := r.opts.Reviewer.ProcessReview(ctx, taskID)
			if err != nil {
				return fmt.Errorf("process review %s: %w", taskID, err)
			}
			r.emit(shardID, "implement", EventReview, formatReviewMessage(taskID, result))
			if strings.EqualFold(result.Outcome, "merged") {
				merged = true
			}
		}
		if merged {
			if err := r.dispatchWave(ctx, shardID); err != nil {
				return err
			}
			continue
		}

		waiting := waitingTaskIDs(tasks)
		if len(waiting) == 0 {
			// All tasks finished (closed) and none in needs-review or
			// in-progress. The design has nothing left to dispatch and no
			// review to process — but the design's own pipeline phase is
			// still "implement". Without this, the loop polls forever
			// (cb-02f789). Trigger advancement by recording a review pass
			// so the gate flow moves the design to the next phase.
			if len(tasks) > 0 && r.opts.Reviewer != nil {
				result, err := r.opts.Reviewer.ProcessReview(ctx, shardID)
				if err != nil {
					return fmt.Errorf("advance design after all tasks closed %s: %w", shardID, err)
				}
				r.emit(shardID, "implement", EventReview, formatReviewMessage(shardID, result))
				// Phase should have advanced; loop top will detect and exit
				continue
			}
			r.emit(shardID, "implement", EventPoll, "Phase: implement -> still waiting on pipeline state")
		} else {
			r.emit(shardID, "implement", EventPoll, fmt.Sprintf("Phase: implement -> still waiting on %s", strings.Join(waiting, ", ")))
		}

		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return normalizeContextError(ctx, err)
		}
	}
}

func (r *Runner) runReview(ctx context.Context, shardID string) error {
	if r.opts.Reviewer == nil {
		return fmt.Errorf("review processor is nil")
	}

	if r.opts.StepMode && r.opts.BeforeStep != nil {
		if err := r.opts.BeforeStep(ctx, shardID, "review"); err != nil {
			return err
		}
	}

	deadline := r.opts.Now().Add(r.opts.PhaseTimeout)

	for {
		if err := ctx.Err(); err != nil {
			return normalizeContextError(ctx, err)
		}
		if !deadline.IsZero() && !r.opts.Now().Before(deadline) {
			return &TimeoutError{ShardID: shardID, Phase: "review", Timeout: r.opts.PhaseTimeout}
		}

		result, err := r.opts.Reviewer.ProcessReview(ctx, shardID)
		if err != nil {
			return fmt.Errorf("process review %s: %w", shardID, err)
		}
		r.emit(shardID, "review", EventReview, formatReviewMessage(shardID, result))

		currentPhase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return err
		}
		if currentPhase != "review" {
			r.emit(shardID, "review", EventTransition, fmt.Sprintf("Phase: review -> %s", currentPhase))
			return nil
		}

		r.emit(shardID, "review", EventPoll, fmt.Sprintf("Phase: review -> still waiting on %s", shardID))
		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return normalizeContextError(ctx, err)
		}
	}
}

func (r *Runner) dispatchWave(ctx context.Context, shardID string) error {
	r.emit(shardID, "implement", EventDispatch, "Phase: implement -> dispatching wave")
	if err := r.opts.WaveDispatcher.DispatchWave(ctx, shardID); err != nil {
		return fmt.Errorf("dispatch wave for shard %s: %w", shardID, err)
	}
	return nil
}

func reviewableTaskIDs(tasks []store.PipelineTaskRecord) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == "needs-review" {
			ids = append(ids, task.TaskShardID)
		}
	}
	return ids
}

func waitingTaskIDs(tasks []store.PipelineTaskRecord) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		switch task.Status {
		case "pending", "in_progress", "needs-review":
			ids = append(ids, task.TaskShardID)
		}
	}
	return ids
}

func formatReviewMessage(shardID string, result ReviewResult) string {
	if strings.TrimSpace(result.Message) != "" {
		return result.Message
	}
	if strings.TrimSpace(result.Outcome) != "" {
		return fmt.Sprintf("process-review %s -> %s", shardID, result.Outcome)
	}
	return fmt.Sprintf("process-review %s", shardID)
}
