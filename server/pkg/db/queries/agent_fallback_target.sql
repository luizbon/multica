-- Per-agent ordered fallback (runtime, model) targets (FORK-2). Rows are the
-- fallback priority list an agent tries when its primary runtime_id is
-- unavailable. See migration 202/203.

-- name: ListAgentFallbackTargets :many
SELECT * FROM agent_fallback_target
WHERE agent_id = $1
ORDER BY position ASC;

-- name: ListAgentFallbackTargetsByAgentIDs :many
-- Batch load for the agent list endpoint so we don't N+1 per agent.
SELECT * FROM agent_fallback_target
WHERE agent_id = ANY(@agent_ids::uuid[])
ORDER BY agent_id, position ASC;

-- name: CreateAgentFallbackTarget :exec
INSERT INTO agent_fallback_target (agent_id, position, runtime_id, model)
VALUES ($1, $2, $3, sqlc.narg('model'));

-- name: DeleteAgentFallbackTargets :exec
-- Clears every fallback target for an agent. Used before re-writing the
-- list so an update is a wholesale replace, matching the
-- agent_invocation_target write model.
DELETE FROM agent_fallback_target
WHERE agent_id = $1;

-- name: DeleteAgentFallbackTargetsByArchivedRuntimeAgents :exec
-- Application-layer replacement for the (deliberately absent) agent_id ON
-- DELETE CASCADE: removes fallback targets for the archived agents a runtime
-- delete is about to hard-delete. MUST run in the same tx as, and BEFORE,
-- DeleteArchivedAgentsByRuntime so no orphan target rows survive the agent
-- rows they belonged to. Mirrors
-- DeleteAgentInvocationTargetsByArchivedRuntimeAgents.
DELETE FROM agent_fallback_target
WHERE agent_id IN (
    SELECT id FROM agent WHERE runtime_id = $1 AND archived_at IS NOT NULL
);
