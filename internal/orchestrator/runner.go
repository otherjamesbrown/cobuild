package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var nonImplementPhases = map[string]struct{}{
	"design":      {},
	"decompose":   {},
	"investigate": {},
	"fix":         {},
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
	PollInterval time.Duration
	PhaseTimeout time.Duration
	StepMode     bool
	Output       io.Writer
	OnEvent      EventHandler
	BeforeStep   func(ctx context.Context, shardID, phase string) error
	SignalCh     <-chan os.Signal
	Now          func() time.Time
	Sleep        func(ctx context.Context, d time.Duration) error
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

	for {
		phase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return err
		}

		switch phase {
		case "done":
			r.emit(shardID, phase, EventTerminal, "Pipeline complete.")
			return nil
		case "deploy":
			r.emit(shardID, phase, EventTerminal, "Deploy requires human approval.")
			return &DeployRequiredError{ShardID: shardID, Phase: phase}
		default:
			if _, ok := nonImplementPhases[phase]; !ok {
				return &UnknownPhaseError{ShardID: shardID, Phase: phase}
			}
		}

		if err := r.step(ctx, shardID, phase); err != nil {
			return err
		}
		if _, err := r.waitForPhaseAdvance(ctx, shardID, phase); err != nil {
			return err
		}
	}
}

func (r *Runner) step(ctx context.Context, shardID, phase string) error {
	if r.opts.StepMode && r.opts.BeforeStep != nil {
		if err := r.opts.BeforeStep(ctx, shardID, phase); err != nil {
			return err
		}
	}

	r.emit(shardID, phase, EventDispatch, fmt.Sprintf("Phase: %s -> dispatching", phase))
	if err := r.dispatcher.Dispatch(ctx, shardID); err != nil {
		return fmt.Errorf("dispatch %s for shard %s: %w", phase, shardID, err)
	}
	return nil
}

func (r *Runner) waitForPhaseAdvance(ctx context.Context, shardID, phase string) (string, error) {
	deadline := r.opts.Now().Add(r.opts.PhaseTimeout)

	for {
		if err := ctx.Err(); err != nil {
			return "", normalizeContextError(ctx, err)
		}
		if !deadline.IsZero() && !r.opts.Now().Before(deadline) {
			return "", &TimeoutError{
				ShardID: shardID,
				Phase:   phase,
				Timeout: r.opts.PhaseTimeout,
			}
		}

		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return "", normalizeContextError(ctx, err)
		}

		currentPhase, err := r.phases.CurrentPhase(ctx, shardID)
		if err != nil {
			return "", err
		}

		if currentPhase != phase {
			r.emit(shardID, phase, EventTransition, fmt.Sprintf("Phase: %s -> %s", phase, currentPhase))
			return currentPhase, nil
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
