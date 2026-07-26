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

### E-4 · Local Metabase has no admin account

The freshly created `argentum_metabase` container has not been through onboarding,
so Metabase-dependent tools cannot authenticate locally. Run
`apps/backend/scripts/setup_metabase.sh`, or document that chart/dashboard flows
require a one-time manual Metabase setup.

---

## State left behind

- Containers `argentum_postgres`, `argentum_postgres_demo`, `argentum_redis`
  (on host port **6385**, not 6380), `argentum_metabase` — left running.
- `cmd/api` and `cmd/worker` — stopped.
- Test data written to the **local** control DB only: company "Smoke Test Co",
  user `smoke@local.test`, one connection, three threads. Nothing remote.
- To reset: `cd apps/backend && docker-compose down -v`.
