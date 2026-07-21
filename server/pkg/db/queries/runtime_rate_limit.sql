-- Rate-limit status registry keyed by (workspace_id, runtime_id, model)
-- (FORK-3). Rows record that a runtime/model pair is currently
-- rate-limited and when that limit resets. See migration 204/205.

-- name: UpsertRuntimeRateLimit :exec
-- Sets/extends rate_limited_until for a (workspace_id, runtime_id, model)
-- triple. One row per triple: re-upserting an already-limited pair
-- overwrites rate_limited_until rather than accumulating history.
INSERT INTO runtime_rate_limit (workspace_id, runtime_id, model, rate_limited_until)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, runtime_id, model) DO UPDATE SET
    rate_limited_until = EXCLUDED.rate_limited_until,
    updated_at = now();

-- name: IsRuntimeModelRateLimited :one
-- Reports whether a specific (runtime_id, model) pair is currently
-- rate-limited for the workspace. Expiry is lazy: a row past
-- rate_limited_until is simply not "currently" limited, with no active
-- clear step (mirrors agent_runtime.last_seen_at staleness checks).
SELECT EXISTS (
    SELECT 1 FROM runtime_rate_limit
    WHERE workspace_id = $1
      AND runtime_id = $2
      AND model = $3
      AND rate_limited_until > now()
) AS is_rate_limited;

-- name: ListActiveRuntimeRateLimits :many
-- Lists every currently-active (not-yet-expired) rate limit for a
-- workspace, optionally narrowed to a single runtime_id. Passing a NULL/
-- invalid runtime_id filter returns all runtimes in the workspace.
SELECT * FROM runtime_rate_limit
WHERE workspace_id = $1
  AND (sqlc.narg('runtime_id')::uuid IS NULL OR runtime_id = sqlc.narg('runtime_id'))
  AND rate_limited_until > now()
ORDER BY runtime_id, model;

-- name: DeleteExpiredRuntimeRateLimits :execrows
-- Stretch cleanup sweep (mirrors runtime_sweeper.go): purges rows whose
-- rate_limited_until is well in the past, purely for table hygiene. Not
-- required for correctness -- ListActiveRuntimeRateLimits and
-- IsRuntimeModelRateLimited already ignore expired rows via the
-- rate_limited_until > now() predicate. Not wired to any caller yet.
DELETE FROM runtime_rate_limit
WHERE rate_limited_until < @older_than::timestamptz;
