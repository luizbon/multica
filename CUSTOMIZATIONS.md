# CUSTOMIZATIONS.md

Decision log for customizations made on top of upstream [multica-ai/multica](https://github.com/multica-ai/multica) in this fork. One entry per issue. See `FORK.md` for the governance model this log supports.

## 2: Add per-agent fallback runtime/model targets
- Branch: issue-2 (see FORK.md "Issue branches" — the originally-planned `custom/issue-<n>` scheme collides with the existing bare `custom` branch ref, so this issue landed on the flat name)
- Files touched:
  - `server/migrations/202_agent_fallback_target.up.sql`, `.down.sql`
  - `server/migrations/203_agent_fallback_target_agent_position_unique_index.up.sql`, `.down.sql`
  - `server/pkg/db/queries/agent_fallback_target.sql`
  - `server/pkg/db/generated/agent_fallback_target.sql.go`
  - `server/pkg/db/generated/models.go`
  - `server/internal/handler/agent_fallback_target.go`
  - `server/internal/handler/agent_fallback_target_test.go`
  - `server/internal/handler/agent.go`
  - `server/internal/handler/runtime.go`
  - `server/internal/handler/runtime_profile.go`
  - `server/cmd/multica/cmd_agent.go`
  - `server/cmd/multica/cmd_agent_test.go`
  - `server/internal/service/builtin_skills/multica-creating-agents/SKILL.md`
  - `server/internal/service/builtin_skills/multica-creating-agents/references/creating-agents-source-map.md`
- Risk tier (per FORK.md): 3 (edit to existing shared file)
- Date: 2026-07-21

## 3: Add rate-limit status registry for runtime/model pairs
- Branch: feat/issue-3 (per FORK.md issue-branch scheme)
- Files touched:
  - `server/migrations/204_runtime_rate_limit.up.sql`, `.down.sql`
  - `server/migrations/205_runtime_rate_limit_workspace_runtime_model_unique_index.up.sql`, `.down.sql`
  - `server/pkg/db/queries/runtime_rate_limit.sql`
  - `server/pkg/db/generated/runtime_rate_limit.sql.go`
  - `server/pkg/db/generated/models.go`
  - `server/internal/rateregistry/rateregistry.go`
  - `server/internal/rateregistry/rateregistry_test.go`
- Risk tier (per FORK.md): 3 (edit to existing shared file — `server/pkg/db/generated/models.go` is appended to by the generated-code update; everything else is net-new)
- Date: 2026-07-21

## 4: Parse rate-limit reset time from provider errors and feed the registry
- Branch: feat/issue-4 (per FORK.md issue-branch scheme)
- Files touched:
  - `server/pkg/taskfailure/ratelimit_reset.go`, `ratelimit_reset_test.go`
  - `server/internal/service/ratelimit_feed.go`, `ratelimit_feed_test.go`
  - `server/internal/service/task.go`
  - `server/internal/daemon/daemon.go`, `daemon_test.go`, `client.go`, `types.go`
  - `server/internal/handler/daemon.go`
  - `server/internal/handler/fail_task_rate_limit_model_test.go`
  - `server/migrations/206_rate_limit_parse_failure.up.sql`, `.down.sql`
  - `server/migrations/207_rate_limit_parse_failure_workspace_created_index.up.sql`, `.down.sql`
  - `server/pkg/db/queries/rate_limit_parse_failure.sql`
  - `server/pkg/db/generated/rate_limit_parse_failure.sql.go`
  - `server/pkg/db/generated/models.go`
  - Mechanical `FailTask` call-site updates (new `model` param): `server/cmd/server/autopilot_listeners_test.go`, `server/internal/handler/chat_attachment_reply_test.go`, `server/internal/handler/chat_input_ownership_test.go`, `server/internal/handler/issue_child_done_test.go`, `server/internal/service/retry_deferred_test.go`, `server/internal/service/task_complete_race_test.go`
- Risk tier (per FORK.md): 3 (edit to existing shared file — `server/internal/service/task.go`'s `FailTask` signature and its daemon/handler call chain)
- Date: 2026-07-22
