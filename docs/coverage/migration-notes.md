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

### 1. Verify Docker image builds — Docker was not running

```bash
open -a Docker      # then wait for it
cd /Users/rizkal/Work/smartsoft/argentum-mono
docker build -f apps/backend/Dockerfile.api     -t argentum-api:local     apps/backend
docker build -f apps/backend/Dockerfile.worker  -t argentum-worker:local  apps/backend
docker build -f apps/backend/Dockerfile.discord -t argentum-discord:local apps/backend
```

All three Dockerfiles `COPY . .` relative to their context, so `apps/backend` as
context should behave exactly as the old repo root did. Unverified until run.

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

### 3. Add the remote — needs a decision

`apps/backend/go.mod` says `fauzanebd`; GHCR and CI say `haritsrizkall`. Pick one:

```bash
cd /Users/rizkal/Work/smartsoft/argentum-mono
git remote add origin git@github.com:<OWNER>/argentum.git
git push -u origin main
```

The Go module path stays `github.com/fauzanebd/argentum` regardless — see the note
in the root `README.md`.

Then archive the three originals read-only on GitHub rather than deleting them.
They are the rollback if step 2 goes wrong.

### 4. Swap into place

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
