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
| POST   | `/api/chat`                      | JWT  | Enqueue a turn (async)                  |
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

## Dashboards and scheduled tasks

| Method | Path                                          | Auth | Notes                     |
| ------ | --------------------------------------------- | ---- | ------------------------- |
| GET    | `/api/dashboards`                             | JWT  | Saved dashboards           |
| DELETE | `/api/dashboards/:id`                         | JWT  | Delete                     |
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
| GET    | `/api/usage/credits`                  | JWT  | Soft balance (not enforced)    |
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

Not HTTP-reachable. Registered in `cmd/worker/main.go:156`.

| Tool                   | Params                                            | Metered as         | Conditional              |
| ---------------------- | ------------------------------------------------- | ------------------ | ------------------------ |
| `list_sources`         | —                                                 | —                  | always                    |
| `get_schema`           | `source_id?`, `tables?`                           | —                  | always                    |
| `run_sql`              | `sql`, `source_id?`                               | `sql_query`        | always                    |
| `create_visualization` | SQL + chart spec + `source_id?`                   | `metabase_card`    | always                    |
| `create_dashboard`     | `cards[]` or `card_ids[]`                         | `metabase_dashboard` | always                  |
| `generate_document`    | `format`, `content`, `spec_version?`, `locale?`, `currency?`, `meta?` | `document_generated` | only if `MINIO_ENDPOINT` |
| `schedule_task`        | `name`, `prompt`, `cron_expression`, `timezone`   | —                  | always                    |

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
| WebSocket event schema         | — **undocumented**                                   | ❌        |
| Agent tool contracts           | — **undocumented** (only the system prompt describes them) | ❌  |

## Observations for the plan

1. **Nine mutating endpoints handle credentials or tenant configuration and none
   are admin-gated** (marked ⚠️ above). Fixing this is a one-line-per-route change
   once `AdminOnly()` is applied — see ticket `T-04`.
2. **`/metrics` is public.** It exposes aggregate token counts and cost. Either
   authenticate it or move it to an internal-only listener — ticket `T-05`.
3. **No machine authentication exists.** Every route above requires a
   human-session JWT. For Argentum to be callable by other agents, a scoped API
   key is the prerequisite — ticket `T-13`.
4. **The WebSocket event schema is the dashboard's most important contract and it
   is undocumented.** Event types in use: `started`, `delta`, `thinking`,
   `tool_call`, `tool_result`, `error`, `final`.
