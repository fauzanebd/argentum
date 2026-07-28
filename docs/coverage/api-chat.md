# Chat over the API — T-A3 record

Ticket: [`../plan/01-tickets.md`](../plan/01-tickets.md) `T-A3`.

Landed 2026-07-28. Depends on `T-A1` (the contract, the `api` channel,
idempotency, cursors). Unblocks `T-A4` (OpenAPI + SDKs).

A report is an artefact; a question is a conversation. `T-A2` shipped the
first; this ships the second, on the streaming contract an integrator cannot
build around if we get it wrong. It also writes down the event schema
[`api-surface.md`](api-surface.md) observation 4 records as **the dashboard's
most important contract and undocumented** — the same events, one publisher,
now with a public name.

---

## 1. What ships

| Route | Scope | Note |
| ----- | ----- | ---- |
| `POST /v1/chat` | `write:chat` | SSE or synchronous, chosen by `Accept`. `Idempotency-Key` required. |
| `GET /v1/threads` | `read:threads` | Cursor-paginated, `api` channel only, filterable by `user_ref` |
| `GET /v1/threads/:id` | `read:threads` | |
| `GET /v1/threads/:id/messages` | `read:threads` | Cursor-paginated, oldest-first |
| `GET /v1/threads/:id/events` | `read:threads` | Attach to the thread's newest turn; `Last-Event-ID` honoured |
| `DELETE /v1/threads/:id` | `write:chat` | Destroying a conversation is not a read |

Supporting pieces: `internal/transport/http/handlers/v1_chat.go`, the SSE
plumbing both `/v1` streams now share (`v1_sse.go`), the pagination parsing
both listings share (`v1_page.go`), keyset reads on
`ThreadRepository`/`MessageRepository`, and one addition to `T-A1`'s
idempotency middleware (`RetainIdempotentRecord`).

**No migration.** The `api` channel and `api_user_ref` landed with `T-A1`.

## 2. The event schema

The names are the dashboard's, unchanged, because one worker publishes both
surfaces. A second vocabulary for HTTP would be a translation layer kept in
step with a schema nobody had written down.

| Event | Data | Carries `id:` |
| ----- | ---- | ------------- |
| `started` | `{thread_id, run_id, at}` | no |
| `delta` | `{content}` — one token or a few | no |
| `thinking` | `{step}` | no |
| `tool_call` | `{tool}` — the name only | no |
| `tool_result` | `{tool}` | no |
| `message` | a message object — only on a resumed stream, for what was missed | **yes** |
| `error` | `{message}`; terminal | no |
| `final` | `{object:"turn", thread_id, run_id, message, usage}`; terminal | **yes** |
| `: heartbeat` | an SSE comment every 15s | — |

`iteration` is published on the bus and deliberately **not** forwarded: it is
the SDK's own loop counter, it means nothing outside this codebase, and a
public contract that leaks it is a public contract that has to keep emitting
it.

**Only persisted frames carry an `id:`,** and that is the whole of the resume
design. A client's `Last-Event-ID` is the last id it saw; deltas have none, so
it stays pinned to the last *durable* point. A reconnect replays the messages
that were missed — which is the part that was real — and attaches live. Token
deltas exist nowhere but the connection that carried them, and an id on one
would promise a replay this system cannot perform.

## 3. Decisions worth carrying forward

### The stream reconciles the bus against the transcript, because pub/sub keeps nothing

Redis pub/sub delivers to whoever is subscribed *at that instant*. The turn is
enqueued before the subscription can exist — the thread id is what the enqueue
returns — so there is a window in which the worker could publish `final` into
an empty room. On the fastest turns, which are exactly the ones the synchronous
door is for.

So every attach does the same two things in the same order: SUBSCRIBE, then ask
the transcript whether the turn has already answered. `LatestAssistantSince` is
that question as one query. Without it the caller holds a connection open
waiting for an event that has already happened; with it, the worst case is a
lost *delta*, never a lost answer.

### `last_message_at` and `created_at` are written by different clocks

The attach route has to decide whether a thread is settled or mid-turn. The
obvious implementation reads the thread row's `last_message_at` and asks for an
assistant message at or after it.

It does not work, and the live gate is what proved it:

```
     last_message_at        |        created_at          | role      | matches
 2026-07-28 16:29:56.871868 | 2026-07-28 16:29:56.871738 | assistant | f
```

`Touch` writes `last_message_at` from the API process's clock; `created_at`
comes from Postgres's `now()`. They are 130µs apart in the wrong direction, so
a settled thread's answer was never `>= last_message_at` and attaching to one
held the connection open until the client gave up — for an answer already in
the database.

The fix compares two rows written by the same clock. `LatestByThread` returns
the newest message of any role, and both cases collapse into one expression:
the turn's window starts at that message. If it is the assistant's, the thread
is settled and the terminal check matches it immediately. If it is the user's,
a turn is running and its answer is whatever lands after the question — which
also excludes the previous turn's answer, sitting older.

The general rule this is an instance of: **never compare timestamps written by
two different writers when you can compare two rows written by one.** The send
path compares a Go timestamp against database timestamps too, and is safe only
because the gap it spans is seconds rather than microseconds.

### A 504 is the wait running out, not the turn

Turns run five to seven tool calls after `T-16`; the gate's own runs took 58
and 130 seconds. The synchronous door is a convenience for short questions, and
when it overruns it answers **504 carrying `{thread_id, run_id}`** so the caller
attaches to the stream instead of asking again and paying twice.

That required a change to `T-A1`'s idempotency middleware. Its rule — *a failed
request forgets its key* — is right for every other 5xx: the next thing a
well-behaved client does with a 500 is retry, and a key that survived would
refuse that retry for 24 hours. It is exactly wrong here, because the work is
still running and still being billed. `RetainIdempotentRecord` marks the one
shape of failure where the request failed and the work did not, and it is
deliberately narrow: use it only when the response body hands the caller a way
to collect what is in flight.

### A hung-up client must not strand its own idempotency key

The middleware completes its record *after* the handler returns, and it used the
request's context to do it. On a streaming route the ordinary ending is the
client hanging up, which cancels that context — so the record stayed `in_flight`
for the full 24-hour TTL and every later retry under that key got `409
request_in_flight` for a turn that had finished minutes earlier.

Bookkeeping for work that has already run is now detached
(`context.WithoutCancel`). This is a general fix, not a chat one: `T-A2`'s
report doors were exposed to the same thing on any client that gave up
mid-render.

### `/v1/threads` is the `api` channel and nothing else

A key is a company credential. Left unfiltered, `GET /v1/threads` would let a
leaked key page through the conversations of named people who have dashboard
sessions — the staff's own chat history, from a credential sitting in someone
else's CI config. The tenant's audit surface for those is the dashboard, which
is role-gated.

So the channel filter is not a default a caller can widen, and a dashboard
thread's id answers 404 from `/v1` exactly like another tenant's. `T-A1` drew
the same line on the write side: a key holder passing a dashboard `thread_id`
to `POST /v1/chat` is refused.

### `user_ref` is enforced, not trusted

The ticket asks that neither `user_ref` be able to read the other's thread. We
cannot authenticate a `user_ref` — the key belongs to the company, and the
reference is the tenant's own identifier for their end user. What we can do is
hold the caller to the one they named: a request that supplies `user_ref` gets
a 404 for any thread that does not carry it.

That turns "our backend passes the logged-in user's id through" from a
convention into a boundary. It answers 404 rather than 403 for the same reason
the cross-tenant case does: a 403 confirms the thread exists, and an existence
oracle over another user's conversations is the whole vulnerability.

### The stream carries the tool's name and not its arguments

A `tool_call` frame says `{"tool":"run_sql"}`. The arguments are the SQL the
agent ran against the tenant's warehouse; the place for those is `T-05`'s audit
log, redacted on the way in and reachable only by an admin. A progress stream is
for what is happening, not for what was queried — the same trim `T-A2` applied
to the report stream.

### `DELETE` takes `write:chat`, and there is no third scope

Destroying a conversation is not a read, so it cannot sit behind
`read:threads` — that is the scope a tenant hands to a reporting job. A
`write:threads` scope would be more precise and is deliberately not minted:
scopes are fixed at a key's creation and there is no `Update`, so a new scope
forces every key issued since `T-13` to be re-minted, and it is the tenant who
has to edit their CI config to do it. `T-A1` made the same argument in the
opposite direction when it shipped two scopes ahead of their routes.

### Usage comes from the metering events, not from the message row

`messages.tokens_in`/`tokens_out` are zero for every streamed turn:
`completeWith` is called with zeros, because the provider reports usage per LLM
call and a turn is five to seven of them. The `final` frame's usage is therefore
a window over `usage_events` — `[turn start, now]` — which is the same data
`/api/usage/*` reports and cannot drift from it.

One consequence is stated in the code and worth repeating: an attach to a thread
whose turn had *already* finished cannot bound that window earlier than the
answer itself, so it reports **no usage block at all** rather than a zero one.
Zeros would say the turn was free, which is never true — the same reasoning that
makes `/v1/me` answer "not enforced" instead of "$0.00".

### One SSE implementation, one pagination parser

`v1_sse.go` and `v1_page.go` exist because the report stream and the chat stream
have to agree about things a caller can observe — that the response is
unbuffered, that every frame is flushed, that a heartbeat is a comment rather
than an event they have to learn to ignore — and because two listings answering
differently to the same malformed `limit` is a difference an integrator
discovers by hitting both. `T-A2`'s handler was refactored onto them rather than
copied from.

## 4. Gate output

Run 2026-07-28 against `cmd/api` + `cmd/worker` on the local
`argentum_postgres`, `redis` on 6385 and the demo warehouse on 5433, with the
LLM on its real provider. Tenant created over the API, warehouse attached over
the API, keys minted over the API. The API was started with `DB_HOST=127.0.0.1`
explicitly rather than by sourcing `apps/backend/.env`, which points at a
**remote** server.

### 1. A streamed turn, `curl -N`

```
$ curl -sN -X POST localhost:8080/v1/chat \
    -H "Authorization: Bearer arg_ed0092a7b9_…" \
    -H "Accept: text/event-stream" -H "Idempotency-Key: gate-stream-1" \
    -d '{"message":"What were our total sales last month?","user_ref":"their-user-42"}'

frame counts: {'started': 1, 'delta': 266, 'tool_call': 7, 'tool_result': 7,
               ':comment': 5, 'final': 1}

event: started      data: {"at":"2026-07-28T16:21:22Z","run_id":"5db0adcc-…","thread_id":"9e865d96-…"}
event: tool_call    data: {"tool":"get_schema"}
event: tool_result  data: {"tool":"get_schema"}
event: tool_call    data: {"tool":"run_sql"}
event: tool_result  data: {"tool":"run_sql"}
: heartbeat        (×5, one every 15s of silence)

id: MTc4NTI1NTc2MzY1MzQyNjpjMWJiNzFmOS0wMjY0LTRlZjgtODcxNi05NDVhYWQ3MWUzNzM
event: final
data: {
  "object": "turn",
  "thread_id": "9e865d96-f84a-4416-91d3-5c9b443f0b34",
  "run_id":    "5db0adcc-4547-4f2c-bdbc-3d6fe9b9a1f0",
  "message": { "id": "c1bb71f9-…", "object": "message", "role": "assistant",
               "content": "I'll help you find the total sales for last month…",
               "created_at": "2026-07-28T16:22:43.653426Z" },
  "usage":   { "tokens_in": 12066, "tokens_out": 2199, "cost_usd": 0.006145 }
}
```

The `final` frame's id decodes to a cursor naming that message — it is what the
client hands back as `Last-Event-ID`.

### 2. The synchronous door, same question

```
$ curl -s -X POST localhost:8080/v1/chat -H "Accept: application/json" …
HTTP 200          (58.7s)
{ "object": "turn", "thread_id": "5a02dcf1-…", "run_id": "17632587-…",
  "message": {…"role":"assistant"…}, "usage": {"tokens_in":4441,"tokens_out":1563,
  "cost_usd":0.003876}, "request_id": "req_800ca14b345280f8e3fee65e77eb53bc" }

the figure in each answer:
  stream: ['12,462,599.03', '3,863,405,700']
  sync:   ['1,028,052,300.00', '1,089,105,700.00', '3,708,552,300.00',
           '3,863,405,700.00', '87,881,300.00']
```

Both doors return **IDR 3,863,405,700** — the true figure, the one `T-16`'s
`C-1` case is about.

### 3. A client that hangs up mid-stream

```
$ curl -sN -X POST … --max-time 8            # killed 8s in
curl exited 28
event: started  data: {…"thread_id":"bdb9a529-…"}
event: delta    data: {"content":"I"}
final frame received before the hangup: False

… 70 seconds later, over a new connection:
$ curl -s localhost:8080/v1/threads/bdb9a529-…/messages
  user      | How many customers did we have last month?
  assistant | I'll help you find out how many customers you had last month. Let me…
```

The turn finished in the worker, the answer persisted, and the next call
collected it.

### 4. The 504, and the resume it points at

```
$ curl -s -X POST localhost:8099/v1/chat …        # API_V1_SYNC_TIMEOUT_SECONDS=5
HTTP 504
{"error":{"type":"server","code":"turn_in_progress",
  "message":"This turn is taking longer than the synchronous window. It is still
             running — stream it from `GET /v1/threads/c34f81e3-…/events` rather
             than asking again, which would pay for it twice."},
 "in_flight":{"thread_id":"c34f81e3-…","run_id":"57db69f1-…","started_at":"…"}}

$ curl -sN localhost:8080/v1/threads/c34f81e3-…/events
resumed stream frames: {'delta': 376, ':comment': 6, 'tool_call': 5,
                        'tool_result': 5, 'final': 1}
final → message 6ddb4337-…, usage {tokens_in: 15576, tokens_out: 4778,
                                   cost_usd: 0.008078}

$ curl -s -X POST … -H "Idempotency-Key: gate-504-1"    # the retry a 504 invites
HTTP 200   Idempotent-Replay: true      same message id: 6ddb4337-…

sql> select … from messages where thread_id='c34f81e3-…';
 user_msgs | assistant_msgs
-----------+----------------
         1 |              1
```

The 504 kept its key, so the retry replayed the answer instead of starting a
second billed turn. One question, one answer.

### 5. A retry arriving *while* the turn runs

```
$ curl … -H "Idempotency-Key: gate-inflight-1"   # 3s after the first
HTTP 409
{"error":{"code":"request_in_flight",
  "message":"The original request under this `Idempotency-Key` is still running.
             Poll it rather than retrying."},
 "in_flight":{"thread_id":"c498a19c-…","run_id":"1cebd52c-…"}}

… once it settled:
HTTP 200  Idempotent-Replay: true   role: assistant
          usage {tokens_in: 12881, tokens_out: 3456, cost_usd: 0.006914}
sql> 1 user, 1 assistant
```

### 6. Threads, isolation and the scope split

```
=== a read:threads-only key cannot spend ===
POST   /v1/chat           HTTP 403   permission / insufficient_scope
DELETE /v1/threads/:id    HTTP 403
GET    /v1/threads        HTTP 200          ← and it can still read

=== four user_refs, four threads ===
  hangup-user-7   bdb9a529-1f57-43c2-8ecc-b20c0be61686
  timeout-user-3  c34f81e3-fe79-4c84-928d-8827ef7a154d
  sync-user-9     5a02dcf1-94cc-43ce-a68b-b70b5596a65b
  their-user-42   9e865d96-f84a-4416-91d3-5c9b443f0b34

  sync-user-9 reading their-user-42's thread by id:  HTTP 404
  …and its messages:                                 HTTP 404
  the owner reading their own:                       HTTP 200
  list filtered to their-user-42:                    1 thread

=== the same user_ref inside the idle gap continues one thread ===
  first turn thread:  9e865d96-f84a-4416-91d3-5c9b443f0b34
  follow-up thread:   9e865d96-f84a-4416-91d3-5c9b443f0b34

=== a dashboard thread is not addressable from /v1 ===
  created over /api:                     201  b1774948-…
  GET /v1/threads/<that id> with a key:  HTTP 404
  present in GET /v1/threads:            False
```

### 7. Pagination and resume

```
$ GET /v1/threads/9e865d96-…/messages?limit=1
  page1: user      | What were our total sales last month?   has_more: True
  page2: assistant | I'll help you find the total sales…     has_more: True
  ?cursor=nonsense → HTTP 400 invalid_cursor

$ curl -sN …/events -H "Last-Event-ID: <the page-1 cursor>"
  event: message  id: MTc4NTI1NTc2MzY1…  assistant "I'll help you find the total sales…"
  event: message  id: MTc4NTI1NjE3NzUy…  user      "And how many orders was that?"
  event: message  id: MTc4NTI1NjE5Njg3…  assistant "I can see from my previous analysis…"

$ curl -s …/events -H "Last-Event-ID: bogus!!"
HTTP 400 {"error":{"code":"invalid_cursor","param":"Last-Event-ID",
  "message":"That `Last-Event-ID` is not one this API issued…"}}
```

### 8. Usage attribution

```
$ GET /api/usage/by-channel
{"channels":[{"channel":"api","thread_count":4,"event_count":24,
              "tokens_in":41100,"tokens_out":10539,"cost_usd":0.024732}]}

$ GET /api/usage/by-user
{"users":[
  {"channel":"api","user_key":"their-user-42", "user_key_kind":"api_user_ref",…},
  {"channel":"api","user_key":"timeout-user-3","user_key_kind":"api_user_ref",…},
  {"channel":"api","user_key":"hangup-user-7", "user_key_kind":"api_user_ref",…},
  {"channel":"api","user_key":"sync-user-9",   "user_key_kind":"api_user_ref",…}]}
```

### 9. The audit rows a key writes — closing two earlier items

`T-13` and `T-A1` each recorded an acceptance item that could not be proven
until a turn was started over HTTP. This is that turn: the `request_id` the
synchronous door returned, on every tool call the agent made.

```
sql> select actor_kind, actor_ref, channel, tool_name, result_status, request_id
     from agent_actions where request_id='req_800ca14b345280f8e3fee65e77eb53bc';

 api_key | 32518469-…(the key id) | api | get_schema           | ok      | req_800ca…
 api_key | 32518469-…             | api | get_schema           | ok      | req_800ca…
 api_key | 32518469-…             | api | run_sql              | ok      | req_800ca…
 api_key | 32518469-…             | api | run_sql              | error   | req_800ca…
 api_key | 32518469-…             | api | run_sql              | ok      | req_800ca…
 api_key | 32518469-…             | api | run_sql              | ok      | req_800ca…
 api_key | 32518469-…             | api | create_visualization | blocked | req_800ca…
```

Three things fall out of one query. The turn is attributed to an integration
rather than to a person who was not there (`T-05`). The id in the response body
is the id in the log, so a support conversation starts from a string the caller
already has (`T-A1`). And the last row is `blocked` rather than `ok` —
`T-16`'s budget guard refused a call after the budget ran out, and `T-05`'s
wrap order is what stops that from being recorded as a success.

### 10. Static checks

```
go build ./... && go vet ./...        clean
go test ./...                         all packages ok
go test -race ./internal/transport/... ./cmd/api/   ok
golangci-lint run ./...               0 issues
```

The handler package has tests for the first time. They run against **miniredis**
rather than a hand-written bus, because the property under test is what a
*subscriber* sees — that the handler is subscribed before it waits, that a
publish into an empty room is recovered from the transcript, that a heartbeat
keeps flowing while nothing else does. A fake bus would only assert that the
file agrees with itself.

Two of them were checked against the defect they exist for: reverting the
`newestTurn` fix makes `TestAttachingToASettledThreadDeliversTheAnswer` hang and
fail, and reverting `context.WithoutCancel` makes
`TestTheRecordIsCompletedEvenWhenTheClientHangsUp` fail with the record still
`in_flight`.

## 5. Acceptance

| Item | Status |
| ---- | ------ |
| An SSE turn streams deltas and ends with `final` carrying the message and usage | ✅ §4.1 |
| The sync door returns the same answer as the SSE door for the same question | ✅ §4.2 |
| Killing the client mid-stream still persists the answer | ✅ §4.3 |
| The same `user_ref` inside the idle gap continues one thread; two refs get two | ✅ §4.6 |
| Neither `user_ref` can read the other's thread by id | ✅ §4.6 |
| `/api/usage/by-channel` shows `api`; `/api/usage/by-user` shows the refs | ✅ §4.8 |
| A sync call over the timeout returns 504 with a resumable `thread_id`, and the turn still completes | ✅ §4.4 |
| A `read:threads`-only key gets 403 on `POST /v1/chat` | ✅ §4.6 |

## 6. Known limits

- **A resumed stream does not replay deltas.** Only the message log is durable.
  A client that reconnects gets the messages it missed and then live events; the
  tokens streamed while it was away are gone. Persisting deltas would mean a row
  per token for a replay window nobody has asked for.
- **`GET /v1/threads/:id/events` attaches to the newest turn, not to a named
  one.** If that turn has already answered, the answer is delivered and the
  stream closes. There is no way to stream a specific historical `run_id`;
  `GET …/messages` is how you read one back.
- **An attach to an already-finished turn reports no usage.** The window cannot
  start earlier than the answer. The send and resume paths, which know when the
  turn began, report it exactly.
- **The subscribe happens after the enqueue**, so a turn that answers in the
  handful of milliseconds between them can lose its *deltas*. The answer is
  never lost — the transcript check covers it — and closing the window entirely
  would mean splitting `ChatEnqueuer.Enqueue` into resolve-then-enqueue, which
  is two callers' worth of divergence for a case the LLM's own latency makes
  vanishingly rare.
- **The thread listing is in creation order, not recency.** `last_message_at`
  moves, so a cursor built on it names a position that has already changed;
  `created_at` does not. A caller who wants "most recently active" has to sort
  what they read.
- **`POST /v1/chat` has no cancel.** A caller who starts a turn pays for it.
  Cancellation means reaching into the worker's run loop, which is `T-16`'s
  budget guard's territory and a ticket of its own.
