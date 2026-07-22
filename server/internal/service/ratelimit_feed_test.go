package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// rateLimitEvidenceProjectID (ratelimit_feed.go) hardcodes a pointer to this
// fork's real "Multica Dev" project. issue.project_id carries a real FK to
// project(id) (migration 034), so the backlog-issue half of
// captureUnparsedRateLimitError needs that row present or the insert fails
// (best-effort: logged and swallowed, no issue created) -- it does not
// exist in a fresh test database. Seeding it under the fixture workspace
// lets seedAttributionFixture's own workspace cascade-delete clean it up.
func seedRateLimitEvidenceProject(t *testing.T, pool *pgxpool.Pool, workspaceID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO project (id, workspace_id, title)
		VALUES ('2fc3664e-f38d-4e98-8ca5-bc94f97eab40', $1, 'Multica Dev (test fixture)')
		ON CONFLICT (id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id
	`, workspaceID); err != nil {
		t.Fatalf("seed rate-limit evidence project: %v", err)
	}
}

func fixtureRuntimeID(t *testing.T, pool *pgxpool.Pool, agentID string) string {
	t.Helper()
	var runtimeID string
	if err := pool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime: %v", err)
	}
	return runtimeID
}

// newRateLimitFeedTask builds an in-memory AgentTaskQueue row (never
// inserted -- feedRateLimitRegistry only reads task.ID/RuntimeID/IssueID/
// AgentID off the struct and resolves the workspace via task.IssueID ->
// issue.workspace_id) wired to the given fixture issue/runtime/agent.
func newRateLimitFeedTask(issueID, runtimeID, agentID string) db.AgentTaskQueue {
	return db.AgentTaskQueue{
		ID:        util.MustParseUUID(uuid.NewString()),
		IssueID:   util.MustParseUUID(issueID),
		RuntimeID: util.MustParseUUID(runtimeID),
		AgentID:   util.MustParseUUID(agentID),
	}
}

// TestFeedRateLimitRegistrySuccessfulParse pins the successful-parse path:
// the registry is upserted with the parsed reset time, and neither the
// raw-text capture table nor a backlog issue gets a new row.
func TestFeedRateLimitRegistrySuccessfulParse(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	seedRateLimitEvidenceProject(t, pool, workspaceID)

	svc := &TaskService{Queries: q, TxStarter: pool}
	task := newRateLimitFeedTask(issueID, runtimeID, agentID)
	const model = "claude-opus-4-8"

	future := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	errMsg := fmt.Sprintf("429 Too Many Requests: reset_at=%s", future.UTC().Format(time.RFC3339))

	svc.feedRateLimitRegistry(ctx, task, errMsg, model)

	var until time.Time
	if err := pool.QueryRow(ctx,
		`SELECT rate_limited_until FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		workspaceID, runtimeID, model).Scan(&until); err != nil {
		t.Fatalf("read registry row: %v", err)
	}
	if diff := until.Sub(future.UTC()); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("rate_limited_until = %v, want ~%v", until, future.UTC())
	}

	var failureCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rate_limit_parse_failure WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		workspaceID, runtimeID, model).Scan(&failureCount); err != nil {
		t.Fatalf("count parse failures: %v", err)
	}
	if failureCount != 0 {
		t.Errorf("rate_limit_parse_failure rows = %d, want 0 (parse succeeded)", failureCount)
	}

	var issueCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue WHERE workspace_id = $1 AND title LIKE 'Unparsed rate-limit error observed%'`,
		workspaceID).Scan(&issueCount); err != nil {
		t.Fatalf("count backlog issues: %v", err)
	}
	if issueCount != 0 {
		t.Errorf("backlog issues created = %d, want 0 (parse succeeded, no evidence to capture)", issueCount)
	}
}

// TestFeedRateLimitRegistryFailedParseCapturesEvidence pins the
// failed-parse path end to end: the registry still gets a row (the fixed 4h
// fallback), the raw error text is captured in rate_limit_parse_failure,
// and a backlog issue is opened in the evidence project with the raw text
// plus runtime_id/model in its body.
func TestFeedRateLimitRegistryFailedParseCapturesEvidence(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	seedRateLimitEvidenceProject(t, pool, workspaceID)

	svc := &TaskService{Queries: q, TxStarter: pool}
	task := newRateLimitFeedTask(issueID, runtimeID, agentID)
	const model = "gpt-6-preview"
	const errMsg = "529 overloaded_error: the upstream provider gave no reset hint at all"

	before := time.Now()
	svc.feedRateLimitRegistry(ctx, task, errMsg, model)

	var until time.Time
	if err := pool.QueryRow(ctx,
		`SELECT rate_limited_until FROM runtime_rate_limit WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		workspaceID, runtimeID, model).Scan(&until); err != nil {
		t.Fatalf("read registry row: %v", err)
	}
	wantUntil := before.Add(4 * time.Hour)
	if diff := until.Sub(wantUntil); diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("rate_limited_until = %v, want ~%v (fixed 4h fallback)", until, wantUntil)
	}

	var gotRawError, gotModel string
	var failureCount int
	rows, err := pool.Query(ctx,
		`SELECT raw_error, model FROM rate_limit_parse_failure WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		workspaceID, runtimeID, model)
	if err != nil {
		t.Fatalf("query parse failures: %v", err)
	}
	for rows.Next() {
		failureCount++
		if err := rows.Scan(&gotRawError, &gotModel); err != nil {
			t.Fatalf("scan parse failure row: %v", err)
		}
	}
	rows.Close()
	if failureCount != 1 {
		t.Fatalf("rate_limit_parse_failure rows = %d, want 1", failureCount)
	}
	if gotRawError != errMsg {
		t.Errorf("raw_error = %q, want %q", gotRawError, errMsg)
	}
	if gotModel != model {
		t.Errorf("model = %q, want %q", gotModel, model)
	}

	var title, description, status, projectID, creatorType, creatorID string
	if err := pool.QueryRow(ctx, `
		SELECT title, description, status, project_id::text, creator_type, creator_id::text
		FROM issue
		WHERE workspace_id = $1 AND title LIKE 'Unparsed rate-limit error observed%'
	`, workspaceID).Scan(&title, &description, &status, &projectID, &creatorType, &creatorID); err != nil {
		t.Fatalf("read created backlog issue: %v", err)
	}
	if status != "backlog" {
		t.Errorf("issue status = %q, want backlog", status)
	}
	if projectID != "2fc3664e-f38d-4e98-8ca5-bc94f97eab40" {
		t.Errorf("issue project_id = %q, want the Multica Dev evidence project", projectID)
	}
	if creatorType != "agent" {
		t.Errorf("issue creator_type = %q, want agent", creatorType)
	}
	if creatorID != agentID {
		t.Errorf("issue creator_id = %q, want %q (task.AgentID)", creatorID, agentID)
	}
	if !strings.Contains(description, errMsg) {
		t.Errorf("issue description does not contain the raw error text:\n%s", description)
	}
	if !strings.Contains(description, runtimeID) {
		t.Errorf("issue description does not contain runtime_id %q:\n%s", runtimeID, description)
	}
	if !strings.Contains(description, model) {
		t.Errorf("issue description does not contain model %q:\n%s", model, description)
	}
}

// TestFeedRateLimitRegistryFailedParseWithoutTxStarter pins the
// degraded-but-safe half of the failed-parse path: when TxStarter is nil
// (e.g. a caller that never wired issue creation), the raw-text capture row
// still lands, but no backlog issue is attempted since there is no
// transaction to run IncrementIssueCounter / NextTopPosition / CreateIssue
// in.
func TestFeedRateLimitRegistryFailedParseWithoutTxStarter(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)

	svc := &TaskService{Queries: q} // no TxStarter
	task := newRateLimitFeedTask(issueID, runtimeID, agentID)
	const model = "no-tx-model"
	const errMsg = "unrecognized provider error with no reset hint"

	svc.feedRateLimitRegistry(ctx, task, errMsg, model)

	var failureCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rate_limit_parse_failure WHERE workspace_id = $1 AND runtime_id = $2 AND model = $3`,
		workspaceID, runtimeID, model).Scan(&failureCount); err != nil {
		t.Fatalf("count parse failures: %v", err)
	}
	if failureCount != 1 {
		t.Errorf("rate_limit_parse_failure rows = %d, want 1 (still captured without TxStarter)", failureCount)
	}

	var issueCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue WHERE workspace_id = $1 AND title LIKE 'Unparsed rate-limit error observed%'`,
		workspaceID).Scan(&issueCount); err != nil {
		t.Fatalf("count backlog issues: %v", err)
	}
	if issueCount != 0 {
		t.Errorf("backlog issues created = %d, want 0 (no TxStarter available)", issueCount)
	}
}

// TestFeedRateLimitRegistryUnresolvableWorkspaceIsNoop guards the early-out
// when the workspace can't be resolved at all (e.g. a task whose issue link
// is missing): feedRateLimitRegistry must log and return rather than
// upserting a registry row keyed by an invalid/empty workspace.
func TestFeedRateLimitRegistryUnresolvableWorkspaceIsNoop(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, _ := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)

	svc := &TaskService{Queries: q, TxStarter: pool}
	task := db.AgentTaskQueue{
		ID:        util.MustParseUUID(uuid.NewString()),
		RuntimeID: util.MustParseUUID(runtimeID),
		AgentID:   util.MustParseUUID(agentID),
		// IssueID/ChatSessionID/AutopilotRunID all left zero-value (invalid),
		// so ResolveTaskWorkspaceID has nothing to resolve from.
	}

	svc.feedRateLimitRegistry(ctx, task, "429 too many requests", "some-model")

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_rate_limit WHERE runtime_id = $1`, runtimeID).Scan(&count); err != nil {
		t.Fatalf("count registry rows: %v", err)
	}
	if count != 0 {
		t.Errorf("runtime_rate_limit rows = %d, want 0 (workspace unresolvable, should no-op)", count)
	}
}
