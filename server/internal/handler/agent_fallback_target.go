package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentFallbackTargetDTO is the wire shape of one ordered fallback
// (runtime, model) entry (FORK-2). List order is the fallback priority
// (first entry = tried first). Model is optional, matching the primary
// agent.model field's contract (empty = runtime default).
type AgentFallbackTargetDTO struct {
	RuntimeID string  `json:"runtime_id"`
	Model     *string `json:"model,omitempty"`
}

// fallbackTargetSpec is the normalised, DB-ready form of one fallback entry.
type fallbackTargetSpec struct {
	runtimeID pgtype.UUID
	model     pgtype.Text
}

// parseFallbackTargetsInput validates and normalises a fallback-target list
// off a create/update request. Each runtime_id is validated to resolve to a
// runtime in the given workspace — the same rule CreateAgent/UpdateAgent
// apply to the primary runtime_id — but is NOT checked against the
// private-runtime ownership gate (canUseRuntimeForAgent): a fallback entry
// only needs to be a valid workspace runtime, not one the caller is allowed
// to newly assign the agent to as primary. model is stored as-is, matching
// the existing top-level model field's contract (no format validation
// beyond runtime support, which is enforced at execution time, not here).
// List order is preserved as the fallback priority.
func (h *Handler) parseFallbackTargetsInput(ctx context.Context, workspaceID pgtype.UUID, targets []AgentFallbackTargetDTO) ([]fallbackTargetSpec, error) {
	specs := make([]fallbackTargetSpec, 0, len(targets))
	for i, t := range targets {
		if t.RuntimeID == "" {
			return nil, fmt.Errorf("fallback_targets[%d]: runtime_id is required", i)
		}
		runtimeUUID, err := util.ParseUUID(t.RuntimeID)
		if err != nil {
			return nil, fmt.Errorf("fallback_targets[%d]: runtime_id is not a valid uuid", i)
		}
		if _, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
			ID:          runtimeUUID,
			WorkspaceID: workspaceID,
		}); err != nil {
			return nil, fmt.Errorf("fallback_targets[%d]: runtime_id does not resolve to a runtime in this workspace", i)
		}
		model := pgtype.Text{}
		if t.Model != nil {
			model = pgtype.Text{String: *t.Model, Valid: *t.Model != ""}
		}
		specs = append(specs, fallbackTargetSpec{runtimeID: runtimeUUID, model: model})
	}
	return specs, nil
}

// replaceAgentFallbackTargetsWithQueries rewrites an agent's fallback target
// list wholesale: clear then re-insert in submitted order (position =
// index). The tx-friendly variant, mirroring
// replaceInvocationTargetsWithQueries — callers holding a
// `qtx := h.Queries.WithTx(tx)` pass it here so the rows land in the same
// transaction as the agent row.
func replaceAgentFallbackTargetsWithQueries(ctx context.Context, q *db.Queries, agentID pgtype.UUID, specs []fallbackTargetSpec) error {
	if err := q.DeleteAgentFallbackTargets(ctx, agentID); err != nil {
		return err
	}
	for i, s := range specs {
		if err := q.CreateAgentFallbackTarget(ctx, db.CreateAgentFallbackTargetParams{
			AgentID:   agentID,
			Position:  int32(i),
			RuntimeID: s.runtimeID,
			Model:     s.model,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) replaceAgentFallbackTargets(ctx context.Context, agentID pgtype.UUID, specs []fallbackTargetSpec) error {
	return replaceAgentFallbackTargetsWithQueries(ctx, h.Queries, agentID, specs)
}

// applyFallbackTargetsToResponse fills FallbackTargets from the loaded rows,
// preserving position order.
func applyFallbackTargetsToResponse(resp *AgentResponse, targets []db.AgentFallbackTarget) {
	dto := make([]AgentFallbackTargetDTO, 0, len(targets))
	for _, t := range targets {
		entry := AgentFallbackTargetDTO{RuntimeID: uuidToString(t.RuntimeID)}
		if t.Model.Valid {
			m := t.Model.String
			entry.Model = &m
		}
		dto = append(dto, entry)
	}
	resp.FallbackTargets = dto
}

// enrichAgentResponseWithFallbackTargets loads an agent's fallback targets
// (ordered by position) and applies them to the response. Used by the
// single-agent detail / create / update responses.
func (h *Handler) enrichAgentResponseWithFallbackTargets(ctx context.Context, resp *AgentResponse, agentID pgtype.UUID) error {
	targets, err := h.Queries.ListAgentFallbackTargets(ctx, agentID)
	if err != nil {
		return err
	}
	applyFallbackTargetsToResponse(resp, targets)
	return nil
}

// enrichAgentResponseWithFallbackTargetsHTTP is the HTTP-boundary wrapper
// that writes a 500 and returns false on failure.
func (h *Handler) enrichAgentResponseWithFallbackTargetsHTTP(w http.ResponseWriter, r *http.Request, resp *AgentResponse, agentID pgtype.UUID) bool {
	if err := h.enrichAgentResponseWithFallbackTargets(r.Context(), resp, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent fallback targets")
		return false
	}
	return true
}

// loadFallbackTargetsByAgent batch-loads fallback targets for a set of
// agents (avoids N+1 on the agent list endpoint), keyed by agent id and
// ordered by position within each agent's slice.
func (h *Handler) loadFallbackTargetsByAgent(ctx context.Context, agents []db.Agent) (map[string][]db.AgentFallbackTarget, bool) {
	ids := make([]pgtype.UUID, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	out := make(map[string][]db.AgentFallbackTarget, len(agents))
	if len(ids) == 0 {
		return out, true
	}
	rows, err := h.Queries.ListAgentFallbackTargetsByAgentIDs(ctx, ids)
	if err != nil {
		return nil, false
	}
	for _, row := range rows {
		aid := uuidToString(row.AgentID)
		out[aid] = append(out[aid], row)
	}
	return out, true
}
