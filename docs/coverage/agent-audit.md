# Agent action audit log — T-05 record

**Landed 2026-07-28.** Ticket:
[`../plan/01-tickets.md`](../plan/01-tickets.md) `T-05`. Finding closed: `S-5`
(`usage_events` records cost, not behaviour).

---

## What was missing

The system could say what a turn cost and what the model replied. It could not
say what the agent *did*: which queries ran against the tenant's warehouse,
which Metabase artefacts were created, which calls failed, or under whose
authority any of it happened. `usage_events` counts events by type; the thread
holds the prose. Neither is a record of the actions themselves.

That gap is tolerable while the only actor is a person watching a stream. It
stops being tolerable at `T-13`, where a key in someone else's CI config makes
calls nobody is watching, and at `T-10`, where the agent starts doing things
rather than describing them.

## The shape of the fix

### One decorator, not seven call sites

`tools.WithAudit` wraps `interfaces.Tool` and writes exactly one row per
execution. It is applied once, in `internal/bootstrap/stack.go`, over the
finished registry:

```go
s.Tools = agentbudget.GuardAll(s.Tools)
s.Tools = tools.WithAuditAll(s.Tools, s.AgentActions)
```

The alternative — a `recorder.Record(...)` line inside each tool — is seven
call sites today and an eighth that somebody forgets, and the forgotten one is
the tool an incident asks about. `s.Tools` is the whole registry: nothing
reaches the agent except through it, so a tool added next year is audited
without its author knowing this package exists. This is the same seam `T-16`
used for the budget guard, for the same reason.

### Order against the budget guard is load-bearing

Audit wraps **outside** `agentbudget.Guard`. A refused tool call returns the
guard's refusal payload with a **nil error** — it has to, because the model
only reads tool results, never errors (`T-16`). Wrapped the other way round,
the audit layer would see a successful call returning JSON and record
`result_status=ok` for a call that never ran.

`agentbudget.IsRefusal` is what distinguishes them, and it lives in
`agentbudget` rather than in the audit code so the refusal payload has one
owner.

### Status is derived, not passed in

| Status | Condition |
| --- | --- |
| `error` | the tool returned an error |
| `blocked` | `agentbudget.IsRefusal(result)` — the call was refused before it ran |
| `truncated` | the result carries `"truncated": true` — the model saw less than the tool retrieved |
| `ok` | anything else |

`rows_returned` reads `row_count` off the result and stays **NULL** when the
result has none. A tool that returns no rows at all is not a query that matched
zero rows, and recording both as `0` would erase the distinction the whole of
`T-16` exists to preserve.

### Redaction keeps the SQL and drops the credential

`args_redacted` is the arguments as JSON with two passes over every string:

- **By key** — `dsn`, `password`, `secret`, `token`, `api_key`, `credential`,
  `private_key`, `authorization`.
- **By value** — a URI carrying `user:password@host`, or the keyword form with
  a `password=` field. Matching the value and not only the key is what catches
  a credential passed under an innocent name.

No tool takes a DSN today; tools address a source by id and the plaintext never
leaves the resolver. The guard is against the tool that does, not the seven
that exist.

It errs toward removing too much: a `SELECT` whose text happens to contain
`password=` is dropped whole. AGENTS.md §2 forbids persisting a decrypted DSN,
and a query lost from the audit log is still in the thread.

`args_hash` is sha256 over the arguments **before** redaction, so two calls
differing only inside a redacted field do not collide.

Oversized arguments (>64 KB serialised — a document spec with an embedded
table, never a query) are replaced by `{"_oversize_bytes": n}`. The row still
records that the call happened, which is the part that cannot be reconstructed.

### The turn-level half no decorator can see

Two things stop a turn without any tool running: an input guardrail refusing
the question, and `guardrails.CheckFabrication` refusing the answer. Neither
reaches a tool, so neither would appear in a tool-call log at all — and "the
agent was stopped from saying that" is the entry an auditor looks for first.

`ChatRunner.WithActionLog` adds one row for those, with `tool_name` naming
which gate closed:

| `tool_name` | Meaning |
| --- | --- |
| `guardrail` | the question was refused on the way in |
| `final_answer` | the reply stated a figure no tool returned (`T-16`) and was replaced |

This is a deliberate departure from the ticket's "a wrapper around the tool
interface, not inside each tool". The wrapper is still the only integration
point for tool calls; this is one further integration point for turns that
never reach one, without which the ticket's own acceptance item — *a blocked
guardrail turn records `result_status=blocked`* — is unsatisfiable.

The refused question's **text** is not stored. A refused question is the input
most likely to contain something a tenant would not want retained; the row
carries its sha256 so a repeated question is still recognisable.

### Attribution

Identity rides the context, set once at the top of `ChatRunner.Run` beside the
company and thread, because the thing that writes the rows is four packages
away and only the runner knows a cron tick is not a person:

```go
kind, ref := actorOf(p)
ctx = tenantctx.WithActor(ctx, kind, ref)
ctx = tenantctx.WithChannel(ctx, string(p.Channel))
ctx = tenantctx.WithMessageID(ctx, p.UserMsgID)
```

`actorOf` returns `schedule` + the task id when `ScheduledTaskID` is set, even
though the payload also carries the `UserID` of whoever authored the schedule.
Attributing an unattended run to that person puts them at a keyboard they were
not sitting at. Otherwise it is `user` + the identity the channel actually has:
the dashboard user id, the Discord user id, the Lark open id, or the phone
number.

`api_key` (`T-13`) and `watcher` (`T-08`) are already in the enum and need only
their middleware to set the context value — which is exactly what `T-13`'s
ticket says its `APIKeyAuth()` does.

### Append-only at the boundary

`domain.AgentActionRepository` exposes `Create` and `ListByCompany`. There is
no `Update` and no `Delete`. A log the audited code can edit is a log nobody
can rely on, so the capability does not exist at the repository boundary rather
than merely going unused. Retention, when it is needed, belongs in a job with
its own authority.

Neither `thread_id` nor `message_id` carries a foreign key. `DELETE
/api/threads/:id` exists: a CASCADE would let a user erase the record of what
the agent did by deleting the conversation, and a SET NULL would erase which
conversation it was. The log outlives its subject. `company_id` keeps its
CASCADE — a deleted tenant takes its whole world with it.

## The endpoint

`GET /api/audit/actions?from&to&thread_id&tool&limit&offset`, admin-only via
`apiPolicy`. Window is RFC3339 defaulting to the last 30 days, matching the
usage endpoints exactly — two audit surfaces disagreeing about what `from`
means is a bug report waiting to be filed.

Admin rather than member for the same reason the DSN routes are: every row
carries the full SQL the agent ran, so the log describes the shape of the
tenant's warehouse to anyone who can list it — a wider view than any single
thread gives.

`args_redacted` is `json.RawMessage`, not `[]byte`: `encoding/json` renders a
byte slice as base64, and a log whose arguments must be decoded before they can
be read is a log nobody reads. This was caught against the running API, not by
inspection.

## Verification

### Unit

```
$ go test ./internal/tools/... ./internal/app/... -race -count=1
ok  	github.com/fauzanebd/argentum/internal/tools	3.255s
ok  	github.com/fauzanebd/argentum/internal/app	14.784s
```

12 new tests. The ones that matter:

- `TestAuditBlockedCallStillRecordedWithoutRunning` — a real
  `agentbudget.Guard` with a one-call budget: the inner tool runs once, two
  rows are written, the second is `blocked`.
- `TestAuditRedactsCredentials` — four credential shapes (URI userinfo, keyword
  `password=`, a nested `api_key`, one inside an array) leave no trace, while
  the SQL and an innocent value survive.
- `TestAuditHashesRawArgsNotRedactedOnes` — two different secrets under the
  same key produce different hashes.
- `TestAuditFailureDoesNotFailTheCall` — a dead control DB does not turn a
  logging outage into a customer-visible one.
- `TestActorOfDistinguishesScheduleFromUser` — a scheduled run is attributed to
  the schedule even when the payload names its author.

### Migration round-trip

```
$ migrate -path migrations/control -database "$URL" version
23
$ migrate -path migrations/control -database "$URL" down 1
23/d agent_actions (29.692458ms)
$ migrate -path migrations/control -database "$URL" up
23/u agent_actions (16.160583ms)
$ migrate -path migrations/control -database "$URL" version
23
```

`cmd/api` applied it on boot: `control DB migrated to version 23`.

### Live — one demo chat

Signed up a fresh company, added the demo DSN, asked *"Chart total sales per
month for 2024 as a line chart, and tell me the total."*

```
      tool_name       | result_status | rows_returned | duration_ms | actor_kind |  channel  | source_id
----------------------+---------------+---------------+-------------+------------+-----------+-----------
 get_schema           | ok            |               |          79 | user       | dashboard |
 get_schema           | ok            |               |           4 | user       | dashboard |
 run_sql              | ok            |             6 |         143 | user       | dashboard |
 run_sql              | ok            |             1 |          26 | user       | dashboard |
 create_visualization | error         |               |          25 | user       | dashboard |
```

The `create_visualization` failure is environmental, not a defect in this
ticket, and the row is the evidence: `error_text` reads *"Metabase database for
source: warehouse not synced to Metabase; add or rotate the DSN so registration
can run"*. Registration then fails because the tenant DSN has to be reachable
from **two** places at once — `localhost:5433` for the host-run worker,
`postgres_demo:5432` for the Metabase container — and the host does not resolve
`postgres_demo`. Same family as `E-1`/`E-4` in
[`environment-notes.md`](environment-notes.md). The audited row is correct
either way: a failed call is exactly what the log is for.

Then *"How do I center a div in CSS?"*, which the guardrail refuses:

```
 tool_name  | result_status | rows_returned | duration_ms | actor_kind |  channel
------------+---------------+---------------+-------------+------------+-----------
 guardrail  | blocked       |               |           0 | user       | dashboard
```

### Live — a scheduled run is attributed to the schedule

A task on `*/2 * * * *` was created and left to fire unattended. Its rows carry
the task id, not the id of the admin who authored it:

```
 tool_name  | result_status | rows_returned | actor_kind |              actor_ref               |  channel
------------+---------------+---------------+------------+--------------------------------------+-----------
 get_schema | ok            |               | schedule   | f310ec02-0b7d-4dcf-b9b2-897964308d75 | dashboard
 run_sql    | ok            |             1 | schedule   | f310ec02-0b7d-4dcf-b9b2-897964308d75 | dashboard
```

`channel` reads `dashboard` because a scheduled task runs on the thread it owns
and that thread's channel is the dashboard. The channel says where the reply
lands; `actor_kind` says who asked. They are different questions, which is why
both columns exist.

### Live — no credential in the table

```
$ pg_dump -U metabase -d argentum --data-only --table=agent_actions > agent_actions.dump
$ grep -c "demo:demo@" agent_actions.dump
0
$ grep -Ec "[a-z]+://[^ /@:]+:[^ /@]+@" agent_actions.dump
0
$ grep -Ec "password *=" agent_actions.dump
0
```

(`demo` alone is not a useful search: it is a substring of `demo_analytics` and
of the source's label. The credential *shape* is what was searched for.)

### Live — the endpoint

```
$ curl -s -o /dev/null -w '%{http_code}' /api/audit/actions
401
$ curl -s -H "Authorization: Bearer $ADMIN" "/api/audit/actions?tool=run_sql"
2 rows
run_sql ok 1 4ff74661 2384bee9 43205690b0c7
run_sql ok 6 4ff74661 2384bee9 9cb1b66d3d26
$ curl -s -H "Authorization: Bearer $OTHER_COMPANY" /api/audit/actions
{"actions":[]} 200
```

A second company signed up on the same instance sees an empty log — the read is
scoped by `company_id` from the JWT, never from a query parameter.

Member-role rejection is covered by `TestGatedRoutesRejectMembers` in
`cmd/api`, which exercises every admin route in `apiPolicy` — including this
one — against both roles through the real router.

## What this does not do

- **No dashboard UI.** The endpoint exists; nothing renders it. `T-A5` builds
  the integrator-facing view and can use the same rows.
- **No retention policy.** Rows accumulate. At current volumes (one row per
  tool call) this is not yet a question, and the answer belongs with a job that
  has authority the agent does not.
- **`source_id` is what the model passed, not what was resolved.** When a
  single-source tenant omits it, the tool resolves the default internally and
  the column is empty. Recording the resolved id means reaching into each
  tool's resolution, which is the per-tool coupling the decorator exists to
  avoid.
- **The API process writes no rows.** `cmd/api` holds the repository read-only;
  the agent runs in the worker. When `T-A2` renders a report inside the API
  request path, that path needs its own write.
- **`apps/backend/docs/api.md` and the Postman collection were not updated,**
  which `agents/verification.md` asks for on a new endpoint. Both still describe
  the pre-dashboard single-tenant service (`POST /v1/query`, `/jobs/:id`) and
  neither has been touched by `T-04` or `T-R5` either — three tickets' worth of
  endpoints are missing from them. Reviving them for one route would leave a
  document that is right about `/api/audit/actions` and wrong about everything
  else, which is the more misleading state. The current inventory is
  [`api-surface.md`](api-surface.md), which this ticket did update. Rewriting or
  retiring the two stale files is worth its own ticket, and `T-A4` — which
  builds an OpenAPI spec with a CI parity check in both directions — is the
  obvious place to fold it in.
