package store

import "time"

// PipelineRun represents a row in the pipeline_runs table.
type PipelineRun struct {
	ID           string    `json:"id"`
	DesignID     string    `json:"design_id"`
	Project      string    `json:"project"`
	CurrentPhase string    `json:"current_phase"`
	Status       string    `json:"status"`
	Mode         string    `json:"mode"` // manual or autonomous
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

// PipelineRunStatus is an enriched view of a pipeline run for the status command.
type PipelineRunStatus struct {
	DesignID     string    `json:"design_id"`
	Project      string    `json:"project"`
	Phase        string    `json:"phase"`
	Status       string    `json:"status"`
	TaskTotal    int       `json:"task_total"`
	TaskDone     int       `json:"task_done"`
	TaskBlocked  int       `json:"task_blocked"`
	LastProgress time.Time `json:"last_progress"`
}

// SessionInput captures the fields needed to start a session record.
type SessionInput struct {
	PipelineID       string
	DesignID         string
	TaskID           string
	Phase            string
	Project          string
	Runtime          string // agent runtime that will handle this dispatch ("claude-code", "codex")
	Model            string
	PromptChars      int
	Prompt           string
	AssembledContext string // full CLAUDE.md / AGENTS.md content the agent sees
	WorktreePath     string
	TmuxSession      string
	TmuxWindow       string
}

// SessionResult captures the outcome of a completed session.
type SessionResult struct {
	ExitCode       int
	FilesChanged   int
	LinesAdded     int
	LinesRemoved   int
	Commits        int
	PRURL          string
	CompletionNote string
	Status         string // completed, failed, timeout
	Error          string
	SessionLog     string
}

// SessionRecord is a row from pipeline_sessions.
type SessionRecord struct {
	ID             string     `json:"id"`
	PipelineID     string     `json:"pipeline_id"`
	DesignID       string     `json:"design_id"`
	TaskID         string     `json:"task_id"`
	Phase          string     `json:"phase"`
	Project        string     `json:"project"`
	Runtime        string     `json:"runtime"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	DurationSec    *int       `json:"duration_seconds,omitempty"`
	Model          *string    `json:"model,omitempty"`
	PromptChars    *int       `json:"prompt_chars,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	FilesChanged   *int       `json:"files_changed,omitempty"`
	LinesAdded     *int       `json:"lines_added,omitempty"`
	LinesRemoved   *int       `json:"lines_removed,omitempty"`
	Commits        *int       `json:"commits,omitempty"`
	PRURL          *string    `json:"pr_url,omitempty"`
	CompletionNote *string    `json:"completion_note,omitempty"`
	Status         string     `json:"status"`
	Error          *string    `json:"error,omitempty"`
	WorktreePath   *string    `json:"worktree_path,omitempty"`
	TmuxSession    *string    `json:"tmux_session,omitempty"`
	TmuxWindow     *string    `json:"tmux_window,omitempty"`
}

// GatePassRate holds first-try pass stats for a gate.
type GatePassRate struct {
	GateName     string  `json:"gate_name"`
	FirstTryPass int     `json:"first_try_pass"`
	TotalDesigns int     `json:"total_designs"`
	PassRate     float64 `json:"pass_rate"`
}
