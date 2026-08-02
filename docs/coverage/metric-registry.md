# The metric registry — T-06, T-07 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 2 — Authoritative
numbers*. The accuracy foundation the "it tells you first" half of the product
rests on: a number defined once, so the same question returns the same answer in
two threads, and so a watcher (T-08) fires on a value that was validated rather
than one the LLM re-derived.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-06` | Metric registry: schema, validation, CRUD, dashboard tab | 3d | **gated live 2026-08-02** |
| `T-07` | `list_metrics` + `query_metric` tools | 1.5d | **gated live 2026-08-02 — the eval-set half (metric cases, before/after pass rate and token delta) is still owed** |

---

## T-06 · Metric registry

### 1. What ships

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/039_metric_definitions.{up,down}.sql` |
| Entity | `internal/domain/metric.go` — `MetricDefinition`, `MetricGrain`, `MetricUnit`, `MetricRepository` |
| **Render + validate** | `internal/metric/template.go` (`Render`, `ValidateTemplate`), `internal/metric/window.go` (comparison windows), `internal/metric/result.go` |
| Parameterised exec | `internal/adapters/db/driver.go` — `Conn.ExecuteReadOnlyParams`, `Dialect.Placeholder`; implemented in all three drivers |
| Service | `internal/app/metric_service.go` — validate-on-save, `Test`, `Query`, one shared `evaluate()` |
| Repository | `internal/adapters/postgres/metric_repo.go` |
| CRUD API | `internal/transport/http/handlers/metrics.go`, `wire.go`; `cmd/api/policy.go` (read=member, write+test=admin) |
| Wiring | `cmd/api/{deps,bootstrap,router}.go`, `internal/bootstrap/stack.go` |
| Dashboard | `apps/dashboard/src/features/settings/metrics-tab.tsx`, `settings-page.tsx` |
| SDK types | `packages/api-types/*` (+ `apps/backend/tygo.yaml` mappings) |

Migration **039**: the ticket's header said `022`, which became `report_branding`
under `T-R5`; the tree was at 038.

### 2. The two properties the ticket turns on

- **The window is bound, never interpolated.** `metric.Render` walks the
  template and replaces each `{{from}}`/`{{to}}` with one dialect placeholder,
  appending the value to an args slice — so a `'; DROP …` in a window value is a
  timestamp the driver escapes, not SQL it parses. This required a new
  `ExecuteReadOnlyParams` on the `Conn` interface and a `Placeholder(n)` on each
  `Dialect` (`$1` / `?` / `@p1`); one placeholder **per occurrence** so MySQL's
  positional `?` needs no reuse. Proven by `TestRenderBindsTheWindowAsParameters`
  and `TestQueryBindsWindowAndReadsTheValue`.
- **A metric that does not work cannot be saved.** `validated` renders the
  template, runs it over a trailing-7-day window in a read-only transaction, and
  requires **exactly one row** whose `value_column` is **numeric and non-null** —
  a null is an error, not a silent zero, which is the same fabrication class T-16
  closed. `Test` runs the identical path without storing, so the dashboard's
  Test button and the save agree. `ValidateTemplate` additionally refuses
  anything but a single SELECT/CTE (comments and string literals scrubbed first,
  so `status = 'deleted'` and `REPLACE()` are not false positives).

### 3. Verified, and what is not

**Verified** (`go test ./...`, `go vet`, `gofmt`, `make types-check`, dashboard
`tsc -b` all green; the T-04 policy/route diff passes with the six new routes):
render binds and rejects unknown/missing tokens; ValidateTemplate accepts a
single SELECT and refuses DELETE/UPDATE/DROP/multi-statement/SELECT INTO/no-
window; the service refuses >1 row, a missing column, a null and a non-numeric
value, and coerces string/`[]byte` decimals; comparison windows abut and step
back a year correctly.

### 4. The live gate — run 2026-08-02

Stack: local compose (control `argentum`, warehouse `demo_analytics` on :5433),
API on :8099, migrations applied on boot (`schema_migrations` 37 → 42). Tenant
`Gate Co T-06`, one admin, one member accepted off a real invite.

**Three metrics, saved against the live warehouse.** Every save renders and
executes, so a stored row is a working query by construction:

| key | template | unit |
| --- | -------- | ---- |
| `revenue` | `SELECT COALESCE(sum(fs.sales_amount),0) … WHERE d.full_date >= {{from}} AND d.full_date < {{to}}` | currency IDR |
| `order_count` | `SELECT count(DISTINCT fs.transaction_id) …` | count |
| `aov` | `SELECT COALESCE(sum(fs.sales_amount)/NULLIF(count(DISTINCT fs.transaction_id),0),0) …` | currency IDR |

**The binding is visible in the response, not inferred.** `POST /api/metrics/test`
returns the rendered SQL, and every window reference came back as a placeholder:

```
"rendered_sql":"SELECT COALESCE(sum(fs.sales_amount),0) AS value FROM fact_sales fs
 JOIN dim_date d ON d.date_id = fs.date_id WHERE d.full_date >= $1 AND d.full_date < $2"
```

**Twice is the same number, and it is the right number.** A fixture metric
pinned to December 2024 (the window tokens kept, in a `{{from}} <= {{to}}`
comparison, so the registry's own rule still applies) returned
`3863405700` on two consecutive calls, against
`select sum(sales_amount) … where full_date between '2024-12-01' and '2024-12-31'`
= **3863405700.00** in psql. That is `C-1`'s figure, reached through the
registry rather than through the model.

**Nine refusals, each naming its reason** (`POST /api/metrics`, status in
brackets):

| attempt | answer |
| ------- | ------ |
| `UPDATE fact_sales SET …` | [400] the template must be a SELECT (or a WITH … SELECT); it starts with something else |
| `SELECT 1 …; DROP TABLE fact_sales` | [400] the template must be a single statement — remove the extra ";" |
| no `{{from}}`/`{{to}}` | [400] the template must reference the window with both {{from}} and {{to}} |
| `… AND 1={{company_id}}` | **[500]** render metric: unknown template parameter {{company_id}} — only {{from}} and {{to}} are allowed |
| `value_column` not selected | [400] the result has no column "value" — value_column must name a selected column |
| `SELECT 'abc' AS value` | [400] column "value" is not a number (not numeric) |
| a template matching 2 rows | [400] the metric returned 2 rows, not one — a metric must aggregate to a single row |
| duplicate `key` | [409] a metric with key "revenue" already exists |
| currency metric, no currency | [400] a currency metric needs a currency code |

**RBAC held.** Member: `GET /api/metrics` 200, `POST /api/metrics` 403
`admin only`, `POST /api/metrics/test` 403.

**Cascade:** deleting a metric took its watcher with it — `GET /api/watchers/:id`
404 and zero rows — which is `T-08`'s acceptance item proven from this end.

#### What the gate found

- **An unknown template token answers 500, not 400.** `ValidateTemplate` checks
  that `{{from}}` and `{{to}}` are *present* and never that anything else is
  absent, so `{{company_id}}` survives save-time validation and fails inside
  `evaluate` at `metric.Render`. That error is wrapped as `render metric: …`
  rather than `domain.ErrInvalidInput`, so `metricFail` falls through to its
  default arm. The text is perfect; the status says the server broke when the
  admin's template did. One line in `ValidateTemplate` (reject any token that is
  not `from`/`to`) puts it with the other nine.
- **`SELECT sum(x) … WHERE d >= {{from}} AND d < {{to}}` cannot be saved on a
  warehouse whose last seven days are empty.** Validation runs
  `metric.ValidationWindow` — trailing 7 days — and `SUM` over no rows is NULL,
  which `toFloat` refuses as "value is null". That refusal is right at query
  time and is exactly `T-16`'s distinction ("no rows matched" ≠ "the sum is
  zero"); at *save* time it rejects a correct definition for the state of the
  last week's data. It is the most natural revenue metric anyone will write, the
  message points at the column's type rather than at the empty window, and
  `COALESCE` is not discoverable from it. Every metric in this gate needed the
  workaround. Worth either validating over a window with data (the metric's own
  grain, stepped back until a row appears) or saying "the validation window
  2026-07-26 → 2026-08-02 matched no rows; wrap the aggregate in COALESCE if
  that is expected".

---

## T-07 · `list_metrics` + `query_metric`

### 1. What ships

`internal/tools/metric_tools.go` — two `interfaces.Tool`s over a narrow
`MetricStore` (satisfied by `app.MetricService`, declared in `tools` to avoid an
`app`→`tools` cycle):

- **`list_metrics`** — key, label, description, unit, grain per enabled metric.
- **`query_metric`** — `metric_key`, `from`, `to` (YYYY-MM-DD), optional
  `compare_to` (`previous_period` | `same_period_last_year`); returns the value,
  the window, and — when compared — the comparison value, delta and delta pct.
  An unknown key returns a helpful result listing the available keys rather than
  failing the turn. Metered as a `sql_query` event (twice when compared).

Both register unconditionally in `tools.Registry` (nil store → "not configured"),
so they appear in the agent allowlist and the template vocabulary on the API's
name-only build; the worker passes a real store. The catalog of enabled metrics
is injected into every turn by `ChatRunner.withMetricsContext`, beside the source
catalog, and `bootstrap.SystemPrompt` now ranks "prefer a defined metric over
run_sql" as guideline 5, above the run_sql mechanics.

### 2. Verified, and what is not

**Verified:** the service-level tool behaviour (bind/read, single-row, numeric,
coercion, delta, unknown-key) via `metric_service_test.go`; the tools compile and
register; the turn injection and prompt change build and pass the existing
composition tests.

### 3. The live gate — run 2026-08-02

Four real turns on the gate tenant, `deepseek/deepseek-v3.2` through the worker.

| asked | tools called | answer |
| ----- | ------------ | ------ |
| "What was our revenue in December 2024?" | `query_metric` (rows=1) | **Rp 3.735.587.550** — psql for the same window: 3735587550.00 |
| "Compare December 2024 against November 2024" | `query_metric` ×2 | Nov 3.526.126.650 → Dec 3.735.587.550, +209.460.900, +5.94% |
| "…using its built-in compare_to previous_period" | `query_metric` (rows=2, `compare_to` set) | Dec 3.735.587.550 vs previous 3.708.552.300, +0.73% — psql for Nov 1 → Dec 1: 3708552300.00 |
| "Which payment method was used most often in December 2024?" | `get_schema`, `run_sql` (rows=5) | answered from SQL — no metric covers it |

So: a metric question goes to the metric, a metric question with a comparison
uses the tool's own comparison when asked for it, and a question no metric
covers still reaches `run_sql`. An unknown key — "what was our gross_margin
metric for December 2024?" — came back with the available keys listed
(`aov`, `order_count`, `revenue`, `cnt_empty`) and a suggestion, rather than a
failed turn.

**The first run of this gate did not produce any of that**, and what it found is
below.

#### What the gate found: every metric-only answer was suppressed as a fabrication

Asked the first question, the agent called `query_metric`, got the figure, and
the user was shown:

> I wasn't able to complete the query for this, so I don't have a figure to give
> you — my query returned no data. I won't quote a number that didn't come from
> your data.

`agent_actions` for that turn:

```
 query_metric | ok      | rows_returned = NULL
 final_answer | blocked | reply stated a figure no tool returned this turn
```

The mechanism, end to end. `guardrails.CheckFabrication` grounds a reply on
`TurnEvidence.DataRows > 0`. `DataRows` is fed by `agentbudget.Tracker.Observe`,
which reads a **`row_count`** key off the tool's own JSON result — `run_sql`
emits one, `query_metric` never did. `query_metric` *was* in `dataTools` (the
comment there says "query_metric joins the list when T-07 lands"), so the tool
was half-wired: counted as a data call, incapable of contributing evidence. The
same key feeds `T-05`'s audit decorator, which is why `rows_returned` was NULL
on a call that succeeded.

The consequence is the registry's whole purpose inverted: the one number in this
system that is validated, stored and re-checked was the one number the agent was
not allowed to say — and the replacement text asserts something untrue ("my
query returned no data") about a query that returned 3,735,587,550. Every
`query_metric`-only turn on every channel was affected, including the briefing
`T-08` sends unprompted.

**Fixed the same day**: the payload carries `row_count` — 1 for a window, 2 when
a comparison ran, on the same reasoning that meters it twice. One evaluation is
exactly one row by construction, because the registry refuses to save a template
that returns any other number and treats a null as an error. Regression tests in
`internal/tools/metric_tools_test.go` cover both the payload and the end it was
felt at (a tracker that stays ungrounded); both fail against the old code. The
transcripts above are the re-run.

**Outstanding — the eval-set half.** The golden set (`testdata/eval/golden.yaml`)
has no metric cases and `internal/eval/tenant.go` seeds no metrics, so the
before/after pass rate and the token delta the ticket asks for need cases
authored and the eval tenant taught to define them first. The behavioural
claims above are proven; the *scored* claim is not.
