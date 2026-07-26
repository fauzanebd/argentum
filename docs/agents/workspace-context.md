# Workspace Context for AI Agents

Load this before your first edit. It is the map plus the list of places where a
plausible-looking change causes real damage.

## Repo map

**Single monorepo** as of `T-00b`. One git repo, one commit per feature, one CI
pipeline.

```
/Users/rizkal/Work/smartsoft/argentum/     ← the repo root
├── apps/
│   ├── backend/          Go: cmd/{api,worker,discord}, internal/, migrations/, config/
│   ├── dashboard/        React 18 + Vite + TanStack — the customer web app
│   ├── landing/          React 18 + Vite — marketing site
│   └── widget/           Preact — embeddable chat widget (created in T-21)
├── packages/
│   ├── api-types/        TS types generated from Go structs (T-02b)
│   └── chat-ui/          Chat components shared by dashboard + widget (T-21)
├── docs/                 This documentation — now tracked
├── .github/workflows/    One pipeline, path-filtered per app
├── pnpm-workspace.yaml
└── Makefile              Top-level entry points: make eval, make test, make dev
```

### What the monorepo changes for you

- **A feature is one commit.** Backend handler, migration, and the TS type that
  mirrors it land together, revert together, and CI checks them together.
- **No more "which repo" step.** Grep once, find both halves.
- **`packages/api-types` is generated, never hand-edited.** If a TS type disagrees
  with its Go struct, that is now a CI failure rather than a runtime surprise.
  Regenerate with `make types`.
- **Deploys are still independent.** Cloudflare Pages builds each frontend from
  its own root directory; the backend still ships as tagged GHCR images. One repo
  does not mean one release.

### The Go module quirk — do not "fix" it

`apps/backend/go.mod` declares `module github.com/fauzanebd/argentum` even though
it now sits in a subdirectory. That is deliberate: keeping the module path meant
**zero import rewrites** across ~120 Go files during the migration. Go module
paths are namespaces, not filesystem paths, and nothing external imports this
module. Changing it to match the directory would rewrite every import in the
codebase for no benefit.

### `apps/widget/` is a published artifact

It differs from the other three: its consumers are other companies' codebases, so
a breaking change cannot be fixed by redeploying. It follows SemVer, ships
immutable versioned CDN paths, and its `/api/embed` contract is versioned
separately from the dashboard API. See `T-19`–`T-23` in
[`../plan/01-tickets.md`](../plan/01-tickets.md).

## Backend — where things live

Paths are relative to `apps/backend/`.

Need to change...                      | Go to
-------------------------------------- | -------------------------------------------------
A business rule                        | `internal/app/*_service.go`
An entity or repository contract        | `internal/domain/` — interface first, then adapter
A SQL query against the control plane   | `internal/adapters/postgres/*_repo.go`
Something the agent can do              | `internal/tools/` + register in `cmd/worker/main.go`
The agent's instructions                | `buildSystemPrompt()` in `cmd/worker/main.go:426`
A guardrail                             | `config/guardrails.yaml`
An HTTP route                           | `internal/transport/http/handlers/` + `cmd/api/router.go`
Dependency wiring (API)                 | `cmd/api/bootstrap.go`
Dependency wiring (worker)              | `cmd/worker/main.go`
A tenant DB dialect                     | `internal/adapters/db/<driver>/`
An env var                              | `internal/config/config.go` — field, default, and `Validate()` if required
The schema                              | `migrations/control/NNN_*.{up,down}.sql`

## Frontend — where things live

Paths are relative to `apps/dashboard/`.

Need to change...            | Go to
---------------------------- | ---------------------------------------
A page or feature            | `src/features/<feature>/`
Shared UI primitive          | `src/components/ui/` (shadcn-style, Radix-based)
Layout / nav / sidebar       | `src/components/layout/`
API client                   | `src/lib/api.ts`
Auth state                   | `src/store/auth.ts` (Zustand)
Live chat stream             | `src/features/chat/use-thread-stream.ts`
Routing                      | `src/routes/index.tsx` (TanStack Router)

## The seven things that will bite you

### 1. There are two processes and they do not share caches

`cmd/api` and `cmd/worker` each build their own schema cache, their own LLM client
cache, and their own guardrail instance. Invalidating in one does **not** affect
the other. The API's `GetSchemaTool` instance exists *only* so DSN rotation can
drop the API-side cache — it is not the tool the agent uses.

If you need cross-process invalidation, publish over the Redis event bus. That
pattern already exists for Discord credential reload (`eventbus.NewRedisBus`).

### 2. Tools are constructed once, but the agent is constructed per turn

`AgentFactory` builds a fresh `sdkagent.Agent` per chat turn, but tools, memory,
and the system prompt are captured in the closure at worker boot.

**Therefore: a tool must be stateless and must read tenant identity from `ctx`
via `tenantctx`, never from a struct field.** A tool holding `companyID` in a
field will serve the wrong tenant the moment two companies chat at once.

### 3. `internal/app` imports `internal/tools`

That is why `UsageRecorder` is declared in `internal/tools/run_sql.go` rather than
in `internal/app`. It looks misplaced; it is not. Moving it creates an import
cycle. The source comment says so — read it before "fixing" it.

### 4. Guardrails are parsed once and rebound per turn

`buildGuardrails()` runs at boot; `guardrailsTpl.WithLLM(light)` rebinds per turn
so each tenant's own light LLM evaluates its `type: llm` patterns. Do not re-parse
the YAML per request — that was a deliberate optimization.

### 5. Returning an error from a worker task means "retry"

`ChatRunner.handleRunError` returns `nil` after writing a user-visible guardrail
message, because retrying a blocked message is pointless and would bill three
times. It returns the error for genuine failures so asynq retries with backoff.
Malformed payloads return `asynq.SkipRetry` so bad tasks archive.

Preserve all three behaviours. Collapsing them into one costs money or loses messages.

### 6. Migrations self-apply on API boot

`cmd/api` runs `migrate.Up` before serving. During a rolling deploy, the new
schema meets old code. **Every migration must be forward-compatible:** add
nullable columns or new tables; do not drop or rename anything a running binary
reads. Add-then-backfill-then-remove across two releases.

### 7. Tenant databases are customer property

Argentum never migrates them, never writes to them, and only reads through
`Conn.ExecuteReadOnly`. `migrations/demo_tenant/` is for the local demo container
only — running it anywhere else creates tables in a customer's warehouse.

## Local development

```bash
# infra
cd apps/backend
docker-compose --profile dev up -d postgres postgres_demo redis metabase

# secrets (once, into .env)
echo "ARGENTUM_JWT_SECRET=$(openssl rand -base64 48)"
echo "ARGENTUM_DSN_KEY=$(openssl rand -hex 32)"

# processes
go run ./cmd/api      # :8080  — applies migrations on boot
go run ./cmd/worker   # consumes chat:run + scheduled:run
go run ./cmd/discord  # only if DISCORD_ENABLED and a tenant has credentials

# frontend
cd ../dashboard && pnpm install && pnpm dev   # :5173
# or from the repo root: pnpm --filter dashboard dev

# demo tenant DSN
postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable
```

Demo schema: `fact_sales`, `dim_customers`, `dim_products`, `dim_date` — a retail
star schema. Every eval case and manual test should target it, so results are
comparable.

## Naming conventions in this codebase

| Thing              | Convention                                     | Example                        |
| ------------------ | ---------------------------------------------- | ------------------------------ |
| Repository iface   | `<Entity>Repository` in `domain`                | `ThreadRepository`             |
| Repository impl    | `<Entity>Repo` in `adapters/postgres`           | `ThreadRepo`                   |
| Service            | `<Domain>Service` in `app`                      | `ScheduledTaskService`         |
| Constructor        | `New<Type>`                                     | `NewChatRunner`                |
| Optional wiring    | `With<Feature>` returning the receiver          | `WithTablePicker`, `WithLark`  |
| Handler            | `<Domain>Handler` + a `Register(rg)` method      | `UsageHandler`                 |
| Tool               | `<name>` snake_case; type `<Name>Tool`           | `run_sql` → `RunSQLTool`       |
| Migration          | `NNN_snake_case.{up,down}.sql`                   | `021_agent_actions.up.sql`     |
| Config accessor    | `Effective<Thing>()` for fallback chains         | `EffectiveLightLLMModel()`     |
| Workspace package  | `@argentum/<name>` under `packages/`             | `@argentum/api-types`          |
| FE feature dir     | kebab-case under `src/features/`                 | `scheduled-tasks/`             |
| FE component file  | kebab-case `.tsx`                                | `task-runs-sheet.tsx`          |

## Danger zones — read the surrounding code twice

| File | Why |
| ---- | --- |
| `internal/app/chat_runner.go` | The core pipeline. Every channel, streaming, metering, and scheduled run flows through it. 630 lines, zero tests as of sprint start. |
| `internal/crypto/dsn.go` | A round-trip bug corrupts every stored connection irrecoverably. |
| `config/guardrails.yaml` | The security boundary. Every regex narrowing carries a comment explaining which false positive it fixed — do not widen one without adding a test case. |
| `internal/adapters/db/*/conn.go` | Read-only transaction + statement timeout. The last defence on tenant data. |
| `internal/app/thread_service.go` | Fork heuristics. A bug here merges two customers' unrelated conversations into one thread — or forks every turn and destroys context. |
| `cmd/worker/main.go` | Wiring order matters: thread service and scheduled service must exist before the tools slice is built, because `schedule_task` needs them. |
| `internal/config/config.go` | 75 env vars. A wrong default ships silently to production. |
