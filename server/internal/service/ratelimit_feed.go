package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueposition"
	"github.com/multica-ai/multica/server/internal/rateregistry"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// rateLimitEvidenceProjectID is the Multica Dev project hosting the
// rate-limit-avoidance epic (FORK-2/3/4) on this self-hosted fork. This
// deployment serves a single workspace, so pinning the project id here
// matches FORK-4's acceptance criteria ("Multica Dev project, backlog
// status, same conventions as this epic's issues") without adding a lookup
// that would only ever resolve to this one value.
const rateLimitEvidenceProjectID = "2fc3664e-f38d-4e98-8ca5-bc94f97eab40"

// rateLimitFallbackCooldown is applied when ParseRateLimitReset cannot
// extract a reset time from the raw provider error text (FORK-4).
const rateLimitFallbackCooldown = 4 * time.Hour

// feedRateLimitRegistry is called from FailTask whenever a task's failure
// was classified as agent_error.provider_capacity_or_rate_limit. It
// best-effort parses a reset time out of the raw error text and upserts the
// FORK-3 rate-limit registry; when parsing fails it falls back to a fixed
// cooldown and captures the raw text for later triage.
//
// model is the model actually in effect for the run (daemon-resolved, not
// necessarily agent.model). Best-effort throughout: nothing here may fail
// the task-fail request itself, so every error is logged and swallowed.
func (s *TaskService) feedRateLimitRegistry(ctx context.Context, task db.AgentTaskQueue, errMsg, model string) {
	if s.Queries == nil || !task.RuntimeID.Valid {
		return
	}
	workspaceID, err := util.ParseUUID(s.ResolveTaskWorkspaceID(ctx, task))
	if err != nil {
		slog.Warn("feed rate limit registry: resolve workspace failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return
	}

	until, ok := taskfailure.ParseRateLimitReset(errMsg)
	if !ok {
		until = time.Now().Add(rateLimitFallbackCooldown)
	}
	if err := rateregistry.SetRateLimited(ctx, s.Queries, workspaceID, task.RuntimeID, model, until); err != nil {
		slog.Warn("feed rate limit registry: upsert failed",
			"task_id", util.UUIDToString(task.ID),
			"runtime_id", util.UUIDToString(task.RuntimeID),
			"error", err)
	}

	if ok {
		return
	}
	s.captureUnparsedRateLimitError(ctx, task, workspaceID, model, errMsg)
}

// captureUnparsedRateLimitError persists the raw error text in a queryable
// table and opens a backlog issue with the same evidence, per FORK-4's
// acceptance criteria. Best-effort: every step logs and returns rather than
// propagating an error to the FailTask caller.
func (s *TaskService) captureUnparsedRateLimitError(ctx context.Context, task db.AgentTaskQueue, workspaceID pgtype.UUID, model, errMsg string) {
	if _, err := s.Queries.CreateRateLimitParseFailure(ctx, db.CreateRateLimitParseFailureParams{
		WorkspaceID: workspaceID,
		RuntimeID:   task.RuntimeID,
		Model:       model,
		RawError:    errMsg,
	}); err != nil {
		slog.Warn("capture unparsed rate limit error: insert failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}

	if s.TxStarter == nil || !task.AgentID.Valid {
		return
	}
	projectID, err := util.ParseUUID(rateLimitEvidenceProjectID)
	if err != nil {
		slog.Warn("capture unparsed rate limit error: bad project id constant", "error", err)
		return
	}

	title := fmt.Sprintf("Unparsed rate-limit error observed — teach the classifier this pattern (runtime %s)",
		util.UUIDToString(task.RuntimeID))
	description := fmt.Sprintf(
		"FORK-4's rate-limit reset parser (`taskfailure.ParseRateLimitReset`) could not extract a reset "+
			"time from this provider error. A fixed %s fallback cooldown was applied to the registry instead.\n\n"+
			"- runtime_id: %s\n- model: %s\n- observed_at: %s\n\nRaw error text:\n\n```\n%s\n```\n",
		rateLimitFallbackCooldown, util.UUIDToString(task.RuntimeID), model,
		time.Now().UTC().Format(time.RFC3339), errMsg,
	)

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("capture unparsed rate limit error: begin tx failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)

	issueNumber, err := qtx.IncrementIssueCounter(ctx, workspaceID)
	if err != nil {
		slog.Warn("capture unparsed rate limit error: increment issue counter failed", "error", err)
		return
	}
	position, err := issueposition.NextTopPosition(ctx, tx, workspaceID, "backlog")
	if err != nil {
		slog.Warn("capture unparsed rate limit error: next position failed", "error", err)
		return
	}
	if _, err := qtx.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID: workspaceID,
		Title:       title,
		Description: pgtype.Text{String: description, Valid: true},
		Status:      "backlog",
		Priority:    "none",
		CreatorType: "agent",
		CreatorID:   task.AgentID,
		Position:    position,
		Number:      issueNumber,
		ProjectID:   projectID,
	}); err != nil {
		slog.Warn("capture unparsed rate limit error: create issue failed", "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("capture unparsed rate limit error: commit failed", "error", err)
	}
}
