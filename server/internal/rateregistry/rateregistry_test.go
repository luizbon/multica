package rateregistry_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/rateregistry"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// These tests exercise the FORK-3 rate-limit registry (rateregistry.go plus
// the generated runtime_rate_limit queries) against a live Postgres with
// migrations 204/205 applied -- the same migration-runner + live-Postgres
// approach CAPCOM used to verify the implementation. They connect to
// DATABASE_URL (default postgres://multica:multica@localhost:5432/multica
// ?sslmode=disable), matching every other live-Postgres suite in this repo,
// and skip cleanly when no database is reachable so CI without a DB sees
// SKIP, not failure.
//
// runtime_rate_limit has no foreign keys by design (see migration 204's
// comment), so tests use fresh random workspace/runtime UUIDs per test
// rather than seeding real workspace/agent_runtime rows, mirroring
// server/internal/integrations/lark/channel_cleanup_test.go.

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("could not connect to %s: %v", dbURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database not reachable at %s: %v", dbURL, err)
	}
	return pool
}

func TestSetRateLimited_UpsertOverwritesInPlace(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	runtimeID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
	})

	first := time.Now().UTC().Add(1 * time.Hour).Truncate(time.Microsecond)
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "gpt-5", first); err != nil {
		t.Fatalf("SetRateLimited (initial): %v", err)
	}

	var rowCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		wsID, runtimeID, "gpt-5").Scan(&rowCount); err != nil {
		t.Fatalf("count after initial upsert: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("rowCount after initial upsert = %d, want 1", rowCount)
	}

	// Re-upserting the same triple must overwrite rate_limited_until in
	// place, not accumulate a second row.
	second := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "gpt-5", second); err != nil {
		t.Fatalf("SetRateLimited (overwrite): %v", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		wsID, runtimeID, "gpt-5").Scan(&rowCount); err != nil {
		t.Fatalf("count after overwrite: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("rowCount after overwrite = %d, want 1 (upsert must overwrite, not accumulate)", rowCount)
	}

	var until time.Time
	if err := pool.QueryRow(ctx,
		`SELECT rate_limited_until FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		wsID, runtimeID, "gpt-5").Scan(&until); err != nil {
		t.Fatalf("read rate_limited_until: %v", err)
	}
	if !until.UTC().Equal(second) {
		t.Errorf("rate_limited_until = %s, want %s (overwritten value)", until.UTC(), second)
	}
}

func TestSetRateLimited_DistinctModelsGetDistinctRows(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	runtimeID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
	})

	until := time.Now().UTC().Add(1 * time.Hour)
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "gpt-5", until); err != nil {
		t.Fatalf("SetRateLimited gpt-5: %v", err)
	}
	// "" is the no-specific-model sentinel (NOT NULL DEFAULT '').
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "", until); err != nil {
		t.Fatalf("SetRateLimited '': %v", err)
	}

	var rowCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2`,
		wsID, runtimeID).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("rowCount = %d, want 2 (distinct models are distinct rows)", rowCount)
	}
}

func TestIsRateLimited_LazyExpiry(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	runtimeID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
	})

	// Never limited: no row at all.
	limited, err := rateregistry.IsRateLimited(ctx, q, wsID, runtimeID, "gpt-5")
	if err != nil {
		t.Fatalf("IsRateLimited (no row): %v", err)
	}
	if limited {
		t.Errorf("IsRateLimited (no row) = true, want false")
	}

	// Currently limited: rate_limited_until in the future.
	future := time.Now().UTC().Add(1 * time.Hour)
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "gpt-5", future); err != nil {
		t.Fatalf("SetRateLimited (future): %v", err)
	}
	limited, err = rateregistry.IsRateLimited(ctx, q, wsID, runtimeID, "gpt-5")
	if err != nil {
		t.Fatalf("IsRateLimited (future): %v", err)
	}
	if !limited {
		t.Errorf("IsRateLimited (future) = false, want true")
	}

	// Expired: rate_limited_until in the past. Expiry is lazy -- the row
	// still exists but must no longer read as "currently" limited. There
	// is no active clear step, so we set it directly rather than via
	// SetRateLimited (which would just re-extend it).
	past := time.Now().UTC().Add(-1 * time.Hour)
	if _, err := pool.Exec(ctx,
		`UPDATE runtime_rate_limit SET rate_limited_until = $1 WHERE workspace_id = $2 AND runtime_id = $3 AND model = $4`,
		past, wsID, runtimeID, "gpt-5"); err != nil {
		t.Fatalf("force-expire row: %v", err)
	}
	limited, err = rateregistry.IsRateLimited(ctx, q, wsID, runtimeID, "gpt-5")
	if err != nil {
		t.Fatalf("IsRateLimited (expired): %v", err)
	}
	if limited {
		t.Errorf("IsRateLimited (expired) = true, want false (lazy expiry)")
	}

	// A different model on the same runtime is unaffected.
	limited, err = rateregistry.IsRateLimited(ctx, q, wsID, runtimeID, "gpt-4")
	if err != nil {
		t.Fatalf("IsRateLimited (other model): %v", err)
	}
	if limited {
		t.Errorf("IsRateLimited (other model) = true, want false")
	}
}

func TestListActiveForWorkspace_ExcludesExpiredAndOtherWorkspaces(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	otherWsID := util.MustParseUUID(uuid.NewString())
	runtimeA := util.MustParseUUID(uuid.NewString())
	runtimeB := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(bg, `DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, otherWsID)
	})

	future := time.Now().UTC().Add(1 * time.Hour)
	past := time.Now().UTC().Add(-1 * time.Hour)

	// Active rows for the workspace under test, across two runtimes.
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeA, "gpt-5", future); err != nil {
		t.Fatalf("seed active A: %v", err)
	}
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeB, "gpt-4", future); err != nil {
		t.Fatalf("seed active B: %v", err)
	}
	// An expired row for the same workspace -- must not appear.
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeA, "expired-model", past); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	// An active row for a different workspace -- must not appear.
	if err := rateregistry.SetRateLimited(ctx, q, otherWsID, runtimeA, "gpt-5", future); err != nil {
		t.Fatalf("seed other workspace: %v", err)
	}

	rows, err := rateregistry.ListActiveForWorkspace(ctx, q, wsID)
	if err != nil {
		t.Fatalf("ListActiveForWorkspace: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2; rows=%+v", len(rows), rows)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.RateLimitedUntil.Time.Before(time.Now()) {
			t.Errorf("row %+v is expired but was returned as active", r)
		}
		seen[r.Model] = true
	}
	if !seen["gpt-5"] || !seen["gpt-4"] {
		t.Errorf("expected both gpt-5 and gpt-4 in results, got %+v", rows)
	}
}

func TestListActiveForRuntime_NarrowsToSingleRuntime(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	runtimeA := util.MustParseUUID(uuid.NewString())
	runtimeB := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
	})

	future := time.Now().UTC().Add(1 * time.Hour)
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeA, "gpt-5", future); err != nil {
		t.Fatalf("seed runtimeA: %v", err)
	}
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeB, "gpt-5", future); err != nil {
		t.Fatalf("seed runtimeB: %v", err)
	}

	rows, err := rateregistry.ListActiveForRuntime(ctx, q, wsID, runtimeA)
	if err != nil {
		t.Fatalf("ListActiveForRuntime: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (narrowed to runtimeA only); rows=%+v", len(rows), rows)
	}
	if rows[0].RuntimeID != runtimeA {
		t.Errorf("rows[0].RuntimeID = %+v, want %+v", rows[0].RuntimeID, runtimeA)
	}
}

func TestDeleteExpiredBefore_OnlyPurgesRowsOlderThanCutoff(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	q := db.New(pool)

	wsID := util.MustParseUUID(uuid.NewString())
	runtimeID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM runtime_rate_limit WHERE workspace_id = $1`, wsID)
	})

	now := time.Now().UTC()
	longExpired := now.Add(-48 * time.Hour)
	recentlyExpired := now.Add(-1 * time.Minute)
	stillActive := now.Add(1 * time.Hour)

	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "long-expired", longExpired); err != nil {
		t.Fatalf("seed long-expired: %v", err)
	}
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "recently-expired", recentlyExpired); err != nil {
		t.Fatalf("seed recently-expired: %v", err)
	}
	if err := rateregistry.SetRateLimited(ctx, q, wsID, runtimeID, "still-active", stillActive); err != nil {
		t.Fatalf("seed still-active: %v", err)
	}

	// Cutoff of "1 hour ago" should purge only long-expired, sparing both
	// the row expired a minute ago and the still-active row.
	cutoff := now.Add(-1 * time.Hour)
	n, err := rateregistry.DeleteExpiredBefore(ctx, q, cutoff)
	if err != nil {
		t.Fatalf("DeleteExpiredBefore: %v", err)
	}
	if n < 1 {
		t.Fatalf("DeleteExpiredBefore rowsAffected = %d, want >= 1", n)
	}

	var remainingModels []string
	rows, err := pool.Query(ctx,
		`SELECT model FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 ORDER BY model`,
		wsID, runtimeID)
	if err != nil {
		t.Fatalf("query remaining: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remainingModels = append(remainingModels, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	hasModel := func(m string) bool {
		for _, x := range remainingModels {
			if x == m {
				return true
			}
		}
		return false
	}
	if hasModel("long-expired") {
		t.Errorf("long-expired row should have been purged, remaining=%v", remainingModels)
	}
	if !hasModel("recently-expired") {
		t.Errorf("recently-expired row should NOT have been purged (newer than cutoff), remaining=%v", remainingModels)
	}
	if !hasModel("still-active") {
		t.Errorf("still-active row should NOT have been purged, remaining=%v", remainingModels)
	}
}
