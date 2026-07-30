# T-A5 — Integrator-facing observability

**Shipped 2026-07-30.** The last open ticket of Sprint 1's API track, and the
one that decides whether an integrator finishes their first bad night with us or
gives up: a 403 at 11pm is now answerable by the tenant, from their own
dashboard, with the request id their script was handed.

Three surfaces:

| Surface | Who reads it | What it answers |
| ------- | ------------ | --------------- |
| Settings → API Keys | the tenant's admin | per key: calls, error rate, last used, and the last 50 non-2xx responses with request id, route, status and error code |
| `GET /v1/usage` | the tenant's *application* | spend over a window they choose, by model, plus the credit position |
| `/metrics` | whoever operates the deployment | per-route latency histograms and status counts, and — behind a token — per-key counters |

---

## 0. The ticket's own precondition was wrong, and it mattered

`01-tickets.md` says, of the `/metrics` work:

> Per-route latency and status histograms on `/metrics`, labelled by route and
> key id. (`/metrics` is secured by `T-05`; do not add this before that lands.)

**`T-05` was the agent audit log and secured nothing here.** `/metrics` has been
served unauthenticated since it existed — it is in `cmd/api/policy.go`'s
`unpolicedPaths`, and moving it off the public router is `T-17`, cut position 3,
which has not landed.

Taken literally the instruction would have blocked the item forever. Taken
loosely it would have published a tenant's API key ids on an open endpoint,
which is the opposite of what the parenthetical was protecting. So:

- Route-level numbers go out as before. A route label names no tenant.
- **Per-key labels require `METRICS_TOKEN`.** Unset is never a match —
  `metricsAuthorized` returns false for an empty token, so leaving the setting
  blank cannot turn every scrape into an authorized one.

That is the smallest thing that makes the instruction true for the data being
added. `T-17` still owns the real fix (internal listener, Prometheus exposition
format); the histogram is already shaped for the second half of that — cumulative
buckets keyed by upper bound with a `+Inf` overflow — so the conversion is a
serializer, not a remodelling.

The `internal/metrics` package comment also claimed the endpoint served
"Prometheus-format counters and histograms". It never has. That is corrected
rather than inherited.

## 1. Two tables, not a request log

The obvious schema is one row per `/v1` request. It answers both questions and
it is wrong: a nightly job polling a report every ten seconds is 8,640 rows a
day for one key, and 99% of them say `200`.

So the counters are a rollup and only the failures keep their detail
(`032_api_observability`):

- **`api_request_stats`** — one row per (key, hour, route, method, status class),
  upserted. Rows per key per day are bounded by routes × methods × 3 classes × 24
  rather than by traffic. The gate's 18 requests produced **5 rows**.
- **`api_request_errors`** — one row per non-2xx, carrying the `request_id` the
  caller was handed, the route pattern, the status, and the envelope's `code` and
  `type`.

Three decisions inside that are load-bearing:

- **The hour, not the day or the minute.** An hour is the coarsest bucket that
  still answers "did it start failing after the 14:00 deploy?". A minute would
  multiply the row count by sixty for a question nobody asks of a counter.
- **The route *pattern*, never the concrete path.** `/v1/reports/:id`, not
  `/v1/reports/8f14e45f`. A raw path makes cardinality a function of how many
  ids a tenant asked about, in a table whose entire design is to not have any.
  A request that matched no route records an empty route and is folded into
  `unmatched` on `/metrics`, so a scanner walking a thousand URLs cannot mint a
  thousand labels.
- **`UPSERT … SET requests = existing + EXCLUDED`, not `= EXCLUDED`.** Two API
  replicas flush the same bucket independently; an assignment keeps whichever
  landed last and silently drops the other replica's traffic. The max is a
  `GREATEST` for the same reason.

Status *class* is stored (2/4/5) rather than the exact status: the exact one
lives on the error row, where there is a row to carry it, and storing it in the
rollup would triple the row count for a number the error rate does not use.
`/metrics` keeps exact statuses, because in-process that costs nothing and
telling a 403 from a 429 is the whole point.

## 2. Recording must not cost the request

`internal/apiobs.Recorder` does a map update under a mutex on the request path
and nothing else. A flush loop writes batches every `API_V1_OBS_FLUSH_SECONDS`
(default 15, inside the ticket's "within a minute").

The trades are stated where they are paid:

- **A killed process loses up to one interval.** Mitigated where it matters:
  `deps.cleanup()` stops the loop and flushes synchronously, so the records
  covering the minutes before a shutdown — exactly when someone is looking —
  survive. Proven in the gate below.
- **A failed write drops its batch.** It does not retry and it does not queue.
  Holding rows across a Postgres outage of unknown length is how an
  observability feature takes down the thing it observes.
- **The error buffer is capped at 1,000 per interval**, and overflow is counted
  and logged at Warn rather than dropped silently. A tenant in a retry storm
  would otherwise turn our memory into their retry budget. The *counters* are
  unaffected — they are per-bucket increments — so the rollup still shows the
  full traffic when the detail list is truncated.

## 3. What a 401 is not

A request that never authenticated has no company and no key. Those samples are
counted on `/metrics` and **never persisted**.

The two alternatives are both dishonest: guess whose credential it was, or show
it to every tenant. An invalid key is answerable from the server log and from
the key's own status in the tab (`revoked` / `expired`), which is where "my key
stopped working" has always been answered.

The middleware placement is what makes the rest countable. It sits **below**
`RequestID` (so the sample carries the id the caller received) and **above**
`APIKeyAuth` (so a 401 is still counted), and reads the key identity *after*
`c.Next()` returns — by which time the authenticator downstream has set it. The
kill switch stays above it: a 503 from a switched-off API is a fact about us, not
about anybody's integration.

## 4. Why `apierr` writes to the gin context

By the time a middleware sees a response, the body is bytes on the wire. Parsing
the `code` back out would mean buffering every response to read two fields off
the few that failed.

So `apierr` stamps `api_error_code` / `api_error_type` on its way past — in
`AbortStatus` **and** in `NewDetail`. The second one is not an afterthought: the
idempotency middleware's `409 request_in_flight` composes its body by hand around
a `Detail`, and it would otherwise be the one failure the recorder could not
name.

A failure that never went through `apierr` records with an empty code. That is
deliberate — recording an invented one would be worse — and there is a test case
for it.

## 5. `GET /v1/usage` is not `/v1/me` with more fields

`/v1/me` answers "can I call at all", in one paste, with no period attached to
the number. `/v1/usage` answers "what did my integration cost over the period I
bill my own users for": a window the caller chooses (`from` / `to`, RFC 3339,
defaulting to the current UTC calendar month, capped at 366 days), broken down
by model.

Four decisions:

- **The period is echoed, never implied.** A spend figure with no period on it
  is a number nobody can reconcile.
- **A bad timestamp is a 400 that names the field.** A caller who sent
  `2026-07-01` gets `param: "from"`, not the default month's numbers looking
  plausible and answering a different question.
- **The `credits` block is `/v1/me`'s object, field for field**, and the spec
  `$ref`s the same `Credits` schema. Two shapes for one concept inside one API
  is a thing integrators trip over.
- **The balance is a pointer, so a zero balance is written.** `omitempty` on a
  `float64` would delete the single most important value the block can carry: a
  present `"balance_usd": 0` says "you are out of credit"; an absent one says
  "nobody looked".

`Credits` now has a Go type bound to it for the first time — `/v1/me` assembles
its block as a `gin.H`, so that schema was unchecked in both directions until
this ticket's `usageCreditsBody` was added to the reflection cases.

A model with tokens and no price keeps its row with `cost_usd: 0`. Dropping it
would make the per-model figures disagree with the total, which is the kind of
discrepancy an integrator finds while reconciling an invoice.

## 6. Where the dashboard reads it

Two routes, both admin-only in the policy table for the same reason the audit log
is: the failure list names every route every integration called, across the whole
company.

```
GET /api/api-keys            admin   roster + per-key traffic for the last 24h
GET /api/api-keys/errors     admin   the last 50 non-2xx, optionally ?key_id=
```

- **The traffic rides with the roster**, like the tool vocabulary in
  `GET /api/agents`. The tab never renders one without the other.
- **`?key_id=` is a query parameter, not a path segment.** `GET /api-keys/errors`
  beside `DELETE /api-keys/:id` is fine; a `GET /api-keys/:id/errors` would put a
  literal beside a wildcard in one gin method tree, which is the collision
  `api_keys.go`'s own comment has been avoiding since `T-13`.
- **A failed stats read degrades to a list with no stats.** An admin who needs to
  revoke a leaked credential must not be blocked by an unreadable counters table.
  A deployment without the recorder answers an empty error list rather than a
  503, because a 503 puts an error banner on a page whose primary job works.
- **Keys with no traffic have no stats entry at all.** "No calls" and "no such
  key" are different facts; a parallel array would have to invent a zero row.

## 7. The gate

Run 2026-07-30 against the local stack: `cmd/api` built as a binary (never
`go run` — see `go-run-serves-stale-binaries`), `API_V1_RATE_PER_MIN=5`,
`API_V1_OBS_FLUSH_SECONDS=3`, `METRICS_TOKEN=gate-token-ta5`, a fresh tenant
(`TA5 Observability Co`) and three keys with different scopes. Migration `032`
applied on boot (`schema_migrations` went 31 → 32).

### The three forced failures, and the ids the callers received

```
$ curl -D - localhost:8099/v1/usage -H "Authorization: Bearer $KEY_B"    # read:threads only
HTTP/1.1 403 Forbidden
X-Request-Id: req_b14e072ea0df36c05270e9d96a1bd95c
{"error":{"type":"permission","code":"insufficient_scope","message":"This key does not have
 the `read:usage` scope. …","request_id":"req_b14e072ea0df36c05270e9d96a1bd95c"}}

$ curl -D - -X POST localhost:8099/v1/reports/render -H "Authorization: Bearer $KEY_C" …
HTTP/1.1 500 Internal Server Error
X-Request-Id: req_2150c828ab06c8b43d513bd9590f755c
{"error":{"type":"server","code":"rendering_unavailable","message":"Document rendering is not
 available on this deployment.","request_id":"req_2150c828ab06c8b43d513bd9590f755c"}}

$ for i in 1..9; do curl localhost:8099/v1/usage -H "Authorization: Bearer $KEY_A"; done
attempt 1: status=200 remaining=3 request_id=req_9e8a2709f972caf18a76f9dd99138512
attempt 2: status=200 remaining=2 request_id=req_949ba6dada27ab6c98cc76f7aafe382b
attempt 3: status=200 remaining=1 request_id=req_e46991c3e837f85a4e4720201d93657d
attempt 4: status=200 remaining=0 request_id=req_132973f13d376bc2e197036ba2359c54
attempt 5: status=429 remaining=0 request_id=req_9cd361777e2e5416ed979faf23630315
…
attempt 9: status=429 remaining=0 request_id=req_fc9dc3daa945773b1cfd67a3886f043b
```

### The same failures in the tenant's own tab, seconds later

`GET /api/api-keys/errors`, five seconds after the last call:

```
limit = 50 | rows = 9
04:54:54  GET   /v1/usage                    429  rate_limit_exceeded    req_9cd361777e2e5416ed979faf23630315
04:54:54  GET   /v1/usage                    429  rate_limit_exceeded    req_fc9dc3daa945773b1cfd67a3886f043b
04:54:54  GET   /v1/usage                    429  rate_limit_exceeded    req_e51b981822f169776084d65520b46d7d
04:54:54  GET   /v1/usage                    429  rate_limit_exceeded    req_c00915d937689b1f6fd697877a9cb475
04:54:54  GET   /v1/usage                    429  rate_limit_exceeded    req_23f6df572d712c55acb881a3cc3a7aae
04:54:45  POST  /v1/reports/render           500  rendering_unavailable  req_2150c828ab06c8b43d513bd9590f755c
04:54:33  GET   /v1/documents/:id/content    404  document_not_found     req_5bf9ffbbe8195fa17157eec0a38eefd0
04:54:33  GET   /v1/usage                    403  insufficient_scope     req_b14e072ea0df36c05270e9d96a1bd95c
04:54:18  GET   /v1/usage                    400  invalid_timestamp      req_a4f56795a866e878923de63f7c4c44d7
```

Every request id matches the one its `curl` received. `?key_id=` narrows to one
key (7 rows, all `9466ac83`).

`GET /api/api-keys`, the roster with its traffic:

```
Gate key C (write:reports)  1  request  1 failed  100%    5ms avg   5ms max
Gate key B (no read:usage)  1  request  1 failed  100%   13ms avg  13ms max
Gate key A (read:usage)    13  requests 7 failed  53.8%   7ms avg  42ms max
```

And the rollup those came from — 18 requests, five rows:

```
                 key                  |      bucket_hour       |           route           | method | class | requests | lat_sum | lat_max
--------------------------------------+------------------------+---------------------------+--------+-------+----------+---------+--------
 9466ac83-…                           | 2026-07-30 04:00:00+00 | /v1/usage                 | GET    |     2 |        7 |     146 |      70
 9466ac83-…                           | 2026-07-30 04:00:00+00 | /v1/usage                 | GET    |     4 |        6 |      10 |       2
 5ecd3a0b-…                           | 2026-07-30 04:00:00+00 | /v1/usage                 | GET    |     4 |        2 |      23 |      13
 9466ac83-…                           | 2026-07-30 04:00:00+00 | /v1/documents/:id/content | GET    |     4 |        1 |       8 |       8
 8b8758eb-…                           | 2026-07-30 04:00:00+00 | /v1/reports/render        | POST   |     5 |        1 |       5 |       5
```

### In the browser

The Chrome extension was not connected, so the dashboard was driven over the
DevTools protocol against headless Chrome — the same approach as the `T-S3` gate,
with one wrinkle worth writing down: a synthetic `element.click()` does **not**
open a Radix tab or an expander. The clicks have to be real
`Input.dispatchMouseEvent` triples at the element's own box, and the first pass
screenshotted the General tab while reporting "clicked API keys".

With real clicks: the API Keys tab shows each key's 24-hour traffic line, and
expanding **Failures** on a key renders its table — key A's five 429s, its 404
and its 400; key B's two 403s; key C's 500. The request ids are monospace and
`select-all`, because the entire point of showing one is that somebody pastes it.

### `GET /v1/usage`

Against seeded `usage_events` rows for the gate tenant (three rows, two models,
one of them unpriced):

```json
{
  "period": { "from": "2026-07-01T00:00:00Z", "to": "2026-08-01T00:00:00Z" },
  "spend": {
    "tokens_in": 2000, "tokens_out": 420, "cost_usd": 0.6,
    "by_model": {
      "claude-opus-5":     { "tokens_in": 1500, "tokens_out": 420, "cost_usd": 0.6 },
      "unpriced-model-v1": { "tokens_in": 500,  "tokens_out": 0,   "cost_usd": 0 }
    }
  },
  "credits": { "enforced": true, "status": "ok", "balance_usd": 25, "grant_usd": 25, "remaining_pct": 100 }
}
```

`?from=2026-07-01` (a date with no time) → `400 invalid_timestamp`, `param: "from"`,
and no query reached the database.

### Admin-only

A member session (invited, accepted, `role: member`) on both routes:

```
GET /api/api-keys/errors → 403 {"error":"admin only"}
GET /api/api-keys        → 403 {"error":"admin only"}
```

### `/metrics`, with and without the token

```
$ curl localhost:8099/metrics                      # no credential
routes: 3 | keys block present: False
  GET /v1/documents/:id/content   1 req 1 err {'404': 1}                        max 8ms
  GET /v1/usage                  13 req 7 err {'200': 6, '400': 1, '403': 1, '429': 5}  max 42ms
  POST /v1/reports/render         1 req 1 err {'500': 1}                        max 5ms

$ curl localhost:8099/metrics -H "Authorization: Bearer gate-token-ta5"
keys: { "5ecd3a0b-…": {"requests": 1, "errors": 1},
        "8b8758eb-…": {"requests": 1, "errors": 1},
        "9466ac83-…": {"requests": 13, "errors": 7} }
GET /v1/usage latency_ms: { "5": 10, "10": 10, "25": 11, "50": 13, … "+Inf": 13,
                            "sum_ms": 99, "count": 13, "max_ms": 42 }
```

A wrong token, and an unset `METRICS_TOKEN` with any bearer, both get the
stripped snapshot.

### The shutdown flush

A 403 issued and the process `SIGTERM`ed inside the three-second flush interval:

```
pre-shutdown 403 request_id=req_1bd07b852ef5fc3cbcbe295dd5a4f381
{"level":"info","msg":"Shutting down…"} … {"level":"info","msg":"Bye"}

$ psql -tAc "select request_id, status, error_code from api_request_errors where request_id = '…'"
req_1bd07b852ef5fc3cbcbe295dd5a4f381|403|insufficient_scope
```

It is visible in the tab's key-B panel at 11:57:35, beside the 11:54:33 one.

### Suites

```
go build ./...          ok
go vet ./...            ok
go test -race ./...     ok   (cmd/api, internal/apiobs, internal/metrics,
                              …/handlers, …/middleware among them)
golangci-lint run ./... 0 issues
make types-check        api-types: 5 generated files are current
make openapi-check      ok  v1.yaml is a valid OpenAPI 3.1 document (15 paths, 48 schemas)
                        ok  postman collection + environment
                        ok  packages/argentum-python/src/argentum/types.py
                        ok  docs/api/quickstart.md quotes 12 example files exactly
tsc --noEmit            ok   (dashboard)
eslint                  ok   (dashboard)
```

All four of `T-A4`'s drift checks pass with the new route, and they earned their
keep: the route-parity test failed until `/v1/usage` was in the spec, the
policy-classification test failed until it was in `unpolicedPaths` with its
scope named, and the scope-parity test proves behaviourally that `read:usage` is
both sufficient and necessary for it.

## 8. New tests

| Package | What they pin |
| ------- | ------------- |
| `internal/apiobs` | hour bucketing and folding; only failures keep detail; the request id survives into the row; unauthenticated samples reach `/metrics` and not the database; the error cap and its dropped counter; a failed flush drops its batch rather than growing; prune throttling; concurrent record-while-flushing (400 requests, no loss); the status-class table including the 3xx `/v1` should never issue |
| `internal/metrics` | the histogram is cumulative with a `+Inf` overflow; exact statuses per route; `unmatched` folding; the 500-key cardinality cap and its overflow counter; `WithoutKeyLabels` strips keys and keeps routes and does not mutate its receiver; a snapshot is a copy (under `-race`) |
| `…/middleware` | the route pattern rather than the concrete path; the recorded request id equals the response header; the error code is captured from `Abort`, `AbortStatus` and a hand-composed `NewDetail` body, and is empty for a handler that wrote its own; an unmatched route records no route; a nil sink is a pass-through |
| `…/handlers` | `/v1/usage`: the month default, an honoured window, five bad-window shapes each naming its param, the per-model join including an unpriced model, the three credit states with a *present* zero balance, a failed credit lookup that keeps the spend, the scope refusal, and the typed 503 with no reader. Plus the errors route: the fields an integrator needs, the key filter, empty-not-broken without the recorder, a reported read failure, the whole-hour stats window, and a stats failure that does not block the roster |
| `cmd/api` | `/metrics` key labels require the token; an unset token is never a match |

## 9. Known limits

- **An invalid API key appears in no tenant's tab**, by design (§3). If "my key
  stopped working" turns out to be the most common support question anyway, the
  narrow fix is to attribute a *revoked or expired* key's 401 by its prefix —
  the prefix is known at that point — and leave genuinely unknown prefixes
  unattributed.
- **A `/metrics` scrape is process-local.** Two API replicas report two halves of
  the traffic, and the worker has its own collector nobody scrapes. The tenant's
  durable view (`api_request_stats`) is correct across replicas; the operator's
  is not, and `T-17` is where that gets an exporter.
- **SSE routes record when the stream ends**, with the whole connection lifetime
  as latency. Correct, and it skews the route histogram for `/v1/chat` and
  `/v1/reports/:id/events` against the synchronous routes beside them. Read those
  two rows as "how long clients stayed", not "how slow we were".
- **`from`/`to` on `/v1/usage` are the whole window API.** No grouping by day, no
  per-`api_user_ref` breakdown. A tenant metering individual users of their own
  product can only do it by calling once per period, which is enough for
  invoicing and not enough for a chart. Additive when someone asks.
- **Retention is 30 days** (`API_V1_OBS_RETENTION_DAYS`), pruned at most hourly by
  whichever process is running. A deployment that is never up for a full hour
  never prunes.
