# Argentum — Product Overview and Capability Inventory

Written 2026-07-26 from source, not from marketing copy. Every capability below
was verified against the code path that implements it.

## 1. The product in one paragraph

Argentum is a multi-tenant, agentic business-intelligence assistant. A company
signs up, registers one or more analytical databases (Postgres, MySQL, or SQL
Server) with an encrypted DSN, and then asks questions in natural language from
whichever surface they already live in: the web dashboard, WhatsApp, Discord, or
Lark. An LLM agent introspects the schema, writes dialect-correct read-only SQL,
executes it inside a read-only transaction, and answers in the user's language —
Indonesian or English, with correct rupiah magnitude formatting. It can also
build Metabase cards and dashboards, produce downloadable PDF/XLSX/CSV
documents, and schedule any of that on a cron.

## 2. Who it is for

The code reveals the target customer more precisely than the landing page does:

- **Indonesian-first.** The guardrail topic filter carries a full Indonesian
  keyword set alongside English. The system prompt has an entire section on
  Juta/Miliar/Triliun magnitude formatting and Indonesian decimal separators.
  `trivialReply()` answers "pagi", "makasih", "sip" in Indonesian.
- **Non-technical business owners and operators**, not analysts. The agent is
  told to never return card IDs, always wrap charts in a dashboard, and always
  ask which data source is meant rather than guessing.
- **Companies whose team lives in chat.** Four channels, three of them chat
  platforms. WhatsApp is a first-class surface with its own phone allowlist,
  thread-forking heuristics, and markdown-link flattening.
- **Multi-system companies.** Multi-source support with LLM-generated source
  descriptions exists specifically so a company with, say, a CRM and an HRIS can
  ask questions that route to the right database.

## 3. Capability inventory

### 3.1 Conversation and channels

| Capability             | Implementation                                                                  |
| ---------------------- | ------------------------------------------------------------------------------- |
| Dashboard chat         | REST `POST /api/chat` → asynq → worker; WebSocket `/api/threads/:id/stream`      |
| WhatsApp               | `internal/whatsapp` — WhatsApp Business API **and** Twilio providers, HMAC verified |
| Discord                | `cmd/discord` gateway process + `internal/discord`, per-tenant bot tokens        |
| Lark / Feishu          | `internal/lark` — webhook in, REST reply out, per-tenant app secrets             |
| Streaming              | Agent deltas, thinking steps, tool calls, tool results fan out over Redis pub/sub |
| Multi-language reply   | System-prompt rule #1; guardrail messages carry `message_en` / `message_id`      |
| Small-talk short-circuit | `trivialReply()` regex skips the whole LLM pipeline on greetings and acks       |

**Threading is genuinely thoughtful.** `ThreadService` implements a hybrid
strategy: one thread chain per identity (phone / Discord user / Lark thread key),
continue the latest thread if the idle gap is under `THREAD_IDLE_MINUTES`, and
above that run a cheap LLM topic classifier against the thread's rolling summary
to decide continue-vs-fork. A rolling summary refreshes every
`SUMMARY_EVERY_N_TURNS` turns so classification stays cheap.

### 3.2 Data access

| Capability            | Implementation                                                              |
| --------------------- | --------------------------------------------------------------------------- |
| Driver abstraction    | `internal/adapters/db` registry + `Conn` interface; drivers self-register     |
| Postgres / MySQL / SQL Server | Three drivers, each with dialect-specific DSN building and read-only tx |
| Schema introspection  | `Conn.ExtractSchema()`, cached in Redis, invalidated on DSN rotation          |
| Read-only enforcement | `Conn.ExecuteReadOnly` — read-only transaction + per-statement timeout        |
| Result capping        | Hard row cap (`MAX_QUERY_ROWS`, default 100) **and** byte cap (`MAX_QUERY_RESULT_BYTES`, default 200 KB) with tail-trimming and a `truncated` flag |
| Multi-source routing  | Source catalog injected into every turn; per-source `db_type` returned in every tool result so the agent picks the right dialect |
| Source descriptions   | `ConnectionDescriber` uses the light LLM to auto-write a description of each connected database, used for routing |
| Connection pooling    | `TenantConnPool` — 200 entries, 30-minute TTL, keyed by (company, source)     |

### 3.3 Agent tools

Seven tools, registered in `cmd/worker/main.go`:

| Tool                  | Purpose                                                                |
| --------------------- | ---------------------------------------------------------------------- |
| `list_sources`        | Enumerate the company's registered databases                            |
| `get_schema`          | Tables, columns, relationships — optionally filtered to named tables     |
| `run_sql`             | Read-only SELECT against one source                                     |
| `create_visualization`| Metabase card from a SQL query, returns `dashboard_cards`               |
| `create_dashboard`    | Combine cards into one dashboard with a shareable URL                   |
| `generate_document`   | PDF / XLSX / CSV to S3-compatible storage, returns a presigned URL      |
| `schedule_task`       | Create a cron-scheduled agent task from within the conversation          |

`generate_document` registers itself only when `MINIO_ENDPOINT` is set — the
rest of the agent runs unchanged without object storage.

### 3.4 Retrieval and context engineering

This is where the most recent engineering effort went, and it is the most
commercially significant part of the system.

- **Embedding-based table picker.** Per-source opt-in
  (`db_connections.enable_table_embedding`). Table descriptions are embedded and
  stored in pgvector; each turn embeds the user message, runs a top-K similarity
  query per source, and prepends a hint naming the likely-relevant tables so the
  agent calls `get_schema` pre-filtered instead of dumping the whole catalog.
  Fails silent — the plain `get_schema` path still works.
- **Anthropic prompt caching.** When the primary LLM interface is Anthropic, the
  agent caches system message, tool definitions, and the conversation prefix with
  a 5-minute TTL. Cache-create and cache-read tokens are metered at 1.25× and
  0.10× the input rate respectively.
- **Context injection order** (from `ChatRunner.Run`): table-picker hint →
  source catalog → currency → company name → user message. Order is deliberate;
  the hint sits closest to the top.
- **Memory hydration.** Prior turns are re-loaded from Postgres into agent memory
  each turn (`HISTORY_HYDRATE_LIMIT`, default 20), guarded against duplicates by
  checking existing conversation memory first.

### 3.5 Guardrails

A YAML-driven engine (`internal/guardrails`) with four action types — `block`,
`require`, `redact`, `filter` — and two pattern types: `regex` and `llm`. The
config is 259 lines and represents real production tuning:

- **Topic enforcement** (`require`): ~15 regex families covering BI vocabulary in
  English and Indonesian, then an LLM pattern as the final admitting gate for
  glue turns and follow-ups. The regexes are annotated with why each was
  narrowed — e.g. bare `margins?` was excluded because it matches CSS discussion.
- **SQL mutation blocking**: unambiguous keywords (DROP/TRUNCATE/ALTER/GRANT/
  REVOKE) match on a trailing space; common English words (CREATE/UPDATE/INSERT)
  require SQL object context so "create a dashboard" is never blocked.
- **Prompt-injection blocking**: regex plus a deliberately conservative LLM
  classifier that defaults to FALSE (the most recent commit tuned exactly this).
- **PII redaction** on output: SSN, credit card, email, Indonesian NIK, phone.

### 3.6 Automation

`scheduled_tasks` + `scheduled_task_runs`, driven by
`asynq.PeriodicTaskManager` with a DB-backed config provider that re-syncs every
30 seconds — so a task created through the UI or by the agent goes live without
a worker restart. Each fire enqueues a normal `chat:run` against a dedicated
thread, and the run row is closed out by `ChatRunner` via the
`ScheduledRunMarker` interface.

### 3.7 Metering and cost control

- Every LLM call flows through `MeteredLLM`, which records tokens in/out plus
  Anthropic cache tokens into `usage_events` with a per-model cost lookup
  (`llm_pricing.go`), falling back to a flat default rate for unknown models.
- Non-LLM actions are also metered: SQL query, Metabase card, dashboard, document.
- Audit endpoints roll usage up by company, thread, channel, and end-user
  identity, over an arbitrary window.
- `company_credits` holds a **soft** balance that is decremented but never
  enforced. See [`03-gap-analysis.md`](03-gap-analysis.md).

### 3.8 Per-tenant LLM configuration

`company_llm_credentials` lets each tenant supply their own provider, model, and
API key (encrypted with the same DSN cipher) for three tiers — primary, light,
and embedding. `llmtenant.ClientCache` resolves and caches 300 clients with a
30-minute TTL, wrapping each in `MeteredLLM`. Missing rows fall back to the
environment defaults. This is BYO-LLM, already built.

### 3.9 Operations

- **Deploy:** Helm chart with separate api / worker / discord deployments,
  Traefik IngressRoute, Bitwarden secret integration, non-root pod security
  context.
- **Images:** GHCR, built and pushed on `v*.*.*` tags.
- **Health:** `/health`, `/ready` (pings control DB), `/metrics` (custom JSON
  snapshot of atomic counters).
- **Migrations:** self-applied by `cmd/api` on boot.

## 4. What the surfaces look like

**Dashboard** (`apps/dashboard`): login/signup, onboarding (add first
connection), chat with streaming tool-call cards and markdown rendering, threads
list, saved/generated dashboards, scheduled tasks (list, form with cron presets,
run history sheet), usage analytics (overview / threads / channels / users tabs
with a thread detail sheet), and settings (general, connections with test +
reindex + RAG probe, phones, integrations, Discord, Lark, about).

**Landing** (`apps/landing`): hero with a scripted live chat demo, features,
how-it-works, use-cases, integrations, CTA. Red/rose theme.

## 5. Positioning as built vs. as marketed

The landing page claims delivery to "web, WhatsApp, and Telegram". Telegram is
not implemented anywhere in the backend; Discord and Lark are, and are not
mentioned. This is a copy/product mismatch worth fixing in either direction —
tracked in [`plan/backlog.md`](../plan/backlog.md).
