# The `/v1` contract — T-A1 record

Ticket: [`../plan/01-tickets.md`](../plan/01-tickets.md) `T-A1`.

Landed 2026-07-28. Depends on `T-13` (keys), `T-05` (audit log), `T-03`
(credits). Unblocks `T-A2` (reports over HTTP) and `T-A3` (chat over HTTP).

This is the shape every `/v1` route inherits. Nothing here is a feature a
customer asks for; all of it is the part that cannot be retrofitted, because
an error format, an idempotency contract and a pagination style become
permanent the first time somebody writes code against them.

---

## 1. What ships

| Piece | Where | Note |
| ----- | ----- | ---- |
| Request ids | `internal/transport/http/middleware/requestid.go` | `X-Request-Id` on every response, in the log fields, in the request context, in the queue payload, in the audit row |
| Error envelope | `internal/transport/http/apierr` | `T-13` shipped the type; this adds `param`, an explicit-status form, and `NewDetail` |
| Kill switch | `middleware.Enabled` | `API_V1_ENABLED=false` → 503 on every `/v1` route, `/api` untouched |
| Body cap | `middleware.MaxBodyBytes` | `API_V1_MAX_BODY_BYTES`, default 1 MiB, checked twice |
| Idempotency | `internal/idempotency` + `middleware.Idempotency` | `idem:{company}:{key}`, 24h, records ids and never payloads |
| Rate-limit headers | `middleware/ratelimit.go` | `RateLimit-Limit/Remaining/Reset` on every response, `Retry-After` from the bucket |
| Cursor pagination | `internal/transport/http/apiv1` | `{data, has_more, next_cursor}`, opaque base64 of `(created_at, id)` |
| `api` channel | migration `025`, domain, thread repo/service, enqueuer, runner, usage SQL, dashboard labels | The channel both `T-A2` and `T-A3` need |
| `request_id` on audit rows | migration `026`, `tools/audit.go`, `queue.ChatRunPayload` | A support request id resolves to the rows it produced |
| Two scopes | `domain.AllScopes` | `write:reports`, `read:documents` — routes land in `T-A2` |
| `GET /v1/me` extended | `handlers/v1_me.go` | Rate limit, credit position, API version |

Config: `API_V1_ENABLED`, `API_V1_RATE_PER_MIN` (from `T-13`),
`API_V1_SYNC_TIMEOUT_SECONDS`, `API_V1_MAX_BODY_BYTES`. All in
`.env.example` with their reasoning.

## 2. Decisions worth carrying forward

### The middleware order is the contract, and the live gate reordered it

```
RequestID → Enabled → MaxBodyBytes → APIKeyAuth → rate limit → [per route] Idempotency
```

The first draft put the kill switch first, on the argument that a switched-off
API should answer before it reads a credential. That argument holds for
everything below it and not for `RequestID`, which reads nothing and touches no
I/O — and with the switch above it, **the 503 went out with no request id in
it**. The two responses most likely to start a support conversation are the
503 from a disabled API and the 401 from a bad key; both were shipping
without the one string that makes them traceable. Found by curling a disabled
API, not by any test. The tests now assert an id on both.

### An idempotency record holds ids, never payloads

The obvious implementation caches the bytes the handler wrote and replays
them. It is wrong here twice over. `POST /v1/reports/render` with
`Accept: application/pdf` answers with megabytes, and keeping that per key for
24 hours makes Redis a document store nobody sized. And a streamed answer has
no bytes to keep — replaying an SSE chat means re-attaching to the turn.

So a record holds `{"report_id":"…","status":"completed"}` and a replay
re-derives the response from it: re-reading object storage and re-presigning,
or re-attaching to a thread. That is also the only way a replayed download
link is still valid an hour later. A 10 MB render leaves a 160-byte record.

Routes that need more than an echo install a `Replayer`; the default writes
the stored result verbatim so a route that installs nothing can never
re-execute.

### Three sub-cases the naive design gets wrong, all of which occur here

- **Same key, different body → 409, not a replay.** A client reusing one key
  across genuinely different requests would otherwise receive the first
  answer forever. 409 rather than 400 because the request is not malformed,
  it conflicts — which tells the caller the fix is a new key.
- **A retry arriving mid-flight → `409 request_in_flight`,** carrying the
  `thread_id`/`report_id` the caller is already waiting on. This is the
  *common* case: it is what a client timeout plus a retry looks like. Without
  the id there is nothing to poll and no reason not to retry again.
- **A failed request forgets its key.** The next thing a well-behaved client
  does with a 500 is retry it. A key that survived the failure would refuse
  that retry for 24 hours — worse than no idempotency at all.

### Redis down does not close the write surface

Both the limiter and the idempotency middleware fail open, matching the house
rule for optional subsystems (`credits.go`). The cost is real and is stated
rather than hidden: a retry during a Redis outage can duplicate. The
alternative is that a Redis hiccup refuses every write on the public API.

### `RateLimit-*` on success, not only on the 429

A client that only learns its budget from a refusal has already been refused
once. The token-bucket Lua now returns `(allowed, tokens, reset)` in one call
— computing the remaining count outside the script would mean a second read
of a bucket that has already moved. `Retry-After` is the bucket's own answer
rather than a flat `1`, because every refused client retrying after the same
second is a synchronised herd.

### The cursor is a pair, and it is opaque

`(created_at, id)`, because rows are ordered by time and two can share a
microsecond, so a keyset predicate needs both halves to be a total order.
Microseconds because that is Postgres's own `timestamptz` resolution — a
nanosecond cursor round-trips through Go and not through the database. Base64
because a cursor is a token to hand back, not a structure to construct: making
it opaque is what keeps it changeable.

Offsets are not offered at all. Rows arrive while a caller pages, and with
`?offset=100` an insert during the walk shows one row twice and hides another.

### The `api` channel has no outbound provider, deliberately

`ChatRunner.completeWith` gets an explicit empty case with a comment saying
why: delivery already happened, because the caller is holding the HTTP
response open. The playbook warns that a missing `switch` case is a silent
no-op — this is the inverse, a present case that must stay empty, and it says
so in the source so nobody "fixes" it later.

Threads are keyed by `(company_id, api_user_ref)` and fork on an idle gap plus
a topic shift, like WhatsApp and Discord. The playbook's rule — native threads
key on the platform's id and skip classification — cuts both ways here,
because the API has both shapes: a caller that tracks conversations passes an
explicit `thread_id` and never reaches the resolver, while a caller that
forwards "our user asked X" has drawn no boundary and gets the heuristic.

An explicit `thread_id` on the `api` channel is checked harder than the
dashboard's equivalent: the thread must be **on the `api` channel**, not
merely in the same company. A key holder passing a dashboard thread's id would
otherwise append a machine turn to a person's chat history and bill it under a
channel it did not arrive on.

### The migration index is not the one the ticket asked for

The ticket says *"unique index on `(company_id, api_user_ref, id)`"*. Including
the primary key makes uniqueness vacuous — every row is already unique by `id`
— so that index constrains nothing while reading as if it does. And a
genuinely unique `(company_id, api_user_ref)` would be wrong in the other
direction: it forbids the fork the resolver performs. It ships as a partial
lookup index on `(company_id, api_user_ref, last_message_at DESC)`, which is
the query that actually runs.

### Two scopes ship before their routes, reversing what `T-13` wrote

`T-13`'s comment said a scope with no route behind it is a checkbox that
promises something. That was right when no keys existed. It stops being right
once they do, because **scopes are fixed at creation and there is no
`Update`**: a scope that appears only when its route does forces every key
minted in the meantime to be re-issued, and it is the tenant who edits their
CI config, not us. Deny-by-default is untouched — a key holding
`write:reports` today reaches nothing, because nothing asks for it yet.

The `writes` flag the dashboard groups on now reads the scope's own prefix
instead of an enumerated pair. The list was two scopes long when that
enumeration was written and is four now; the next person to add one would have
filed a write under "reads".

### `/v1/me` says "not enforced" rather than "$0.00"

`BudgetState` grew an `Enforced` flag. Without it, a deployment with credit
enforcement switched off reports a zero balance to `GET /v1/me`, which reads
as *you are out of credit* — the opposite of the truth. It is false on the
fail-open paths too, which is accurate rather than convenient: nothing was
enforced for those calls either.

### A caller's own request id is accepted, after it is checked

An integrator correlating their logs with ours sends the id they already have,
and echoing it costs nothing. Echoing it *unchecked* puts caller-controlled
bytes into a log line and into `agent_actions.request_id` — a newline is a
forged second log event, a quote is a field boundary somebody's parser will
believe. Anything outside `[A-Za-z0-9_.:-]`, empty, or over 128 bytes is
replaced by one of ours rather than rejected: the caller gets a working
request and a usable id, and the id they sent is simply not the one that comes
back.

## 3. Gate

Backend suite, lint, and a live run against local Postgres and Redis. The API
was started with explicit local env (**never** by sourcing
`apps/backend/.env`, which points `DB_HOST` at a remote host — see
[`rbac.md`](rbac.md)).

### Migrations

```
$ docker exec argentum_postgres psql -U metabase -d argentum \
    -c "select version, dirty from schema_migrations;"
 version | dirty
---------+-------
      26 | f

 api_user_ref    | text
    "idx_threads_api_user" btree (company_id, api_user_ref, last_message_at DESC)
        WHERE api_user_ref IS NOT NULL
```

`025` and `026` applied on boot from a schema at `024`. Both round-trip:

```
$ migrate -path migrations/control -database "$DB" down 2
26/d agent_actions_request_id (22.481625ms)
25/d api_channel (30.53825ms)
    → version 24, api_user_ref and request_id columns gone

$ migrate -path migrations/control -database "$DB" up
25/u api_channel (12.649208ms)
26/u agent_actions_request_id (20.880375ms)
    → version 26, idx_threads_api_user and idx_agent_actions_request back
```

### `GET /v1/me` with a key

```
$ curl -sD- localhost:8080/v1/me -H "Authorization: Bearer arg_745d8ed58d_…"
HTTP/1.1 200 OK
Ratelimit-Limit: 120
Ratelimit-Remaining: 119
Ratelimit-Reset: 0
X-Request-Id: req_673dc136a529799907448baabe4586d2

{
  "api_version": "2026-07-28",
  "company": {"id": "4dc04924-…", "name": "T-A1 Gate Co"},
  "credits": {"enforced": true, "byo_llm": false, "status": "ok",
              "balance_usd": 25, "grant_usd": 25, "remaining_pct": 100},
  "key": {"id": "ca983af6-…", "name": "T-A1 gate key",
          "scopes": ["read:documents", "read:usage", "write:reports"]},
  "rate_limit": {"requests_per_minute": 120}
}
```

The key was minted through the dashboard route with the two new scopes, which
is also the check that they are in the vocabulary end to end.

### The two authorities do not cross

```
$ curl -sD- localhost:8080/v1/me -H "Authorization: Bearer <dashboard JWT>"
HTTP/1.1 401 Unauthorized
X-Request-Id: req_2ab851f9840085640021fccb4f6d29d6

{"error":{"type":"authentication","code":"invalid_api_key",
          "message":"That API key is not valid, or it has been revoked or expired.",
          "request_id":"req_2ab851f9840085640021fccb4f6d29d6"}}

$ curl -s -o /dev/null -w '%{http_code}' localhost:8080/api/api-keys \
    -H "Authorization: Bearer arg_745d8ed58d_…"
401
```

Both directions are also asserted against the real router for **every** route
in `cmd/api/v1_test.go` — 66 `/api` routes and every `/v1` route.

### Request ids

```
$ curl -sD- … -H 'X-Request-Id: caller-trace-42'      → X-Request-Id: caller-trace-42
$ curl -sD- … -H 'X-Request-Id: bad id with spaces'   → X-Request-Id: req_db4930e73e9a…
```

### Rate limit

```
$ seq 1 130 | xargs -P 8 -I{} curl -s -o /dev/null -w '%{http_code}\n' \
    localhost:8080/v1/me -H "Authorization: Bearer arg_…" | sort | uniq -c
 120 200
  10 429

HTTP/1.1 429 Too Many Requests
Ratelimit-Limit: 120
Ratelimit-Remaining: 0
Ratelimit-Reset: 1
Retry-After: 1

{"error":{"type":"rate_limit","code":"rate_limit_exceeded", …}}
```

Exactly the minute's budget, then refusals carrying all four headers.

### Kill switch

```
$ API_V1_ENABLED=false go run ./cmd/api
$ curl -sD- localhost:8080/v1/me -H "Authorization: Bearer arg_…"
HTTP/1.1 503 Service Unavailable
Retry-After: 30
X-Request-Id: req_12b80bb5d2e0beb442597f2d97eb4988

{"error":{"type":"server","code":"api_disabled",
          "message":"The Argentum public API is temporarily unavailable. Retry shortly.",
          "request_id":"req_12b80bb5d2e0beb442597f2d97eb4988"}}

no credential at all:                    status=503
GET /api/usage/by-channel (dashboard):   status=200
```

### The `api` channel in the rollups

An `api` thread, a usage event and an audit row were inserted directly,
because a real turn needs a worker and an LLM key (see §4). What that proves
is the half that only exists in SQL — the fifth `user_key_kind` arm and the
channel label:

```
GET /api/usage/by-channel
{"channels":[{"channel":"api","thread_count":1,"event_count":1,
              "tokens_in":1200,"tokens_out":340,"cost_usd":0.0045}]}

GET /api/usage/by-user
{"users":[{"channel":"api","user_key":"their-user-42",
           "user_key_kind":"api_user_ref", …}]}
```

### A request id resolves to a row

```
GET /api/audit/actions?request_id=req_673dc136a529799907448baabe4586d2
{"actions":[{"actor_kind":"api_key","actor_ref":"ca983af6-…","channel":"api",
             "tool_name":"run_sql","request_id":"req_673dc136a529799907448baabe4586d2", …}]}

GET /api/audit/actions?request_id=req_does_not_exist
{"actions":[]}
```

### Envelope discipline

`grep -rn 'gin.H{"error"'` over everything on the `/v1` path — the handler,
the middleware it runs behind, and `apierr` — returns nothing. The only hits
in the middleware package are `Auth` and `RequireRole`, which are `/api`-only
and never run on `/v1` (asserted by `TestV1RoutesAreNotRoleGated`). The
standing guard is stronger than the grep: `TestV1AlwaysAnswersWithARequestID`
parses **every** `/v1` response as the envelope and fails if one is not.

### Suite and lint

```
$ go test ./...        → 26 packages ok, 0 failures
$ make test            → go test -race ./... → 26 packages ok, 0 failures
$ make lint-go         → 0 issues
$ make lint-web        → 0 errors, 6 warnings (unchanged, all pre-existing)
```

Two packages have tests that did not before — `internal/idempotency` and
`internal/transport/http/apiv1`, both new here — taking the covered count from
24 to 26.

New test files: `middleware/requestid_test.go`, `middleware/v1guard_test.go`,
`middleware/idempotency_test.go`, `middleware/ratelimit_test.go`,
`idempotency/store_test.go`, `apiv1/apiv1_test.go`, `app/api_channel_test.go`,
plus cases in `cmd/api/v1_test.go` and `tools/audit_test.go`.

`miniredis` becomes a direct test dependency. The three behaviours the
idempotency store and the limiter depend on — `SET NX` losing a race,
`KeepTTL` not resetting the clock, and the Lua bucket's arithmetic — are
Redis's semantics, and a hand-written fake would only assert that the code
agrees with itself.

## 4. What is not met, and why

**Four acceptance items have no live transcript, because they have no route to
run against yet.** `T-A1` ships no `POST` route: `/v1/me` is the only `/v1`
route that exists, and it is a `GET`. The items are:

- a replayed `Idempotency-Key` returning the same logical response with
  `Idempotent-Replay: true`;
- a mid-flight replay returning `409 request_in_flight`;
- the same key with a changed body returning 409;
- no idempotency record over 4 KiB after a 10 MB render.

All four are covered by tests — the last one renders an actual 10 MB response
body through the middleware and measures the record — and all four get a curl
transcript in `T-A2`, whose `POST /v1/reports/render` is the first route to
carry the middleware. The body cap is in the same position: unit-tested in
both its forms, live at `T-A2`.

This is the same shape as `T-13`, which shipped `GET /v1/me` precisely because
a credential with nothing to authenticate against cannot be gate-tested. The
honest statement is that the mechanism is proven and the wiring of it to a
real route is not, because the route is the next ticket.

**A turn on the `api` channel has not run end to end.** The acceptance item
asks that a turn started through `/v1` shows `channel=api` in
`/api/usage/by-channel` and that `completeWith` attempts no outbound send.
Starting one needs `/v1/chat` (`T-A3`) and a live LLM key. What is proven
here: the SQL rollups (live, above), the resolver and validation (unit), and
the empty `completeWith` case (source, with the comment that keeps it empty).

**`request_id` reaching an audit row is proven in halves.** The decorator
writing it from the context, and the payload carrying it across the process
boundary, are unit-tested; the column, the filter and the tenant scoping are
proven live. The single end-to-end — an HTTP call whose id appears in a row a
worker wrote — needs the same `/v1/chat` route, and lands with `T-A3`.

## 5. Known limits

- **`GET` requests ignore `Idempotency-Key`.** The middleware is installed per
  route, and a read does not need a Redis key and a 24-hour TTL to be
  idempotent. "Accepted everywhere else" is satisfied by ignoring it rather
  than by recording it.
- **No `RateLimit-*` headers when Redis is absent.** The limiter is not
  installed at all in that configuration, so there is no budget to report.
- **The idempotency record is not transactional.** `update` reads, mutates and
  writes back; the only concurrent writer is a second request under the same
  key, and that one is refused with a 409 before it reaches the store. The
  lost-update race requires a client to defeat the check that exists to
  prevent it.
- **Body-hash equality is byte equality.** Two JSON documents differing only
  in key order are two different requests. Conservative in the safe
  direction: it can refuse a retry a smarter comparison would have replayed,
  and it cannot replay one that asked for something else.
- **`apps/backend/docs/api.md` is stale and now says so.** It describes a
  pre-refactor single-tenant service with a `POST /v1/query` that does not
  exist. **Superseded 2026-07-29 by `T-A4`**: the `/v1` contract is
  `apps/backend/openapi/v1.yaml`, served at `GET /v1/openapi.json` and checked
  against the router in both directions. What remains in `api.md` is the
  dashboard's `/api` surface, and its banner now says so.
