-- +goose Up
-- Drop FK from pipeline_runs.design_id to shards(id).
-- design_id is a logical reference to a work item in ANY connector (CP, Beads, etc.),
-- not just the shards table. The FK only worked with Context Palace.
ALTER TABLE pipeline_runs DROP CONSTRAINT IF EXISTS pipeline_runs_design_id_fkey;

-- +goose Down
-- Re-add the FK (only valid if all design_ids exist in shards)
ALTER TABLE pipeline_runs ADD CONSTRAINT pipeline_runs_design_id_fkey FOREIGN KEY (design_id) REFERENCES shards(id);
