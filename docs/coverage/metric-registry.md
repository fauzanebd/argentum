# The metric registry — T-06, T-07 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 2 — Authoritative
numbers*. The accuracy foundation the "it tells you first" half of the product
rests on: a number defined once, so the same question returns the same answer in
two threads, and so a watcher (T-08) fires on a value that was validated rather
than one the LLM re-derived.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-06` | Metric registry: schema, validation, CRUD, dashboard tab | 3d | **code complete + unit-tested — live gate outstanding** |
| `T-07` | `list_metrics` + `query_metric` tools | 1.5d | **code complete + unit-tested — eval gate outstanding** |

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

**Outstanding — the live gate:** define three demo-tenant metrics (revenue,
order count, AOV), paste each validated value, and run one injection payload in
the window param to show the refusal. Needs a running stack with the demo
warehouse.

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

**Outstanding — the eval gate:** an eval run with metric-specific cases showing
"revenue last month" calls `query_metric` not `run_sql`, a no-metric question
still works, and the before/after pass rate **and** token delta (this should
reduce mean input tokens). Needs a running LLM and the demo tenant.
