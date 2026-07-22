-- Raw provider error text that FORK-4's rate-limit reset parser
-- (taskfailure.ParseRateLimitReset) could not parse. See migration 206/207.

-- name: CreateRateLimitParseFailure :one
-- Records a raw provider error string the parser gave up on, alongside the
-- runtime/model pair and a timestamp, so it can be reviewed and turned into
-- a new parser pattern.
INSERT INTO rate_limit_parse_failure (workspace_id, runtime_id, model, raw_error)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListRecentRateLimitParseFailures :many
-- Read-only list of the most recent unparsed rate-limit errors for a
-- workspace, newest first. No dashboard -- this backs a minimal CLI/API
-- read path only.
SELECT * FROM rate_limit_parse_failure
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2;
