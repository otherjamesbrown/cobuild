package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// advancePipelinePhase is the single entry point for all normal phase
// transitions. It resolves the next phase from the workflow config and
// atomically advances the pipeline, failing if the current phase doesn't
// match expectations.
//
// Every code path that advances a pipeline phase should call this function
// instead of store.UpdateRunPhase directly. UpdateRunPhase is reserved for
// admin/reset tools that intentionally force a phase.
func advancePipelinePhase(
	ctx context.Context,
	st store.Store,
	cn connector.Connector,
	pCfg *config.Config,
	designID string,
	expectedCurrentPhase string,
) (nextPhase string, err error) {
	if st == nil {
		return "", fmt.Errorf("no store configured")
	}

	nextPhase, err = resolveNextPhase(ctx, cn, pCfg, designID, expectedCurrentPhase)
	if err != nil {
		return "", err
	}
	if nextPhase == "" {
		return "", fmt.Errorf("no next phase after %q for %s", expectedCurrentPhase, designID)
	}

	// Empty-design guard. A design short-circuiting to done with zero child
	// tasks is almost always a silent failure — the operator sees green
	// gates and moves on while no code was produced (cb-d5e1dd #1). Fail
	// loud instead. Bugs and tasks are allowed to advance without children.
	if nextPhase == "done" {
		if err := assertDesignHasChildTasks(ctx, cn, designID); err != nil {
			return "", err
		}
	}

	if err := st.AdvancePhase(ctx, designID, expectedCurrentPhase, nextPhase); err != nil {
		return "", err
	}
	return nextPhase, nil
}

// assertDesignHasChildTasks blocks the advance-to-done for a design with
// no child tasks. Bugs and tasks are allowed through; they're leaves by
// definition. Returns nil if connector unavailable (we don't block on
// connector errors — this is a guard, not a gate).
func assertDesignHasChildTasks(ctx context.Context, cn connector.Connector, designID string) error {
	if cn == nil {
		return nil
	}
	item, err := cn.Get(ctx, designID)
	if err != nil || item == nil {
		return nil
	}
	if item.Type != "design" {
		return nil
	}
	edges, err := cn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil
	}
	for _, e := range edges {
		if e.Type == "" || e.Type == "task" {
			return nil
		}
	}
	return fmt.Errorf(
		"empty-design guard: %s has no child tasks — refusing to advance to done (cb-d5e1dd #1). "+
			"Run decompose first, or close the shard manually if no code is needed.",
		designID,
	)
}

// advancePipelinePhaseTo advances to a specific target phase, still
// validating the current phase matches expectations. Use this when
// the caller knows the exact target (e.g. complete → review).
func advancePipelinePhaseTo(
	ctx context.Context,
	st store.Store,
	designID string,
	expectedCurrentPhase string,
	targetPhase string,
) error {
	if st == nil {
		return fmt.Errorf("no store configured")
	}
	return st.AdvancePhase(ctx, designID, expectedCurrentPhase, targetPhase)
}

// resolveNextPhase determines the next phase using the workflow config.
// Returns an error when the connector / config is unavailable or the
// current phase isn't declared in any workflow — the previous hardcoded
// phase-ordering fallback was the exact "config over code" drift the
// 2026-04-14 review flagged (cb-9a336c). Callers should surface the
// error rather than silently advancing.
func resolveNextPhase(
	ctx context.Context,
	cn connector.Connector,
	pCfg *config.Config,
	designID string,
	currentPhase string,
) (string, error) {
	if cn == nil {
		return "", fmt.Errorf("resolve next phase for %s: no connector configured (cb-9a336c)", designID)
	}
	item, err := cn.Get(ctx, designID)
	if err != nil {
		return "", fmt.Errorf("resolve next phase for %s: get work item: %w", designID, err)
	}
	if item == nil {
		return "", fmt.Errorf("resolve next phase for %s: work item not found", designID)
	}

	bootstrap, resolveErr := pipelinestate.ResolveBootstrap(item, pCfg)
	if resolveErr != nil {
		return "", fmt.Errorf("resolve next phase for %s: %w", designID, resolveErr)
	}
	cfg := pCfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	next := cfg.NextPhaseInWorkflow(bootstrap.Workflow, currentPhase)
	if next == "" {
		return "", fmt.Errorf("resolve next phase for %s: no phase after %q in workflow %q (check pipeline.yaml)",
			designID, currentPhase, bootstrap.Workflow)
	}
	return next, nil
}

// advanceDesignToCompleted marks a parent design's pipeline as done/completed.
// Uses AdvancePhase with optimistic locking — if the design isn't in the
// expected phase, it logs a warning instead of failing.
func advanceDesignToCompleted(ctx context.Context, st store.Store, cn connector.Connector, pCfg *config.Config, designID, expectedPhase string) {
	if st == nil {
		return
	}

	// Already done — just ensure status is marked completed
	if expectedPhase == "done" {
		if err := st.UpdateRunStatus(ctx, designID, "completed"); err != nil {
			fmt.Printf("  Warning: failed to mark %s completed: %v\n", designID, err)
		}
		return
	}

	// Try workflow-aware advance first
	next, err := advancePipelinePhase(ctx, st, cn, pCfg, designID, expectedPhase)
	if err != nil {
		// If the phase already moved past our expectation, that's fine —
		// another path (poller, orchestrator) already advanced it.
		fmt.Printf("  Warning: could not advance %s from %s: %v\n", designID, expectedPhase, err)
		return
	}

	// If we advanced to an intermediate phase, keep going until done
	for next != "done" && next != "" {
		var err error
		next, err = advancePipelinePhase(ctx, st, cn, pCfg, designID, next)
		if err != nil {
			fmt.Printf("  Warning: could not advance %s past %s: %v\n", designID, next, err)
			return
		}
	}

	if err := st.UpdateRunStatus(ctx, designID, "completed"); err != nil {
		fmt.Printf("  Warning: failed to mark %s completed: %v\n", designID, err)
	}
}
