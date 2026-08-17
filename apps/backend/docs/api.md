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

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/saved-dashboards` | **Deprecated.** Metabase-backed dashboards (moved off `/api/dashboards` in T-D10, removed in T-D15). |
| DELETE | `/api/saved-dashboards/:id` | **Deprecated.** Deletes the Metabase dashboard and the local row. |
| ANY | `/metabase/*` | **Deprecated.** Transparent reverse proxy to the embedded analytics dashboard. Forward the request as-is. |

---

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
