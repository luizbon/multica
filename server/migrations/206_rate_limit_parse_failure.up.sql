-- Queryable capture of raw provider error text that FORK-4's rate-limit
-- reset parser (taskfailure.ParseRateLimitReset) could NOT parse. Every row
-- here paired with a runtime_rate_limit upsert used the fixed 4h fallback
-- cooldown instead of a parsed reset time -- this table is the evidence a
-- human/agent needs to add a new pattern to the parser, not an analysis of
-- the text itself.
--
-- NO foreign keys by design (this repo's migration rule): workspace_id and
-- runtime_id relationships are maintained in the application layer.
CREATE TABLE rate_limit_parse_failure (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    runtime_id  UUID NOT NULL,
    model       TEXT NOT NULL DEFAULT '',
    raw_error   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE rate_limit_parse_failure IS
    'Raw provider error text FORK-4''s rate-limit reset parser could not parse, captured alongside runtime_id/model/timestamp so a human/agent can teach taskfailure.ParseRateLimitReset the pattern. No DB foreign keys: workspace_id / runtime_id relationships are maintained in the application layer (see migration comment).';
