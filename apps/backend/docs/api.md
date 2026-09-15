# Argentum API

> **Stale — do not build against the `/v1` section of this file (noted 2026-07-28, `T-A1`).**
> This document predates the `bigref` refactor and describes a single-tenant
> service with no authentication and a `POST /v1/query` endpoint. Neither
> exists. `/v1` is now the multi-tenant public API: API-key authentication, a
> typed error envelope, idempotency and per-key rate limits, described in
> [`../../../docs/coverage/api-foundation.md`](../../../docs/coverage/api-foundation.md).
> **Superseded 2026-07-29 by `T-A4`.** The `/v1` contract is now
> [`apps/backend/openapi/v1.yaml`](../openapi/v1.yaml), served at
> `GET /v1/openapi.json` and checked against the gin route tree in both
> directions by CI. Start at
> [`docs/api/quickstart.md`](../../../docs/api/quickstart.md).
>
> What remains below describes the **dashboard's** `/api` routes, and even that
> predates the `bigref` refactor in places. It is kept as history rather than
> deleted because several sections are the only written description of routes
> `T-02b` will eventually generate types for.

HTTP reference for the Argentum analytics service. Send a natural-language question, get back a structured insight backed by SQL execution and an optional dashboard link.

## Overview

- **Base URL:** `http://localhost:8080` (default; configure per deployment)
- **Content type:** `application/json` for requests and responses, except where noted
- **Authentication:** none on the analytics endpoints. Webhook routes verify provider signatures (see WhatsApp Webhooks)
- **Tenancy:** each instance serves a single tenant, fixed at startup. There is no tenant header; the tenant is implicit in which instance you call

## Conventions

- All endpoints return JSON unless explicitly described as a stream or proxy
- Error responses use a single envelope: `{ "error": "<message>" }`
- Query endpoints time out after 60 seconds
- Optional fields are omitted when empty

### Status codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Invalid request body or missing required field |
| 403 | Webhook signature or verification token rejected |
| 404 | Resource not found (e.g. unknown job id) |
| 500 | Server-side failure |
| 503 | Service not ready (dependency unhealthy) |

---

## Endpoints

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check. Returns service identity. |
| GET | `/ready` | Readiness probe. 200 when all dependencies are reachable, 503 otherwise. |
| GET | `/metrics` | Snapshot of in-memory metrics. |

**`GET /health`** response:
```json
{
  "status": "healthy",
  "tenant": "acme",
  "timestamp": 1730000000
}
```

**`GET /ready`** response (200 or 503):
```json
{
  "ready": true,
  "source_db": true,
  "internal": true,
  "rabbitmq": true,
  "timestamp": 1730000000
}
```

---

### Query (v1)

#### `POST /v1/query`

Synchronous natural-language query. Returns the full agent answer once execution completes.

Request body:
```json
{
  "session_id": "demo",
  "query": "top 5 customers by revenue last month"
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `session_id` | string | no | Conversation key for follow-ups. Defaults to `anonymous`. |
| `query` | string | yes | Natural-language question. |

Response: an [`AgentResponse`](#agentresponse).

#### `POST /v1/query/stream`

Same input as `/v1/query`, but streams agent progress as **Server-Sent Events** (`Content-Type: text/event-stream`). Each event is a JSON `StreamEvent`. The terminal `done` event carries the final `AgentResponse`.

Example wire frame:
```
event: status
data: {"type":"status","data":{"phase":"planning"}}

event: tool_call
data: {"type":"tool_call","data":{"tool":"sql","parameters":{...}}}

event: done
data: {"type":"done","data":{ ...AgentResponse... }}
```

Event types: see [`StreamEvent`](#streamevent).

---

### Dashboards

Native dashboards (T-D10). A dashboard stores a question and a column mapping,
never values, so `/data` re-runs the panels' queries against the tenant's
warehouse on every open. Reads are open to members; deleting is admin-only.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/dashboards` | The company's dashboards, newest first. |
| GET | `/api/dashboards/:id` | One dashboard's stored definition, without running it. |
| GET | `/api/dashboards/:id/data` | Resolve it: bind the filters and run every panel. |
| DELETE | `/api/dashboards/:id` | Delete one. Admin only. |

Filter values go in the query string, by filter name. A `date_range` filter
named `period` accepts either `?period=last_30d` (a preset: `last_7d`,
`last_30d`, `mtd`, `qtd`, `ytd`, `last_month`) or an explicit
`?period_from=2024-01-01&period_to=2024-01-31`, where both bounds are inclusive
days. Anything the dashboard did not declare is ignored rather than merged.
`?refresh=1` is accepted and does nothing until the panel cache lands (T-D8).

Every panel answers for itself: one that fails carries its own `error` and the
response is still `200`, so a single timed-out query does not blank the others.
A `400` means the request's filters were wrong and names which one; the body
`{"error": "..."}` is safe to show.

There is no create or update route. A dashboard is authored by the agent through
`create_dashboard`, which is the one path its validation rules live on.

**Restricted dashboards (T-Z5).** An admin can restrict a dashboard with
`PUT /api/access/dashboard/:id/mode` and grant it per person; an admin is not
granted by rank. For a person without the grant, `GET /api/dashboards` omits it,
and `GET /api/dashboards/:id`, `/data` and `/shares` answer
`403 {"error": "...", "resource_kind": "dashboard"}` — an id that is not one of
this company's dashboards still gets that route's own `404`. A failed access check
is a `503`, and the list is never served unfiltered.

A restricted dashboard cannot be shared: `POST /api/dashboards/:id/shares` answers
`409` with the reason, whoever asks. Restricting one revokes its live share links
in the same transaction, and the mode route answers
`200 {"access_mode": "restricted", "revoked_shares": 2}`. A link that somehow
survives does not open a restricted dashboard; it is answered as a revoked one.
Revoking a link and deleting the dashboard never ask for a grant.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/saved-dashboards` | **Deprecated.** Metabase-backed dashboards (moved off `/api/dashboards` in T-D10, removed in T-D15). |
| DELETE | `/api/saved-dashboards/:id` | **Deprecated.** Deletes the Metabase dashboard and the local row. |
| ANY | `/metabase/*` | **Deprecated.** Transparent reverse proxy to the embedded analytics dashboard. Forward the request as-is. |

---

### Sources and uploaded documents, restricted (T-Z6)

An admin restricts a data source with `PUT /api/access/connection/:id/mode`, and an
uploaded document with `PUT /api/access/document/:id/mode`, and grants either per
person. For a person without the grant — an admin not granted it included:

| Route | Answer |
|-------|--------|
| `GET /api/connections` | A **member**'s list omits the source. An **admin**'s list is whole: it is where sources are configured, and the agent form saves every source ticked there. |
| `POST /api/connections/:id/freshness/test`, `/regenerate-description`, `/test-rag` | `403 {"error": "...", "resource_kind": "connection"}` — the three routes that return what is in a source. The other connection routes configure and are not asked. |
| `GET /api/knowledge/documents` | Omits the document. A page can come back shorter than its `limit`. |
| `GET /api/knowledge/documents/:id`, `/tables`, `/pages/:page` | `403 {"error": "...", "resource_kind": "document"}` |
| `GET` and `PATCH /api/knowledge/tables/:tableId`, `POST /api/knowledge/tables/:tableId/apply` | The same `403`, asked of the document the table came from, before a body is read or anything written. `POST …/unpublish` is not asked. |

A failed access check is `503 {"error": "could not check access; try again"}`, and
no list is ever served unfiltered. **A grant on a source never changes what an agent
may query** — that is the agent's own source list.

### Agents on the doors with no person (T-Z8)

A grant is a person's, and only the dashboard carries one. Every other way into a
restricted agent follows its own rule:

| Door | Rule |
|------|------|
| API keys | `POST /api/api-keys` takes `agent_ids` (optional, fixed at creation). Empty is every agent, restricted ones included. A listed key reaches only those agents; see `openapi/v1.yaml` for `agent_not_allowed`. The key's record carries `agent_ids`. |
| Channel bindings | `POST /api/agent-bindings` takes `acknowledge_restricted`. Binding a restricted agent without it is a `400` naming the acknowledgement; with it, the binding stores `restricted_acknowledged_at` / `_by` and an audit row `agent_binding.acknowledge_restricted` names the admin. Reads carry `agent_access_mode`. A binding to a restricted agent that nobody acknowledged answers nothing, and says so to the person who wrote in. |
| Website widget | Never reaches a restricted agent. `GET /api/embed/config` omits them; a pick of one is a `404`; a conversation running as one is a `403`. |
| Watchers, scheduled tasks | Re-checked at every fire against their creator. A refused one is switched off with `disabled_reason` (`creator_not_granted`, `creator_removed`) on its record; switching it back on clears it. |

| Method | Path | Description |
|--------|------|-------------|
| PUT | `/api/agent-bindings/:id/acknowledgement` | Acknowledge, for an existing binding, that anyone who can post on its address can use its restricted agent. Admin only. `200 {"binding": …}`; `409` when the agent is open; `404` for another company's binding; idempotent, keeping the first admin. |

### What access leaves on the audit log (T-Z9)

`GET /api/audit` returns these rows beside the tool calls. Each is a pseudo tool:
nothing ran.

| `tool_name` | `result_status` | Written when | Actor, and arguments |
|------|------|------|------|
| `access.refused` | `blocked` | A request for one agent, dashboard, source, document or conversation is refused because of who asked — by a route, a pick, a turn, a room add, a tool, or a job firing. Not when a list leaves something out, an id does not exist, or a check could not be made. | The person refused; on the widget, a key or a channel, the visitor, key or platform identity. `resource_kind`, `resource_id`, `reason` (`not_granted`, `not_cleared`, `not_on_key`, `creator_removed`), `door`. `agent_id` or `thread_id` is set too. |
| `access.grant`, `access.revoke` | `ok` | `PUT`/`DELETE /api/access/:kind/:id/grants/:userID` changed something | The admin. `user_id` (the person), `resource_kind`, `resource_id` |
| `access.mode` | `ok` | `PUT /api/access/:kind/:id/mode` changed the mode, or revoked a link | The admin. `access_mode`, `previous_access_mode`, `revoked_shares`, `resource_kind`, `resource_id` |
| `capability.grant`, `capability.revoke` | `ok` | `PUT`/`DELETE /api/users/:id/capabilities/:capability` changed something | The admin. `user_id`, `capability` |

A repeated grant, or a revoke of what is not held, writes no row and still answers
`204`. When the row cannot be written, a grant or a re-open is undone and answers an
error, while a revoke or a restriction stands.

Every such refusal also counts once on `/metrics` as
`argentum_access_refusals_total{kind, reason}` — but only in the process that
refused it. The API serves `/metrics`, so refusals made by the worker's tools and
jobs, or by the Discord bot, are not in that series.

### Links and proposals from a restricted agent's conversation (T-Z13, T-Z14)

| Route | While an agent that is or was in the document's or proposal's conversation is restricted |
|------|------|
| `POST /api/documents/:id/shares` | `409 {"error": "this document was made in a conversation with a restricted agent, …"}`, whoever asks, a person granted the agent included. `503` when that cannot be checked. |
| `GET /api/documents/:id/shares` | Each live link carries `"paused": true`: it will not open until every agent in the conversation is open again. Nothing is revoked. |
| `GET /share/:token` | Answered exactly as a revoked link; the view is not counted or audited. |
| `GET /api/actions/pending` | Omits every proposal raised in a conversation the caller may not read. One with no conversation is listed as before. |
| `GET /api/actions/:id`, `POST …/approve`, `POST …/reject` | `404 {"error": "no such action proposal"}` for a proposal the caller may not read, before the role check, admins included. `503` when that cannot be checked. |

### Voice (T-W7)

`POST /api/threads/:id/voice` — member, **and** the `voice` capability, granted per
person (`PUT /api/users/:id/capabilities/voice`). An admin without the grant is
refused like anybody else. The route exists only on a deployment with
`SPEECH_ENABLED=true` and a usable provider; everywhere else it is the router's
`404`.

A recording in, a transcript out. **No message is written and no turn is started**:
the transcript is sent with `POST /api/chat`, edited or not, like anything typed.

`multipart/form-data`:

| Field | |
|------|------|
| `audio` | The recording: `audio/webm`, `audio/ogg`, `audio/mp4`, `audio/x-m4a`, `audio/mpeg`, `audio/wav`, `audio/x-wav` or `audio/flac`, parameters such as `;codecs=opus` allowed. The bytes must be that format. |
| `duration_ms` | Required. The length as the client measured it. |
| `language` | Optional ISO-639-1 code (`id`, `en`; `id-ID` is read as `id`). Default: the tenant's document locale, else `id` for a rupiah tenant, else none — the provider detects it. Never English by default. |

`200 {"transcript": "…", "language": "id", "seconds": 3.4, "clip_id": "…", "expires_at": "…"}`.
`seconds` is what was billed: the provider's measurement where it reports one, else
`duration_ms`. `clip_id` and `expires_at` are absent when the clip could not be
recorded; the transcript is good either way.

| Status | When |
|------|------|
| `400` | No `audio`, a missing or non-positive `duration_ms`, a `language` that is not a two-letter code |
| `402` | Out of credits — checked before the provider is called |
| `403` | `{"error": "an admin has not granted you this", "capability": "voice"}`, before the recording is read |
| `404` | Another company's conversation, or one hidden from the caller (T-Z10), before the recording is read |
| `413` | `{"error": "a recording must be 60 seconds or shorter"}` — longer than `SPEECH_MAX_CLIP_SECONDS` by `duration_ms`, or larger than that length at 384 kbit/s. The byte cap is what bounds a bill; the declared length is a client's claim |
| `415` | Not an accepted format, or bytes that are not the format declared |
| `502` | `{"error": "the speech service could not transcribe that recording; try again, or type your question"}`. Nothing kept, nothing billed |

The audio is kept under `voice/<company_id>/` for `SPEECH_RETENTION_DAYS` (default 7,
at most 90). The worker's sweep (`SPEECH_SWEEP_CRON`, hourly) deletes a clip when it
expires or when its conversation is deleted, and a company's data erasure deletes
every clip and the whole prefix. Each transcription is one `speech_transcription`
usage event, priced per second of audio for the model that transcribed it.

#### An answer read aloud (T-W8)

`GET /api/messages/:id/audio` — member, **and** the `voice` capability. The route exists
only where `SPEECH_ENABLED=true`, a synthesiser is usable (`SPEECH_TTS_*`) and object
storage is configured; everywhere else it is the router's `404`.

`200`, `audio/mpeg`, `Cache-Control: private, max-age=3600`. Nothing is synthesised until
the first request for a message. That request has the light model reduce the answer to a
few spoken sentences (no table, no markdown, figures rounded), checks the reduction,
synthesises it and keeps it. Every later request for the message is served from what was
kept, and billed nothing.

The check is deterministic. Every figure spoken must be one the written answer states,
rounded no further than the precision it is spoken at: "about 1.2 million" for 1,234,567
passes, "2 million" does not, and neither does a digit the written answer does not have.
A figure spelled in words is refused, because it cannot be read. So is a table row, code,
SQL or a link.

| Status | When |
|------|------|
| `402` | Out of credits, before either model is called |
| `403` | `{"error": "an admin has not granted you this", "capability": "voice"}` |
| `404` | Another company's message, one in a conversation hidden from the caller (T-Z10), or a message that is not an agent's answer |
| `422` | `{"error": "this answer cannot be read aloud; read it instead", "reason": "spoken 2 million, nearest written 1,234,567: …"}`. Remembered: the next request answers the same without asking a model again |
| `502` | `{"error": "the speech service could not read that answer aloud; read it instead, or try again"}`. Nothing kept and no synthesis billed; the next request tries again |

The audio is kept under `voice/<company_id>/answers/` for `SPEECH_RETENTION_DAYS`, deleted
by the same worker sweep as recordings (and when its message is deleted), and removed by a
company's erasure. Each synthesis is one `speech_synthesis` usage event, priced per
character for the model that read it; the reduction is its own `llm_call` on the light
model.

---

### Documents

Generated documents, for the dashboard (the integrator surface is `/v1/documents`).

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| `GET` | `/api/documents` | member | The newest 50 for the company, each with a presigned `download_url` minted per read; `page_count` on a `carousel` row |
| `GET` | `/api/documents/:id/pages/:page` | member | One slide of a carousel as `image/jpeg`, `Cache-Control: private, max-age=3600`. `404` for another company's id, a page under 1 or over `page_count`, or a document with no pages. The dashboard fetches these through its API client rather than an `<img src>`, because a persisted message never carries a presigned image URL (T-G6) |
| `GET` | `/api/documents/:id/carousel` | member | The manifest beside the slides (T-G7): `caption` assembled as it would be pasted — text, blank line, hashtags — plus `text`, `hashtags`, one `alts` entry per page, and `pages`. Same `Cache-Control` and same tenant boundary as the page route. `404` for another company's id and for any format that is not `carousel`, which is how the approval card decides a proposal is about a post in one request rather than two |

### Jobs *(only when WhatsApp integration enabled)*

| Method | Path | Description |
|--------|------|-------------|
| GET | `/jobs/:id` | Fetch a background job by id. 404 if not found. |
| GET | `/jobs/stats` | Aggregate job counters (queued, running, succeeded, failed). |

---

### WhatsApp Webhooks *(only when WhatsApp integration enabled)*

#### `GET /webhook/whatsapp`

Handshake used by the messaging provider to verify the endpoint.

Query params: `hub.mode`, `hub.verify_token`, `hub.challenge`.
Returns the `hub.challenge` value as plain text on success, `403` otherwise.

#### `POST /webhook/whatsapp`

Inbound message. Two payload shapes are accepted:

- **Meta WhatsApp Business** — JSON body, signed via `X-Hub-Signature-256` (HMAC-SHA256 of the raw body)
- **Twilio** — `application/x-www-form-urlencoded` body, signed via `X-Twilio-Signature`

Response:
```json
{
  "status": "queued",
  "job_id": "…",
  "timestamp": 1730000000
}
```

Invalid signatures are rejected with `403`.

---

## Schemas

### `AgentResponse`

```json
{
  "message_id": "string",
  "query": "string",
  "insight": "string",
  "query_result": { "...": "optional structured result" },
  "dashboard_url": "string (optional)",
  "follow_up_questions": ["string", "..."],
  "error": "string (only on partial failure)"
}
```

| Field | Type | Always present | Notes |
|-------|------|---------------|-------|
| `message_id` | string | yes | Echoes the session id of this turn. |
| `query` | string | yes | The original input. |
| `insight` | string | yes | Human-readable answer. |
| `query_result` | object | no | Structured rows when the agent ran SQL. Includes `columns`, `rows`, `row_count`, timing. |
| `dashboard_url` | string | no | Link to a dashboard view backing the answer. |
| `follow_up_questions` | string[] | no | Suggested next prompts. |
| `error` | string | no | Set when the agent partially failed but still returned a usable response. |

### `StreamEvent`

```json
{ "type": "status", "data": { "...": "varies by type" } }
```

| `type` | Carried in `data` |
|--------|------------------|
| `status` | `{ "phase": "loading_context" \| "planning" \| ... }` |
| `tool_call` | `{ "tool": "sql" \| "respond" \| ..., "parameters": { ... } }` |
| `tool_result` | Tool output (shape depends on tool) |
| `insight` | Final natural-language answer (string) |
| `done` | A complete `AgentResponse` |
| `error` | `{ "message": "string" }` |

### `Error`

```json
{ "error": "invalid body" }
```

---

## Examples

A complete, ready-to-import Postman collection lives at [`docs/postman/argentum.postman_collection.json`](./postman/argentum.postman_collection.json) with environment variables in [`docs/postman/argentum.postman_environment.json`](./postman/argentum.postman_environment.json).
