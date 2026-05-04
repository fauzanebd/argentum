# Argentum

Argentum is a B2B agentic analytics assistant. Each customer connects their
analytical database (Postgres or MySQL today, more drivers planned), then
chats with Argentum from the dashboard or WhatsApp. The agent runs SQL,
builds Metabase dashboards, and replies in the user's language.

## Architecture (V1)

```
                     ┌─────────────────────┐
                     │  argentum-dashboard │   (Vite + React)
                     │  Vite/React/Tailwind│
                     └──────────┬──────────┘
                       REST + WS │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│                cmd/api  — control plane + producer           │
│  HTTP (gin): /api/auth/* /api/connections /api/threads       │
│              /api/chat   /api/usage/*    /api/threads/:/stream│
│              /webhook/whatsapp                               │
│  ChatEnqueuer  → asynq.Enqueue(chat:run) ──────────┐         │
│  WS handler    ← Redis SUBSCRIBE argentum:thread:* │         │
└─────────────────────────────────────────────┬──────┘         │
                                              ▲                ▼
                                              │     ┌────────────────────┐
                                              │     │   Redis (asynq +   │
                                              │     │   pub/sub bus)     │
                                              │     └─────────┬──────────┘
                                              │               │
                                              │               ▼
                                  PUBLISH events       ┌──────────────────────────────┐
                                  argentum:thread:{id} │   cmd/worker  — agent runner │
                                              └───────│ asynq.Server (chat:run)       │
                                                      │ agent-sdk-go + tools           │
                                                      │ ChatRunner                     │
                                                      │   - run agent, persist messages│
                                                      │   - publish events to Redis    │
                                                      │   - send WhatsApp on WA threads│
                                                      └──────┬───────────────────────┘
                                                             │
                                                             ▼
                                                      Tenant analytical DBs
                                                       (Postgres / MySQL)

   Postgres (control)        Metabase (dashboards)
```

## Tech stack

**Backend (Go 1.25)**

- Gin for HTTP, Gorilla WebSocket for streaming
- `agent-sdk-go` (Ingenimax) for LLM orchestration, memory, guardrails
- `database/sql` with `lib/pq` and `go-sql-driver/mysql`
- `golang-migrate/migrate` for control plane schema
- `golang-jwt/jwt/v5` and Argon2id (`x/crypto/argon2`) for auth
- AES-256-GCM for DSN encryption at rest

**Frontend (`../argentum-dashboard`)**

- React 18 + TypeScript + Vite
- TanStack Router + TanStack Query
- Tailwind + shadcn-style primitives (Radix UI)
- Zustand for auth state, React Hook Form + Zod for forms

**Infrastructure**

- Postgres 16 (control plane and Metabase metadata)
- Redis 7 (rate-limit bucket, asynq queue, agent memory, WS pub/sub)
- Asynq for chat task fan-out + retry
- Metabase 0.60 for dashboards

## Database layout

- **Control plane** (`migrations/control/`) — companies, users, encrypted
  DB connections, phone allowlist, conversation threads, messages, usage
  events, credits. Chat task state lives in Redis (asynq), not Postgres.
  Auto-applied at API startup via `golang-migrate`.
- **Tenant analytical DBs** are supplied by customers and never migrated by
  Argentum. The driver layer introspects them at runtime via
  `Conn.ExtractSchema()` and runs every query inside a read-only
  transaction.
- **Demo tenant** (`migrations/demo_tenant/`) — a retail star-schema
  (fact_sales, dim_customers, dim_products, dim_date) for local
  development, applied to the `postgres_demo` container under the
  `dev` compose profile.

## Quick start (local development)

1. **Generate the required secrets**:

   ```bash
   echo "ARGENTUM_JWT_SECRET=$(openssl rand -base64 48)"
   echo "ARGENTUM_DSN_KEY=$(openssl rand -hex 32)"
   ```

   Paste into `.env` (copied from `.env.example`).

2. **Bring up infrastructure**:

   ```bash
   docker-compose --profile dev up -d postgres postgres_demo redis metabase
   ```

3. **Run the API server and worker** (two terminals or `&`):

   ```bash
   go run ./cmd/api      # serves HTTP + WebSocket
   go run ./cmd/worker   # consumes chat:run tasks
   ```

   Migrations apply automatically on first API run.

4. **Run the dashboard**:

   ```bash
   cd ../argentum-dashboard
   npm install
   npm run dev    # http://localhost:5173
   ```

5. **Sign up** at `http://localhost:5173/signup`, then add a tenant DB
   connection on the onboarding page. Use the demo tenant DSN:
   `postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable`

6. (Optional) **Connect WhatsApp / Twilio**: configure `TWILIO_*` or
   `WHATSAPP_*` env vars, expose `:8080/webhook/whatsapp` via ngrok, and
   add the sender's phone number under Settings → Phones.

7. **Configure WhatsApp Webhook**

For local development, use ngrok to expose your local server:

```bash
docker-compose up -d ngrok
# Get the public URL
curl http://localhost:4040/api/tunnels
```

Then configure your WhatsApp Business webhook:

- Callback URL: `https://<ngrok-url>/webhook/whatsapp`
- Verify Token: Use the value from `WHATSAPP_WEBHOOK_VERIFY_TOKEN` in your `.env`

## Project layout

```
argentum/
  cmd/
    api/                      HTTP + WS + asynq producer
    worker/                   asynq consumer (agent runner)
  config/                     YAML — agent persona + guardrail rules
  internal/
    domain/                   pure entities + repository interfaces
    app/                      use case services
                              - ChatEnqueuer (API), ChatRunner (worker)
                              - AuthService, CompanyService
                              - ThreadService, UsageService, MeteredLLM
    adapters/
      db/                     tenant DB driver abstraction
        postgres/             Postgres driver
        mysql/                MySQL driver
      postgres/               control-plane repository implementations
    auth/                     password hashing + JWT signer
    crypto/                   AES-256-GCM DSN cipher
    queue/                    asynq task contract + Enqueuer
    transport/
      http/handlers/          Gin route handlers
      http/middleware/        auth, CORS, rate limit, logging
      ws/                     WebSocket handler (Redis pub/sub)
      eventbus/               Redis pub/sub EventBus implementation
    tools/                    agent-sdk Tool implementations
    metabase/                 Metabase REST client
    guardrails/               YAML-driven guardrail engine
    metrics/                  atomic counter snapshots
    cache/                    Redis cache helpers
    whatsapp/                 WhatsApp + Twilio providers
    tenantctx/                request-scoped tenant identity
    migrate/                  golang-migrate runner
  migrations/
    control/                  Argentum control plane (managed)
    demo_tenant/              dev fixtures (never run on real tenants)
  pkg/models/                 transport-level message types
```

## Security

- Passwords hashed with Argon2id (memory=64 MiB, t=3, p=2).
- Access tokens are short-lived (15 min) JWTs; refresh tokens are
  httpOnly cookies (7 days, configurable).
- Tenant DSNs are encrypted at rest with AES-256-GCM (ARGENTUM_DSN_KEY).
- Every tenant query runs inside a read-only transaction with a
  per-statement timeout enforced by the driver dialect.
- Inbound webhooks are HMAC-SHA256 verified.
- YAML guardrails block SQL mutations, common SQL injection patterns,
  and redact credit-card / SSN / email PII from inputs and outputs.

## Endpoints (selection)

| Method   | Path                            | Auth | Purpose                                |
| -------- | ------------------------------- | ---- | -------------------------------------- |
| POST     | `/api/auth/signup`              | —    | create a new company + admin           |
| POST     | `/api/auth/login`               | —    | issue JWTs (refresh as cookie)         |
| POST     | `/api/auth/refresh`             | —    | exchange refresh cookie for access JWT |
| GET      | `/api/meta/supported-databases` | —    | which DB types the agent can dial      |
| POST     | `/api/connections`              | ✓    | register a tenant DSN (encrypted)      |
| POST     | `/api/connections/test`         | ✓    | dry-run a DSN before saving            |
| GET      | `/api/threads`                  | ✓    | list company threads                   |
| GET      | `/api/threads/:id/messages`     | ✓    | thread history                         |
| POST     | `/api/chat`                     | ✓    | send a message → agent runs async      |
| GET      | `/api/threads/:id/stream`       | ✓    | WebSocket stream of chat events        |
| GET      | `/api/usage/summary`            | ✓    | current-month usage + cost             |
| GET/POST | `/webhook/whatsapp`             | HMAC | WA Business / Twilio inbound webhooks  |

## Production deploy

```bash
docker-compose --profile prod up -d postgres redis metabase api worker dashboard
```

`api` and `worker` are independent processes that can scale separately.
Tasks enqueued to Redis via asynq survive API restarts, retry on transient
LLM failures (3 attempts with backoff), and fan out events to any API
replica via Redis pub/sub on `argentum:thread:{id}`. The dashboard
container builds the Vite app and serves it through nginx, proxying
`/api` and `/webhook` to the `api` service. The API binary self-applies
migrations on every boot so rolling deploys are safe as long as schema
changes are forward-compatible.

## License

Internal — All rights reserved.
