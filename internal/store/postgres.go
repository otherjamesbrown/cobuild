package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Store using PostgreSQL via pgx.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a store backed by a Postgres connection pool.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// Migrate creates the CoBuild tables if they don't exist.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS pipeline_runs (
		id TEXT PRIMARY KEY,
		design_id TEXT NOT NULL,
		project TEXT NOT NULL,
		current_phase TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(design_id)
	);
	CREATE TABLE IF NOT EXISTS pipeline_gates (
		id TEXT PRIMARY KEY,
		pipeline_id TEXT NOT NULL,
		design_id TEXT NOT NULL,
		gate_name TEXT NOT NULL,
		phase TEXT NOT NULL,
		round INTEGER NOT NULL,
		verdict TEXT NOT NULL,
		reviewer TEXT,
		readiness_score INTEGER,
		task_count INTEGER,
		body TEXT,
		review_shard_id TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(pipeline_id, gate_name, round)
	);
	CREATE TABLE IF NOT EXISTS pipeline_tasks (
		id TEXT PRIMARY KEY,
		pipeline_id TEXT NOT NULL,
		task_shard_id TEXT NOT NULL,
		design_id TEXT NOT NULL,
		wave INTEGER,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_pipeline_runs_design ON pipeline_runs(design_id);
	CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project_status ON pipeline_runs(project, status);
	CREATE INDEX IF NOT EXISTS idx_pipeline_gates_pipeline ON pipeline_gates(pipeline_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_pipeline_gates_design ON pipeline_gates(design_id, phase, round);
	CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_pipeline ON pipeline_tasks(pipeline_id);
	CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_design ON pipeline_tasks(design_id);
	-- Runtime column for pipeline_sessions (added alongside codex runtime support).
	-- Idempotent ADD COLUMN IF NOT EXISTS; defaults to claude-code for historical rows.
	ALTER TABLE IF EXISTS pipeline_sessions
		ADD COLUMN IF NOT EXISTS runtime TEXT NOT NULL DEFAULT 'claude-code';
	`
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

// --- Pipeline Runs ---

func (s *PostgresStore) CreateRun(ctx context.Context, designID, project, phase string) (*PipelineRun, error) {
	if designID == "" {
		return nil, fmt.Errorf("design_id is required")
	}
	if phase == "" {
		phase = "design"
	}

	run := &PipelineRun{
		ID:           newID("pr"),
		DesignID:     designID,
		Project:      project,
		CurrentPhase: phase,
		Status:       "active",
	}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, design_id, project, current_phase, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at
	`, run.ID, run.DesignID, run.Project, run.CurrentPhase, run.Status).Scan(&run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create pipeline run: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) CreateRunWithMode(ctx context.Context, designID, project, phase, mode string) (*PipelineRun, error) {
	if designID == "" {
		return nil, fmt.Errorf("design_id is required")
	}
	if phase == "" {
		phase = "design"
	}
	if mode == "" {
		mode = "manual"
	}

	run := &PipelineRun{
		ID:           newID("pr"),
		DesignID:     designID,
		Project:      project,
		CurrentPhase: phase,
		Status:       "active",
		Mode:         mode,
	}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, design_id, project, current_phase, status, mode)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, run.ID, run.DesignID, run.Project, run.CurrentPhase, run.Status, run.Mode).Scan(&run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create pipeline run: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) SetRunMode(ctx context.Context, designID, mode string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs SET mode = $2, updated_at = NOW() WHERE design_id = $1
	`, designID, mode)
	if err != nil {
		return fmt.Errorf("set run mode: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetRun(ctx context.Context, designID string) (*PipelineRun, error) {
	var run PipelineRun
	err := s.pool.QueryRow(ctx, `
		SELECT id, design_id, project, current_phase, status, COALESCE(mode, 'manual'), created_at, updated_at
		FROM pipeline_runs WHERE design_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, designID).Scan(&run.ID, &run.DesignID, &run.Project, &run.CurrentPhase, &run.Status, &run.Mode, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("no pipeline run for design %s: %w", designID, ErrNotFound)
		}
		return nil, fmt.Errorf("get pipeline run: %w", err)
	}
	return &run, nil
}

func (s *PostgresStore) UpdateRunPhase(ctx context.Context, designID, phase string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs SET current_phase = $2, updated_at = NOW() WHERE design_id = $1
	`, designID, phase)
	if err != nil {
		return fmt.Errorf("update run phase: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no pipeline run for design %s", designID)
	}
	return nil
}

func (s *PostgresStore) UpdateRunStatus(ctx context.Context, designID, status string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs SET status = $2, updated_at = NOW() WHERE design_id = $1
	`, designID, status)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no pipeline run for design %s", designID)
	}
	return nil
}

func (s *PostgresStore) ListRuns(ctx context.Context, project string) ([]PipelineRunStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			pr.design_id,
			pr.current_phase,
			pr.status,
			COALESCE(tc.total, 0),
			COALESCE(tc.done, 0),
			COALESCE(tc.blocked, 0),
			pr.updated_at
		FROM pipeline_runs pr
		LEFT JOIN (
			SELECT pipeline_id,
				COUNT(*) as total,
				COUNT(*) FILTER (WHERE status = 'completed') as done,
				COUNT(*) FILTER (WHERE status = 'failed') as blocked
			FROM pipeline_tasks
			GROUP BY pipeline_id
		) tc ON tc.pipeline_id = pr.id
		WHERE ($1 = '' OR pr.project = $1)
		ORDER BY pr.updated_at DESC
	`, project)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []PipelineRunStatus
	for rows.Next() {
		var r PipelineRunStatus
		if err := rows.Scan(&r.DesignID, &r.Phase, &r.Status, &r.TaskTotal, &r.TaskDone, &r.TaskBlocked, &r.LastProgress); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// --- Pipeline Gates ---

func (s *PostgresStore) RecordGate(ctx context.Context, input PipelineGateInput) (*PipelineGateRecord, error) {
	if input.PipelineID == "" || input.DesignID == "" || input.GateName == "" || input.Phase == "" || input.Verdict == "" {
		return nil, fmt.Errorf("pipeline_id, design_id, gate_name, phase, and verdict are required")
	}

	round, err := s.GetLatestGateRound(ctx, input.PipelineID, input.GateName)
	if err != nil {
		return nil, fmt.Errorf("compute gate round: %w", err)
	}
	round++

	rec := &PipelineGateRecord{
		ID:             newID("pg"),
		PipelineID:     input.PipelineID,
		DesignID:       input.DesignID,
		GateName:       input.GateName,
		Phase:          input.Phase,
		Round:          round,
		Verdict:        input.Verdict,
		Reviewer:       input.Reviewer,
		ReadinessScore: input.ReadinessScore,
		TaskCount:      input.TaskCount,
		Body:           input.Body,
		ReviewShardID:  input.ReviewShardID,
	}

	err = s.pool.QueryRow(ctx, `
		INSERT INTO pipeline_gates (
			id, pipeline_id, design_id, gate_name, phase, round, verdict,
			reviewer, readiness_score, task_count, body, review_shard_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING created_at
	`,
		rec.ID, rec.PipelineID, rec.DesignID, rec.GateName, rec.Phase, rec.Round, rec.Verdict,
		rec.Reviewer, rec.ReadinessScore, rec.TaskCount, rec.Body, rec.ReviewShardID,
	).Scan(&rec.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("record gate: %w", err)
	}
	return rec, nil
}

func (s *PostgresStore) GetGateHistory(ctx context.Context, designID string) ([]PipelineGateRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pipeline_id, design_id, gate_name, phase, round, verdict,
			reviewer, readiness_score, task_count, body, review_shard_id, created_at
		FROM pipeline_gates WHERE design_id = $1
		ORDER BY created_at ASC
	`, designID)
	if err != nil {
		return nil, fmt.Errorf("get gate history: %w", err)
	}
	defer rows.Close()

	var records []PipelineGateRecord
	for rows.Next() {
		var r PipelineGateRecord
		if err := rows.Scan(
			&r.ID, &r.PipelineID, &r.DesignID, &r.GateName, &r.Phase, &r.Round, &r.Verdict,
			&r.Reviewer, &r.ReadinessScore, &r.TaskCount, &r.Body, &r.ReviewShardID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan gate record: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *PostgresStore) GetLatestGateRound(ctx context.Context, pipelineID, gateName string) (int, error) {
	var round *int
	err := s.pool.QueryRow(ctx, `
		SELECT MAX(round) FROM pipeline_gates WHERE pipeline_id = $1 AND gate_name = $2
	`, pipelineID, gateName).Scan(&round)
	if err != nil {
		return 0, fmt.Errorf("get latest gate round: %w", err)
	}
	if round == nil {
		return 0, nil
	}
	return *round, nil
}

// --- Pipeline Tasks ---

func (s *PostgresStore) AddTask(ctx context.Context, pipelineID, taskShardID, designID string, wave *int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO pipeline_tasks (id, pipeline_id, task_shard_id, design_id, wave, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, newID("pt"), pipelineID, taskShardID, designID, wave, "pending")
	if err != nil {
		return fmt.Errorf("add pipeline task: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListTasks(ctx context.Context, pipelineID string) ([]PipelineTaskRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pipeline_id, task_shard_id, design_id, wave, status, created_at, updated_at
		FROM pipeline_tasks WHERE pipeline_id = $1
		ORDER BY wave NULLS LAST, created_at ASC
	`, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("list pipeline tasks: %w", err)
	}
	defer rows.Close()

	var tasks []PipelineTaskRecord
	for rows.Next() {
		var t PipelineTaskRecord
		if err := rows.Scan(
			&t.ID, &t.PipelineID, &t.TaskShardID, &t.DesignID, &t.Wave, &t.Status, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *PostgresStore) UpdateTaskStatus(ctx context.Context, taskShardID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_tasks SET status = $2, updated_at = NOW() WHERE task_shard_id = $1
	`, taskShardID, status)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

// --- Pipeline Sessions ---

func (s *PostgresStore) CreateSession(ctx context.Context, input SessionInput) (*SessionRecord, error) {
	// Default runtime to claude-code for callers that predate the runtime
	// abstraction — matches the pipeline_sessions column default, keeps
	// old analytics queries working unchanged.
	runtime := input.Runtime
	if runtime == "" {
		runtime = "claude-code"
	}
	rec := &SessionRecord{
		ID:         newID("ps"),
		PipelineID: input.PipelineID,
		DesignID:   input.DesignID,
		TaskID:     input.TaskID,
		Phase:      input.Phase,
		Project:    input.Project,
		Runtime:    runtime,
		Status:     "running",
	}

	var model, wtPath, tmuxSess, tmuxWin *string
	var promptChars *int
	var prompt *string
	if input.Model != "" {
		model = &input.Model
	}
	if input.PromptChars > 0 {
		promptChars = &input.PromptChars
	}
	if input.Prompt != "" {
		prompt = &input.Prompt
	}
	if input.WorktreePath != "" {
		wtPath = &input.WorktreePath
	}
	if input.TmuxSession != "" {
		tmuxSess = &input.TmuxSession
	}
	if input.TmuxWindow != "" {
		tmuxWin = &input.TmuxWindow
	}

	var assembledCtx *string
	if input.AssembledContext != "" {
		assembledCtx = &input.AssembledContext
	}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO pipeline_sessions (
			id, pipeline_id, design_id, task_id, phase, project, runtime,
			model, prompt_chars, prompt, assembled_context, worktree_path, tmux_session, tmux_window
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING started_at
	`, rec.ID, rec.PipelineID, rec.DesignID, rec.TaskID, rec.Phase, rec.Project, rec.Runtime,
		model, promptChars, prompt, assembledCtx, wtPath, tmuxSess, tmuxWin,
	).Scan(&rec.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return rec, nil
}

func (s *PostgresStore) EndSession(ctx context.Context, id string, result SessionResult) error {
	var errStr *string
	if result.Error != "" {
		errStr = &result.Error
	}
	var prURL *string
	if result.PRURL != "" {
		prURL = &result.PRURL
	}
	var sessionLog *string
	if result.SessionLog != "" {
		sessionLog = &result.SessionLog
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_sessions SET
			ended_at = NOW(),
			duration_seconds = EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER,
			exit_code = $2,
			files_changed = $3,
			lines_added = $4,
			lines_removed = $5,
			commits = $6,
			pr_url = $7,
			status = $8,
			error = $9,
			session_log = $10
		WHERE id = $1
	`, id, result.ExitCode, result.FilesChanged, result.LinesAdded, result.LinesRemoved,
		result.Commits, prURL, result.Status, errStr, sessionLog)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetSession(ctx context.Context, taskID string) (*SessionRecord, error) {
	var r SessionRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, pipeline_id, design_id, task_id, phase, project, runtime,
			started_at, ended_at, duration_seconds, model, prompt_chars,
			exit_code, files_changed, lines_added, lines_removed, commits, pr_url,
			status, error, worktree_path
		FROM pipeline_sessions WHERE task_id = $1
		ORDER BY started_at DESC LIMIT 1
	`, taskID).Scan(
		&r.ID, &r.PipelineID, &r.DesignID, &r.TaskID, &r.Phase, &r.Project, &r.Runtime,
		&r.StartedAt, &r.EndedAt, &r.DurationSec, &r.Model, &r.PromptChars,
		&r.ExitCode, &r.FilesChanged, &r.LinesAdded, &r.LinesRemoved, &r.Commits, &r.PRURL,
		&r.Status, &r.Error, &r.WorktreePath,
	)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &r, nil
}

func (s *PostgresStore) ListSessions(ctx context.Context, designID string) ([]SessionRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pipeline_id, design_id, task_id, phase, project, runtime,
			started_at, ended_at, duration_seconds, model, prompt_chars,
			exit_code, files_changed, lines_added, lines_removed, commits, pr_url,
			status, error, worktree_path
		FROM pipeline_sessions WHERE design_id = $1
		ORDER BY started_at ASC
	`, designID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var r SessionRecord
		if err := rows.Scan(
			&r.ID, &r.PipelineID, &r.DesignID, &r.TaskID, &r.Phase, &r.Project, &r.Runtime,
			&r.StartedAt, &r.EndedAt, &r.DurationSec, &r.Model, &r.PromptChars,
			&r.ExitCode, &r.FilesChanged, &r.LinesAdded, &r.LinesRemoved, &r.Commits, &r.PRURL,
			&r.Status, &r.Error, &r.WorktreePath,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, r)
	}
	return sessions, rows.Err()
}

// --- Insights ---

func (s *PostgresStore) GetRunStatusCounts(ctx context.Context, project string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT status, COUNT(*) FROM pipeline_runs WHERE project = $1 GROUP BY status
	`, project)
	if err != nil {
		return nil, fmt.Errorf("run status counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (s *PostgresStore) GetTaskStatusCounts(ctx context.Context, project string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pt.status, COUNT(*)
		FROM pipeline_tasks pt
		JOIN pipeline_runs pr ON pt.pipeline_id = pr.id
		WHERE pr.project = $1
		GROUP BY pt.status
	`, project)
	if err != nil {
		return nil, fmt.Errorf("task status counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (s *PostgresStore) GetGatePassRates(ctx context.Context, project string) ([]GatePassRate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT gate_name,
			COUNT(*) FILTER (WHERE round = 1 AND verdict = 'pass') as first_try_pass,
			COUNT(DISTINCT (pipeline_id, gate_name)) as total_designs
		FROM pipeline_gates
		WHERE design_id IN (SELECT design_id FROM pipeline_runs WHERE project = $1)
		GROUP BY gate_name
		ORDER BY gate_name
	`, project)
	if err != nil {
		return nil, fmt.Errorf("gate pass rates: %w", err)
	}
	defer rows.Close()

	var rates []GatePassRate
	for rows.Next() {
		var g GatePassRate
		if err := rows.Scan(&g.GateName, &g.FirstTryPass, &g.TotalDesigns); err != nil {
			return nil, err
		}
		if g.TotalDesigns > 0 {
			g.PassRate = float64(g.FirstTryPass) / float64(g.TotalDesigns) * 100
		}
		rates = append(rates, g)
	}
	return rates, rows.Err()
}

func (s *PostgresStore) GetGateFailures(ctx context.Context, project string) ([]PipelineGateRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pipeline_id, design_id, gate_name, phase, round, verdict,
			reviewer, readiness_score, task_count, body, review_shard_id, created_at
		FROM pipeline_gates
		WHERE design_id IN (SELECT design_id FROM pipeline_runs WHERE project = $1)
		  AND verdict != 'pass' AND body IS NOT NULL
		ORDER BY gate_name
	`, project)
	if err != nil {
		return nil, fmt.Errorf("gate failures: %w", err)
	}
	defer rows.Close()

	var records []PipelineGateRecord
	for rows.Next() {
		var r PipelineGateRecord
		if err := rows.Scan(
			&r.ID, &r.PipelineID, &r.DesignID, &r.GateName, &r.Phase, &r.Round, &r.Verdict,
			&r.Reviewer, &r.ReadinessScore, &r.TaskCount, &r.Body, &r.ReviewShardID, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *PostgresStore) GetAvgTaskDuration(ctx context.Context, project string) (*float64, error) {
	var avg *float64
	err := s.pool.QueryRow(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (pt.updated_at - pt.created_at)) / 60)
		FROM pipeline_tasks pt
		JOIN pipeline_runs pr ON pt.pipeline_id = pr.id
		WHERE pr.project = $1 AND pt.status IN ('completed', 'done', 'closed')
	`, project).Scan(&avg)
	if err != nil {
		return nil, fmt.Errorf("avg task duration: %w", err)
	}
	return avg, nil
}

// --- Helpers ---

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UTC().UnixMilli(), hex.EncodeToString(buf))
}

// IsTableMissing returns true if the error indicates a missing table.
func IsTableMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "relation") || strings.Contains(msg, "table not found")
}
