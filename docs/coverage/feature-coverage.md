# Feature Coverage Matrix

What Argentum actually does, as of 2026-07-26 (`argentum` @ `3891579`).

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
| Slack                  | ❌     | Not implemented                                                          |
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
| Document generation             | ✅     | PDF / XLSX / CSV → S3, presigned URL. Registers only if MinIO set   |
| Scheduled agent tasks           | ✅     | Cron + timezone, DB-backed periodic manager, 30s sync, run history  |
| Embedding table picker (RAG)    | ✅     | Per-source opt-in, top-K hint injection, silent-fail                |
| Anthropic prompt caching        | ✅     | System + tools + conversation prefix, 5m TTL, cache tokens billed   |
| Multi-language reply            | ✅     | EN / ID, with Indonesian magnitude + separator rules                |
| Rolling thread summary          | ✅     | Every `SUMMARY_EVERY_N_TURNS` turns                                 |
| Topic-based thread forking      | ✅     | Idle gap → cheap LLM classifier → continue or fork                  |
| Small-talk short-circuit        | ✅     | Skips the entire LLM pipeline on greetings/acks                     |
| Multi-step reasoning depth      | 🟡     | Capped at **3 iterations**; deep chains truncate                    |
| Metric / semantic layer         | ❌     | Every question re-derives SQL. Same question can yield two numbers  |
| Write-back / actions            | ❌     | Structurally read-only by design; no permissioned action layer      |
| Proactive alerts / watchers     | ❌     | Automation is cron-only; nothing is condition-triggered             |
| Multi-agent / specialist agents | ❌     | Single agent, single prompt                                         |
| Forecasting / anomaly detection | ❌     | Not implemented                                                     |

## Safety and guardrails

| Feature                     | Status | Notes                                                                   |
| --------------------------- | ------ | ----------------------------------------------------------------------- |
| Read-only query enforcement | ✅     | Read-only transaction + per-statement timeout in the driver              |
| Row + byte result caps      | ✅     | 100 rows / 200 KB default, tail-trimmed with a `truncated` flag          |
| SQL mutation blocking       | ✅     | Tuned so "create a dashboard" / "update me on sales" pass                |
| SQL injection patterns      | ✅     | Regex family on input                                                   |
| Prompt-injection blocking   | ✅     | Regex + conservative LLM classifier (defaults FALSE)                    |
| Topic enforcement           | ✅     | Bilingual regex families + LLM admitting gate                           |
| PII redaction               | 🟡     | Works, but over-broad: any 16-digit number, all emails, all phone numbers are blanked in output — breaks legitimate customer-contact queries |
| System-prompt leak guard    | 🟡     | Blocks any output containing "you are an ai"; false-positives on "what can you do?" |
| Rate limiting               | 🟡     | Flat 60 req/min for all authenticated callers; not per-plan, not per-channel |
| Agent action audit log      | ❌     | No record of what the agent did, only what it cost                      |

## Accounts, billing, admin

| Feature                        | Status | Notes                                                             |
| ------------------------------ | ------ | ----------------------------------------------------------------- |
| Signup / login / refresh       | ✅     | Argon2id, 15m access JWT, httpOnly refresh cookie                  |
| Company + user model           | ✅     | One user → exactly one company                                     |
| Role model (admin / member)    | 🔧     | `AdminOnly()` middleware exists, **applied to zero routes**        |
| Team invite / user management   | ❌     | Only `GET /api/users/me` is exposed                                |
| Per-tenant LLM credentials     | ✅     | Primary / light / embedding tiers, encrypted, cached               |
| Usage metering                 | ✅     | LLM (incl. cache tokens), SQL, cards, dashboards, documents        |
| Usage audit endpoints          | ✅     | By company / thread / channel / end-user, arbitrary window         |
| Per-message cost attribution   | ❌     | `messages.tokens_*` always 0; `usage_events.message_id` always ""  |
| Credit balance                 | 🟡     | Tracked and decremented, **never enforced** — no spend ceiling      |
| Plans / quotas / payment       | ❌     | No tiers, no Stripe/Xendit, no invoicing                            |
| Self-serve onboarding          | 🟡     | Signup → add connection works; no guided setup, no embedding prompt |

## Platform / integration surface

| Feature                     | Status | Notes                                                     |
| --------------------------- | ------ | --------------------------------------------------------- |
| REST API for the dashboard  | ✅     | Documented in `apps/backend/docs/api.md` + Postman collection |
| WebSocket event stream      | ✅     | Redis-fanned so any API replica serves any thread          |
| Inbound webhooks            | ✅     | WhatsApp, Discord, Lark — all signature-verified           |
| API keys / machine auth     | ❌     | JWT-only; no customer system can integrate                 |
| MCP server                  | ❌     | Tools live only in the worker's in-process registry        |
| Outbound webhooks           | ❌     | Nothing can subscribe to Argentum events                   |
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
| Metrics endpoint            | 🟡     | Custom JSON, **unauthenticated**, not Prometheus format            |
| Structured JSON logging     | ✅     | logrus, JSON formatter, asynq logs unified into it                 |
| Graceful shutdown           | ✅     | Worker drains in-flight tasks on SIGTERM                           |
| Distributed tracing         | ❌     | Not implemented                                                    |
| Error tracking (Sentry)     | ❌     | Not implemented                                                    |
| Persisted run traces        | ❌     | Tool calls stream to the UI, then are discarded                    |
| CI test gate                | ❌     | CI builds api + worker only — no test, vet, lint, or frontend check |

---

## Coverage by area, at a glance

| Area                        | Depth        | Comment                                                     |
| --------------------------- | ------------ | ----------------------------------------------------------- |
| Chat + channels             | ██████████   | Best-in-class for the segment. Four surfaces, all polished.  |
| Data access + SQL safety    | █████████░   | Three drivers, correct isolation. Missing warehouse drivers. |
| Context / cost engineering  | █████████░   | RAG, caching, tiering, capping. Genuinely sophisticated.     |
| Guardrails                  | ████████░░   | Heavily tuned; PII rules over-reach.                          |
| Automation                  | ████░░░░░░   | Cron only. No condition triggers, no proactivity.            |
| Metering / usage visibility | ███████░░░   | Rich reporting; no enforcement, no message-level attribution. |
| Accounts / RBAC / teams     | ███░░░░░░░   | Single-user-per-company in practice. Role model unwired.      |
| Billing / monetization      | ██░░░░░░░░   | Tracks cost, cannot charge or cap it.                         |
| Platform / agent-callable   | ██░░░░░░░░   | No machine auth, no MCP, no outbound webhooks.               |
| Embeddability               | ░░░░░░░░░░   | Zero. Argentum lives only in its own dashboard and in chat apps. |
| Testing / evaluation        | █░░░░░░░░░   | 3/35 packages. No answer-quality measurement at all.          |
| Observability               | ███░░░░░░░   | Logs + counters. No traces, no error tracking, no replay.     |
