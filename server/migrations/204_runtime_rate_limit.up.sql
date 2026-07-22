-- Rate-limit status registry for (workspace, runtime, model) triples
-- (FORK-3), part of the rate-limit-avoidance epic started with FORK-2
-- (agent_fallback_target). This migration is registry plumbing only: it
-- stores rate_limited_until so a future orchestration step (a later issue)
-- can skip unavailable fallback targets. Nothing writes to this table yet
-- (daemon rate-limit detection is a separate follow-up issue) and nothing
-- reads from it yet (automatic failover orchestration, also separate).
--
-- Expiry is lazy: a row is "currently rate-limited" iff
-- rate_limited_until > now(), checked at read time. There is no active
-- "clear" step, mirroring how this codebase already treats runtime
-- online/offline via agent_runtime.last_seen_at staleness rather than a
-- toggled flag.
--
-- One row per (workspace_id, runtime_id, model): the write path is an
-- upsert that extends rate_limited_until in place rather than
-- accumulating history. model is NOT NULL DEFAULT '' (matching
-- runtime_usage's convention) so a runtime-wide limit with no specific
-- model still participates in the uniqueness constraint predictably
-- (Postgres would otherwise treat multiple NULLs as distinct).
--
-- NO foreign keys by design (this repo's migration rule): workspace_id and
-- runtime_id relationships are maintained in the application layer. A
-- rate_limited_until row pointing at a since-deleted runtime is simply
-- inert -- the read path only ever asks "is (runtime_id, model) currently
-- limited", and an unknown runtime_id never matches a live fallback
-- candidate.
--
-- No index/unique constraint here: this repo requires
-- CREATE [UNIQUE] INDEX CONCURRENTLY, which cannot share a migration file
-- with other statements -- the uniqueness + lookup index is added in the
-- next migration.
CREATE TABLE runtime_rate_limit (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID NOT NULL,
    runtime_id          UUID NOT NULL,
    model               TEXT NOT NULL DEFAULT '',
    rate_limited_until  TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE runtime_rate_limit IS
    'Rate-limit status registry keyed by (workspace_id, runtime_id, model) (FORK-3). rate_limited_until is compared to now() lazily at read time; there is no active clear step. Not yet written or read by any caller. No DB foreign keys: workspace_id / runtime_id relationships are maintained in the application layer (see migration comment).';
