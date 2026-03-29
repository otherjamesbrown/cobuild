-- +goose Up
-- Drop all FK constraints from CoBuild tables to shards(id).
-- design_id and task_shard_id are logical references to work items in ANY
-- connector (Context Palace, Beads, etc.), not just the shards table.
-- The FKs only worked with Context Palace.
ALTER TABLE pipeline_runs DROP CONSTRAINT IF EXISTS pipeline_runs_design_id_fkey;
ALTER TABLE pipeline_gates DROP CONSTRAINT IF EXISTS pipeline_gates_design_id_fkey;
ALTER TABLE pipeline_tasks DROP CONSTRAINT IF EXISTS pipeline_tasks_task_shard_id_fkey;

-- +goose Down
ALTER TABLE pipeline_runs ADD CONSTRAINT pipeline_runs_design_id_fkey FOREIGN KEY (design_id) REFERENCES shards(id);
ALTER TABLE pipeline_gates ADD CONSTRAINT pipeline_gates_design_id_fkey FOREIGN KEY (design_id) REFERENCES shards(id);
ALTER TABLE pipeline_tasks ADD CONSTRAINT pipeline_tasks_task_shard_id_fkey FOREIGN KEY (task_shard_id) REFERENCES shards(id);
