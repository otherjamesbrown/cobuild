// Package store defines the interface for CoBuild's own data persistence.
//
// CoBuild stores its orchestration state (pipeline runs, gate records, task
// tracking) separately from work items. Work items live in external systems
// and are accessed via connectors. The Store handles only CoBuild's own tables.
//
// Implementations:
//   - PostgresStore: direct Postgres via pgx (current default)
//   - (future) SQLiteStore: embedded SQLite for single-user mode
//   - (future) FileStore: YAML + JSONL files for zero-dependency mode
package store

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get* methods when the requested record does not exist.
var ErrNotFound = errors.New("not found")

// ErrPhaseConflict is returned by AdvancePhase when the pipeline's current
// phase does not match the caller's expectedCurrentPhase. This prevents
// stale or concurrent callers from advancing the pipeline out of order.
var ErrPhaseConflict = errors.New("phase conflict")

// Store abstracts CoBuild's internal data persistence.
type Store interface {
	// --- Pipeline Runs ---

	CreateRun(ctx context.Context, designID, project, phase string) (*PipelineRun, error)
	CreateRunWithMode(ctx context.Context, designID, project, phase, mode string) (*PipelineRun, error)
	GetRun(ctx context.Context, designID string) (*PipelineRun, error)
	ListRuns(ctx context.Context, project string) ([]PipelineRunStatus, error)
	UpdateRunPhase(ctx context.Context, designID, phase string) error
	// AdvancePhase atomically advances the pipeline phase, but only if the
	// current phase matches expectedCurrent. Returns ErrPhaseConflict if
	// another caller already advanced the phase. This is the preferred method
	// for all normal phase transitions — UpdateRunPhase should only be used
	// by admin/reset tools that intentionally force a phase.
	AdvancePhase(ctx context.Context, designID, expectedCurrent, nextPhase string) error
	UpdateRunStatus(ctx context.Context, designID, status string) error
	SetRunMode(ctx context.Context, designID, mode string) error
	ResetRun(ctx context.Context, designID, phase string) error

	// --- Pipeline Gates ---

	RecordGate(ctx context.Context, input PipelineGateInput) (*PipelineGateRecord, error)
	GetGateHistory(ctx context.Context, designID string) ([]PipelineGateRecord, error)
	GetLatestGateRound(ctx context.Context, pipelineID, gateName string) (int, error)
	// GetPreviousGateHash returns the findings_hash of the fail gate at
	// round (currentRound-1) for the same pipeline+gate. Returns nil when
	// no such gate exists. Used by review escalation (cb-f55aa0).
	GetPreviousGateHash(ctx context.Context, pipelineID, gateName string, currentRound int) (*string, error)

	// --- Pipeline Tasks ---

	AddTask(ctx context.Context, pipelineID, taskShardID, designID string, wave *int) error
	ListTasks(ctx context.Context, pipelineID string) ([]PipelineTaskRecord, error)
	ListTasksByDesign(ctx context.Context, designID string) ([]PipelineTaskRecord, error)
	GetTaskByShardID(ctx context.Context, taskShardID string) (*PipelineTaskRecord, error)
	GetTasksByWave(ctx context.Context, designID string, wave int) ([]PipelineTaskRecord, error)
	IsWaveClosed(ctx context.Context, designID string, wave int) (bool, error)
	UpdateTaskStatus(ctx context.Context, taskShardID, status string) error
	UpdateTaskRebaseStatus(ctx context.Context, taskShardID, rebaseStatus string) error

	// --- Pipeline Sessions ---

	CreateSession(ctx context.Context, input SessionInput) (*SessionRecord, error)
	EndSession(ctx context.Context, id string, result SessionResult) error
	GetSession(ctx context.Context, taskID string) (*SessionRecord, error)
	ListSessions(ctx context.Context, designID string) ([]SessionRecord, error)
	ListRunningSessions(ctx context.Context, project string) ([]SessionRecord, error)
	// CancelRunningSessions marks all running sessions for a design as
	// cancelled. Called by reset to prevent stale sessions from blocking
	// re-dispatch.
	CancelRunningSessions(ctx context.Context, designID string) (int, error)
	// CancelRunningSessionsForShard cancels running sessions that reference
	// shardID as either design_id or task_id. Broader than the above — used
	// by reset/recover to catch sessions whose design_id was misrecorded
	// during dispatch.
	CancelRunningSessionsForShard(ctx context.Context, shardID string) (int, error)
	// MarkSessionEarlyDeath sets early_death=true on a session row. Used by
	// the post-dispatch liveness probe when the tmux window disappears
	// before the agent had time to produce meaningful output.
	MarkSessionEarlyDeath(ctx context.Context, sessionID string, errorDetail string) error

	// --- Insights (read-only aggregates) ---

	GetRunStatusCounts(ctx context.Context, project string) (map[string]int, error)
	GetTaskStatusCounts(ctx context.Context, project string) (map[string]int, error)
	GetGatePassRates(ctx context.Context, project string) ([]GatePassRate, error)
	GetGateFailures(ctx context.Context, project string) ([]PipelineGateRecord, error)
	GetAvgTaskDuration(ctx context.Context, project string) (*float64, error)

	// --- Lifecycle ---

	// Migrate creates or updates the store's schema.
	Migrate(ctx context.Context) error

	// Close releases any resources held by the store.
	Close() error
}
