# Monorepo Migration — Record and Remaining Steps

`T-00b`, executed 2026-07-26. Local migration complete; four steps remain, each
needing a human decision or a browser.

## What was done

Built at `/Users/rizkal/Work/smartsoft/argentum-mono`. **The three original repos
were not modified** — they remain at `/Users/rizkal/Work/smartsoft/argentum/`.

| Step | Result |
| ---- | ------ |
| `git subtree add` × 3 | 75 commits; all three histories in the graph |
| Layout | `apps/{backend,dashboard,landing}`, `packages/`, `docs/` at root |
| Go module path | Unchanged (`github.com/fauzanebd/argentum`) → **zero import rewrites** |
| `.github/` | Moved to repo root (Actions only reads it there) |
| CI | Rewritten: per-job path filters, `go vet`, `go test -race`, `cmd/discord` build, web build+lint job |
| `release.yaml` | Scoped to `apps/backend/**` so frontend/docs changes don't mint backend tags |
| pnpm workspace | Root lockfile; per-app lockfiles removed; widget examples excluded from the workspace |
| Root `Makefile` | Single entry point — `make help` lists targets |
| Docker | Build context moved to `apps/backend`; `argentum-discord` image added to the matrix |

### Cleanups folded in

- Recovered `apps/backend/docs/` — ~1,100 lines of API reference, integration
  guides, and the Postman collection that `.gitignore` had excluded, so they
  existed only on this machine (finding **Q-9**).
- Un-ignored `.env.example` (finding **Q-10**).
- Fixed the dashboard `lint` script, which called an eslint that was never
  installed (finding **Q-11**).
- Deleted `scratch-chat-page-plan.md` (finding **P-5**), a stray `package-lock.json`,
  and two committed `.tsbuildinfo` files.
- CI `GO_VERSION` 1.25 → 1.26 to match `go.mod` (finding **Q-8**).

### Verified locally

```
backend:    go build ./...      OK
            go vet ./...        clean
            go test ./...       same 3 passing packages as the pre-migration baseline
web:        pnpm -r build       dashboard + landing OK
            pnpm -r lint        OK
history:    git blame apps/backend/internal/app/chat_runner.go → d782129, dcd0355, 94fe370, 17f81f5, 8cf653b …
            git blame apps/dashboard/src/features/chat/chat-page.tsx → 0687da5 …
tree diff:  no .go differences vs. the original repo
secrets:    no .env, .env.local, .DS_Store, or .claude/settings.local.json staged
```

### What git history does and does not do after subtree

| Command | Works? |
| ------- | ------ |
| `git blame apps/backend/<path>` | ✅ reaches real pre-migration commits |
| `git show 3891579` — original SHAs | ✅ preserved, so all doc citations stay valid |
| `git log -- apps/backend/<path>` | ❌ post-migration commits only |
| `git log --full-history -- <path-without-apps/backend>` | ✅ full pre-migration history |

Path-filtered `log` does not cross the merge because old commits recorded old
paths. `filter-repo` would fix that but would rewrite every SHA — see the note in
`../plan/01-tickets.md` `T-00b` for why that trade was rejected.

---

## Remaining steps

### 1. Docker image builds — ✅ DONE

All three images build from the `apps/backend` context and the binaries **run**
(they fail at config validation with `LLM_API_KEY is required`, which proves the
binary executes rather than merely existing in the layer):

| Image | Size |
| ----- | ---- |
| `argentum-api` | 77 MB |
| `argentum-worker` | 78 MB |
| `argentum-discord` | 47 MB |

**Environment finding — docker CLI version skew.** A nix-installed docker CLI
shadows Docker Desktop's:

```
/Users/rizkal/.nix-profile/bin/docker   24.0.5  (API 1.43)  ← first on PATH
/usr/local/bin/docker                   29.1.3
```

The daemon requires API ≥ 1.44, so `docker run`, `docker image inspect`, and most
non-build subcommands fail with *"client version 1.43 is too old"*. `docker build`
happens to work, which makes this easy to misdiagnose. Fix by putting
`/usr/local/bin` ahead of `~/.nix-profile/bin` on PATH, or removing docker from the
nix profile. Until then, use the full path
`/Applications/Docker.app/Contents/Resources/bin/docker`.

### 2. Reconfigure Cloudflare Pages — the only step that can break production

Two projects. **Deploy a preview branch and confirm before repointing production.**

For each, in Pages → Settings → Builds & deployments:

| Setting | `argentum-dashboard` | `argentum-landing` |
| ------- | -------------------- | ------------------ |
| Root directory | `apps/dashboard` | `apps/landing` |
| Build command | `pnpm install --frozen-lockfile && pnpm build` | `pnpm install --frozen-lockfile && pnpm build` |
| Build output directory | `dist` | `dist` |

Watch for:

- **Lockfile resolution.** The lockfile now lives at the repo root, not in the app
  directory. Pages usually detects a pnpm workspace and installs from the root; if
  `--frozen-lockfile` fails, set the root directory to the repo root instead and use
  `pnpm install --frozen-lockfile && pnpm --filter dashboard build` with output
  `apps/dashboard/dist`.
- **`apps/dashboard/functions/`** — the SPA-fallback Pages middleware. It must still
  be discovered relative to the new root. Confirm deep links (e.g. `/settings`)
  resolve on the preview, not just `/`.
- **Environment variables.** `VITE_*` values are per-project in Pages, unaffected by
  the move — but confirm they are present on the preview deployment.

### 3. Push to `fauzanebd/argentum` — ✅ DONE

Pushed `3891579..2f56e39` as a fast-forward, plus the CI fix below. `pre-monorepo`
exists on the remote as a named ref at the old `main` (`3891579`).

Results:

| Run | Outcome |
| --- | ------- |
| `Release` on main | Auto-tagged **`v0.10.1`** — bumped from `v0.10.0`, so tag continuity survived the migration |
| `CI` on main | ✅ Detect changes / Backend / Web all green; docker correctly skipped (not a tag) |
| `CI` on tag `v0.10.1` | ✅ Backend green, Web skipped, all three images built and pushed |

#### Bug found and fixed: tag pushes stopped publishing images

The first `v0.10.1` tag run skipped **every** job. On a tag push
`dorny/paths-filter` has no base ref to diff against, so it reported no changes;
the `backend` job's `if` evaluated false and skipped; and because `docker` lists
`backend` in `needs`, **a skipped dependency skipped the docker job too**. The
release completed with no images — silently.

Fixed by making the backend job unconditional for tags, so tests still gate
publishing and the dependency resolves:

```yaml
if: needs.changes.outputs.backend == 'true' || startsWith(github.ref, 'refs/tags/v')
```

Verified by deleting and re-cutting `v0.10.1`: Backend green, all three images
published. **This is the failure mode to watch for whenever a job gains a
`needs:` on a path-filtered job** — the skip cascades silently rather than failing.

#### Follow-up: `argentum-discord` is a new GHCR package

It has never been published before (the pre-monorepo CI never built `cmd/discord`).
New GHCR packages default to **private** and have no linked repository, so a
cluster pulling it will fail on auth until its visibility and image pull secret are
configured — unlike `argentum-api` and `argentum-worker`, which are already set up.

---

### Original push plan (kept for reference)

**Decision: reuse the existing backend repo.** The monorepo HEAD descends from all
three repo heads, so this is a fast-forward — `origin/main` (`3891579`) is an
ancestor of HEAD. Nothing on the remote is discarded, and no force is needed.

```
31 new commits · 359 files changed, +21185 / -397
```

Reuse also keeps the 19 release tags (so `release.yaml` bumps `v0.10.0` → `v0.11.0`
rather than restarting), `secrets.PAT`, GHCR package linkage, and issues — and it
realigns the repo URL with the Go module path `github.com/fauzanebd/argentum`.

```bash
cd /Users/rizkal/Work/smartsoft/argentum-mono
git branch -f pre-monorepo origin/main    # safety ref
git push -u origin main
```

**What the push sets off, by design:**

1. `release.yaml` matches (`apps/backend/**` changed) → auto-tags **`v0.11.0`**.
2. That tag triggers the `docker` job → builds and pushes `argentum-api`,
   `argentum-worker`, and `argentum-discord` to GHCR, including `latest`.

The images are verified locally (step 1), and they come from identical Go source —
but if any cluster pulls `latest` on a rolling deploy, it will pick them up. Decide
whether that is acceptable before pushing.

#### Three GitHub identities — worth untangling separately

| Account | Role |
| ------- | ---- |
| `fauzanebd` | owns `argentum` (backend) and `argentum-dashboard` |
| `haritsrizkall` | owns `argentum-landing`; also the GHCR image owner and CI username |
| `rizkalaliamdy` | the account `gh` is authenticated as; owns none of them |

`gh api` reports `push=false, admin=false` on all three for the authenticated
account, yet commits are authored by `rizkalaliamdy <rizkal@tr8.io>` — so pushes
evidently go over SSH under different credentials than the `gh` token. Confirm this
before relying on `gh` for release or CI work.

Argentum is a Smartsoft product living on personal accounts. Transferring to a
company org is likely right, but do it **after** deploys are verified — a GitHub
transfer preserves issues, tags, and sets up redirects, so there is no reason to
debug an ownership change and a Pages reconfiguration at the same time.

### 4. Swap into place

**Do not archive `argentum-dashboard` or `argentum-landing` until step 2 is done.**
Cloudflare Pages still builds both frontends from those repos, so they remain the
production source of truth for the dashboard and landing site until Pages is
repointed at the monorepo. Archiving them early breaks frontend deploys.

Only after steps 1–3 pass:

```bash
cd /Users/rizkal/Work/smartsoft
mkdir -p _archive
mv argentum/argentum argentum/argentum-dashboard argentum/argentum-landing _archive/
mv argentum/.claude argentum-mono/.claude 2>/dev/null || true
rm -rf argentum/docs argentum/.DS_Store
rmdir argentum 2>/dev/null && mv argentum-mono argentum
```

Local-only files already copied into the monorepo and still gitignored:
`apps/backend/.env`, `apps/dashboard/.env.local`, `.claude/settings.local.json`.

### 5. Then finish `T-00`

The runtime smoke test was deferred when the monorepo was moved ahead of it:

```bash
make infra && make api    # separate terminal: make worker
make web
```

Then sign up, add the demo DSN, and run the seven-step smoke test in
`../agents/verification.md`. Record the outcome in `environment-notes.md`.
