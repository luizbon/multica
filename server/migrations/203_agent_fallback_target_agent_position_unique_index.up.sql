-- Enforces one fallback slot per (agent, position) and backs the ordered
-- lookup by agent_id. Keep this as the migration's only statement:
-- PostgreSQL rejects CREATE INDEX CONCURRENTLY inside a transaction or
-- multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_fallback_target_agent_position
    ON agent_fallback_target (agent_id, position);
