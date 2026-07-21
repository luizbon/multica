package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParseFallbackTargetsInput covers the validation rules FORK-2 imposes on
// each fallback entry: runtime_id is required and must resolve to a runtime
// in the caller's workspace (same rule CreateAgent/UpdateAgent apply to the
// primary runtime_id); model is stored as-is with no format validation.
func TestParseFallbackTargetsInput(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsUUID := parseUUID(testWorkspaceID)
	runtimeID := handlerTestRuntimeID(t)

	t.Run("empty runtime_id is rejected", func(t *testing.T) {
		_, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{{RuntimeID: ""}})
		if err == nil {
			t.Fatal("expected error for empty runtime_id")
		}
	})

	t.Run("malformed uuid is rejected", func(t *testing.T) {
		_, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{{RuntimeID: "not-a-uuid"}})
		if err == nil {
			t.Fatal("expected error for malformed runtime_id")
		}
	})

	t.Run("well-formed uuid that resolves to no runtime in this workspace is rejected", func(t *testing.T) {
		_, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{
			{RuntimeID: "99999999-9999-9999-9999-999999999999"},
		})
		if err == nil {
			t.Fatal("expected error for runtime not in workspace")
		}
	})

	t.Run("valid entry without model normalises to an invalid/absent pgtype.Text", func(t *testing.T) {
		specs, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{
			{RuntimeID: runtimeID},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(specs))
		}
		if specs[0].model.Valid {
			t.Errorf("expected model to be absent, got %+v", specs[0].model)
		}
	})

	t.Run("valid entry with model is preserved and order is kept", func(t *testing.T) {
		model := "gpt-fallback"
		specs, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{
			{RuntimeID: runtimeID},
			{RuntimeID: runtimeID, Model: &model},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 2 {
			t.Fatalf("expected 2 specs, got %d", len(specs))
		}
		if specs[0].model.Valid {
			t.Errorf("first spec model should be absent, got %+v", specs[0].model)
		}
		if !specs[1].model.Valid || specs[1].model.String != model {
			t.Errorf("second spec model = %+v, want %q", specs[1].model, model)
		}
	})

	t.Run("empty string model is treated as absent, matching the primary model field's contract", func(t *testing.T) {
		empty := ""
		specs, err := testHandler.parseFallbackTargetsInput(ctx, wsUUID, []AgentFallbackTargetDTO{
			{RuntimeID: runtimeID, Model: &empty},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if specs[0].model.Valid {
			t.Errorf("expected empty-string model to normalise to absent, got %+v", specs[0].model)
		}
	})
}

// TestCreateAgent_PersistsFallbackTargetsInOrder exercises the create path
// end-to-end: fallback_targets in the request body must land as ordered rows
// and come back in the same order on both the create response and a
// subsequent GetAgent.
func TestCreateAgent_PersistsFallbackTargetsInOrder(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	model := "model-a"

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":       "Fallback Target Create Agent",
		"runtime_id": runtimeID,
		"fallback_targets": []map[string]any{
			{"runtime_id": runtimeID, "model": model},
			{"runtime_id": runtimeID},
		},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID) })

	assertOrderedFallbackTargets(t, created.FallbackTargets, runtimeID, &model)

	w = httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+created.ID, nil), "id", created.ID)
	testHandler.GetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var fetched AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	assertOrderedFallbackTargets(t, fetched.FallbackTargets, runtimeID, &model)
}

func assertOrderedFallbackTargets(t *testing.T, got []AgentFallbackTargetDTO, runtimeID string, firstModel *string) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("expected 2 fallback targets, got %d: %+v", len(got), got)
	}
	if got[0].RuntimeID != runtimeID {
		t.Errorf("first target runtime_id = %q, want %q", got[0].RuntimeID, runtimeID)
	}
	if got[0].Model == nil || *got[0].Model != *firstModel {
		t.Errorf("first target model = %v, want %q", got[0].Model, *firstModel)
	}
	if got[1].RuntimeID != runtimeID {
		t.Errorf("second target runtime_id = %q, want %q", got[1].RuntimeID, runtimeID)
	}
	if got[1].Model != nil {
		t.Errorf("second target model = %v, want nil", got[1].Model)
	}
}

// TestCreateAgent_RejectsInvalidFallbackTargetRuntime guards that a bad
// fallback runtime_id fails the whole create (400) and — because validation
// happens before the insert transaction — leaves no partial agent row behind.
func TestCreateAgent_RejectsInvalidFallbackTargetRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	const agentName = "Fallback Target Invalid Runtime Agent"

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":       agentName,
		"runtime_id": runtimeID,
		"fallback_targets": []map[string]any{
			{"runtime_id": "99999999-9999-9999-9999-999999999999"},
		},
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, agentName,
	).Scan(&count); err != nil {
		t.Fatalf("count agent rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no agent row after rejected create, found %d", count)
	}
}

// TestUpdateAgent_FallbackTargetsTriState mirrors the tri-state contract
// UpdateAgentRequest documents for FallbackTargets (same pattern as
// mcp_config/thinking_level): omitted means no change, present (including an
// empty list) means wholesale replace.
func TestUpdateAgent_FallbackTargetsTriState(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "Fallback Target Tri-State Agent", nil)

	update := func(body map[string]any) AgentResponse {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, body), "id", agentID)
		testHandler.UpdateAgent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp AgentResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode update response: %v", err)
		}
		return resp
	}

	// Seed an initial fallback target list.
	seeded := update(map[string]any{
		"fallback_targets": []map[string]any{{"runtime_id": runtimeID}},
	})
	if len(seeded.FallbackTargets) != 1 {
		t.Fatalf("expected 1 seeded fallback target, got %+v", seeded.FallbackTargets)
	}

	// Omitted field: an unrelated update must leave the list untouched.
	unchanged := update(map[string]any{"description": "tri-state omission probe"})
	if len(unchanged.FallbackTargets) != 1 {
		t.Fatalf("omitted fallback_targets must not change the list; got %+v", unchanged.FallbackTargets)
	}

	// Present with a new list: wholesale replace, order preserved.
	modelB := "model-b"
	replaced := update(map[string]any{
		"fallback_targets": []map[string]any{
			{"runtime_id": runtimeID, "model": modelB},
			{"runtime_id": runtimeID},
		},
	})
	if len(replaced.FallbackTargets) != 2 {
		t.Fatalf("expected 2 fallback targets after replace, got %+v", replaced.FallbackTargets)
	}
	if replaced.FallbackTargets[0].Model == nil || *replaced.FallbackTargets[0].Model != modelB {
		t.Errorf("first replaced target model = %v, want %q", replaced.FallbackTargets[0].Model, modelB)
	}
	if replaced.FallbackTargets[1].Model != nil {
		t.Errorf("second replaced target model = %v, want nil", replaced.FallbackTargets[1].Model)
	}

	// Present with an explicit empty list: clears the whole thing.
	cleared := update(map[string]any{"fallback_targets": []map[string]any{}})
	if len(cleared.FallbackTargets) != 0 {
		t.Fatalf("expected empty fallback_targets after explicit clear, got %+v", cleared.FallbackTargets)
	}
}

// TestListAgents_IncludesFallbackTargetsBatchLoaded exercises
// loadFallbackTargetsByAgent, the N+1-avoidance batch loader ListAgents uses:
// an agent with fallback targets must show them in position order, and an
// agent with none must come back with an empty (non-nil) slice rather than
// leaking another agent's rows.
func TestListAgents_IncludesFallbackTargetsBatchLoaded(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	withTargets := createHandlerTestAgent(t, "Fallback Target List Agent With Targets", nil)
	withoutTargets := createHandlerTestAgent(t, "Fallback Target List Agent Without Targets", nil)

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fallback_target (agent_id, position, runtime_id, model)
		VALUES ($1, 0, $2, NULL), ($1, 1, $2, 'model-list')
	`, withTargets, runtimeID); err != nil {
		t.Fatalf("seed fallback targets: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.ListAgents(w, newRequest(http.MethodGet, "/api/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed []AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode agent list: %v", err)
	}

	byID := map[string]AgentResponse{}
	for _, a := range listed {
		byID[a.ID] = a
	}

	got, ok := byID[withTargets]
	if !ok {
		t.Fatalf("agent with targets missing from list")
	}
	if len(got.FallbackTargets) != 2 {
		t.Fatalf("expected 2 fallback targets, got %+v", got.FallbackTargets)
	}
	if got.FallbackTargets[0].Model != nil {
		t.Errorf("first target model = %v, want nil", got.FallbackTargets[0].Model)
	}
	if got.FallbackTargets[1].Model == nil || *got.FallbackTargets[1].Model != "model-list" {
		t.Errorf("second target model = %v, want model-list", got.FallbackTargets[1].Model)
	}

	gotEmpty, ok := byID[withoutTargets]
	if !ok {
		t.Fatalf("agent without targets missing from list")
	}
	if len(gotEmpty.FallbackTargets) != 0 {
		t.Fatalf("expected no fallback targets, got %+v", gotEmpty.FallbackTargets)
	}
}

// TestArchiveAgentsAndDeleteRuntime_CleansUpFallbackTargets covers the FORK-2
// hard-delete cleanup path: agent_fallback_target has no agent_id FK, so
// DeleteAgentFallbackTargetsByArchivedRuntimeAgents must run in the same
// transaction as the archived-agent hard-delete or the rows would survive as
// orphans referencing a deleted agent.
func TestArchiveAgentsAndDeleteRuntime_CleansUpFallbackTargets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)

	cascadeRuntimeID := createCascadeFixtureRuntime(t, ctx, "Fallback Cleanup Cascade Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, cascadeRuntimeID, "Fallback Cleanup Cascade Agent")

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fallback_target (agent_id, position, runtime_id, model)
		VALUES ($1, 0, $2, 'model-cleanup')
	`, agentID, runtimeID); err != nil {
		t.Fatalf("seed fallback target: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/runtimes/"+cascadeRuntimeID+"/archive-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{agentID}})
	req = withURLParam(req, "runtimeId", cascadeRuntimeID)
	testHandler.ArchiveAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var targetRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_fallback_target WHERE agent_id = $1`, agentID).Scan(&targetRows); err != nil {
		t.Fatalf("count fallback target rows: %v", err)
	}
	if targetRows != 0 {
		t.Fatalf("expected fallback target rows to be cleaned up, found %d", targetRows)
	}
}

// TestDeleteAgentRuntime_CleansUpFallbackTargetsForArchivedAgent covers the
// same FORK-2 cleanup on the direct (non-cascade) delete path: a runtime with
// only already-archived agents can be deleted directly, and its archived
// agents' fallback target rows must not survive as orphans.
func TestDeleteAgentRuntime_CleansUpFallbackTargetsForArchivedAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)

	cascadeRuntimeID := createCascadeFixtureRuntime(t, ctx, "Fallback Cleanup Direct Delete Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, cascadeRuntimeID, "Fallback Cleanup Direct Delete Agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive fixture agent: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fallback_target (agent_id, position, runtime_id, model)
		VALUES ($1, 0, $2, NULL)
	`, agentID, runtimeID); err != nil {
		t.Fatalf("seed fallback target: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/runtimes/"+cascadeRuntimeID, nil)
	req = withURLParam(req, "runtimeId", cascadeRuntimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var targetRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_fallback_target WHERE agent_id = $1`, agentID).Scan(&targetRows); err != nil {
		t.Fatalf("count fallback target rows: %v", err)
	}
	if targetRows != 0 {
		t.Fatalf("expected fallback target rows to be cleaned up, found %d", targetRows)
	}
}
