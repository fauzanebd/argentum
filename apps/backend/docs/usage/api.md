# Usage Audit API

Inspect token usage and cost for your account: across the whole company, per
chat thread, per individual message, per channel, or per end-user.

All endpoints are scoped to the calling company. You can never see another
tenant's data, even if you guess a thread id.

## Basics

- **Base URL:** depends on deployment (`http://localhost:8080` for local).
- **Auth:** send your access token as `Authorization: Bearer <token>`. The
  company is derived from the token — you do not pass a company id.
- **Format:** all responses are JSON.
- **Money:** costs are reported in USD as decimal numbers (e.g. `0.012345`).
  The underlying ledger stores micro-USD (1 USD = 1,000,000 micro-USD); only
  the raw event endpoint exposes that field.
- **Time:** all timestamps are ISO-8601 / RFC3339 UTC (`2026-05-17T10:00:00Z`).

## Time window

Most endpoints accept two optional query parameters:

| Param | Format | Default |
|-------|--------|---------|
| `from` | RFC3339 timestamp | 30 days ago |
| `to`   | RFC3339 timestamp | now |

Example: `?from=2026-04-01T00:00:00Z&to=2026-05-01T00:00:00Z`.

If you omit both, you get the last 30 days.

## Pagination

List endpoints accept:

| Param | Default | Max |
|-------|---------|-----|
| `limit`  | 50 (events: 100) | 200 (events: 500) |
| `offset` | 0 | — |

---

## Endpoints

### 1. Company summary (already existed)

`GET /usage/summary`

Whole-company totals for the **current calendar month**. Does not accept
`from`/`to`. Use this as the reconciliation reference for the per-thread and
per-channel views over the same period.

**Response**
```json
{
  "from": "2026-05-01T00:00:00Z",
  "to":   "2026-06-01T00:00:00Z",
  "total_cost_usd": 12.345678,
  "total_tokens_in": 1500000,
  "total_tokens_out": 320000,
  "event_counts": {
    "llm_call": 842,
    "sql_query": 311,
    "metabase_card": 14
  },
  "cost_by_event_type_usd": {
    "llm_call": 11.20,
    "sql_query": 1.10,
    "metabase_card": 0.045678
  },
  "cost_by_model_usd": {
    "claude-sonnet-4-6": 9.80,
    "deepseek-v3.2": 1.40
  },
  "tokens_in_by_model":  { "claude-sonnet-4-6": 1100000, "deepseek-v3.2": 400000 },
  "tokens_out_by_model": { "claude-sonnet-4-6": 220000,  "deepseek-v3.2": 100000 }
}
```

---

### 2. Credit balance

`GET /usage/credits`

Current soft credit balance for the company.

**Response**
```json
{
  "company_id": "…",
  "balance_micro_usd": 87654321,
  "monthly_grant_micro_usd": 100000000,
  "updated_at": "2026-05-17T08:00:00Z"
}
```

Divide `balance_micro_usd` by 1,000,000 to get USD.

---

### 3. Per-thread listing

`GET /usage/threads?from=…&to=…&limit=…&offset=…`

One row per chat thread that produced any usage in the window, ordered by
total cost descending. Use this to answer **"which conversations are
expensive?"**

**Response**
```json
{
  "threads": [
    {
      "thread_id": "f8c1…",
      "channel": "whatsapp",
      "title": "Weekly revenue review",
      "last_message_at": "2026-05-16T14:21:00Z",
      "event_count": 42,
      "tokens_in": 78000,
      "tokens_out": 9400,
      "cache_create_tokens_in": 12000,
      "cache_read_tokens_in": 65000,
      "cost_usd": 0.842110
    }
  ]
}
```

Empty array if the company had no activity in the window.

---

### 4. Per-thread summary

`GET /usage/threads/{thread_id}?from=…&to=…`

Same shape as the company summary, but scoped to one thread. Use this to
break a single conversation down by event type and by model.

If the thread belongs to another company (or does not exist), you get an
empty summary — not a 404 — so probing IDs leaks nothing.

**Response**: same JSON shape as `GET /usage/summary`.

---

### 5. Per-message audit trail

`GET /usage/threads/{thread_id}/events?limit=…&offset=…`

Every raw `usage_event` for the thread, newest first. One row per LLM call,
SQL query, Metabase card, or document generation.

**Response**
```json
{
  "events": [
    {
      "id": "9b…",
      "company_id": "…",
      "thread_id": "f8c1…",
      "message_id": "a4…",
      "event_type": "llm_call",
      "model": "claude-sonnet-4-6",
      "tokens_in": 3120,
      "tokens_out": 410,
      "cache_create_tokens_in": 0,
      "cache_read_tokens_in": 2800,
      "cost_micro_usd": 18540,
      "metadata": null,
      "created_at": "2026-05-16T14:21:00Z"
    },
    {
      "event_type": "sql_query",
      "cost_micro_usd": 500,
      "created_at": "2026-05-16T14:20:58Z"
    }
  ]
}
```

`event_type` values:

| Value | Meaning |
|-------|---------|
| `llm_call`           | One model call (input + output + cache tokens). |
| `sql_query`          | One SQL query against your warehouse. |
| `metabase_card`      | One Metabase chart created. |
| `metabase_dashboard` | One Metabase dashboard created. |
| `topic_classify`     | One internal topic classification. |
| `document_generated` | One generated document (export, report). |

Non-LLM events carry zero in the token columns; the cost is a flat per-event
charge from the pricing table.

---

### 6. Per-channel rollup

`GET /usage/by-channel?from=…&to=…`

Cost grouped by the channel the conversation entered through. Useful for
answering **"how much does WhatsApp cost us versus the dashboard?"**

**Response**
```json
{
  "channels": [
    { "channel": "whatsapp",  "thread_count": 124, "event_count": 5230, "tokens_in": 2400000, "tokens_out": 410000, "cost_usd": 18.42 },
    { "channel": "dashboard", "thread_count":  31, "event_count":  920, "tokens_in":  380000, "tokens_out":  78000, "cost_usd":  2.91 },
    { "channel": "discord",   "thread_count":  12, "event_count":  180, "tokens_in":   90000, "tokens_out":  21000, "cost_usd":  0.78 },
    { "channel": "lark",      "thread_count":   4, "event_count":   28, "tokens_in":   12000, "tokens_out":   2900, "cost_usd":  0.11 }
  ]
}
```

Possible `channel` values: `whatsapp`, `dashboard`, `discord`, `lark`.

---

### 7. Per-user rollup

`GET /usage/by-user?from=…&to=…`

Cost grouped by end-user identity. The identity column varies by channel
(dashboards have user accounts; WhatsApp has phone numbers; Discord and Lark
have provider-specific IDs), so each row tells you both **who** and **what
kind of id** that is.

**Response**
```json
{
  "users": [
    { "channel": "dashboard", "user_key": "5d2c…",          "user_key_kind": "user_id",         "thread_count": 8,  "event_count": 410, "tokens_in": 220000, "tokens_out": 47000, "cost_usd": 2.10 },
    { "channel": "whatsapp",  "user_key": "+628123456789",  "user_key_kind": "phone",           "thread_count": 12, "event_count": 622, "tokens_in": 310000, "tokens_out": 58000, "cost_usd": 2.85 },
    { "channel": "discord",   "user_key": "182739182739",   "user_key_kind": "discord_user_id", "thread_count": 3,  "event_count":  72, "tokens_in":  35000, "tokens_out":  8000, "cost_usd": 0.31 },
    { "channel": "lark",      "user_key": "ou_abc123",      "user_key_kind": "lark_open_id",    "thread_count": 2,  "event_count":  18, "tokens_in":  10000, "tokens_out":  2200, "cost_usd": 0.09 }
  ]
}
```

`user_key_kind` is one of `user_id`, `phone`, `discord_user_id`,
`lark_open_id`, or `unknown` (for legacy threads with no attributed user).

---

## Errors

All errors use the same envelope:

```json
{ "error": "<message>" }
```

| Code | When |
|------|------|
| 400 | Bad `from` / `to` format (must be RFC3339) or other invalid query. |
| 401 | Missing or invalid bearer token. |
| 500 | Server-side failure. |

Cross-tenant access does not error — it returns an empty result.

---

## Recipes

**"What did this WhatsApp conversation cost me?"**
1. Find the thread id from your chat listing UI or `GET /threads`.
2. `GET /usage/threads/{id}` for the high-level breakdown.
3. `GET /usage/threads/{id}/events` for the line-by-line audit.

**"Reconcile a monthly invoice"**
1. `GET /usage/summary` → total for the current month.
2. `GET /usage/by-channel?from=<month-start>&to=<month-end>` → split by
   channel. The sum of `cost_usd` should match the total from step 1.

**"Find heavy users"**
1. `GET /usage/by-user?from=<7d-ago>` → sorted by cost descending.
2. For any heavy user, take their `user_key` and cross-reference your thread
   listing to find which conversations they own; drill into each with
   `GET /usage/threads/{id}`.

**"Spot expensive caching patterns"**

`cache_create_tokens_in` is billed at 1.25× the normal input rate;
`cache_read_tokens_in` at 0.10×. A thread with a high
`cache_create_tokens_in` and low `cache_read_tokens_in` is paying the cache
write premium without amortising it — worth investigating.
