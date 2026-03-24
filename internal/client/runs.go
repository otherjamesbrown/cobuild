package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// PipelineRun represents a row in the pipeline_runs table.
type PipelineRun struct {
	ID           string    `json:"id"`
	DesignID     string    `json:"design_id"`
	Project      string    `json:"project"`
	CurrentPhase string    `json:"current_phase"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PipelineGateRecord represents a row in the pipeline_gates table.
type PipelineGateRecord struct {
	ID             string    `json:"id"`
	PipelineID     string    `json:"pipeline_id"`
	DesignID       string    `json:"design_id"`
	GateName       string    `json:"gate_name"`
	Phase          string    `json:"phase"`
	Round          int       `json:"round"`
	Verdict        string    `json:"verdict"`
	Reviewer       *string   `json:"reviewer,omitempty"`
	ReadinessScore *int      `json:"readiness_score,omitempty"`
	TaskCount      *int      `json:"task_count,omitempty"`
	Body           *string   `json:"body,omitempty"`
	ReviewShardID  *string   `json:"review_shard_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// PipelineTaskRecord represents a row in the pipeline_tasks table.
type PipelineTaskRecord struct {
	ID          string    `json:"id"`
	PipelineID  string    `json:"pipeline_id"`
	TaskShardID string    `json:"task_shard_id"`
	DesignID    string    `json:"design_id"`
	Wave        *int      `json:"wave,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PipelineGateInput captures the fields needed to record a gate verdict.
type PipelineGateInput struct {
	PipelineID     string
	DesignID       string
	GateName       string
	Phase          string
	Verdict        string
	Reviewer       *string
	ReadinessScore *int
	TaskCount      *int
	Body           *string
	ReviewShardID  *string
}

func newPipelineID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UTC().UnixMilli(), hex.EncodeToString(buf))
}

// CreatePipelineRun creates a new pipeline_runs row.
func (c *Client) CreatePipelineRun(ctx context.Context, designID, project, phase string) (*PipelineRun, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	if designID == "" {
		return nil, fmt.Errorf("design_id is required")
	}
	if project == "" {
		project = c.Config.Project
	}
	if phase == "" {
		phase = "design"
	}

	run := &PipelineRun{
		ID:           newPipelineID("pr"),
		DesignID:     designID,
		Project:      project,
		CurrentPhase: phase,
		Status:       "active",
	}

	err = conn.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, design_id, project, current_phase, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at
	`, run.ID, run.DesignID, run.Project, run.CurrentPhase, run.Status).Scan(&run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create pipeline run: %v", err)
	}

	return run, nil
}

// GetPipelineRun fetches a pipeline run by design_id.
func (c *Client) GetPipelineRun(ctx context.Context, designID string) (*PipelineRun, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	var run PipelineRun
	err = conn.QueryRow(ctx, `
		SELECT id, design_id, project, current_phase, status, created_at, updated_at
		FROM pipeline_runs
		WHERE design_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, designID).Scan(&run.ID, &run.DesignID, &run.Project, &run.CurrentPhase, &run.Status, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("no pipeline run found for design %s", designID)
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "does not exist") || strings.Contains(errMsg, "relation") {
			return nil, fmt.Errorf("pipeline_runs table not found -- run the pipeline_runs migration first: %v", err)
		}
		return nil, fmt.Errorf("failed to get pipeline run for design %s: %v", designID, err)
	}
	return &run, nil
}

// UpdatePipelineRunPhase updates the current_phase of a pipeline run.
func (c *Client) UpdatePipelineRunPhase(ctx context.Context, designID, phase string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	result, err := conn.Exec(ctx, `
		UPDATE pipeline_runs SET current_phase = $2, updated_at = NOW() WHERE design_id = $1
	`, designID, phase)
	if err != nil {
		return fmt.Errorf("failed to update pipeline run phase: %v", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no pipeline run found for design %s", designID)
	}
	return nil
}

// UpdatePipelineRunStatus updates the status of a pipeline run.
func (c *Client) UpdatePipelineRunStatus(ctx context.Context, designID, status string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	result, err := conn.Exec(ctx, `
		UPDATE pipeline_runs SET status = $2, updated_at = NOW() WHERE design_id = $1
	`, designID, status)
	if err != nil {
		return fmt.Errorf("failed to update pipeline run status: %v", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no pipeline run found for design %s", designID)
	}
	return nil
}

// RecordGate records a gate verdict in the pipeline_gates table.
func (c *Client) RecordGate(ctx context.Context, input PipelineGateInput) (*PipelineGateRecord, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	if input.PipelineID == "" {
		return nil, fmt.Errorf("pipeline_id is required")
	}
	if input.DesignID == "" {
		return nil, fmt.Errorf("design_id is required")
	}
	if input.GateName == "" {
		return nil, fmt.Errorf("gate_name is required")
	}
	if input.Phase == "" {
		return nil, fmt.Errorf("phase is required")
	}
	if input.Verdict == "" {
		return nil, fmt.Errorf("verdict is required")
	}

	round, err := c.GetLatestGateRound(ctx, input.PipelineID, input.GateName)
	if err != nil {
		return nil, fmt.Errorf("failed to compute gate round: %v", err)
	}
	round++

	rec := &PipelineGateRecord{
		ID:             newPipelineID("pg"),
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

	err = conn.QueryRow(ctx, `
		INSERT INTO pipeline_gates (
			id, pipeline_id, design_id, gate_name, phase, round, verdict,
			reviewer, readiness_score, task_count, body, review_shard_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12
		)
		RETURNING created_at
	`,
		rec.ID, rec.PipelineID, rec.DesignID, rec.GateName, rec.Phase, rec.Round, rec.Verdict,
		rec.Reviewer, rec.ReadinessScore, rec.TaskCount, rec.Body, rec.ReviewShardID,
	).Scan(&rec.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to record gate: %v", err)
	}

	return rec, nil
}

// GetGateHistory returns all gate records for a design_id.
func (c *Client) GetGateHistory(ctx context.Context, designID string) ([]PipelineGateRecord, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT id, pipeline_id, design_id, gate_name, phase, round, verdict,
			reviewer, readiness_score, task_count, body, review_shard_id, created_at
		FROM pipeline_gates
		WHERE design_id = $1
		ORDER BY created_at ASC
	`, designID)
	if err != nil {
		return nil, fmt.Errorf("failed to get gate history for design %s: %v", designID, err)
	}
	defer rows.Close()

	var records []PipelineGateRecord
	for rows.Next() {
		var r PipelineGateRecord
		if err := rows.Scan(
			&r.ID, &r.PipelineID, &r.DesignID, &r.GateName, &r.Phase, &r.Round, &r.Verdict,
			&r.Reviewer, &r.ReadinessScore, &r.TaskCount, &r.Body, &r.ReviewShardID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan gate record: %v", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// GetLatestGateRound returns the highest round number for a given pipeline and gate name.
func (c *Client) GetLatestGateRound(ctx context.Context, pipelineID, gateName string) (int, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close(ctx)

	var round *int
	err = conn.QueryRow(ctx, `
		SELECT MAX(round) FROM pipeline_gates WHERE pipeline_id = $1 AND gate_name = $2
	`, pipelineID, gateName).Scan(&round)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest gate round: %v", err)
	}
	if round == nil {
		return 0, nil
	}
	return *round, nil
}

// AddPipelineTask adds a task shard to a pipeline run.
func (c *Client) AddPipelineTask(ctx context.Context, pipelineID, taskShardID, designID string, wave *int) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	id := newPipelineID("pt")
	_, err = conn.Exec(ctx, `
		INSERT INTO pipeline_tasks (id, pipeline_id, task_shard_id, design_id, wave, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, pipelineID, taskShardID, designID, wave, "pending")
	if err != nil {
		return fmt.Errorf("failed to add pipeline task: %v", err)
	}
	return nil
}

// ListPipelineTasks returns all tasks for a pipeline run.
func (c *Client) ListPipelineTasks(ctx context.Context, pipelineID string) ([]PipelineTaskRecord, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT id, pipeline_id, task_shard_id, design_id, wave, status, created_at, updated_at
		FROM pipeline_tasks
		WHERE pipeline_id = $1
		ORDER BY wave NULLS LAST, created_at ASC
	`, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("failed to list pipeline tasks: %v", err)
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
