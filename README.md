# Argentum

A B2B agentic analytics assistant. Customers connect their analytical database
(Postgres, MySQL, or SQL Server), then ask questions in natural language from the
web dashboard, WhatsApp, Discord, or Lark. The agent introspects the schema, runs
read-only SQL, builds Metabase dashboards, generates downloadable documents, and
replies in the user's language.

By [Smartsoft](https://smartsoft.co.id).

## Layout

```
apps/
  backend/      Go 1.26 — cmd/{api,worker,discord,mcp}, internal/, migrations/
  dashboard/    React 18 + Vite + TanStack — the customer web app
  landing/      React 18 + Vite — marketing site
packages/       Shared TypeScript packages (see docs/plan for what lands here)
docs/           Product research, coverage matrices, development plan, agent guides
```

Consolidated from three separate repositories. All three histories are preserved —
`git blame` works across the boundary, and the original commit SHAs still resolve.

## Quick start

```bash
make deps      # go mod download + pnpm install
make infra     # postgres, demo postgres, redis, metabase via Docker Compose
make api       # :8080 — applies control migrations on boot
make worker    # consumes chat:run and scheduled:run
make web       # :5173 dashboard
```

Then sign up at `http://localhost:5173/signup` and add the demo tenant DSN:

```
postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable
```

`make help` lists every target.

Secrets: copy `apps/backend/.env.example` to `apps/backend/.env` and fill in.
Generate the two required keys with:

```bash
echo "ARGENTUM_JWT_SECRET=$(openssl rand -base64 48)"
echo "ARGENTUM_DSN_KEY=$(openssl rand -hex 32)"
```

## Verify

```bash
make check     # go vet + go test -race + build everything
make lint
```

CI runs the same commands, path-filtered per app: a backend change runs the Go
jobs, a frontend change runs the web job, and a docs-only change runs neither.

## Documentation

| Where | What |
| ----- | ---- |
| [`docs/`](docs/) | Product research, feature and test coverage, the development plan |
| [`docs/AGENTS.md`](docs/AGENTS.md) | Working contract for AI agents — read before editing |
| [`apps/backend/docs/`](apps/backend/docs/) | REST API reference, integration guides, Postman collection |
| [`apps/backend/README.md`](apps/backend/README.md) | Backend architecture and configuration detail |

## A note on the Go module path

`apps/backend/go.mod` declares `module github.com/fauzanebd/argentum` even though
it lives in a subdirectory. This is deliberate: a Go module path is a namespace,
not a filesystem path, and nothing external imports this module. Keeping it meant
the monorepo migration required **zero changes to any Go import statement**.
Do not "fix" it — doing so rewrites every import in the backend for no benefit.

## Deployment

Each app deploys independently:

- **backend** — tagged `v*.*.*` pushes build and publish `argentum-api`,
  `argentum-worker`, and `argentum-discord` images to GHCR. Helm chart in
  `apps/backend/helm/`.
- **dashboard / landing** — Cloudflare Pages, one project each, built from their
  own root directory within this repo.

## License

Internal — All rights reserved.
