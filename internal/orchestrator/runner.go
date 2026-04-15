// Package orchestrator drives a pipeline through its phases from the
// current state to a terminal state (done, deploy-approval-required,
// timeout, or unrecoverable block). It calls out to a Dispatcher for
// single-shard phases, a WaveDispatcher for design implement phases,
// and a Reviewer for review-phase polling, but owns no transport
// specifics itself — wiring lives in cmd/orchestrate.go.
//
// The Runner is deliberately small: it reads the current phase from a
// PhaseSource, fans out to one of runImplement / runReview / step
// based on the phase name, and waits for the phase to advance. Retry
// logic for failed gates (cb-13744c), dead-agent recovery
// (cb-f93173), and the task-shard direct-dispatch path (cb-55f364)
// all hang off this loop.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/domain"
)

var nonImplementPhases = map[string]struct{}{
	domain.PhaseDesign:      {},
	domain.PhaseDecompose:   {},
	domain.PhaseInvestigate: {},
	domain.PhaseFix:         {},
}

// Dispatcher triggers the phase's dispatched agent.
type Dispatcher interface {
	Dispatch(ctx context.Context, shardID string) error
}

// DispatchFunc adapts a function into a Dispatcher.
type DispatchFunc func(ctx context.Context, shardID string) error

func (f DispatchFunc) Dispatch(ctx context.Context, shardID string) error {
	return f(ctx, shardID)
}

// Options configures the orchestration loop.
type Options struct {
	PollInterval   time.Duration
	PhaseTimeout   time.Duration
	StepMode       bool
	Output         io.Writer
	Tasks          TaskSource
	WaveDispatcher WaveDispatcher
	Reviewer       ReviewProcessor
	OnEvent        EventHandler
	BeforeStep     func(ctx context.Context, shardID, phase string) error
	Monitor        ProgressMonitor
	GateHistory    GateHistorySource // optional — enables auto-retry on failed gates (cb-13744c)
	MaxGateRetries int               // 0 = use default (3); cap on re-dispatches per phase after a failed gate
	// DeadAgentRecoverer, when set, lets the implement loop detect and
	// recover tasks whose dispatched agent has died silently (cb-f93173 #1).
	// When nil, dead-agent recovery is skipped and stuck tasks run out the
	// phase timeout as before.
	DeadAgentRecoverer DeadAgentRecoverer
	// ShardTypeSource, when set, lets the implement phase distinguish
	// task-type shards (direct dispatch) from designs (wave dispatch). When
	// nil, implement always assumes design-with-children, which loops forever
	// on task-type shards (cb-55f364).
	ShardTypeSource ShardTypeSource
	SignalCh        <-chan os.Signal
	Now             func() time.Time
	Sleep           func(ctx context.Context, d time.Duration) error
}

// Runner drives a pipeline from its current phase.
type Runner struct {
	phases     PhaseSource
	dispatcher Dispatcher
	opts       Options
}

// NewRunner creates a runner with defaulted options.
func NewRunner(phases PhaseSource, dispatcher Dispatcher, opts Options) *Runner {
	opts = opts.withDefaults()
	return &Runner{
		phases:     phases,
		dispatcher: dispatcher,
		opts:       opts,
	}
}

// Run executes the non-implement orchestration loop until it reaches a terminal state.
func (r *Runner) Run(ctx context.Context, shardID string) error {
	if r == nil {
		return fmt.Errorf("runner is nil")
	}
	if r.phases == nil {
		return fmt.Errorf("phase source is nil")
	}
	if r.dispatcher == nil {
		return fmt.Errorf("dispatcher is nil")
	}

	ctx, stop := signalAwareContext(ctx, r.opts.SignalCh)
	defer stop()

	maxRetries := r.opts.MaxGateRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	gateRetries := map[string]int{}

	for {
		phase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return err
		}

		switch phase {
		case domain.PhaseDone:
			r.emit(shardID, phase, EventTerminal, "Pipeline complete.")
			return nil
		case domain.PhaseDeploy:
			r.emit(shardID, phase, EventTerminal, "Deploy requires human approval.")
			return &DeployRequiredError{ShardID: shardID, Phase: phase}
		case domain.PhaseImplement:
			if err := r.runImplement(ctx, shardID); err != nil {
				return err
			}
			continue
		case domain.PhaseReview:
			if err := r.runReview(ctx, shardID); err != nil {
				return err
			}
			continue
		default:
			if _, ok := nonImplementPhases[phase]; !ok {
				return &UnknownPhaseError{ShardID: shardID, Phase: phase}
			}
		}

		gateCountBefore, _ := r.gateCount(ctx, shardID)

		if err := r.step(ctx, shardID, phase); err != nil {
			return err
		}
		newPhase, err := r.waitForPhaseAdvance(ctx, shardID, phase)
		if err != nil {
			return err
		}

		// If the phase did not change but a new failed gate appeared,
		// re-dispatch (cb-13744c). Cap retries so we don't spin forever.
		if newPhase == phase {
			gateCountAfter, _ := r.gateCount(ctx, shardID)
			if gateCountAfter > gateCountBefore {
				gateRetries[phase]++
				if gateRetries[phase] >= maxRetries {
					return &BlockedError{
						ShardID:     shardID,
						Phase:       phase,
						Reason:      StopReasonBlockedReview,
						Message:     fmt.Sprintf("gate failed %d times in phase %q — needs human intervention", gateRetries[phase], phase),
						Recoverable: true,
					}
				}
				r.emit(shardID, phase, EventPoll, fmt.Sprintf("Phase: %s -> retry %d/%d after failed gate", phase, gateRetries[phase], maxRetries))
				// Loop continues; next iteration will read same phase and re-dispatch
				continue
			}
		}
	}
}

// gateCount returns the total number of gate records for a design, used
// to detect new failed gates between dispatches (cb-13744c).
func (r *Runner) gateCount(ctx context.Context, shardID string) (int, error) {
	if r.opts.GateHistory == nil {
		return 0, nil
	}
	history, err := r.opts.GateHistory.GetGateHistory(ctx, shardID)
	if err != nil {
		return 0, err
	}
	return len(history), nil
}

func (r *Runner) step(ctx context.Context, shardID, phase string) error {
	if r.opts.StepMode && r.opts.BeforeStep != nil {
		if err := r.opts.BeforeStep(ctx, shardID, phase); err != nil {
			return err
		}
	}

	r.emit(shardID, phase, EventDispatch, fmt.Sprintf("Phase: %s -> dispatching", phase))
	if err := r.dispatcher.Dispatch(ctx, shardID); err != nil {
		if errors.Is(err, ErrInterrupted) {
			return &InterruptedError{ShardID: shardID, Phase: phase}
		}
		return fmt.Errorf("dispatch %s for shard %s: %w", phase, shardID, err)
	}
	return nil
}

func (r *Runner) waitForPhaseAdvance(ctx context.Context, shardID, phase string) (string, error) {
	deadline := r.opts.Now().Add(r.opts.PhaseTimeout)
	lastSnapshot, err := r.snapshot(ctx, shardID, phase)
	if err != nil {
		return "", err
	}
	gateCountAtEntry, _ := r.gateCount(ctx, shardID)

	for {
		if err := ctx.Err(); err != nil {
			return "", wrapStopError(shardID, phase, normalizeContextError(ctx, err))
		}
		if !deadline.IsZero() && !r.opts.Now().Before(deadline) {
			return "", &TimeoutError{
				ShardID:         shardID,
				Phase:           phase,
				Timeout:         r.opts.PhaseTimeout,
				BlockingTaskIDs: blockingTaskIDs(lastSnapshot, shardID, phase),
			}
		}

		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return "", wrapStopError(shardID, phase, normalizeContextError(ctx, err))
		}

		currentPhase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return "", err
		}

		if currentPhase != phase {
			r.emit(shardID, phase, EventTransition, fmt.Sprintf("Phase: %s -> %s", phase, currentPhase))
			return currentPhase, nil
		}

		// Phase hasn't moved. Check if a new gate record appeared (which
		// must be a fail since pass would have advanced the phase).
		// Return same phase so Run can decide whether to retry (cb-13744c).
		if cur, _ := r.gateCount(ctx, shardID); cur > gateCountAtEntry {
			r.emit(shardID, phase, EventPoll, fmt.Sprintf("Phase: %s -> failed gate detected", phase))
			return phase, nil
		}

		lastSnapshot, err = r.snapshot(ctx, shardID, phase)
		if err != nil {
			return "", err
		}
		if lastSnapshot != nil && lastSnapshot.Blocker != nil {
			blocker := lastSnapshot.Blocker
			return "", &BlockedError{
				ShardID:         shardID,
				Phase:           phase,
				Reason:          blocker.Reason,
				Message:         blocker.Message,
				BlockingTaskIDs: blockingTaskIDs(lastSnapshot, shardID, phase),
				Recoverable:     blocker.Recoverable,
			}
		}

		r.emit(shardID, phase, EventPoll, fmt.Sprintf("Phase: %s -> still waiting", phase))
	}
}

func (r *Runner) emit(shardID, phase string, kind EventKind, message string) {
	event := Event{
		Time:    r.opts.Now(),
		ShardID: shardID,
		Phase:   phase,
		Kind:    kind,
		Message: message,
	}
	if r.opts.OnEvent != nil {
		r.opts.OnEvent(event)
	}
}

func (o Options) withDefaults() Options {
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	if o.PhaseTimeout <= 0 {
		o.PhaseTimeout = 2 * time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepContext
	}
	if o.OnEvent == nil {
		o.OnEvent = writerEventHandler(o.Output)
	}
	return o
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Runner) snapshot(ctx context.Context, shardID, phase string) (*ProgressSnapshot, error) {
	if r.opts.Monitor == nil {
		return nil, nil
	}
	return r.opts.Monitor.Snapshot(ctx, shardID, phase)
}

func signalAwareContext(ctx context.Context, ch <-chan os.Signal) (context.Context, context.CancelFunc) {
	if ch == nil {
		return ctx, func() {}
	}

	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-ctx.Done():
		case <-ch:
			cancel(ErrInterrupted)
		}
	}()
	return ctx, func() { cancel(nil) }
}

func normalizeContextError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
	}
	return err
}

func wrapStopError(shardID, phase string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInterrupted) {
		return &InterruptedError{ShardID: shardID, Phase: phase}
	}
	return err
}

func blockingTaskIDs(snapshot *ProgressSnapshot, shardID, phase string) []string {
	if snapshot != nil {
		if snapshot.Blocker != nil && len(snapshot.Blocker.BlockingTaskIDs) > 0 {
			return append([]string(nil), snapshot.Blocker.BlockingTaskIDs...)
		}
		if len(snapshot.BlockingTaskIDs) > 0 {
			return append([]string(nil), snapshot.BlockingTaskIDs...)
		}
	}
	if phase == "" || shardID == "" {
		return nil
	}
	return []string{shardID}
}
