ALTER TABLE pipeline_tasks
ADD COLUMN IF NOT EXISTS rebase_status TEXT NOT NULL DEFAULT 'none';
