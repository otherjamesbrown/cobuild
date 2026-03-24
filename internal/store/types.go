package store

import "time"

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

// GatePassRate holds first-try pass stats for a gate.
type GatePassRate struct {
	GateName     string  `json:"gate_name"`
	FirstTryPass int     `json:"first_try_pass"`
	TotalDesigns int     `json:"total_designs"`
	PassRate     float64 `json:"pass_rate"`
}
