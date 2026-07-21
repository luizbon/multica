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
