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

## Deploying

Build images from `custom` using upstream's `Dockerfile` / `Dockerfile.web` unmodified where possible — same conflict-avoidance logic as everything else here. Push to your own registry and deploy from there; don't deploy directly off `main`.
