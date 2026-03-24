-- Pipeline operational state tables
-- Moves pipeline tracking from shard metadata JSON into queryable relational tables

CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY,
    design_id TEXT NOT NULL REFERENCES shards(id),
    project TEXT NOT NULL,
    current_phase TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'blocked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(design_id)
);

CREATE TABLE IF NOT EXISTS pipeline_gates (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id),
    design_id TEXT NOT NULL REFERENCES shards(id),
    gate_name TEXT NOT NULL,
    phase TEXT NOT NULL,
    round INTEGER NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('pass', 'fail')),
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
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id),
    task_shard_id TEXT NOT NULL REFERENCES shards(id),
    design_id TEXT NOT NULL,
    wave INTEGER,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_design ON pipeline_runs(design_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project_status ON pipeline_runs(project, status);
CREATE INDEX IF NOT EXISTS idx_pipeline_gates_pipeline ON pipeline_gates(pipeline_id, created_at);
CREATE INDEX IF NOT EXISTS idx_pipeline_gates_design ON pipeline_gates(design_id, phase, round);
CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_pipeline ON pipeline_tasks(pipeline_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_design ON pipeline_tasks(design_id);
