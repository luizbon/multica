-- Backs the read-only "list recent unparsed rate-limit errors" query.
-- Keep this as the migration's only statement: PostgreSQL rejects
-- CREATE INDEX CONCURRENTLY inside a transaction or multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rate_limit_parse_failure_workspace_created
    ON rate_limit_parse_failure (workspace_id, created_at DESC);
