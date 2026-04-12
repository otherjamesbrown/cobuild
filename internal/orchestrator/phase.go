package orchestrator

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// PhaseSource reads the current pipeline phase for a shard.
type PhaseSource interface {
	CurrentPhase(ctx context.Context, shardID string) (string, error)
}

// RunStore is the narrow store dependency needed by the runner.
type RunStore interface {
	GetRun(ctx context.Context, designID string) (*store.PipelineRun, error)
}

// StorePhaseSource reads phases from pipeline_runs via the store.
type StorePhaseSource struct {
	Store RunStore
}

func (s StorePhaseSource) CurrentPhase(ctx context.Context, shardID string) (string, error) {
	if s.Store == nil {
		return "", fmt.Errorf("phase source store is nil")
	}

	run, err := s.Store.GetRun(ctx, shardID)
	if err != nil {
		return "", err
	}
	if run == nil {
		return "", store.ErrNotFound
	}
	return run.CurrentPhase, nil
}
