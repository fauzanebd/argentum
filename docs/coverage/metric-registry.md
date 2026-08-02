# The metric registry — T-06, T-07 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 2 — Authoritative
numbers*. The accuracy foundation the "it tells you first" half of the product
rests on: a number defined once, so the same question returns the same answer in
two threads, and so a watcher (T-08) fires on a value that was validated rather
than one the LLM re-derived.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-06` | Metric registry: schema, validation, CRUD, dashboard tab | 3d | **gated live 2026-08-02** |
| `T-07` | `list_metrics` + `query_metric` tools | 1.5d | **gated live and scored 2026-08-02** — the eval found a language regression the registry causes, open (§5) |

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

### 4. The eval gate — run 2026-08-02

Five cases in a new `metric_registry` category, and `eval.ensureMetrics`, which
brings the eval tenant's registry to the state a run asks for: the three metrics
when `-metrics` is true (the default), **none of them** when it is false. The
removing half is what makes the comparison honest — the eval tenant is reused
across runs by design, so without it "with metrics" and "without" would be the
same run twice.

Same five questions, same model, same tenant, twenty minutes apart:

|  | before (`-metrics=false`) | after |
| --- | --- | --- |
| passed | **1/5** | **5/5** |
| mean input tokens | **12,711** | **3,296** (−74%) |
| mean output tokens | 778 | 473 |
| cost for the five | $0.0219 | $0.0122 |

Per case, and the tool calls are the story:

| case | input tokens | latency | tools after |
| ---- | ------------ | ------- | ----------- |
| `metric-revenue-december` | 8,880 → 7,637 (−14%) | 42.3s → 11.5s | `query_metric` |
| `metric-order-count-december` | 5,254 → 1,160 (−78%) | 39.0s → 10.6s | `list_metrics`, `query_metric` |
| `metric-aov-december` | 2,802 → 1,333 (−52%) | 76.0s → 13.0s | `query_metric` |
| `metric-comparison-december-vs-november` | 30,053 → 1,707 (−94%) | 85.3s → 14.9s | `query_metric` ×2 |
| `metric-uncovered-question-falls-back` | 16,565 → 4,641 (−72%) | 31.7s → 23.3s | `get_schema`, `run_sql` |

The comparison case is the one worth reading twice. Without the registry the
agent spent **30,053** input tokens walking the schema twice and writing two
joins; with it, two `query_metric` calls and 1,707. That is the ticket's
"should reduce mean input tokens measurably" arriving as a factor of eighteen on
the question a business actually asks every month.

The fifth case is the guard against over-correction: no metric covers payment
methods, and the agent still reached `run_sql` and answered *Bank Transfer* —
in **both** runs. It is the one case that passed before, and it is why the
category is not simply "does it call `query_metric`".

**What the before run also showed**, and it is worth keeping: with the registry
empty the agent called `list_metrics` first, said *"There are no defined metrics
available, so I'll need to query the retail sales database directly"*, and did.
The fallback is not a silent one.

### 5. The full set, and the regression the gate exists to catch

The ticket's other half — *the eval suite must not regress* — is where this run
stops being good news. **40 cases with metrics defined: 17 passed (42.5%)**
against a 97.0% baseline. The 23 failures are three unrelated things, and
separating them was most of the work:

| cause | count | verdict |
| ----- | ----- | ------- |
| `must_call: [run_sql]` where `query_metric` answered | 10 | the set was wrong, not the agent |
| an English question answered in Indonesian | 11 | a real regression, cause not yet found |
| a chart case with no Metabase running | 1 | environment, not code |
| `id-jumlah-transaksi` stated no figure | 1 | one-off |

**The ten tool-choice failures were the golden set encoding a fact that stopped
being true.** `must_call: [run_sql]` on *"what were our total sales?"* was
written when `run_sql` was the only tool that could answer it; the assertion's
intent was always "the agent went and got the number rather than inventing it".
With a registry defined, `query_metric` is the *better* answer and the case
failed it. `Expect.MustCallAny` is the fix — at least one of the named tools —
and the ten cases now name both. Re-run: **8 of 10 pass**, and the two that do
not fail on language alone.

That leaves the merged set at **25/40 (62.5%)**, with 13 language failures,
one chart and one number.

#### The finding: defining metrics makes the agent answer in the wrong language

Eleven English cases came back in Indonesian. The baseline had none, so the
first question is whether the registry caused it or six days of model drift did.
Eight of them re-run with `-metrics=false`:

| | with metrics | without |
| --- | --- | --- |
| `total-profit-all-time`, `total-units-sold`, `top-sales-channel`, `sales-by-category`, `people-total-salary`, `guardrail-false-positive-margin` | Indonesian ✗ | English ✓ |
| `no-data-marketing-spend`, `guardrail-off-topic-recipe` | Indonesian ✗ | Indonesian ✗ |

So six of eight are **caused by defining metrics**, and two are a smaller
pre-existing problem that owes nothing to this ticket.

**A hypothesis, tested and refuted — recorded because the next person will have
the same one.** `withMetricsContext` prepends its `[System context: …]` block to
the *user message*, so the caller's sentence ends up buried under our
scaffolding; the obvious reading is that "reply in the user's language" degrades
when the message is mostly not the user's. `T-A2b` fixed exactly that shape by
moving the report directive into a per-turn system-prompt addendum, so the same
move was made here and the six cases re-run against it.

**It fixed three and left three.** At that sample size, with an LLM at
temperature 0.2, that is indistinguishable from noise — the delivery position is
not the mechanism. The change was reverted rather than shipped: a prompt-delivery
edit with no measured benefit is precisely what this harness exists to prevent,
and keeping it would have meant carrying a comment that claims a result the data
does not support.

#### What actually fixed it: the rule had to sit next to the question

Dumping the composed message settled it. The turn the model receives is
**entirely English** — about 1,500 characters of `[System context: …]` blocks
(tables, metrics, sources, currency, organization) and then the user's sentence
last. There is no Indonesian anywhere in it.

So this was never language *detection*. The model was **defaulting** to
Indonesian — the exact failure guideline 1 already names ("never default to
Indonesian when the user wrote in English") — and the metric catalog widened the
gap between that rule and the question until the rule stopped holding. Which is
also why moving the catalog changed nothing: it was still the same distance.

`withLanguageReminder` restates the rule as the **last** block before the user's
own words, ~70 characters on every turn, naming both directions. Measured on
thirteen cases:

| | before the reminder | after |
| --- | --- | --- |
| the six registry-caused English failures | 0/6 | **6/6** |
| the two failures the registry did not cause | 0/2 | 1/2 |
| the five Indonesian cases | — | **no language failures** |

The Indonesian side is the half that mattered most: a fix that dragged
Indonesian answers into English would have been worse than the bug. Three of the
five `indonesian` cases pass outright and the other two fail on a number and a
tool assertion — **not one** replies in the wrong language.

`guardrail-off-topic-recipe` still answers a refusal in Indonesian. It failed
with metrics and without them, so it is not this ticket's, and it is a refusal
rather than an answer — worth a look under `T-07b` with the other guardrail
wording.

#### A second thing the Indonesian side showed — once

On one run `id-total-penjualan` (sales across all time) found the `revenue`
metric, worked out that it could not use it — *"metrik ini memiliki grain 'per
month' dan memerlukan rentang tanggal"* — and **stopped with no figure** instead
of falling back to `run_sql`. That is the registry becoming a wall, which is the
failure the fifth `metric_registry` case exists to guard against.

**It did not reproduce.** The same case passes in the final run, so this is an
observed behaviour rather than a standing defect, and it is recorded at that
strength deliberately: one occurrence of a plausible failure is worth writing
down and is not worth changing a prompt over. What would settle it is a golden
case that asks an unbounded question against a windowed metric every run — the
question is cheap and the answer is currently a coin toss nobody is watching.

### 6. The set after all of it

**40/40, 100%** — `deepseek/deepseek-v3.2`, mean 5,385 input tokens, $0.115 for
the run. Every category clean, including `ambiguous-headcount`, which has been
the one standing failure since `T-16` recorded it in July.

| | cases | pass |
| --- | --- | --- |
| `T-16` baseline, 2026-07-27 | 33 | 97.0% |
| this run, 2026-08-02 | 40 | **100%** |

Not a like-for-like comparison — the set grew by five `metric_registry` cases,
ten assertions moved from `must_call` to `must_call_any`, and one prompt line
was added — and all three of those changes are in this file with the measurement
that motivated them.
