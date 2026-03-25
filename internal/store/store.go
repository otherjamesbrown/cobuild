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

import "context"

// Store abstracts CoBuild's internal data persistence.
type Store interface {
	// --- Pipeline Runs ---

	CreateRun(ctx context.Context, designID, project, phase string) (*PipelineRun, error)
	GetRun(ctx context.Context, designID string) (*PipelineRun, error)
	ListRuns(ctx context.Context, project string) ([]PipelineRunStatus, error)
	UpdateRunPhase(ctx context.Context, designID, phase string) error
	UpdateRunStatus(ctx context.Context, designID, status string) error

	// --- Pipeline Gates ---

	RecordGate(ctx context.Context, input PipelineGateInput) (*PipelineGateRecord, error)
	GetGateHistory(ctx context.Context, designID string) ([]PipelineGateRecord, error)
	GetLatestGateRound(ctx context.Context, pipelineID, gateName string) (int, error)

	// --- Pipeline Tasks ---

	AddTask(ctx context.Context, pipelineID, taskShardID, designID string, wave *int) error
	ListTasks(ctx context.Context, pipelineID string) ([]PipelineTaskRecord, error)
	UpdateTaskStatus(ctx context.Context, taskShardID, status string) error

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
