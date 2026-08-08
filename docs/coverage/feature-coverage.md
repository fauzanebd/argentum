# Feature Coverage Matrix

What Argentum actually does. Written 2026-07-26 (`argentum` @ `3891579`) and
kept current since; the rows below carry the ticket that last moved them.

**Last revised 2026-08-03**, after the live-gate run of 2026-08-02 proved ten
tickets that had landed code-complete. Three rows in *Agent capability* had said
`❌` since the file was written and were shipped and gated the day before —
the metric registry, watchers, and the action framework. A coverage matrix that
under-reports is the same defect as one that over-reports: it is read to decide
what to build next.

**Legend**
`✅ Shipped` — complete, in use.
`🟡 Partial` — works, but with a stated limitation.
`🔧 Built, not wired` — code exists, nothing calls it.
`❌ Absent` — not implemented.

---

## Channels

| Feature                | Status | Notes                                                                    |
| ---------------------- | ------ | ------------------------------------------------------------------------ |
| Dashboard web chat     | ✅     | Streaming over WebSocket, tool-call cards, markdown                       |
| WhatsApp Business API  | ✅     | HMAC-verified webhook, phone allowlist, markdown-link flattening          |
| WhatsApp via Twilio    | ✅     | Alternate provider behind the same `whatsapp.Provider` interface          |
| Discord                | ✅     | Dedicated `cmd/discord` gateway, per-tenant bot tokens, user allowlist    |
| Lark / Feishu          | ✅     | Webhook in, REST reply out, per-tenant app secret, thread-key mapping     |
| Telegram               | ❌     | **Advertised on the landing page.** Not implemented anywhere              |
| Slack                  | ✅     | Events API webhook in, `chat.postMessage` out, per-tenant bot token, user allowlist, thread + DM keying |
| Email                  | ❌     | Not implemented                                                          |

## Data sources

| Feature                       | Status | Notes                                                       |
| ----------------------------- | ------ | ----------------------------------------------------------- |
| Postgres driver               | ✅     | Read-only tx, statement timeout, DSN builder                 |
| MySQL driver                  | ✅     | Same contract                                                |
| SQL Server driver             | ✅     | TLS 1.0 allowance + cert trust for IP-based hosts; no read-only tx option (dialect limitation, handled) |
| Multi-source per company      | ✅     | Source catalog injected per turn; agent asks when ambiguous  |
| LLM-generated source description | ✅  | `ConnectionDescriber`, regenerate endpoint                   |
| Connection test before save   | ✅     | `POST /api/connections/test`                                 |
| DSN encryption at rest        ​| ✅     | AES-256-GCM, key from `ARGENTUM_DSN_KEY`                     |
| Schema cache + invalidation   | 🟡     | Redis-cached, invalidated on DSN rotation — **per process**; API and worker caches are independent |
| BigQuery / Snowflake / ClickHouse | ❌ | Driver registry makes these additive, none written           |
| Non-SQL sources (Sheets, APIs) | ❌    | Not implemented                                              |

## Agent capability

| Feature                        | Status | Notes                                                              |
| ------------------------------ | ------ | ------------------------------------------------------------------ |
| Natural-language → SQL          | ✅     | Dialect-aware per source via `db_type` in every tool result         |
| Schema introspection            | ✅     | Optionally pre-filtered to named tables                             |
| Metabase card creation          | ✅     | Returns `dashboard_cards` for direct hand-off                       |
| Metabase dashboard creation     | ✅     | Card IDs never surfaced to users; always wrapped                    |
| Document generation             | ✅     | PDF / PPTX / XLSX / CSV → S3, presigned URL. Registers only if MinIO set. PDF rebuilt in `T-R2`: cover, running header, `Page N of M`, numbered sections, KPI cards, callouts, typed and locale-formatted cells, content-weighted columns. `T-R3` added chart sections — line, bar, grouped/stacked bar, pie, donut, sparkline, drawn in Go at 200 DPI on the shared token palette. `T-R4` added the deck: the same spec projected onto 16:9 slides, prose in the speaker notes, tables continuing across `(cont.)` slides, OOXML written by hand and byte-deterministic. `T-R5` made the identity the tenant's: logo, accent colour, legal name, document language, confidentiality label and footer line, resolved per field so a partly configured tenant is partly branded rather than broken |
| Scheduled agent tasks           | ✅     | Cron + timezone, DB-backed periodic manager, 30s sync, run history  |
| Embedding table picker (RAG)    | ✅     | Per-source opt-in, top-K hint injection, silent-fail                |
| Anthropic prompt caching        | ✅     | System + tools + conversation prefix, 5m TTL, cache tokens billed   |
| Multi-language reply            | ✅     | EN / ID, with Indonesian magnitude + separator rules                |
| Rolling thread summary          | ✅     | Every `SUMMARY_EVERY_N_TURNS` turns                                 |
| Topic-based thread forking      | ✅     | Idle gap → cheap LLM classifier → continue or fork                  |
| Small-talk short-circuit        | ✅     | Skips the entire LLM pipeline on greetings/acks                     |
| Multi-step reasoning depth      | ✅     | `T-16` replaced the flat 3-iteration cap with a per-turn budget: tools are refused during the final permitted iteration so the model writes its answer knowing it ran out, and `ForDocument` raises the ceiling for a turn that has to render a file. The SDK runs the turn under that same number — it used to take its own from boot config, which made the document headroom unreachable |
| Metric / semantic layer         | ✅     | `T-06`/`T-07`: `metric_definitions` — one validated SQL expression per metric, with grain and window — plus `list_metrics` and `query_metric`. The agent prefers a defined metric over re-deriving SQL and falls back to `run_sql` when none covers the question. **Gated live 2026-08-02**: the same number twice, and it is the number `psql` returns. On the five registry eval cases, 1/5 → 5/5 and mean input tokens 12,711 → 3,296 ([`metric-registry.md`](metric-registry.md)) |
| Write-back / actions            | ✅     | `T-10`/`T-11`/`T-12b`: `propose_action` → human approval → exactly-once execution, with `send_message` and `http_action` registered and per-company enablement. SQL is still read-only and always will be — writes go through this framework, never through the query path. **Gated live 2026-08-02**: approved once executes once, twice does not, rejected is terminal, a 25-hour-old proposal cannot be approved ([`action-framework.md`](action-framework.md)). `T-12a`'s WhatsApp delivery is code-complete with its live gate deferred by the repo owner — closing it sends a real message to a real handset |
| Proactive alerts / watchers     | ✅     | `T-08`/`T-09`: a watcher evaluates a metric on a schedule, and a breach fires an agent turn that explains itself and delivers to the tenant's channels, with cooldown suppression and a mandatory dry-run before enable. **Gated live 2026-08-02** ([`watchers.md`](watchers.md), [`watchers-ui.md`](watchers-ui.md)) |
| Tenant agent roster (Marketing / Ops / HR / Finance) | ✅ | `T-S1` built the roster — `agents` + `agent_sources`, CRUD, Settings tab. `T-S2` made it run: persona appended to the system prompt, tools filtered by allowlist, sources enforced in `ResolveSource` and `list_sources`, `agent_id` on every audit row and usage event. `T-S3` put a picker in the dashboard chat and `T-S5` put `agent_id` on `POST /v1/chat` and `POST /v1/reports` beside a keyless `GET /v1/agents`. `T-S4` closed it: a Discord channel, Lark chat or WhatsApp number binds to an agent, and an inbound message answers as that one. All five gated live ([`agent-roster.md`](agent-roster.md)) |
| The agent knows the business it works for | ✅ | `T-B1` ships `company_profiles` — industry, what the business does, free-form context, fiscal year start — rendered into every turn's system prompt ahead of the persona, capped at 600 tokens and shown back in Settings exactly as the model reads it. A company with no row is byte-identical to before. `T-B3` replaced the blank create form with a gallery: six templates in `config/agent_templates.yaml` — Finance, Sales, Operations, Marketing, People, Customer Support — plus a first-class blank card, each prefilling persona, tools and hint-matched sources into an ordinary editable draft, with the template's starter questions offered on the agent's first empty thread. Templates are code, never tenant rows; `agents.template_key` is analytics only. `T-B2` drafts the profile from the connected source's own metadata for the tenant to review, and `T-B4`'s **Generate with AI** turns a sentence the tenant types into an improved description plus the persona to run it with, applied into the fields with one Undo. All four gated live, `T-B2`/`T-B3`/`T-B4` on 2026-08-01 ([`business-context.md`](business-context.md)) |
| The prompt describes the tools the turn actually holds | ✅ | The system prompt was a constant naming all nine tools while `filterTools` handed the model whatever the agent's allowlist left, so a Sales-template agent — `get_schema`, `run_sql`, `create_visualization` — was told it could also generate documents and wrap cards in a dashboard. Asked for a sales overview "report in PDF" it answered with markdown in the chat and told the user to press Ctrl+P. The catalog and every tool-dependent guideline are now composed per turn from the composed tool list, numbered without gaps; a turn with no tools is told so rather than promised nine. The six gallery cards all suggest the same read-and-report set (`schedule_task` and `propose_action` on none of them, deliberately), and `043` / `044` backfill the agents that predate the fix — output tools to every scoped agent, `query_metric` only where no `agent_sources` row would be crossed |
| Multi-agent / specialist agents | ❌     | Internal planner + specialists. Backlog, not the roster above       |
| Forecasting / anomaly detection | ❌     | Not implemented                                                     |

## Safety and guardrails

| Feature                     | Status | Notes                                                                   |
| --------------------------- | ------ | ----------------------------------------------------------------------- |
| Read-only query enforcement | ✅     | Read-only transaction + per-statement timeout in the driver              |
| Row + byte result caps      | ✅     | 100 rows / 200 KB default, tail-trimmed with a `truncated` flag          |
| SQL mutation blocking       | ✅     | Tuned so "create a dashboard" / "update me on sales" pass                |
| SQL injection patterns      | ✅     | Regex family on input                                                   |
| Prompt-injection blocking   | ✅     | Regex + conservative LLM classifier (defaults FALSE). Argentum's own per-turn instructions travel in the system prompt rather than the user message (`T-A2b`), so the classifier only ever judges what a caller sent — it refused four of five agentic report turns while they did not ([`api-reports.md`](api-reports.md) §7) |
| Topic enforcement           | ✅     | Bilingual regex families + LLM admitting gate                           |
| PII redaction               | ✅     | `T-07b`. Three findings closed together. The rules **had never executed** — agent-sdk-go applies output guardrails on its blocking path and every chat turn streams — so they now run in `ChatRunner`, the same seam the fabrication check uses, after it and never before. `redact_nik` fires only on a number a `nik`/`ktp` label sits beside, so a 16-digit order id survives. And the contact rules are a per-company policy (`companies.pii_redaction_mode`: `strict` \| `contact_ok` \| `off`, default `strict`, editable in Settings), because there is no email shape that tells a legitimate customer-contact query apart from a leak. `contact_ok` returns emails and phones; identity documents and card numbers still go, and a failed policy read redacts at strict |
| System-prompt leak guard    | ✅     | `T-07b`: narrowed to first-person disclosure (`my instructions are…`, `my system prompt says…`). "What can you do?" and "I can't share my system prompt" both answer normally. Carries no PII class, so no tenant policy can switch it off |
| Rate limiting               | 🟡     | Flat 60 req/min per company for dashboard sessions; `T-13` adds a separate per-key bucket for `/v1` (`API_V1_RATE_PER_MIN`, default 120). Neither is per-plan, and both fail open when Redis is unhealthy |
| Agent action audit log      | ✅     | `T-05`: one append-only `agent_actions` row per tool call — actor, channel, redacted args, status, rows, duration — written by a decorator over the whole tool registry, plus a row for a turn a guardrail stopped. Admin-only `GET /api/audit/actions` ([`agent-audit.md`](agent-audit.md)). No UI yet; `T-A5` builds one |

## Accounts, billing, admin

| Feature                        | Status | Notes                                                             |
| ------------------------------ | ------ | ----------------------------------------------------------------- |
| Signup / login / refresh       | ✅     | Argon2id, 15m access JWT, httpOnly refresh cookie                  |
| Company + user model           | ✅     | One user → exactly one company                                     |
| Role model (admin / member)    | ✅     | `T-04`: 26 routes admin-gated by a policy table the router's route list is diffed against ([`rbac.md`](rbac.md)) |
| Team invite / user management   | 🟡     | `T-04`: invite → accept → login, role change, revoke, last-admin guard. **No email** — the link is handed to the inviting admin |
| Per-tenant LLM credentials     | ✅     | Primary / light / embedding tiers, encrypted, cached               |
| Usage metering                 | ✅     | LLM (incl. cache tokens), SQL, cards, dashboards, documents        |
| Usage audit endpoints          | ✅     | By company / thread / channel / end-user, arbitrary window         |
| Per-message cost attribution   | ❌     | `messages.tokens_*` always 0; `usage_events.message_id` always ""  |
| Credit balance                 | 🟡     | `T-03`: enforced before every turn on every channel and every scheduled fire; BYO-key tenants exempt ([`credit-enforcement.md`](credit-enforcement.md)). **No way to top up but SQL** |
| Plans / quotas / payment       | ❌     | No tiers, no Stripe/Xendit, no invoicing                            |
| Self-serve onboarding          | 🟡     | Signup → add connection works; no guided setup, no embedding prompt |

## Platform / integration surface

| Feature                     | Status | Notes                                                     |
| --------------------------- | ------ | --------------------------------------------------------- |
| REST API for the dashboard  | ✅     | `apps/backend/docs/api.md` still describes the dashboard's `/api` routes and is **partly stale** — it predates the `bigref` refactor. Its `/v1` section is superseded by the OpenAPI spec (`T-A4`) and the file now says so at the top. The Postman collection was **not** current, despite this row's previous claim: it described a server with no auth layer and a tenant fixed by `TENANT_ID`. `T-A4` regenerated it from the spec, `/v1` only |
| Public `/v1` contract       | ✅     | `T-A1`: typed error envelope, `X-Request-Id` end to end, `Idempotency-Key` with replay/in-flight/reuse handling, `RateLimit-*` headers, cursor pagination, kill switch, body cap, `api` channel ([`api-foundation.md`](api-foundation.md)). **`T-A2` applied all of it to real routes** and **`T-A3` closed the last two tested-only items**. **`T-A4` published it**: OpenAPI 3.1 at `apps/backend/openapi/v1.yaml`, served keyless at `GET /v1/openapi.json`, with four CI checks binding it to the code in both directions — routes, scopes, response fields, and the committed artifacts generated from it ([`api-contract.md`](api-contract.md)) |
| Client SDKs                 | ✅     | `T-A4`: `@argentum/sdk` (Node, no runtime dependencies) and `argentum` (Python, sync + async). Wire types generated from the spec, ergonomics hand-written; retry with backoff honouring `Retry-After`, automatic `Idempotency-Key` reused across retries, typed errors mirroring the envelope, cursor pagination followed for you. Neither is published to a registry yet ([`api-contract.md`](api-contract.md) §6) |
| Integrator documentation    | ✅     | `T-A4`: [`docs/api/quickstart.md`](../api/quickstart.md) — empty directory to a branded PDF, curl then Node then Python, measured at 1s and 4s of machine time. Every code block on the page is a file CI executes against a real server, and a block that has drifted from its file is a red build |
| Reports over the API        | ✅     | `T-A2`: `POST /v1/reports/render` takes a spec and returns a file (JSON with a presigned URL, or the bytes inline); `POST /v1/reports` takes a prompt, runs a real agent turn and answers 202, collectable by poll, SSE or a signed callback. `GET /v1/documents` is cursor-paginated and filterable, `:id` re-presigns on every read, `/content` streams. All four formats. An untrusted spec is capped on rows, columns, string length, sections and chart points **before** a renderer sees it ([`api-reports.md`](api-reports.md)) |
| Chat over the API           | ✅     | `T-A3`: `POST /v1/chat` streams a turn over SSE — `started`, `delta`, `thinking`, `tool_call`, `tool_result`, `final`, with a 15s heartbeat and `Last-Event-ID` resume — or blocks for the answer and returns 504 with resumable ids when the wait, not the turn, runs out. Threads keyed by the tenant's own `user_ref`, cursor-paginated transcript reads, and `user_ref` isolation enforced rather than trusted ([`api-chat.md`](api-chat.md)) |
| WebSocket / SSE event stream | ✅    | Redis-fanned so any API replica serves any thread. `T-A2` added a second reader of the same pubsub (report progress) and `T-A3` a third (chat), rather than a second event pipeline. **The schema is documented for the first time** — [`api-chat.md`](api-chat.md) §2, closing `api-surface.md` observation 4 |
| Inbound webhooks            | ✅     | WhatsApp, Discord, Lark — all signature-verified           |
| API keys / machine auth     | ✅     | `T-13`: scoped, hashed, revocable keys with a per-key rate bucket and a Settings tab; `/v1` authenticates with them and refuses a dashboard JWT ([`api-keys.md`](api-keys.md)). **`T-A2` is the first real call site**: `write:reports` and `read:documents` gate seven routes, `T-A3`'s `write:chat` and `read:threads` six more, and a key with no scopes is proven to reach nothing but `/v1/me`. `T-A3` wrote the first audit rows attributed to a key |
| Per-key API observability   | ✅     | `T-A5`: Settings → API Keys shows each key's last-24h calls, error rate and latency, and the last 50 non-2xx responses with the request id the caller was handed. `GET /v1/usage` reports spend by model over a window the caller picks, plus the credit position. `/metrics` grows per-route latency histograms; its per-key labels need `METRICS_TOKEN`, and since 2026-08-03 so does the endpoint itself — unset means loopback only ([`api-observability.md`](api-observability.md)) |
| MCP server (Argentum as server) | ✅ | `T-14` (2026-08-03): `cmd/mcp` serves eight tools — `list_sources`, `get_schema`, `run_sql`, `list_metrics`, `query_metric`, `list_watchers`, `create_visualization`, `create_dashboard` — over the streamable HTTP transport, authenticated by the same API key `/v1` takes and gated by two new scopes (`read:data`, `write:visualizations`). It adapts `internal/tools` and reimplements nothing, so each call is budget-guarded and writes an `agent_actions` row attributed to the key without a line of audit code. `generate_document`, `schedule_task` and `propose_action` are deliberately absent — an MCP client is an agent we did not write. Gate outstanding ([`mcp-server.md`](mcp-server.md), [`../mcp/setup.md`](../mcp/setup.md)) |
| MCP client (the tenant's server as a source) | ✅ | `T-M1`→`T-M3`: a tenant registers their own MCP server — ticketing, CRM, an internal ops API — behind an SSRF guard that resolves the host before storing it, and their agents call its tools alongside `run_sql`. Namespaced per server, budget-guarded, audited and metered like any other tool. **Gated live 2026-08-02 against a real server**, not a mock: a bound agent answered a delivery-status question from the courier's own `tools/call`. `T-M4` (2026-08-03) opened the write half without opening a second write path: a tool an admin classified as a write is offered as a proposing tool, and running it is the `mcp_call` action inside `T-10`'s framework — approve once, execute once, with the card showing the literal payload and the gates re-read at approval time. Its live gate is outstanding ([`mcp-source.md`](mcp-source.md)) |
| Outbound webhooks           | ✅     | `T-A2` built the sender: `internal/webhookout` signs HMAC-SHA256 over `<t>.<body>`, delivers through asynq with exponential retry, refuses a target on our own network, and logs every attempt. **`T-15` (2026-08-03) added the subscription model on top of it, not beside it**: `webhook_subscriptions`, a fan-out for `watcher.breached`, `action.executed` and `scheduled_task.completed` — the last two published on failure as well as success — and auto-disable after twenty consecutive failed deliveries, with Settings → Webhooks showing each subscription's health. No second signer and no second retry loop. Gate outstanding ([`outbound-webhooks.md`](outbound-webhooks.md)) |
| Public/embeddable dashboards | 🟡    | Metabase URLs are shareable; no Argentum-native embedding  |
| **Embeddable chat widget**  | ❌     | No way to put Argentum on a customer's own internal site. Using it means leaving that site. Planned T-19→T-23 |
| Embed auth (HMAC identity)  | ❌     | No browser-safe key type, no origin allowlist, no short-lived session token |
| Client SDKs (npm / pip)     | ❌     | Nothing published                                          |

## Operations

| Feature                    | Status | Notes                                                            |
| -------------------------- | ------ | ---------------------------------------------------------------- |
| Docker images              | ✅     | api / worker / discord, GHCR, tag-triggered                       |
| Helm chart                 | ✅     | Separate deployments, Traefik IngressRoute, Bitwarden secrets      |
| Auto-applied migrations    | ✅     | On `cmd/api` boot                                                 |
| Health / readiness probes   | ✅     | `/health`, `/ready` (pings control DB)                            |
| Metrics endpoint            | ✅     | `T-17`: Prometheus text exposition by default, JSON snapshot on `?format=json`, plus turn/tool/LLM/watcher/action counters and a ServiceMonitor template. Queue depth is still absent (needs an `asynq.Inspector`). **Authenticated**: `METRICS_TOKEN` set → the token or 401; unset → loopback only, 404 to everyone else. The snapshot carries LLM spend and token totals, which is what made the open version a disclosure |
| Structured JSON logging     | ✅     | logrus, JSON formatter, asynq logs unified into it                 |
| Graceful shutdown           | ✅     | Worker drains in-flight tasks on SIGTERM                           |
| Distributed tracing         | 🟡     | `T-17`: `internal/tracing` exports OTLP behind `OTEL_EXPORTER_OTLP_ENDPOINT` and installs no provider when it is unset, so a span costs nothing on a deployment with no collector. One span per turn and one per tool call, started at the same seam as the audit row. The guardrail, memory-hydration and embedding spans are not wired yet ([`observability.md`](observability.md)) |
| Error tracking (Sentry)     | ❌     | Not implemented                                                    |
| Persisted run traces        | ❌     | Tool calls stream to the UI, then are discarded                    |
| CI test gate                | ✅     | `T-02`: `go vet`, `golangci-lint` and `go test -race` on every backend change, plus the dashboard's own lint and build |
| Generated API types         | ✅     | `T-02b`: `packages/api-types` is generated from the Go structs by tygo and committed; the `types` job regenerates and diffs, so a struct change without `make types` is a red build. The dashboard's four hand-written `types.ts` files are gone ([`generated-types.md`](generated-types.md)) |

---

## Coverage by area, at a glance

| Area                        | Depth        | Comment                                                     |
| --------------------------- | ------------ | ----------------------------------------------------------- |
| Chat + channels             | ██████████   | Best-in-class for the segment. Four surfaces, all polished.  |
| Data access + SQL safety    | █████████░   | Three drivers, correct isolation. Missing warehouse drivers. |
| Context / cost engineering  | █████████░   | RAG, caching, tiering, capping. Genuinely sophisticated.     |
| Guardrails                  | █████████░   | Heavily tuned, and after `T-07b` the output half actually runs — under a policy the tenant sets. The open one is `semantic_prompt_injection`, which refused ordinary admin instructions in two consecutive live gates. |
| Automation                  | ████████░░   | `T-08`/`T-09` watchers: condition-triggered, delivered to the tenant's channels, with `T-10`'s approval framework behind anything that writes. Thresholds and percent-change only — nothing statistical yet. |
| Metering / usage visibility | ████████░░   | Rich reporting, and `T-03` enforces the balance before every turn on every channel. Still no per-message attribution. |
| Accounts / RBAC / teams     | ███████░░░   | `T-04`: roles enforced, teams expressible, removal ends sessions. No email delivery, no token revocation, two roles only. |
| Billing / monetization      | ██░░░░░░░░   | Tracks cost, cannot charge or cap it.                         |
| Platform / agent-callable   | █████████░   | `T-13`→`T-A5`: scoped machine keys and a published `/v1` contract — reports, chat, documents, usage — with generated Node and Python SDKs, and a docs site serving the quickstart and the spec to an integrator without the repo. `T-M1`→`T-M3` added the other direction: the tenant's own MCP server as a tool source. No MCP server of our own (`T-14`), no webhook subscriptions (`T-15`). |
| Embeddability               | ░░░░░░░░░░   | Zero. Argentum lives only in its own dashboard and in chat apps. |
| Testing / evaluation        | ███████░░░   | 22/49 packages after `T-02` and `T-04`; every CRITICAL one covered, `golangci-lint` at 0 issues, CI gates on `-race`. Answer quality is measured separately: `make eval` scores the golden set through the real agent. **40/40 on 2026-08-02** — five metric-registry cases added, ten assertions widened from `must_call` to `MustCallAny` where `query_metric` is now the better answer, and one prompt line (`withLanguageReminder`) that closed a language regression the registry's catalog had opened. Not like-for-like with the earlier 97.0% of 33 questions; each of the three changes is recorded with the measurement that motivated it ([`eval-baseline.md`](eval-baseline.md), [`metric-registry.md`](metric-registry.md) §3). |
| Observability               | ███████░░░   | Logs + counters, `T-05`'s append-only record of every tool call, and after `T-A5` a per-key `/v1` request record the **tenant** can read — calls, error rate, and the last 50 failures with the request id the caller was handed — plus per-route latency histograms on `/metrics` ([`api-observability.md`](api-observability.md)). `/metrics` now needs a credential or a loopback peer. No traces, no error tracking, and the Prometheus exposition format is still `T-17`'s. |
