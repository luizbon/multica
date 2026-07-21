-- Per-agent ordered fallback (runtime, model) targets (FORK-2).
--
-- An agent currently has a single fixed runtime_id + optional model. To
-- support rate-limit avoidance (a runtime like Antigravity rate-limits per
-- model, not globally; Claude Code's Fable model has its own separate
-- limit), an agent gains an ordered list of fallback (runtime, model) pairs
-- to try when its primary target is unavailable. This migration is data-model
-- only — storing and exposing the config. Automatic use of it (detecting
-- rate limits and actually failing over) is later work.
--
-- NO foreign keys by design (this repo's migration rule, matching the
-- MUL-3963 agent_invocation_target precedent): relationships are maintained
-- in the application layer. agent_id is cleaned up alongside agent
-- hard-deletes (DeleteAgentFallbackTargetsByArchivedRuntimeAgents runs
-- before DeleteArchivedAgentsByRuntime in the runtime-delete tx, mirroring
-- the invocation-target cleanup). runtime_id is not cleaned up on runtime
-- deletion — a fallback pointing at a since-deleted runtime is inert (the
-- consuming failover logic, added in a later issue, must tolerate a
-- runtime_id that no longer resolves).
--
-- position is the fallback priority (0 = tried first). No index/unique
-- constraint here: this repo requires CREATE [UNIQUE] INDEX CONCURRENTLY,
-- which cannot share a migration file with other statements — the ordering
-- unique index is added in the next migration.
CREATE TABLE agent_fallback_target (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id    UUID NOT NULL,
    position    INT NOT NULL,
    runtime_id  UUID NOT NULL,
    model       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE agent_fallback_target IS
    'Ordered fallback (runtime_id, model) pairs an agent tries when its primary runtime_id is unavailable (FORK-2). position = priority (0 first). Not yet consumed by the daemon or task orchestration. No DB foreign keys: agent_id / runtime_id relationships are maintained in the application layer (see migration comment).';
