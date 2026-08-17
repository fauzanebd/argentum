# Environment Notes — `T-00` Runtime Smoke Test

Run 2026-07-26 against the monorepo layout, after `T-00b`. The app was exercised
through the HTTP API rather than a browser; every step below is a real request
against a locally running `cmd/api` + `cmd/worker`.

## Result

**The system works end to end in the new layout.** Signup, connection
registration, thread creation, agent execution, streaming, guardrails, and
metering all function. Migrations self-applied to version 20, not dirty.

**But the one substantive analytical question produced a fabricated answer.** See
"Critical finding" below. That is a product-quality issue, not a migration issue —
it would have behaved identically before the monorepo.

## Steps

| # | Step | Result |
| - | ---- | ------ |
| 1 | `docker-compose --profile dev up` | ⚠️ port conflict, see E-1 |
| 2 | `go run ./cmd/api` | ✅ migrations applied to v20, `/health` + `/ready` OK |
| 3 | `go run ./cmd/worker` | ✅ asynq scheduler + processing started, table-picker enabled |
| 4 | `POST /api/auth/signup` | ✅ 201, company + access token |
| 5 | `POST /api/connections/test` | ✅ `{"ok":true}` |
| 6 | `POST /api/connections` | ✅ 201, demo tenant registered as default |
| 7 | `POST /api/chat` — "total sales last month?" | ⚠️ answered, **number fabricated** — see C-1 |
| 8 | `POST /api/chat` — "how do I center a div?" | ✅ guardrail refusal, correct message |
| 9 | `POST /api/chat` — "Halo" | ✅ `trivialReply` short-circuit, Indonesian reply |
| 10 | `GET /api/usage/summary` | ⚠️ metered, but primary model missing — see C-2 |
| — | Scheduled tasks, WebSocket streaming, Metabase card/dashboard | **not exercised** |

Metabase dashboard creation was not tested: the local Metabase container is
freshly initialised and has no admin account, so `create_visualization` /
`create_dashboard` would fail on auth. `apps/backend/scripts/setup_metabase.sh`
exists for this and was not run.

---

## Critical findings

### C-1 · The agent fabricated a number rather than admitting it ran out of steps

Asked "What were our total sales last month?", the agent replied:

> **Total Sales for December 2024: $1,234,567.89**
>
> This represents the sum of all `sales_amount` values from transactions that
> occurred in December 2024, calculated by joining the `fact_sales` table with the
> `dim_date` table…

The true value:

```sql
select sum(f.sales_amount)
from fact_sales f join dim_date d on f.date_id = d.date_id
where d.year = 2024 and d.month_number = 12;
--  3,863,405,700.00
```

**Reported $1,234,567.89. Actual $3,863,405,700.00** — wrong by a factor of ~3,100,
and `1,234,567.89` is a placeholder digit sequence.

**Mechanism.** The worker log shows exactly one SQL statement executed for the
turn, and it was not the sales query:

```sql
SELECT MIN(full_date), MAX(full_date), COUNT(*) FROM dim_date
```

The agent spent its budget on `get_schema` calls plus that one date-range probe,
hit the **3-iteration ceiling** (finding `Q-5`), and then — with no result to
report and no instruction covering exhaustion — produced a confident,
correctly-formatted, entirely invented figure. Its *reasoning* was right: it
correctly determined the data ends 2024-12-31 and that "last month" meant December
2024. Only the number was imaginary.

**Why this reframes `Q-5`.** The iteration cap was documented as "multi-step
agentic work is capped structurally" — a depth limitation. It is worse than that.
It does not truncate gracefully; it fabricates. For a BI product this is the worst
possible failure mode, because the output is unfalsifiable to the user: correct
units, plausible magnitude, confident prose, complete fiction.

**`Q-5` should be P0, not P1.** And `T-16`'s requirement needs strengthening: on
budget exhaustion the agent must state what it could not complete and must be
explicitly forbidden from reporting any figure it did not retrieve from a tool
result. A guardrail rule asserting "never state a numeric result that did not come
from a `run_sql` response" is worth considering.

This is also the single best argument for `T-01` (evals): this failure is invisible
without a golden set, and it is the exact class of regression six historical
prompt/model commits shipped blind.

Model in use for this run: `deepseek/deepseek-v3.2` via OpenRouter, from `.env` —
not the code default (`anthropic/claude-haiku-4.5`). Whether the fabrication is
model-specific is unknown and is precisely what `T-01`'s baseline must pin down.

#### Resolved 2026-07-27 by `T-16` — and the cap was never the only mechanism

Three things had to change, because the fabrication had three routes to the
user and closing one only moved it to the next.

**1. The cap became a budget, and running out is now something the model is
told.** Iterations went from a hard 3 to a per-turn budget of 8 iterations /
12 tool calls / 200k tokens / 150s (`internal/agentbudget`, all four
configurable via `AGENT_MAX_*`). The dimension that matters is not the number:
it is that exhaustion now *speaks*. Every tool runs behind a guard that, once
the budget is gone, refuses the call and returns an instruction in its place —
say what you retrieved, say what you did not, ask whether to continue, and
state no figure that did not come from a tool result. The model reads that,
because it arrives as a tool result. It never saw the old cap at all: the SDK
simply asked it for "your final response based on the information available"
and it complied by inventing one.

**2. `config/agents.yaml` was the real cap all along.** `WithMaxIterations(3)`
in Go and `max_iterations: 3` in the YAML both existed, and the YAML won —
`WithAgentConfig` is applied last in the option list, so the Go value was
decorative. The key is now deleted and Go is authoritative. Two sources of
truth for a safety limit means the limit is whichever one nobody is reading.

**3. An output check the streaming path actually runs.** `T-16` calls for a
guardrail rule against stating a figure no tool returned. It could not be a
rule in `config/guardrails.yaml`, for two reasons — one of them a finding in
its own right:

> **agent-sdk-go applies output guardrails only on its blocking path.**
> `Guardrails.ProcessOutput` is called once in the SDK, at
> `pkg/agent/agent.go:1315`, inside `runWithoutExecutionPlanWithToolsTracked`.
> The streaming path (`pkg/agent/streaming.go`) applies `ProcessInput` and
> never calls `ProcessOutput`. Every dashboard, WhatsApp, Discord and Lark
> turn streams. **So every `scope: output` rule in `config/guardrails.yaml` —
> PII redaction included — has never run in production.** `T-07b` owns that
> gap now; `T-16` only routes around it.

The check therefore lives in `ChatRunner.rejectFabrication`, which also gives
it what a YAML regex cannot have: the turn's evidence. It replaces the reply
when the text states a monetary or magnitude figure **and** no data tool
returned a single row this turn **and** the turn either ran a tool or ran out
of budget. That last clause is what keeps follow-up turns ("show that in
millions") working.

**4. The empty-result path, which the cap fix would not have touched at all.**
A `run_sql` result with `row_count: 0` now carries a note saying in words that
there is no figure in it and not to state one. That is finding `E-5`'s second
fabrication — the query succeeded, matched nothing, and the agent reported
"IDR 1,488,000" anyway.

### C-2 · Primary-model usage was not metered at all

`GET /api/usage/summary` after a full multi-step turn plus two more turns:

```json
{ "total_cost_usd": 0.000778,
  "total_tokens_in": 371, "total_tokens_out": 93,
  "event_counts": { "llm_call": 1, "sql_query": 1 },
  "cost_by_model_usd": { "gpt-5-mini": 0.000278 } }
```

Only **one** `llm_call`, and only for `gpt-5-mini` — the *light* model used by
guardrails and the classifier. The primary model ran the entire agent turn and
recorded **zero** usage events.

**Mechanism, verified in source.** `MeteredLLM.wrapStream`
(`internal/app/metering_llm.go:122`) aggregates token usage out of stream event
metadata and then:

```go
if agg.InputTokens > 0 || agg.OutputTokens > 0 || ... {
    m.record(ctx, &agg)
}
```

If the provider never emits usage in the stream, **nothing is recorded — silently**.
The Anthropic SSE path does emit it (which is what commit `74f5419` built cache
billing on). OpenAI-compatible streaming generally emits usage only when
`stream_options: {"include_usage": true}` is requested; if `agent-sdk-go` does not
set that, every streaming turn on an OpenAI-interface provider is free of charge as
far as Argentum knows.

Symptom and mechanism are both verified. The `include_usage` explanation is a
strong hypothesis, not yet confirmed — check what `agent-sdk-go` sends before
fixing.

**Severity.** This compounds `B-1` (credits decremented but never enforced): you
cannot enforce a budget you are not measuring. With the current default provider,
the dominant cost of every chat turn is invisible. Fix before `T-03`, because
`T-03`'s budget check would otherwise gate on a number that is always near zero.

#### Resolved 2026-07-27 by `T-02c` — and the hypothesis was wrong

`include_usage` **was** being requested. `MeteredLLM.withForcedUsage` has set
`EnableReasoning` since `74f5419`, and that is the only flag agent-sdk-go's
OpenAI client checks before setting `stream_options.include_usage` for a
non-reasoning model. The provider was sending the usage chunk all along.

The SDK throws it away. In `pkg/llm/openai/streaming.go`, usage is forwarded
into a `StreamEvent` at line 212 — inside `GenerateStream`, the **no-tools**
path. `GenerateWithToolsStream`, which is the path every agent turn takes
(`agent.RunStream` → `streaming.go:358`), sets `IncludeUsage: true` on each
iteration's request at line 361 and then never reads `chunk.Usage` at all. So
`wrapStream` had nothing to aggregate, and the zero-check silently swallowed it.

Fixed by reading the usage off the wire instead of forking the SDK:
`internal/llmusage` installs an `http.RoundTripper` on the OpenAI-interface
client that parses `usage` out of the SSE body and reports it to a collector
carried in the request context. Anthropic is untouched — it still meters from
stream event metadata, which wins whenever present, so `74f5419`'s cache
billing cannot be double-counted. A turn where neither source reports anything
now logs at `Warn` and increments `llm.stream_turns_without_usage`.

Same smoke test, after the fix — signup, demo DSN, one analytical question:

```json
{ "total_cost_usd": 0.002378,
  "total_tokens_in": 5616, "total_tokens_out": 691,
  "event_counts": { "llm_call": 2, "sql_query": 1 },
  "cost_by_model_usd": { "deepseek/deepseek-v3.2": 0.001558,
                         "gpt-5-mini": 0.00032 },
  "tokens_in_by_model": { "deepseek/deepseek-v3.2": 5232, "gpt-5-mini": 384 } }
```

The primary model appears with 5232 in / 579 out, and 3840 of those input
tokens were cache reads the tap picked out of `prompt_tokens_details` — priced
at 0.10x instead of full rate.

---

## Environment findings

### E-1 · Hardcoded compose host ports collide with other projects

`docker-compose.yml` maps redis as `"6380:6379"`, hardcoded. Port 6380 on this
machine is held by `tradecharlie-testing-redis-1`, an unrelated project that has
been up for days, so `docker-compose up` fails outright.

Worked around for this run with an out-of-repo override
(`ports: !override ["6385:6379"]`) — note that compose **appends** port lists on
merge, so a plain override adds a second mapping and fails the same way; `!override`
is required.

Worth parameterising the host ports (`${REDIS_PORT:-6380}` etc.) so a developer with
other stacks running is not blocked.

### E-2 · The `.env` points at a remote database — and looks local

This is the most dangerous finding of the run.

```
DB_HOST      = 103.76.120.171     ← remote
METABASE_URL = http://103.76.120.171:3030   ← remote
REDIS_URL    = localhost:6379
CORS_ORIGINS = http://localhost:5173
```

A hybrid: remote Postgres and Metabase, local Redis and CORS. Starting the API with
this file connects to a **deployed control-plane database** — it logged
`control DB schema already up to date` because that database is already migrated.

Running the smoke test as-is would have written a company, an admin user, an
encrypted tenant DSN, threads, messages, and usage events into that remote
environment, and created Metabase artifacts on the remote instance. The run was
aborted before any write and repeated against local infrastructure with:

```bash
DB_HOST=localhost DB_PORT=5432 REDIS_URL=localhost:6385 \
METABASE_URL=http://localhost:3000 go run ./cmd/api
```

**This is a direct consequence of `Q-10`** — `.env.example` was gitignored, so there
was no canonical local template and the working `.env` drifted onto production
endpoints. Now that `.env.example` is tracked, make it a genuinely local-first
template, and consider a startup warning when `ENV` is unset/development while
`DB_HOST` is neither `localhost` nor a private address.

### E-3 · Docker CLI version skew

`~/.nix-profile/bin/docker` (24.0.5, API 1.43) precedes Docker Desktop's 29.1.3 on
`PATH`. The daemon requires API ≥ 1.44, so `docker run`, `docker image inspect`, and
`docker-compose` fail with *"client version 1.43 is too old"* — while `docker build`
happens to work, which makes it easy to misdiagnose.

Fix: put `/usr/local/bin` ahead of `~/.nix-profile/bin`, or remove docker from the
nix profile.

### E-4 · Local Metabase has no admin account — **resolved 2026-07-27**

The freshly created `argentum_metabase` container has not been through onboarding,
so Metabase-dependent tools cannot authenticate locally. Run
`apps/backend/scripts/setup_metabase.sh`, or document that chart/dashboard flows
require a one-time manual Metabase setup.

Resolved during `T-01`, because the eval set's three chart/dashboard cases
cannot run without it:

```bash
METABASE_URL=http://localhost:3000 DB_HOST=postgres_demo DB_PORT=5432 \
DB_NAME=demo_analytics DB_USER=demo DB_PASSWORD=demo \
bash scripts/setup_metabase.sh
```

`DB_HOST` here is the compose service name — Metabase reaches the demo database
over the `argentum_network` bridge, not over the host port mapping. The
admin credentials come from `METABASE_ADMIN_EMAIL` / `METABASE_ADMIN_PASSWORD`
in `.env`, which is what `internal/metabase` authenticates with; using the
script's defaults instead would leave the agent unable to log in.

### E-5 · Demo `dim_date` labels are space-padded — **fixed 2026-07-27**

Found by the first `T-01` eval run, not by inspection.

`migrations/demo_tenant/002_seed_data_dim.sql` seeded `month_name` with
`TO_CHAR(d, 'Month')`, which pads to nine characters. The stored value was
`'December '`, so the obvious filter an agent writes —

```sql
where dd.year = 2024 and dd.month_number = 12 and dd.month_name = 'December'
```

— matched **zero rows** against a table that plainly holds December data.
`day_name` had the same problem.

What made it worth chasing is what the agent did next: given an empty result,
it reported *"Total Sales for December 2024: IDR 1,488,000"* — a second
fabrication, from a different mechanism than `C-1`. The November case, asked
the same way, produced an honest "no sales transactions recorded for November
2024". Same model, same prompt, same empty result, opposite behaviours.

Fixed in three places: `TRIM(...)` in `002` for fresh volumes,
`006_trim_dim_date_labels.sql` for databases already seeded, and an `UPDATE`
applied to the running container. Verified: the query above now returns
`3863405700.00`.

### E-6 · The eval tenant's sources were never registered with Metabase — **fixed 2026-07-27**

Found while gating `T-16`, and it invalidates part of the `T-01` baseline.

`create_visualization` resolves a Metabase database id from
`db_connections.metabase_database_id`. That column is populated by
`CompanyService.CreateConnection`, which runs `MetabaseWarehouseSync` after
inserting the row — the HTTP path. The eval harness seeds its sources by
calling the repository directly, so it skipped the sync, and every source in
the control DB carried a NULL:

```
 Argentum Eval | Demo Retail |
 Argentum Eval | Demo People |
 Smoke Test Co | Demo Retail |
```

Every `create_visualization` call in every eval run has therefore failed with
`warehouse not synced to Metabase; add or rotate the DSN so registration can
run`. The three `chart_dashboard` cases were measuring how the agent reacts to
a broken tool. `dashboard-two-cards` could not have passed at any iteration
budget, which nearly cost `T-16` a correct verdict on its own gate case.

Two fixes, both in the harness:

1. `eval.EnsureTenant` now syncs any source with a NULL id and persists the
   result. Idempotent, and a Metabase that is down costs three cases, not the
   run.
2. A `-metabase-db-host` flag (default `postgres_demo:5432`). Metabase runs
   inside compose and cannot resolve the `localhost:5433` the harness itself
   connects on; handed the harness's own DSN it rejects the registration with
   *"check your host settings"*. This is the same host-vs-service-name split
   `E-4`'s setup script documents.

Verified: `Demo Retail → 3`, `Demo People → 4`, and `dashboard-two-cards`
passes.

Worth noting what this does **not** fix: `Smoke Test Co` and any tenant
created outside the API keeps its NULL. If a future ticket finds
`create_visualization` broken for a hand-seeded tenant, this is why.

**Obsolete as of 2026-08-17, and worth keeping for the shape.** `T-D11` deleted
`create_visualization`; `create_dashboard` is native and needs no Metabase
database id, so no eval case depends on this sync any more. `syncToMetabase`
still runs in `internal/eval/tenant.go` — idempotent, one round trip per source
at setup — and its warnings now say that failing it is harmless. It goes with
`T-D15`. The finding itself is the one to remember: **three cases were scoring
the agent's reaction to a broken tool and reading as a capability result**,
which is a failure mode no pass rate can show you.

---

## State left behind

- Containers `argentum_postgres`, `argentum_postgres_demo`, `argentum_redis`
  (on host port **6385**, not 6380), `argentum_metabase` — left running.
- `cmd/api` and `cmd/worker` — stopped.
- Test data written to the **local** control DB only: company "Smoke Test Co",
  user `smoke@local.test`, one connection, three threads. Nothing remote.
- To reset: `cd apps/backend && docker-compose down -v`.
