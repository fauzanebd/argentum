# Watchers — T-08 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 3 — It tells you
first*. **The wedge** — the ticket that changes how a company works. A watcher
evaluates a defined metric (T-06) on a cron and, when the number breaches a
condition, fires a real agent turn to explain the move and delivers the
explanation to WhatsApp, Discord, Lark, or the dashboard, unprompted. It is the
shift from "answer when asked" to "tell you first".

It builds directly on the metric registry: a watcher fires off a **defined,
validated** number through the same evaluation path `query_metric` uses, never
one the LLM re-derived. That is the whole reason T-06/T-07 came first — the first
false alarm off a flaky number would destroy trust permanently.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-08` | Watchers: schema, evaluation loop, breach → agent turn → multi-channel delivery, dry-run gate | 3d | **gated live 2026-08-02** — dashboard channel only; a fire delivered to WhatsApp/Discord/Lark is still owed |

`T-09` (the dashboard UI) is a separate ticket with its own record:
[`watchers-ui.md`](watchers-ui.md).

---

## T-08 · Watchers domain + evaluation loop + delivery

### 1. What ships

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/040_watchers.{up,down}.sql` — `watchers` + `watcher_events` |
| Entity | `internal/domain/watcher.go` — `Watcher`, `WatcherEvent`, `WatcherGrain`, `WatcherComparator`, `WatcherChannel`, `WatcherDelivery`, `WatcherRepository` |
| Service | `internal/app/watcher_service.go` — CRUD, `DryRun`, `HandleFire`, `CompleteFire`, comparator + window arithmetic, proactive delivery |
| Repository | `internal/adapters/postgres/watcher_repo.go` |
| Queue | `internal/queue/tasks.go` (`watcher:eval`, `WatcherEvalPayload`, `WatcherEventID` on `ChatRunPayload`), `internal/queue/scheduler.go` (`WatcherConfigProvider`) |
| Runner hook | `internal/app/chat_runner.go` — `WatcherFireCloser`, `WithWatchers`, the `completeWith` delivery hand-off, and the `watcher` actor kind |
| Proactive Lark | `internal/lark/{provider,client}.go` — `Send(chatID)` beside `Reply(messageID)` |
| CRUD API | `internal/transport/http/handlers/watchers.go`, `wire.go`; `cmd/api/policy.go` (read=member, write+dry-run=admin) |
| Worker wiring | `cmd/worker/main.go` — the `watcher:eval` handler, the second periodic task manager, `WithDelivery` |
| Stack + API wiring | `internal/bootstrap/stack.go`, `cmd/api/{deps,bootstrap,router}.go` |
| Config | `internal/config/config.go` — `WATCHER_ENABLED`, `WATCHER_MAX_PER_COMPANY` |
| SDK types | `packages/api-types/*` (generated from the new domain + wire structs) |

Migration **040**: the ticket's header read `023` until 2026-07-30; `023` has
been `agent_actions` since `T-05`, and the tree was at 039 (`metric_definitions`)
— the sixth ticket in a row whose reserved number was already spent.

### 2. The properties the ticket turns on

- **A watcher fires off a defined number, not a re-derived one.** `HandleFire`
  and `DryRun` both evaluate through `MetricEvaluator` — a narrow interface over
  `MetricService` — so a watcher's value is the same number the admin validated
  at save and a chat turn returns through `query_metric`. There is one evaluation
  path, not a watcher-specific copy.
- **A watcher is born silent, and cannot go live without a dry-run.** `Create`
  forces `enabled=false`. `Update` refuses to set `enabled=true` unless a dry-run
  was recorded within the last 24h — the guard against the trust-destroying false
  alarm. Editing the *condition* (metric, grain, comparator, threshold,
  compare_to) clears the standing dry-run, because a dry-run over the old
  condition says nothing about the new one; the cleared row then fails the enable
  check until a fresh dry-run runs.
- **A standing breach fires once per cooldown, not once per tick.** `last_fired_at`
  is stamped at fire time — *before* the turn is enqueued — so a broken delivery
  channel cannot turn a persistent breach into a fire storm. Default cooldown is
  12h (`720` minutes).
- **The window is the most recent complete period, in the watcher's timezone.**
  Complete rather than to-date, so the number is stable within a period and a
  comparison is a whole period against a whole period. The cron cadence decides
  how often that stable number is checked; the cooldown keeps a standing breach
  quiet between checks. Timezone is bound with an embedded `time/tzdata`, the same
  fix `T-02` made for scheduled tasks — the deployed alpine images ship no
  zoneinfo.
- **Delivery is proactive and multi-channel, reusing one turn.** A breach enqueues
  **one** `chat:run` against the watcher's dedicated thread; when it completes,
  `ChatRunner.completeWith` hands the answer to `WatcherService.CompleteFire`,
  which pushes it to every configured channel and records the per-channel outcome
  on the event. Because a watcher has no inbound message to reply to, Lark needed
  a new `Send(chat_id)` beside `Reply(message_id)`; WhatsApp `SendMessage` and the
  Discord outbound bus were already proactive; the dashboard channel is a no-op —
  the answer is already in the thread.
- **A second config provider, not a second scheduler.** `WatcherConfigProvider`
  turns enabled `watchers` rows into `watcher:eval` periodic configs, run under
  its own `asynq.PeriodicTaskManager`. The two managers never collide because
  their entries carry different task types — exactly the shape the ticket asked
  for ("a second DB-backed config provider … Do not build a second scheduler").
- **An unattended breach on an exhausted tenant refuses.** `HandleFire` runs the
  same budget check `ScheduledTaskService` does, for the same reason: a
  `watcher:eval` tick never passes through `ChatEnqueuer`, so the credit gate has
  to be a second integration point. A refused fire records
  `suppressed_reason='credits_exhausted'` rather than spending.

### 3. Verified, and what is not

**Verified** (`go test ./...`, `go vet`, `gofmt`, `make types-check` all green;
the `T-04` policy/route diff passes with the seven new routes):

- **Window arithmetic** — `completePeriod` and `trailingPeriods` for day/week/month,
  that periods abut and step back correctly, and that boundaries are computed in
  the watcher's timezone (an `Asia/Jakarta` case crossing a UTC day boundary).
- **Comparator evaluation** — `gt`/`lt`/`pct_change_gt`/`pct_change_lt`/`no_data`,
  including: `no_data` breaches when the metric returns no usable row and holds
  when it does; a threshold comparator does *not* breach (and does not error)
  when the metric has no value; a real (non-`ErrInvalidInput`) error propagates
  so asynq retries.
- **`HandleFire`** — a breach writes a breached event, stamps the cooldown,
  appends the briefing, and enqueues one `chat:run` carrying the `WatcherEventID`;
  a non-breach writes a silent event and enqueues nothing and does not touch the
  cooldown; a breach inside cooldown records `suppressed_reason='cooldown'` and
  fires nothing; an exhausted tenant records `credits_exhausted`; a disabled
  metric skips the tick without querying.
- **`CompleteFire`** — records the assistant message and one delivery outcome per
  channel; delivers to WhatsApp/Discord/Lark and treats the dashboard as already
  delivered; records `skipped` for a channel whose provider is not wired.
- **`DryRun`** — counts breaches across the trailing periods and records the
  dry-run timestamp.
- **CRUD guards** — a new watcher is disabled; the per-company cap rejects the
  next one; `pct_change` without `compare_to` is refused; enabling without a fresh
  dry-run is refused; a condition change clears the dry-run.
- **`WatcherConfigProvider`** — one `watcher:eval` config per enabled watcher,
  the task type is correct, a non-UTC timezone is folded into the cron spec, and
  the payload names only the watcher.

### 3a. The live gate — run 2026-08-02

Local stack (postgres, demo warehouse, redis, MinIO), API on :8099 and a worker
on a private Redis DB index, both from explicitly built binaries. Two watchers on
a metric pinned to December 2024, cron `* * * * *`, cooldown 720m, one
`dashboard` channel each.

**Enable is gated, and the dry-run is what opens it.** `PUT` with
`enabled: true` before any dry-run:

```
[400] {"error":"invalid input: run a dry-run within the last 24h before enabling this watcher"}
```

`POST /watchers/:id/dry-run` then answered
`{"periods_evaluated":14,"would_have_fired":14,…}` for the breaching condition
and `would_have_fired: 0` for the impossible one — the number `T-09`'s form puts
in front of the toggle. Both enabled 200 afterwards.

**The wedge, working.** Three `watcher_events` rows inside two minutes:

```
 name                                        | fired_at | metric_value | breached | suppressed | delivery
 December revenue above 1M                   | 08:30:00 |   3863405700 | t        | -          | [{"status":"delivered","channel":"dashboard"}]
 December revenue above 999 trillion         | 08:31:00 |   3863405700 | f        | -          | -
 December revenue above 1M                   | 08:31:00 |   3863405700 | t        | cooldown   | -
```

Row 1 enqueued a turn into the watcher's own thread, which the worker logged as
`watcher breached; agent turn enqueued`. The briefing prompt it received:

> [Watcher alert: December revenue above 1M] The metric "Revenue (Dec 2024
> fixture)" (revenue_dec_fixture) for the period 2026-08-01 to 2026-08-02 has
> breached its condition. - Current value: 3863405700.00 - Condition: gt
> 1000000.00 … Explain the likely drivers of this move in 120 words or fewer, and
> name what to check next.

The agent answered with `run_sql` breakdowns by day, channel and payment method —
an explanation rather than a restatement, which is what the prompt asks for.
Row 2 is the acceptance item "writes an event row and sends nothing". Row 3 is
the cooldown, recorded rather than silent.

**Cascade:** deleting the metric returned the watcher's `GET` 404 and left zero
rows.

#### What the gate found

- **The briefing states the figure at the wrong scale.** The reply's last line
  reads *"The current value of $3,863,405,700 (approximately $3.86 million)"* —
  three orders out, and a `$` on an IDR metric. `T-16`'s check passes it,
  correctly: the figure *is* tool-derived. This is the same defect `T-B1`'s gate
  recorded on 2026-07-31 (`$3,863,405,700` vs `$3,863,405.70` from one SQL
  result), and it matters more here than there. A chat reply is read by the
  person who asked, in front of the thread that produced it; a watcher briefing
  arrives unprompted, in a channel, as the first thing anyone sees. The gap is
  precisely stated: `T-16` proves a number was queried, not that it was printed
  at the right scale. The metric carries `unit` and `currency` and the renderer
  already formats correctly for documents (`internal/report/format`); the model
  is retyping a figure it was handed.

  **Fixed 2026-08-03 for exactly this shape.** `guardrails.CheckScale` runs
  beside the fabrication check on every turn — briefings included, since a
  watcher fire is a chat turn — and corrects a magnitude word that disagrees
  with the figure it restates: this line now reads *"approximately $3.86
  billion"*. It changes the unit and nothing else, only when the restated digits
  are already right under some other unit, and it logs every correction at Warn
  with the before and after, because a guardrail that edits text and says
  nothing is one nobody can tune. The `$`-on-an-IDR-metric half is untouched —
  that is a currency symbol, not a scale, and the fix for it is the renderer
  the finding already names.

  What is not fixed is the cross-turn case (`business-context.md` §6): one
  total, two magnitudes, in replies that are each internally consistent. That
  needs tool values carried into `TurnEvidence`.
- **Nothing else.** The evaluation loop, the cooldown, the dry-run gate, the
  cascade and the delivery record all behaved as the unit tests said — the first
  ticket in nine where the live half did not contradict them.

### 4. Deviations from the ticket, and why

- **`watchers.thread_id` is a column the ticket's schema did not list.** Each
  watcher mints a dedicated thread at creation, reused across fires, exactly like
  a scheduled task — so the dashboard renders a watcher's history as one
  conversation and `watcher_events.thread_id` has a stable target. The ticket's
  event table already carried `thread_id`/`message_id`, which only makes sense
  against a thread that outlives a single fire.
- **`suppressed_reason` carries `credits_exhausted` as well as `cooldown`.** The
  ticket named only `cooldown`; an unattended breach refused for credit is the
  same shape — a real breach that did not deliver — and deserves the same
  visibility rather than silence.
- **The window is the last *complete* period, not the current one.** A defensible
  alternative was period-to-date; complete was chosen so comparisons are
  whole-against-whole and the number is stable within a period. The cron cadence
  and cooldown, not a moving window, decide how often a watcher speaks. A watcher
  whose cron out-paces its grain (a daily cron on a monthly grain) is guarded by
  the cooldown; the sensible pairing is cron cadence ≈ grain, and the dashboard
  (`T-09`) is where that guidance belongs.
- **`ThreadService`, the enqueuer and the company repo are consumed through
  narrow interfaces** (`WatcherThreads`, `WatcherEnqueuer`, `WatcherCompanies`)
  rather than the concrete types the scheduled-task service uses. This is what
  makes `HandleFire` and the delivery path unit-testable without Redis or a real
  thread stack — the same "declare the dependency at the consumer" rule the chat
  runner's loaders follow.
