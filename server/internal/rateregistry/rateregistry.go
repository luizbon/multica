// Package rateregistry is the rate-limit status registry for
// (workspace, runtime, model) triples (FORK-3), part of the rate-limit-
// avoidance epic started with FORK-2 (agent_fallback_target).
//
// This package is plumbing only: nothing calls SetRateLimited yet (that is
// the daemon rate-limit detection, a separate follow-up issue) and nothing
// calls IsRateLimited / ListActive yet (that is automatic failover
// orchestration, also a separate follow-up issue).
package rateregistry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SetRateLimited upserts the rate-limit status for a (workspaceID,
// runtimeID, model) triple, setting/extending rate_limited_until. Callers
// that don't track a specific model should pass "" for model, matching the
// runtime_rate_limit table's NOT NULL DEFAULT empty-string column.
//
// One row is kept per triple: calling this again for an already-limited
// pair overwrites rate_limited_until rather than accumulating history.
func SetRateLimited(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID, model string, until time.Time) error {
	return q.UpsertRuntimeRateLimit(ctx, db.UpsertRuntimeRateLimitParams{
		WorkspaceID:      workspaceID,
		RuntimeID:        runtimeID,
		Model:            model,
		RateLimitedUntil: pgtype.Timestamptz{Time: until.UTC(), Valid: true},
	})
}

// IsRateLimited reports whether a specific (runtimeID, model) pair is
// currently rate-limited for the workspace. Expiry is lazy: a row past
// rate_limited_until is simply not "currently" limited -- there is no
// active clear step, mirroring how this codebase already treats runtime
// online/offline via agent_runtime.last_seen_at staleness.
func IsRateLimited(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID, model string) (bool, error) {
	return q.IsRuntimeModelRateLimited(ctx, db.IsRuntimeModelRateLimitedParams{
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		Model:       model,
	})
}

// ListActiveForWorkspace lists every currently-active (not-yet-expired)
// rate limit across all runtimes in the workspace.
func ListActiveForWorkspace(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) ([]db.RuntimeRateLimit, error) {
	return q.ListActiveRuntimeRateLimits(ctx, db.ListActiveRuntimeRateLimitsParams{
		WorkspaceID: workspaceID,
	})
}

// ListActiveForRuntime lists every currently-active (not-yet-expired) rate
// limit for a single runtime in the workspace, across all its rate-limited
// models.
func ListActiveForRuntime(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID) ([]db.RuntimeRateLimit, error) {
	return q.ListActiveRuntimeRateLimits(ctx, db.ListActiveRuntimeRateLimitsParams{
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
	})
}

// DeleteExpiredBefore is the stretch cleanup sweep (mirrors the pattern in
// cmd/server/runtime_sweeper.go): it purges rows whose rate_limited_until
// is older than the given cutoff, purely for table hygiene. It is not
// required for correctness -- IsRateLimited and the List* helpers above
// already ignore expired rows via the rate_limited_until > now() predicate
// -- and it is not wired into any sweeper loop yet.
func DeleteExpiredBefore(ctx context.Context, q *db.Queries, cutoff time.Time) (int64, error) {
	return q.DeleteExpiredRuntimeRateLimits(ctx, pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true})
}
