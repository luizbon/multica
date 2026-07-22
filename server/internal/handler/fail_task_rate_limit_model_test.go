package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func failTaskWithBody(t *testing.T, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/fail", body, testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	testHandler.FailTask(w, req)
	return w
}

// TestFailTaskThreadsModelIntoRateLimitRegistry is the end-to-end guard for
// FORK-4's model plumbing: TaskFailRequest.Model (server/internal/handler/
// daemon.go) must decode off the daemon's POST body and flow, untouched,
// through TaskService.FailTask into the FORK-3 rate-limit registry upsert
// whenever the failure classifies as agent_error.provider_capacity_or_rate_limit.
//
// This is the piece the 6 mechanically-updated FailTask call sites (which
// all pass model="") don't exercise: a real HTTP request carrying a
// non-empty model, ending in a registry row keyed by that exact model.
func TestFailTaskThreadsModelIntoRateLimitRegistry(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 AND runtime_id IS NOT NULL LIMIT 1`,
		testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, assignee_type, assignee_id)
		VALUES ($1, 'fail-task model plumbing fixture', 'in_progress', 'none', $2, 'member', 999098, 0, 'agent', $3)
		RETURNING id
	`, testWorkspaceID, testUserID, agentID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, created_at, started_at)
		VALUES ($1, $2, $3, 'running', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("setup: running task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM runtime_rate_limit WHERE runtime_id = $1 AND model = 'daemon-resolved-fallback-model'`, runtimeID)
	})

	future := time.Now().Add(90 * time.Minute).Truncate(time.Second)
	body := map[string]any{
		"error":          "429 Too Many Requests: reset_at=" + future.UTC().Format(time.RFC3339),
		"failure_reason": "agent_error.provider_capacity_or_rate_limit",
		"model":          "daemon-resolved-fallback-model",
	}
	w := failTaskWithBody(t, taskID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("FailTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var gotUntil time.Time
	var rowCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), coalesce(max(rate_limited_until), 'epoch'::timestamptz)
		FROM runtime_rate_limit
		WHERE workspace_id = $1 AND runtime_id = $2 AND model = 'daemon-resolved-fallback-model'
	`, testWorkspaceID, runtimeID).Scan(&rowCount, &gotUntil); err != nil {
		t.Fatalf("read registry row: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("runtime_rate_limit rows for model 'daemon-resolved-fallback-model' = %d, want 1", rowCount)
	}
	if diff := gotUntil.Sub(future.UTC()); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("rate_limited_until = %v, want ~%v (parsed from the request's error text)", gotUntil, future.UTC())
	}
}
