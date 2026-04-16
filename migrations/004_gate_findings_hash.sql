-- cb-f55aa0: track findings hash for review escalation.
-- When consecutive review rounds produce the same hash, process-review
-- blocks the pipeline instead of burning retry budget.
ALTER TABLE IF EXISTS pipeline_gates ADD COLUMN IF NOT EXISTS findings_hash TEXT;
