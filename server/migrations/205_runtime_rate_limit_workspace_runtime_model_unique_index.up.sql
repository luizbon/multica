-- Enforces one row per (workspace_id, runtime_id, model) for the upsert
-- write path and backs both lookup shapes the read path needs: an exact
-- (workspace_id, runtime_id, model) check and a (workspace_id, runtime_id)
-- or workspace_id-only prefix scan for "list all currently-limited pairs".
-- Keep this as the migration's only statement: PostgreSQL rejects
-- CREATE INDEX CONCURRENTLY inside a transaction or multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_runtime_rate_limit_workspace_runtime_model
    ON runtime_rate_limit (workspace_id, runtime_id, model);
