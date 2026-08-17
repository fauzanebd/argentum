# API and Tool Surface Inventory

Complete route inventory extracted from handler `Register()` functions,
2026-07-26. 61 HTTP routes + 1 WebSocket + 1 reverse proxy + 7 agent tools.

**Re-counted from the router itself after `T-04` (2026-07-28): 70 HTTP routes +
1 WebSocket + 1 reverse proxy**, and **74 after `T-R5`** added the four report
branding routes (`gin.Engine.Routes()`, counting the proxy's
nine methods as one). `T-04` added six of those — two public invite routes and
four team routes. The rest of the gap is a hand-count that had drifted; the
number above is left as written because the tables below, not the headline,
are what this document is for.

> **Re-checked 2026-08-17, and the honest headline is that this file lags the
> router by a long way.** `cmd/api/policy.go` classifies **143** authenticated
> `/api` routes today, against the ~74 this document's tables cover — the gap is
> every surface added after `T-R5`: metrics, watchers, actions, agents and
> bindings, API keys, embed keys and sessions, MCP servers, the cookbook,
> message feedback, company profile, documents and shares, Slack, and all of
> `/v1`. Those live in their own coverage docs
> ([`api-foundation.md`](api-foundation.md), [`api-reports.md`](api-reports.md),
> [`api-chat.md`](api-chat.md), [`api-keys.md`](api-keys.md),
> [`metric-registry.md`](metric-registry.md), [`watchers.md`](watchers.md),
> [`action-framework.md`](action-framework.md), [`embed-auth.md`](embed-auth.md),
> [`mcp-source.md`](mcp-source.md)) and in `openapi/v1.yaml`, which is
> generated and gated. **The tables below were corrected where they were
> *wrong*; they are still not complete.** `TestEveryAuthedRouteIsClassified` and
> the spec are the source of truth — this remains a reading copy, and the count
> above is the number to check it against.

`Auth` column: `—` public, `JWT` any authenticated member, `JWT+` admin only,
`HMAC` signature-verified webhook.

> **Updated after `T-04` (2026-07-28).** Every `⚠️ not admin-gated` marker below
> is gone because the routes are gated now — and more of them than the ticket
> named. The decision for every authenticated route lives in
> `apps/backend/cmd/api/policy.go`; `TestEveryAuthedRouteIsClassified` fails if
> a route is added without one, or if an entry outlives its route. That test,
> not this table, is the source of truth — this file is a reading copy.
> Rationale per route: [`rbac.md`](rbac.md).

## Public

| Method | Path                            | Auth | Handler        |
| ------ | ------------------------------- | ---- | -------------- |
| GET    | `/health`                       | —    | `health.go`    |
| GET    | `/ready`                        | —    | `health.go`    |
| GET    | `/metrics`                      | —    | `health.go` ⚠️ **unauthenticated cost/token data** |
| GET    | `/api/meta/supported-databases` | —    | `meta.go`      |

## Auth

| Method | Path                 | Auth | Notes                              |
| ------ | -------------------- | ---- | ---------------------------------- |
| POST   | `/api/auth/signup`   | —    | Creates company + admin user       |
| POST   | `/api/auth/login`    | —    | Access JWT + refresh cookie        |
| POST   | `/api/auth/refresh`  | —    | Refresh cookie → new access JWT    |
| POST   | `/api/auth/logout`   | —    | Clears refresh cookie              |
| GET    | `/api/auth/invite`   | —    | `?token=` → invite preview; does not consume it |
| POST   | `/api/auth/accept-invite` | — | Sets the password, activates the account, logs in |

## Chat and threads

| Method | Path                             | Auth | Notes                                  |
| ------ | -------------------------------- | ---- | -------------------------------------- |
| GET    | `/api/threads`                   | JWT  | List company threads                    |
| POST   | `/api/threads`                   | JWT  | New dashboard thread                    |
| GET    | `/api/threads/:id`               | JWT  | Thread detail                           |
| DELETE | `/api/threads/:id`               | JWT  | Delete thread                           |
| GET    | `/api/threads/:id/messages`      | JWT  | History                                 |
| POST   | `/api/chat`                      | JWT  | Enqueue a turn (async). **402** when the tenant is out of credit; `budget_warning` on the 202 when close to it (`T-03`) |
| GET    | `/api/threads/:id/stream`        | JWT  | **WebSocket**; accepts token via `?at=` |

## Connections

| Method | Path                                                | Auth | Notes                            |
| ------ | --------------------------------------------------- | ---- | -------------------------------- |
| GET    | `/api/connections`                                  | JWT  | List sources                      |
| POST   | `/api/connections`                                  | JWT+ | Register a DSN (encrypted)        |
| PATCH  | `/api/connections/:id`                              | JWT+ | Update label / description / flags |
| PUT    | `/api/connections/:id/dsn`                          | JWT+ | Rotate DSN                        |
| POST   | `/api/connections/:id/default`                      | JWT+ | Set default source                |
| POST   | `/api/connections/:id/regenerate-description`       | JWT+ | Re-run the LLM describer          |
| POST   | `/api/connections/:id/reindex-embeddings`           | JWT+ | Rebuild the table-picker index    |
| POST   | `/api/connections/:id/test-rag`                     | JWT+ | Probe top-K retrieval for a query |
| POST   | `/api/connections/:id/test`                         | JWT+ | Test a saved connection           |
| POST   | `/api/connections/test`                             | JWT+ | Dry-run a DSN before saving; outbound to any host |
| DELETE | `/api/connections/:id`                              | JWT+ | Remove source                     |

## Company settings

| Method | Path                        | Auth | Notes                                     |
| ------ | --------------------------- | ---- | ----------------------------------------- |
| GET    | `/api/settings`             | JWT  | Company settings                           |
| PUT    | `/api/settings`             | JWT+ | Update settings                            |
| GET    | `/api/phones`               | JWT  | WhatsApp allowlist                         |
| POST   | `/api/phones`               | JWT+ | Add phone                                  |
| DELETE | `/api/phones/:phone`        | JWT+ | Remove phone                               |
| GET    | `/api/config/models`        | JWT  | Resolved models per role + per-1K rates    |
| GET    | `/api/users/me`             | JWT  | Current user                               |

## Team (`T-04`)

| Method | Path                    | Auth | Notes                                        |
| ------ | ----------------------- | ---- | -------------------------------------------- |
| GET    | `/api/users`            | JWT+ | Members and pending invites, with invite state |
| POST   | `/api/users/invite`     | JWT+ | Creates a pending user + a single-use token. **The plaintext token is returned once and never readable again** — there is no mail transport yet |
| PATCH  | `/api/users/:id`        | JWT+ | Change role; 409 on the last admin            |
| DELETE | `/api/users/:id`        | JWT+ | Deactivate a member, or delete a pending one (frees the globally unique email); 409 on the last admin |

## Report branding (`T-R5`)

| Method | Path                              | Auth | Notes                                        |
| ------ | --------------------------------- | ---- | -------------------------------------------- |
| GET    | `/api/reports/branding`           | JWT+ | The record, plus the defaults a blank field falls back to and the limits the form should enforce. Admin even for the read — it returns nothing a member can act on |
| PUT    | `/api/reports/branding`           | JWT+ | 400 with the measured contrast ratio when the accent is too pale for paper |
| POST   | `/api/reports/branding/logo`      | JWT+ | multipart `logo`; PNG/JPEG ≤512 KB, re-encoded to PNG, returns the key only — it does **not** save the record |
| POST   | `/api/reports/preview`            | JWT+ | Renders a fixed sample with the branding in the body, or with the stored record when the body is empty. Returns `application/pdf` for an `<iframe>` |

## Agent audit log (`T-05`)

| Method | Path                    | Auth | Notes                                        |
| ------ | ----------------------- | ---- | -------------------------------------------- |
| GET    | `/api/audit/actions`    | JWT+ | One row per tool call the agent made, newest first. Filters: `from`/`to` (RFC3339, default last 30 days), `thread_id`, `tool`, `limit` (default 100, max 500), `offset`. Admin because every row carries the full SQL the agent ran |

## Dashboards and scheduled tasks

**`/api/dashboards` changed hands in `T-D10` (2026-08-17).** It now serves
dashboards this product executes itself; the Metabase-backed list moved to
`/api/saved-dashboards`. The native one got the good name because it is the one
that stays — `T-D15` removes the other. **There is deliberately no create or
update route**: a dashboard is authored by the agent through `create_dashboard`,
and a second authoring surface would be a second place for the validation rules
to drift before there is a UI that needs one.

| Method | Path                                          | Auth | Notes                     |
| ------ | --------------------------------------------- | ---- | ------------------------- |
| GET    | `/api/dashboards`                             | JWT  | Native dashboards, definition only |
| GET    | `/api/dashboards/:id`                         | JWT  | One spec                   |
| GET    | `/api/dashboards/:id/data`                    | JWT  | Resolved panels. Separate from the definition on purpose: opening a dashboard runs a dozen queries against a tenant warehouse, and a client that only wants the title should not have to. `?refresh=1` is read and dropped until `T-D8` |
| DELETE | `/api/dashboards/:id`                         | JWT+ | Admin — a dashboard is a dozen panels somebody's Monday depends on |
| GET    | `/api/saved-dashboards`                       | JWT  | The Metabase-backed list, until `T-D15` |
| DELETE | `/api/saved-dashboards/:id`                   | JWT  | Delete                     |
| GET    | `/api/scheduled-tasks`                        | JWT  | List                       |
| POST   | `/api/scheduled-tasks`                        | JWT  | Create                     |
| GET    | `/api/scheduled-tasks/:id`                    | JWT  | Detail                     |
| PATCH  | `/api/scheduled-tasks/:id`                    | JWT  | Update                     |
| DELETE | `/api/scheduled-tasks/:id`                    | JWT+ | Delete                     |
| GET    | `/api/scheduled-tasks/:id/runs`               | JWT  | Run history                |
| GET    | `/api/scheduled-tasks/:id/runs/:runID`        | JWT  | Run detail                 |

## Usage

| Method | Path                                  | Auth | Notes                        |
| ------ | ------------------------------------- | ---- | ---------------------------- |
| GET    | `/api/usage/summary`                  | JWT  | Current-month rollup          |
| GET    | `/api/usage/credits`                  | JWT  | Balance + grant. **Enforced** since `T-03` |
| GET    | `/api/usage/threads`                  | JWT  | Per-thread rows, windowed      |
| GET    | `/api/usage/threads/:id`              | JWT  | One thread's breakdown         |
| GET    | `/api/usage/threads/:id/events`       | JWT  | Raw event audit trail          |
| GET    | `/api/usage/by-channel`               | JWT  | Rollup by channel              |
| GET    | `/api/usage/by-user`                  | JWT  | Rollup by end-user identity    |

## Integrations (Discord / Lark)

| Method | Path                        | Auth | Notes                                 |
| ------ | --------------------------- | ---- | ------------------------------------- |
| GET    | `/api/discord`              | JWT  | Credentials (secret-redacted)           |
| PUT    | `/api/discord`              | JWT+ | Save bot token                          |
| DELETE | `/api/discord`              | JWT+ | Remove                                  |
| GET    | `/api/discord/users`        | JWT  | Allowlist                               |
| POST   | `/api/discord/users`        | JWT+ | Add                                     |
| DELETE | `/api/discord/users/:id`    | JWT+ | Remove                                  |
| GET    | `/api/lark`                 | JWT  | Credentials                             |
| PUT    | `/api/lark`                 | JWT+ | Save app secret                         |
| DELETE | `/api/lark`                 | JWT+ | Remove                                  |
| GET    | `/api/lark/users`           | JWT  | Allowlist                               |
| POST   | `/api/lark/users`           | JWT+ | Add                                     |
| DELETE | `/api/lark/users/:id`       | JWT+ | Remove                                  |

## Webhooks

| Method | Path                             | Auth | Notes                                    |
| ------ | -------------------------------- | ---- | ---------------------------------------- |
| GET    | `/webhook/whatsapp`              | token | Meta verification challenge              |
| POST   | `/webhook/whatsapp`              | HMAC  | Inbound WA / Twilio messages             |
| POST   | `/webhook/discord/interactions`  | HMAC  | Discord interactions (Ed25519)           |
| POST   | `/webhook/lark/events/:app_id`   | HMAC  | Lark event callback, per-app routing     |

## Reverse proxy

| Method | Path                 | Auth | Notes                                                   |
| ------ | -------------------- | ---- | ------------------------------------------------------- |
| ANY    | `/metabase/*path`    | —    | Proxies to `METABASE_URL` when configured. Metabase's own auth applies |

## Agent tools (worker in-process registry)

Not HTTP-reachable. Built by `internal/tools.Registry()`
(`internal/tools/registry.go:94-130`) — **not** `cmd/worker/main.go:156`, which
is where this table said to look until 2026-08-17. `Names()` on that same list is
what the agents API serves as checkboxes, so the registry cannot drift from the
allowlist UI.

| Tool                   | Params                                            | Metered as         | Conditional              |
| ---------------------- | ------------------------------------------------- | ------------------ | ------------------------ |
| `list_sources`         | —                                                 | —                  | always                    |
| `get_schema`           | `source_id?`, `tables?`                           | —                  | always                    |
| `list_metrics`         | —                                                 | —                  | always (empty without a registry) |
| `query_metric`         | `metric_key`, `from?`, `to?`, `grain?`            | `sql_query`        | always                    |
| `run_sql`              | `sql`, `source_id?`                               | `sql_query`        | always                    |
| `create_dashboard`     | `title`, `panels[]`, `description?`, `source_id?`, `filters?`, `timezone?` | `metabase_dashboard` — see below | always      |
| `schedule_task`        | `name`, `prompt`, `cron_expression`, `timezone`   | —                  | always                    |
| `ask_clarification`    | `question`                                        | —                  | always, and with no dependencies at all (`T-Q4`) |
| `propose_action`       | `kind`, `params`                                  | —                  | always; refuses with "not configured" when no registry is wired |
| `generate_document`    | `format`, `content`, `spec_version?`, `locale?`, `currency?`, `meta?` | `document_generated` | only if `MINIO_ENDPOINT` |

Plus the tenant's own MCP tools, namespaced per registered server and discovered
at runtime (`T-M1`→`T-M4`) — those are per-tenant and so not in a static table.

**`create_visualization` is gone** (`T-D11`, 2026-08-17), and with it the
four-calls-per-chart round trip. `create_dashboard` no longer takes `cards[]`;
it carries every panel inline and returns `dashboard_id`, `url`, `row_count` and
per-panel warnings.

**Two metering names now lie, and the lie is deliberate.** A native dashboard is
recorded as `metabase_dashboard` (`internal/domain/usage.go:15`, via
`RecordMetabaseDashboard`) because renaming the event kind would split every
historical rollup at an arbitrary date for a cosmetic gain. `metabase_card` is
now a kind **nothing writes** — it stays defined so old rows still decode.
Rename both when `T-D16` drops the Metabase columns, not before.

`generate_document`'s parameters grew in `T-R2`. The contract is additive:
`spec_version: 2` opts a PDF into the branded layout, and `locale` / `currency`
/ `meta` are optional overrides of the company defaults. A call written against
the old shape renders exactly as it did before — the v1 and v2 JSON shapes
unmarshal into the same Go types. See
[`report-rendering.md`](report-rendering.md).

## Documentation status

| Surface                        | Documented in                                       | Current? |
| ------------------------------ | --------------------------------------------------- | -------- |
| Core API                       | `apps/backend/docs/api.md` (202 lines)                   | Verify against route list above |
| Usage endpoints                | `apps/backend/docs/usage/api.md` (290 lines)             | ✅ recent |
| Scheduled tasks                | `apps/backend/docs/scheduled-tasks/api.md` (191 lines)   | ✅        |
| Discord + Lark                 | `apps/backend/docs/lark-discord-integrations/api.md` (289) | ✅     |
| Regenerate description         | `apps/backend/docs/db-regenerate-description/api.md` (112) | ✅     |
| Postman collection             | `apps/backend/docs/postman/`                             | Verify   |
| WebSocket / SSE event schema   | [`api-chat.md`](api-chat.md) §2 (`T-A3`)             | ✅ 2026-07-28 |
| Agent tool contracts           | — **undocumented** (only the system prompt describes them) | ❌  |

## Observations for the plan

1. **Nine mutating endpoints handle credentials or tenant configuration and none
   are admin-gated** (marked ⚠️ above). Fixing this is a one-line-per-route change
   once `AdminOnly()` is applied — see ticket `T-04`.
2. **`/metrics` is public.** It exposes aggregate token counts and cost. Either
   authenticate it or move it to an internal-only listener — ticket `T-05`.
3. ~~**No machine authentication exists.**~~ **Closed 2026-07-28 by `T-13`.**
   Every route above still requires a human-session JWT, and now refuses an API
   key outright. Machine callers use `/v1`, a sibling namespace authenticated by
   a scoped key (`Authorization: Bearer arg_…`), which refuses a dashboard JWT
   just as flatly. `GET /v1/me` was the only route on it until `T-A2`, which
   added seven more — both report doors, the report poll and its SSE stream, and
   the three document routes — each gated by `write:reports` or
   `read:documents`, and `T-A3` six more: `POST /v1/chat` plus the thread,
   transcript, event-stream and delete routes, gated by `write:chat` or
   `read:threads` ([`api-keys.md`](api-keys.md),
   [`api-reports.md`](api-reports.md), [`api-chat.md`](api-chat.md)).
4. ~~**The WebSocket event schema is the dashboard's most important contract and
   it is undocumented.**~~ **Closed 2026-07-28 by `T-A3`.** The same seven
   events — `started`, `delta`, `thinking`, `tool_call`, `tool_result`, `error`,
   `final` — are now a published contract on `/v1`, with their payloads, which
   frames carry a resumable `id:`, and why `iteration` is not among them:
   [`api-chat.md`](api-chat.md) §2. One worker publishes both surfaces, so
   documenting the HTTP one documents the WebSocket one; what is still not
   written down is the *dashboard's* consumption of it, which is a frontend
   concern rather than a contract.
