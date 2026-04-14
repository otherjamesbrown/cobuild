package orchestrator

import (
	"context"
	"errors"
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

// DeadAgentRecoverer resets tasks whose dispatched agent has died so the
// orchestrator can re-dispatch them. An implementation typically checks
// the tmux windows and pipeline_sessions rows for the task, and if neither
// shows a live agent, cancels any stale session records, resets the task
// to pending/open, and returns true. Returning false means the task is
// still live — the runner should keep polling (cb-f93173 #1).
type DeadAgentRecoverer interface {
	RecoverDeadAgent(ctx context.Context, taskID string) (recovered bool, reason string, err error)
}

// DeadAgentRecoverFunc adapts a function into a DeadAgentRecoverer.
type DeadAgentRecoverFunc func(ctx context.Context, taskID string) (bool, string, error)

func (f DeadAgentRecoverFunc) RecoverDeadAgent(ctx context.Context, taskID string) (bool, string, error) {
	return f(ctx, taskID)
}

// ReviewResult captures the single-line outcome the runner should emit.
type ReviewResult struct {
	Outcome string
	Message string
}

func (r *Runner) runImplement(ctx context.Context, shardID string) error {
	// Task-type shards ARE the unit of work — the implement phase dispatches
	// a single agent against the shard itself, not a wave of children. Route
	// them through the direct dispatch path used by non-implement phases;
	// otherwise DispatchWave finds zero child-of edges, prints "All tasks
	// complete.", and the poll loop spins forever (cb-55f364).
	if r.opts.ShardTypeSource != nil {
		if t, err := r.opts.ShardTypeSource.ShardType(ctx, shardID); err == nil && t == "task" {
			return r.runDirectImplement(ctx, shardID)
		}
	}

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
			// Probe in-progress tasks for dead agents. If any recover, we
			// re-dispatch the wave instead of polling forever (cb-f93173 #1).
			if r.opts.DeadAgentRecoverer != nil {
				recovered := r.recoverDeadAgentsInWave(ctx, shardID, tasks)
				if recovered > 0 {
					if err := r.dispatchWave(ctx, shardID); err != nil {
						return err
					}
					continue
				}
			}
			r.emit(shardID, "implement", EventPoll, fmt.Sprintf("Phase: implement -> still waiting on %s", strings.Join(waiting, ", ")))
		}

		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return normalizeContextError(ctx, err)
		}
	}
}

// runDirectImplement dispatches a single agent against a task-type shard and
// waits for the phase to advance (implement -> review). Mirrors the non-
// implement phase flow in runner.go but is invoked by Run()'s "implement"
// branch when ShardTypeSource identifies the shard as a task (cb-55f364).
func (r *Runner) runDirectImplement(ctx context.Context, shardID string) error {
	if r.dispatcher == nil {
		return fmt.Errorf("dispatcher is nil")
	}
	if r.opts.StepMode && r.opts.BeforeStep != nil {
		if err := r.opts.BeforeStep(ctx, shardID, "implement"); err != nil {
			return err
		}
	}
	r.emit(shardID, "implement", EventDispatch, "Phase: implement -> dispatching")
	if err := r.dispatcher.Dispatch(ctx, shardID); err != nil {
		if errors.Is(err, ErrInterrupted) {
			return &InterruptedError{ShardID: shardID, Phase: "implement"}
		}
		return fmt.Errorf("dispatch implement for shard %s: %w", shardID, err)
	}
	_, err := r.waitForPhaseAdvance(ctx, shardID, "implement")
	return err
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

	// Cap consecutive fail-gate retries so a reviewer that keeps rejecting
	// the PR surfaces a BlockedError instead of polling to PhaseTimeout.
	// Ports the cb-13744c retry logic from Run into the review path —
	// previously runReview had no cap at all, so cb-999bec piled up 139
	// review/fail gates in 1h before timing out (cb-87371e).
	maxRetries := r.opts.MaxGateRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	gateCountAtEntry, _ := r.gateCount(ctx, shardID)
	failCount := 0

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

		// Phase hasn't moved. If a new gate appeared it must be a fail
		// (pass would have advanced the phase). Count it toward the cap.
		if cur, _ := r.gateCount(ctx, shardID); cur > gateCountAtEntry {
			failCount += cur - gateCountAtEntry
			gateCountAtEntry = cur
			if failCount >= maxRetries {
				return &BlockedError{
					ShardID:     shardID,
					Phase:       "review",
					Reason:      StopReasonBlockedReview,
					Message:     fmt.Sprintf("review rejected %d times in a row — needs human intervention (see latest review gate for findings)", failCount),
					Recoverable: true,
				}
			}
			r.emit(shardID, "review", EventPoll, fmt.Sprintf("Phase: review -> retry %d/%d after failed gate", failCount, maxRetries))
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

// recoverDeadAgentsInWave probes each in_progress task via the
// DeadAgentRecoverer and returns the number of tasks that were successfully
// reset. pending tasks are skipped — they haven't been dispatched yet.
func (r *Runner) recoverDeadAgentsInWave(ctx context.Context, shardID string, tasks []store.PipelineTaskRecord) int {
	recovered := 0
	for _, t := range tasks {
		if t.Status != "in_progress" {
			continue
		}
		ok, reason, err := r.opts.DeadAgentRecoverer.RecoverDeadAgent(ctx, t.TaskShardID)
		if err != nil {
			r.emit(shardID, "implement", EventPoll, fmt.Sprintf("dead-agent probe %s -> error: %v", t.TaskShardID, err))
			continue
		}
		if ok {
			r.emit(shardID, "implement", EventPoll, fmt.Sprintf("dead-agent recovered %s: %s", t.TaskShardID, reason))
			recovered++
		}
	}
	return recovered
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
