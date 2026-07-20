# FORK.md

Governance for this repo as a personal fork of [multica-ai/multica](https://github.com/multica-ai/multica). This file covers *how to manage the fork itself* — remotes, branches, sync cadence, where customizations go. For how the application is built/architected, see `AGENTS.md` / `CLAUDE.md`.

## Why this file is separate

Multica ships extremely fast (dozens of commits/day, 100+ releases). `AGENTS.md` and `CLAUDE.md` at the repo root are upstream's own actively-maintained dev docs — editing them directly would mean fighting merge conflicts on every sync. Fork-governance instructions live here instead, in a file upstream will never touch.

## License note

Multica's license is a modified Apache-2.0: personal/internal self-hosted use (including customization) does not require a commercial license. A commercial license is only needed if this fork is offered as a hosted service to third parties or embedded in something sold/distributed. Don't remove the branding in `apps/web/` unless you've cleared that separately.

## Remotes

Already configured in this clone:

```bash
git remote -v
# origin    https://github.com/luizbon/multica.git   (your fork)
# upstream  https://github.com/multica-ai/multica.git (source of truth)
```

## Branch layout

- **`main`** — mirrors `upstream/main` exactly. Never commit here directly; it exists only as a clean base to diff/rebase against.
- **`custom`** — the actual working branch with your customizations. This is what you build and deploy from. (Create it with `git checkout -b custom main` if it doesn't exist yet.)

Tag `main` at each sync so a bad rebase can always be rolled back to the last known-good base:

```bash
git tag upstream-$(date +%F) main
```

## Where customizations go (least conflict-prone first)

1. **Env / compose overrides** — copy `.env.example` to `.env` for config. For Docker changes, add a `docker-compose.custom.yml` and layer it on top instead of editing `docker-compose.selfhost.yml` directly:
   ```bash
   docker compose -f docker-compose.selfhost.yml -f docker-compose.custom.yml up -d
   ```
2. **New files, not edits** — new components in `apps/web`, new handlers in `server/`, additive DB migrations (this repo's `CLAUDE.md` requires `CREATE INDEX CONCURRENTLY` in its own migration file — plays nicely with additive-only changes). Upstream rarely conflicts with files it doesn't know exist.
3. **Edits to existing/shared files** — last resort. Keep each such change as its own small, separately-committed diff on `custom` with a clear commit message, so a future rebase conflict tells you exactly which patch needs re-applying instead of untangling a mixed commit.

## Sync procedure

Run roughly weekly given upstream's commit velocity:

```bash
git checkout main
git pull upstream main
git tag upstream-$(date +%F)

git checkout custom
git rebase main
# resolve conflicts commit-by-commit; small isolated commits from step 3 above make this tractable

pnpm test
make check
docker compose -f docker-compose.selfhost.yml -f docker-compose.custom.yml build

git push --force-with-lease origin custom
```

If a rebase goes bad: `git rebase --abort`, then `git reset --hard upstream-<last-good-date>` on `custom` and re-apply the missing commits by hand.

## Automation

Sync is driven by an Autopilot on the self-hosted Multica instance (not CI) — running the fork-maintenance loop on the same platform being forked. **Live**, created via the `multica` CLI against the local self-hosted instance (`http://localhost:8080`):

| Object | ID | Note |
|---|---|---|
| Workspace | `40142403-7514-4b82-9045-5a1ba7757b7c` (`multica-fork`) | dedicated, keeps sync issues off the main board |
| Agent | `61bfe3bd-a18c-4439-8ff8-e3e79e058817` ("Fork Sync") | bound to the local Claude Code runtime |
| Project | `a64f796d-f20a-4bd8-bbaa-8ad08f117d6e` ("Fork Sync") | repo resource pinned to `custom` |
| Autopilot | `286bc8b9-c5ea-4ac4-be29-07903369b680` ("Fork sync {{date}}") | `create_issue` mode, subscribed: Luiz Bon |
| Trigger | `61a42ca0-0789-438a-9fab-797409c574db` | schedule, `0 9 * * 1`, `Australia/Sydney` — weekly Monday 9am |

Commands to reproduce or modify this setup:

### Setup

```bash
# 1. Dedicated workspace, so weekly sync issues don't clutter your main board.
#    Slug/issue-prefix are permanent — chosen here, not changeable later.
multica workspace create --name "Multica Fork" --slug multica-fork --issue-prefix FORK
multica workspace switch multica-fork

# 2. An agent bound to a Bash-capable coding tool. `--runtime-id` is required
#    (not a name) — get it from `multica runtime list --output json`; a daemon
#    already running on this machine auto-registers one per workspace it belongs to.
multica agent create --name "Fork Sync" --runtime-id <runtime-id> \
  --instructions "You maintain a personal fork of multica-ai/multica. Follow the sync procedure in FORK.md at the repo root exactly. Never push to main. Never force-push custom except with --force-with-lease, and only after a clean rebase plus passing tests/checks."

# 3. A project with the repo attached, pinned to the custom branch (create
#    doesn't take --ref; set it in a follow-up resource update):
multica project create --title "Fork Sync" --repo https://github.com/luizbon/multica --lead "Fork Sync"
#   note the printed resource id, then:
multica project resource update <project-id> <resource-id> --ref custom
```

### The autopilot

```bash
multica autopilot create \
  --title "Fork sync {{date}}" \
  --description "$(cat <<'PROMPT'
Sync the `custom` branch of the multica fork with upstream.

1. On the checked-out repo: `git remote add upstream https://github.com/multica-ai/multica.git`
   if it isn't already configured.
2. `git checkout main && git pull upstream main`, then `git push origin main`
   (fast-forward only) and `git tag upstream-$(date +%F) main && git push origin --tags`.
3. `git checkout custom && git rebase main`.
4. If the rebase is clean: run `pnpm test` and `make check`. If both pass,
   `git push --force-with-lease origin custom`, then comment on this issue with
   a one-line summary ("synced cleanly, pushed") and close it.
5. If the rebase conflicts, or tests/checks fail after a clean rebase: do NOT
   push and do NOT resolve conflicts yourself. Leave the rebase in progress
   (or `git rebase --abort` if that's cleaner to hand off), comment on this
   issue with exactly which files conflicted or which check failed, and leave
   the issue open for manual review.
PROMPT
)" \
  --agent "Fork Sync" \
  --mode create_issue \
  --project <project-id> \
  --priority medium \
  --subscriber "<your name>" \
  --output json
# note the returned autopilot-id from the response, then:
multica autopilot trigger-add <autopilot-id> \
  --kind schedule --cron "0 9 * * 1" --timezone Australia/Sydney \
  --label "weekly-monday-sydney"
```

`create_issue` mode (not `run_only`) is deliberate here even though the prompt closes clean-sync issues itself: it gives every run — clean or not — a visible, commented record on the board, which doubles as the monitoring Multica's own docs recommend (autopilot failures don't send notifications or auto-retry, so an agent-level crash before it reaches step 4/5 just leaves a `failed` run in history with an open issue). `--subscriber` puts the created issue in your normal issue-subscription notifications, which covers that gap.

### Verifying it worked

```bash
multica autopilot get 286bc8b9-c5ea-4ac4-be29-07903369b680 --output json
multica autopilot runs 286bc8b9-c5ea-4ac4-be29-07903369b680 --output json
```

Use `multica autopilot trigger 286bc8b9-c5ea-4ac4-be29-07903369b680` for a manual run instead of waiting for Monday to test the setup.

## Deploying

Build images from `custom` using upstream's `Dockerfile` / `Dockerfile.web` unmodified where possible — same conflict-avoidance logic as everything else here. Push to your own registry and deploy from there; don't deploy directly off `main`.
