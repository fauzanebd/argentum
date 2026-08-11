# Native dashboards roadmap — replacing Metabase with a live query layer

Written 2026-08-11 against `1115e90`. Sixteen tickets, ~17 days of backend work,
five tracks. Ticket ids are `T-D1` → `T-D16`; `D` is unused elsewhere and does
not collide with the `T-H…` hardening tickets in
`03-security-hardening-roadmap.md` or the `S-n` finding codes.

> # ⚠ Status: substantially superseded — re-plan before starting
>
> **Re-verified 2026-08-11 against `main` @ `cc06dc7`.** This roadmap was written
> against `1115e90`, a commit that is **not an ancestor of `main`** — it was a
> pre-monorepo branch whose work reached `main` by another route. Worse, the
> working tree it was written from was in a broken intermediate state:
> `apps/dashboard/` held only `dist/` and `node_modules/`. The roadmap's scope
> note concluded "the dashboard frontend source is not in this checkout at all"
> and sized Track E around that. **That observation was an artifact of the broken
> checkout, not a fact about the repo.** `apps/dashboard/src/` has 105 source
> files on `main`.
>
> Re-verification found **nine of the thirteen "❌ Absent" capabilities are now
> shipped**, including the Track A foundation ticket. The design thinking below
> is still worth keeping; its premises are not. Read
> [What changed](#what-changed-since-this-was-written) first, then decide what to
> re-plan. Do not estimate off the ticket days as written.
>
> **Path convention.** Every path outside quotation marks has been moved to its
> post-monorepo location under `apps/backend/`. Paths *inside* `"…"` are verbatim
> quotations of the original draft and are deliberately left at their old
> pre-monorepo spelling, so the correction is legible as a correction.

Metabase is not the analytics engine here. It is a chart renderer, a dashboard
host and a link server bolted onto a product that already owns the hard parts:
schema introspection, read-only execution against three dialects, tenant
connection pooling, and an agent that writes the SQL. What we are removing is the
last mile.

| Claim we want to make | State today |
| --------------------- | ----------- |
| A dashboard shows current numbers | **False.** A Metabase card is created once and re-executed by Metabase on its own connection; Argentum has no idea when or whether it ran. |
| A dashboard link can be revoked or expired | **False for Metabase dashboards.** `GetPublicDashboardURL` mints a Metabase public link with no expiry, no revocation and no viewer identity. Note that *report* shares now have exactly this machinery — see `T-D13`. |
| We can answer "who read this customer's warehouse" | **False for Metabase.** Metabase queries never touch our audit path. An audit table does exist for tool calls (`023_agent_actions`); Metabase simply does not write to it. |
| Charts match the product's look | **False.** They are Metabase's chrome inside an anchor tag that opens a new tab. |
| A saved query is bounded | **False for saved charts.** `run_sql` caps rows; a Metabase card does not, because Metabase runs it. |
| Tenant DSNs live in one place | **False.** Every registered DSN is mirrored into Metabase via `UpsertWarehouse`. |

---

## What changed since this was written

Verified file by file against `main` @ `cc06dc7`. This table replaces the
"What is actually here to build on" section of the original draft.

| Capability | Original claim | Verified today |
| --- | --- | --- |
| **Parameterised query execution** | ❌ Absent — "`Conn` exposes only `ExecuteReadOnly`; no args variant and `Dialect` has no `Placeholder`" | ✅ **Shipped.** `ExecuteReadOnlyParams` is on the `Conn` interface (`apps/backend/internal/adapters/db/driver.go:42`) and implemented in all three dialects; `Dialect.Placeholder(n int)` is at `driver.go:80`. Landed under T-06/T-07 for the metric registry. **`T-D1` is done.** |
| **Metric / semantic layer** | ❌ Absent — "no `internal/metric`, no `metric_definitions`" | ✅ **Shipped.** `apps/backend/migrations/control/039_metric_definitions.{up,down}.sql` and `apps/backend/internal/app/metric_service.go` (409 lines). **This voids the reasoning behind decision 2** — see below. |
| **Chart rendering of any kind** | ❌ Absent — "No chart package anywhere" | ✅ **Shipped.** `apps/backend/internal/report/chart/` (7 files: `chart.go`, `draw.go`, `labels.go`, `normalize.go`, `theme.go`, `empty.go`, tests), plus `report/pdf/chart.go` and `report/pptx/chart.go`. |
| **Share tokens, public share pages** | ❌ Absent — "`internal/auth/` is `jwt.go` + `password.go`" | ✅ **Shipped for reports.** `apps/backend/internal/auth/sharetoken.go`, `apps/backend/migrations/control/050_report_shares.{up,down}.sql`, and `handlers.NewReportShareHandler` registered at `apps/backend/cmd/api/router.go:71`. `apps/backend/internal/auth/` also now holds `apikey.go`, `embedkey.go`, `invite.go`. |
| **Audit log** | ❌ Absent — "No `agent_actions` table, no audit package" | ✅ **Shipped.** `apps/backend/migrations/control/023_agent_actions` and `026_agent_actions_request_id`; `apps/backend/internal/tools/audit.go`; `handlers.NewAuditHandler` at `apps/backend/cmd/api/router.go:72`. |
| **Declarative route RBAC** | ❌ Absent — "No `cmd/api/policy.go`" | ✅ **Shipped, and now load-bearing.** `apps/backend/cmd/api/policy.go` + `policy_test.go`, `apps/backend/internal/transport/http/middleware/rolepolicy.go`. **Unlisted routes are denied** (`rolepolicy.go:34-37`). `T-D10` must add its routes to the policy or they will 403. |
| **Tool registry** | ❌ Absent — "tools wired inline in `cmd/worker/main.go`" | ✅ **Shipped.** `apps/backend/internal/tools/registry.go`. `T-D12`'s "there is no registry file to edit" is wrong. |
| **The dashboard frontend** | ❌ "not in this repo — no `.tsx` file exists anywhere in this checkout" | ✅ **Present.** 105 files under `apps/dashboard/src/`. The observation was an artifact of a broken working tree. |
| `apps/backend/internal/cache/` | ❌ Dead code | ✅ **Still dead — claim holds.** No Go file imports `github.com/fauzanebd/argentum/internal/cache`; the only mention is a comment at `apps/backend/internal/app/credits.go:86`. `InvalidateSQLCache` is still a no-op (`apps/backend/internal/cache/redis.go:294`) and `InferQueryType` still string-matches years (`redis.go:126`, not `:124`). Still delete it. |
| Read-only execution, 3 dialects, timeout | ✅ Shipped | ✅ Confirmed — `apps/backend/internal/adapters/db/driver.go:28-47` |
| Tenant connection pool, DSN-rotation detection | ✅ Shipped | ✅ Confirmed — resolver returns a `version` token (`pool.go:23`), pool compares on every hit (`pool.go:92`) |
| Schema introspection | ✅ Shipped | ✅ Confirmed — `driver.go:47` (`ExtractSchema`) |
| Source resolution by company | ✅ Shipped | ✅ Confirmed — `apps/backend/internal/tools/source_resolve.go` |
| Redis, asynq queue, cron scheduler | ✅ Shipped | ✅ Confirmed — `apps/backend/internal/queue/` |
| Event bus for pushing to clients | ✅ Shipped | ✅ Confirmed — `apps/backend/internal/transport/eventbus/redis.go` |
| Document renderer, no charts | 🟡 Partial | 🟡 The `types.go:6` comment still reads "Adding charts later means extending Section/Sheet types", but `apps/backend/internal/report/chart/` now exists independently. Reconcile before quoting this. |
| **Statement validation** | ❌ Absent | ❌ **Confirmed absent.** No `ValidateStatement`, no `sqlguard` anywhere. `T-D2` is still net-new. |
| `apps/backend/internal/dashboard/` package | ❌ Absent | ❌ **Confirmed absent** — 0 files. Tracks A–D's new packages are still net-new. |

### Consequences for the plan

1. **`T-D1` (1.5d, "nothing else can start without it") is already done.** Bound
   parameters exist across all three dialects with the exact signature this
   roadmap proposed. Track A shrinks to `T-D2` + `T-D3`.
2. **Decision 2 rests on a false premise.** The original text: *"There is no
   metric registry in this repo to route panels through — `internal/domain/` has
   no `metric.go` and there is no `metric_definitions` table — so 'metrics only'
   would have meant building a semantic layer first."* There is now a metric
   registry. Whether panels should route through it is an open design question
   again, and it is the single most consequential thing to re-decide.
3. **`T-D9`'s premise is wrong.** "There is no audit table in this repo, so one
   is created rather than reused." There is one. Decide whether
   `dashboard_query_log` is a new table or rows in `agent_actions` — the original
   argument for a separate table (it logs warehouse reads, not tool calls) may
   still win, but it has to be made against a table that exists.
4. **`T-D13` should reuse, not invent.** "Token minting is new — `internal/auth/`
   has no share-token file." It has one, with the SHA-256-of-random-bytes design
   this ticket specifies, already in production for report shares.
5. **`T-D10` will 403 without a policy entry.** "There is no declarative role
   policy in this repo to update" is now false and is a build-breaker, not a
   nicety.
6. **Track E is not backend-only.** The dashboard frontend is in this repo and
   the UI work can be estimated normally.

---

## The three decisions this roadmap was built on

Taken 2026-08-11, **before** re-verification. Decision 2 needs retaking.

| Decision | Consequence for this plan |
| --- | --- |
| **Existing Metabase dashboards are abandoned, not converted.** | No converter, no dual-write. `saved_dashboards` rows survive read-only through the deprecation window and are dropped in `T-D16`. *Unaffected by re-verification.* |
| **Panels may carry agent-written SQL**, not only registry metrics. | ⚠ **Retake this.** The justification was that no metric registry existed. One does now (`039_metric_definitions`, `apps/backend/internal/app/metric_service.go`). Governance is still execution control — bound parameters, read-only transaction, row caps, statement timeout, an audit row per warehouse read — but "route panels through the registry" is a live option again. |
| **Embedding is inside Argentum's own app only.** | No cross-origin iframe story, no per-share origin allowlist, no Go-served HTML shell. Shares get `frame-ancestors 'none'` and that is the whole frame policy. *Unaffected — though note an embed surface has since shipped (`apps/backend/internal/auth/embedkey.go`), so confirm this is still the intent.* |

---

## Migration numbering

⚠ **All four numbers this roadmap claimed are taken.** The original text said
"the last applied migration is `023_thread_slack`" — that was true on `1115e90`,
where the Slack migrations were `021`–`023`. On `main` those same migrations are
`047`–`049`, and the highest is **`055_query_examples`**.

| Original | Name | Ticket | Now occupied by |
| --- | --- | --- | --- |
| 024 | `dashboards` | `T-D5` | taken |
| 025 | `dashboard_query_log` | `T-D9` | taken |
| 026 | `dashboard_shares` | `T-D13` | `026_agent_actions_request_id` |
| 027 | `drop_metabase_columns` | `T-D16` | taken |

**Renumber to 056–059** when these tickets are written, and re-check with
`make migration-next` at the time — the number moves. Each carries a 20–40 line
header comment stating the decision and the alternative rejected, matching
`003_metering.up.sql` and `013_drop_table_embeddings_ivfflat`.

---

## Track A — The query engine (3d → ~1.5d) · nothing else can start without it

### ~~`T-D1` Bound parameters across all three dialects — 1.5d~~ ✅ SHIPPED

Delivered under T-06/T-07. `apps/backend/internal/adapters/db/driver.go:42`:

```go
// ExecuteReadOnlyParams is ExecuteReadOnly with bound query parameters. It
// exists for the metric registry (T-06/T-07): a metric's window bounds are
// passed as args and referenced in the SQL through the dialect's placeholder
// syntax, so a `'; DROP …` in a window value is data the driver escapes
// rather than SQL it runs.
ExecuteReadOnlyParams(ctx context.Context, sql string, args []any, maxRows int) (*QueryResult, error)
```

`Dialect.Placeholder(n int) string` is at `driver.go:80`. `ExecuteReadOnly`
remained as the no-args path, exactly as this ticket proposed. Nothing to do.

**The SQL Server caveat recorded in `03-security-hardening-roadmap.md` still
stands:** that driver has no read-only transaction option
(`apps/backend/internal/adapters/db/sqlserver/conn.go:33-35`). `T-D2` is what
stands in front of it.

### `T-D2` Statement validation — 0.5d · still net-new

`apps/backend/internal/dashboard/sqlguard.go`. One exported function:

```go
// ValidateStatement accepts a single SELECT (or WITH … SELECT) and refuses
// everything else. Comments and string literals are scrubbed before the scan,
// so `status = 'deleted'` and a column named `update_count` are not false
// positives.
func ValidateStatement(sql string) error
```

Refuses: a second statement after a semicolon, any mutating keyword at statement
position, and anything that is not a SELECT/WITH at the top. This is the only
thing standing between an agent-written panel and SQL Server's missing read-only
transaction, so it runs at **save** and again at **resolve** — the stored spec is
not trusted just because it was validated once.

> Coordinate with `T-H4` in the hardening roadmap, which proposes a real parser
> (`pg_query_go` / `vitess` sqlparser) for the same job. Two validators would
> drift. Decide whether this is the lexer stopgap `T-H4` replaces, or whether
> `T-H4` should land first and this ticket become a call into it.

**Test.** Table-driven, both directions. A `WITH` CTE passes; `SELECT 1; DROP
TABLE t` fails; `SELECT '; DROP TABLE t'` passes.

### `T-D3` The parameter template binder — 1d · still net-new

`apps/backend/internal/dashboard/template.go`.

```go
// Render replaces each {{name}} in the template with one dialect placeholder
// and appends its value to args, left to right.
//
// A token outside `declared` is an error, not an empty string. This matters
// more than the binding does: a template that may reference an undeclared
// token may reference one that resolves to nothing, and `WHERE tenant = `
// is valid SQL that returns the whole table.
func Render(template string, placeholder func(n int) string,
            values map[string]any, declared []string) (string, []any, error)
```

Filter kinds and coercion:

| Kind | Request form | Coerced to |
| --- | --- | --- |
| `date_range` | preset name, or `from`/`to` as `YYYY-MM-DD` | two `time.Time` in the dashboard's zone |
| `date` | `YYYY-MM-DD` | `time.Time` |
| `enum` | string | string |
| `number` | decimal string | `float64` |
| `bool` | `true`/`false` | `bool` |

Presets: `last_7d`, `last_30d`, `mtd`, `qtd`, `ytd`, `last_month`, resolved
server-side at request time. **A default is stored as a preset name, never as two
timestamps** — that is the entire difference between a live dashboard and a
snapshot.

An `enum` value is deliberately **not** checked against its option list. Options
are a UX affordance; the security boundary is the binding shipped in `T-D1`. A
value outside the set returns no rows, which is the correct outcome. Adding a
check would suggest the check is what makes it safe, and the day someone adds a
filter kind without one, that belief is what breaks.

`_ "time/tzdata"` must be imported wherever presets resolve — the deployed images
carry no zoneinfo. **Check `metric_service.go` first**: the metric registry
already resolves windows and may have solved this.

---

## Track B — The dashboard model (5d)

### `T-D4` The spec package — 1.5d

`apps/backend/internal/dashboard/spec/`. A panel stores a **question and a column
mapping**, not values.

```go
type Dashboard struct {
    SpecVersion int      `json:"spec_version"` // 1
    Title       string   `json:"title"`
    SourceID    string   `json:"source_id"`    // default every panel inherits
    Filters     []Filter `json:"filters,omitempty"`
    Panels      []Panel  `json:"panels"`
    RefreshSecs int      `json:"refresh_secs,omitempty"`
    TimeZone    string   `json:"timezone,omitempty"`
}

type Panel struct {
    ID     string  `json:"id"`     // stable across edits; the cache and the grid key on it
    Title  string  `json:"title,omitempty"`
    Viz    string  `json:"viz"`    // line|bar|grouped_bar|stacked_bar|pie|donut|kpi|table
    Layout Layout  `json:"layout"` // 12-column grid, integer units
    SQL    string  `json:"sql"`    // single SELECT, {{tokens}} bound at run time
    Map    Mapping `json:"map"`
    Fmt    string  `json:"fmt,omitempty"` // text|number|currency|percent|date
}

// Mapping names the columns a panel reads. Named, never positional: SELECT *
// reorders the day the tenant adds a column, and a chart whose series silently
// became a different column draws without complaint and cannot be seen to be
// wrong.
type Mapping struct {
    Label      string   `json:"label,omitempty"`       // x-axis / category column
    Series     []string `json:"series,omitempty"`      // wide form: month, revenue, cost
    SeriesBy   string   `json:"series_by,omitempty"`   // long form: month, channel, revenue
    Value      string   `json:"value,omitempty"`
    DeltaValue string   `json:"delta_value,omitempty"` // kpi comparison column
}
```

`Project(panel, *db.QueryResult) (*Resolved, error)` turns a result set into what
the browser draws. It refuses a spec that sets both `Series` and `SeriesBy`
rather than picking one silently, and a `Map` naming a column the result lacks
returns **the column names that would have worked** — the same repair-instruction
shape `run_sql` uses when a query fails.

Series after the long-form pivot are capped at 8. Beyond that the panel draws
eight and sets `SeriesTruncated`, which is a different fact from `Truncated`
("there is more data") and reads differently to a person.

⚠ The original text closed: *"Because there is no chart renderer in this repo,
`Resolved` is JSON for a browser only."* There **is** a chart renderer
(`apps/backend/internal/report/chart/`). Whether `Resolved` should also feed it —
giving server-side chart images and dashboard-to-PDF for free — is now a live
option and is re-opened in [Out of scope](#out-of-scope).

### `T-D5` Migration `dashboards` — 0.5d

*(Renumber — `024` is taken. See [Migration numbering](#migration-numbering).)*

```sql
CREATE TABLE IF NOT EXISTS dashboards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id    UUID REFERENCES conversation_threads(id) ON DELETE SET NULL,
    source_id    UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    spec         JSONB NOT NULL,
    spec_version INTEGER NOT NULL DEFAULT 1,
    refresh_secs INTEGER,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dashboards_company ON dashboards(company_id, created_at DESC);
CREATE INDEX idx_dashboards_thread  ON dashboards(company_id, thread_id) WHERE thread_id IS NOT NULL;
```

Three things the header comment must argue:

1. **One JSONB column, not five tables.** A dashboard is authored as a unit by
   one tool call, carries a `spec_version`, and normalising costs a migration per
   spec field and a three-way join on the hot read to buy a query nobody asked
   for.
2. **A new table, not columns on `saved_dashboards`.** `006`'s row is a pointer
   at a Metabase object. Its `thread_id` is `NOT NULL` and cascades — which would
   delete a dashboard an executive opens every Monday because somebody tidied the
   chat thread that created it. Here `thread_id` is nullable provenance and does
   not cascade.
3. **`source_id` is `ON DELETE RESTRICT`.** Deleting a connection a dashboard
   reads should fail loudly at the delete, not silently empty the dashboard.

### `T-D6` Domain, repository, service — 1.5d

Rewrite `apps/backend/internal/domain/dashboard.go` and
`apps/backend/internal/adapters/postgres/dashboard_repo.go`.

**Fix a live tenancy defect while doing it — re-verified, still present.**
`DashboardRepository.GetByID(ctx, id)` takes no company id
(`apps/backend/internal/domain/dashboard.go:23`) and the service compares
ownership in Go afterwards. Every method on the new repository takes `companyID`
beside the id, so the isolation is in the `WHERE` clause and not in a comparison
somebody can forget.

On save, every distinct `source_id` in the spec is intersected with the company's
own connections. A stored dashboard must not be a latent cross-tenant read
waiting for a resolver bug.

Save-time validation — **refuse on structure, warn on execution**:

| Failure | Outcome |
| --- | --- |
| `ValidateStatement` fails | Refuse the whole save, name the panel |
| A `{{token}}` no filter declares, or a filter no panel binds | Refuse, name the token |
| `Map` names a column the result lacks | Refuse that panel, save the rest, return the columns that would have worked |
| Execution times out or errors | **Save with a warning** on that panel |

The asymmetry is deliberate: a dashboard is a dozen statements an agent wrote in
a turn that is about to end, and losing eleven good panels because one hit a cold
cache is the worse failure.

### `T-D7` The resolver — 1.5d

`apps/backend/internal/dashboard/resolve.go`. Loads the row company-scoped,
coerces params against the declared filters, then fans panels out.

| Cap | Value | Reason |
| --- | --- | --- |
| `maxRows` chart | 2 000 | `run_sql`'s cap is 100, tuned for LLM context — far too small for a chart |
| `maxRows` table | 500 | Past this a browser table wants pagination, which v1 does not have |
| `maxRows` KPI | 2 | Enough to tell "exactly one row" from "more than one" |
| series after pivot | 8 | Beyond eight, a categorical palette stops being distinguishable |
| per-panel deadline | 15s, `DASHBOARD_PANEL_TIMEOUT` | So a browser waiting on twelve panels has a bounded worst case |
| concurrent panels | 4 | Twelve simultaneous connections into a customer's production replica is a load pattern they did not agree to |

A panel that fails fills its own `error` field; the response is still `200` and
the other panels render. One timing out must not blank the eleven that answered.

**Filter options never get their own route.** They resolve inside the dashboard
response. A `GET /api/dashboards/:id/filters/:name/options` endpoint would be a
generic query executor reachable by anyone who can open the dashboard —
including, after `T-D13`, a stranger holding a share link.

---

## Track C — Caching and accountability (2d)

### `T-D8` Panel cache and request collapsing — 1d

```
dash:panel:1:<sha256( companyID ␟ sourceID ␟ connVersion ␟ dbType ␟ renderedSQL ␟ argsJSON ␟ maxRows )>
```

Keyed on **content, not on `(dashboard_id, panel_id)`**, so a definition edit and
a filter change both invalidate for free. There is no invalidation hook to write
and therefore no edit path that can forget to call it.

`connVersion` is the component that is easy to omit and expensive to get wrong: a
rotated DSN can point at an entirely different database, and a cache keyed only
on SQL would keep serving the old warehouse's numbers. The token already exists —
`ConnectionResolver.Resolve` returns it
(`apps/backend/internal/adapters/db/pool.go:23`) and the pool compares it on every
hit (`pool.go:92`) — it is simply not reachable by callers. Add:

```go
func (p *TenantConnPool) ForWithMeta(ctx context.Context, companyID, sourceID string) (Conn, ConnMeta, error)
```

`For` keeps its signature and delegates, so no existing caller changes.

TTL `DASHBOARD_PANEL_CACHE_TTL`, default 60s. Write a small Redis helper next to
the resolver; **do not** use `apps/backend/internal/cache/` — re-verified as still
dead code, imported by nothing.

**No stale-while-revalidate.** The failure that is real on day one is the
thundering herd: twenty people open the same dashboard at 09:00 and twelve panels
each run twenty times against a customer's warehouse. A per-process in-flight map
— `map[string]*call` behind a mutex, about forty lines — collapses that. Hand
this rather than adding `golang.org/x/sync`: the module does not depend on it
today and this is the only consumer.

**No per-process L1 cache.** A dashboard's whole value is that the number is
current; an in-process copy would let two API replicas show two different figures
for the same URL. Redis is the only layer.

### `T-D9` The query log — 1d

*(Renumber — `025` is taken.)*

⚠ **Premise corrected.** The original read "There is no audit table in this repo,
so one is created rather than reused." There is one — `023_agent_actions`, with
`apps/backend/internal/tools/audit.go` and an `AuditHandler` at
`apps/backend/cmd/api/router.go:72`. Decide
explicitly whether this is a new table or rows on the existing one. The argument
for separate (this records warehouse reads by share visitors and schedules, not
agent tool calls, and `actor_kind` has no analogue in `agent_actions`) is still a
good argument — but make it against the table that exists.

```sql
CREATE TABLE IF NOT EXISTS dashboard_query_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dashboard_id UUID,          -- no FK: the log outlives the dashboard
    panel_id     TEXT NOT NULL,
    source_id    UUID NOT NULL,
    actor_kind   TEXT NOT NULL, -- user|share|schedule
    actor_ref    TEXT NOT NULL DEFAULT '',
    sql_text     TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}'::jsonb,
    row_count    INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL, -- ok|error|truncated
    error        TEXT NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dashboard_query_log_company ON dashboard_query_log(company_id, created_at DESC);
```

**A row is written only on a cache miss**, and the header comment must make that
argument rather than leave it to be discovered: with `T-D8`'s singleflight and a
60s TTL, misses are bounded by `panels ÷ TTL` per dashboard per replica
*regardless of how many people are watching* — roughly 12 rows a minute in the
worst case and near zero in steady state. This is not a concession to volume. The
event worth recording is "the customer's warehouse was read", not "a browser
rendered a number it already had".

`dashboard_id` deliberately carries no foreign key, for the same reason the log
records `sql_text` inline: the question *"what ran against my database last
month"* is asked about deleted dashboards more often than about live ones.

Whatever table it lands in, give it a retention story — see `T-H6` in the
hardening roadmap, and reuse `apps/backend/internal/apiobs`'s prune shape.

Metering, on the same cache-miss rule: a new `dashboard_query` usage event priced
at the existing SQL-query cost. **Keep `UsageEventMetabaseCard` and
`UsageEventMetabaseDashboard` in `apps/backend/internal/domain/usage.go`
permanently**, with a comment saying nothing writes them any more and they exist
so historical rows still render a label. Delete `MetabaseCardCost` /
`MetabaseDashboardCost` from the pricing struct
(`apps/backend/internal/app/usage_service.go:21-22`, defaults at `:38-39`) once
the tools are gone.

---

## Track D — The agent surface and sharing (5d)

### `T-D10` HTTP routes — 0.5d

| Route | Notes |
| --- | --- |
| `GET /api/dashboards` | list |
| `GET /api/dashboards/:id` | definition |
| `GET /api/dashboards/:id/data` | resolve; `?refresh=1` bypasses the cache |
| `DELETE /api/dashboards/:id` | |

Registered on the existing authenticated group in
`apps/backend/cmd/api/router.go` beside `NewDashboardHandler` (`router.go:94`).

⚠ **Correction — this is a build-breaker.** The original read "There is no
declarative role policy in this repo to update." There is:
`apps/backend/cmd/api/policy.go`, enforced by
`apps/backend/internal/transport/http/middleware/rolepolicy.go`, and **unlisted
routes are denied** (`rolepolicy.go:34-37`). `TestEveryAuthedRouteIsClassified` in
`apps/backend/cmd/api/policy_test.go` fails the build for a route with no entry.
Every route
above needs a policy classification in the same ticket.

### `T-D11` Collapse the two tools into one — 1.5d

`create_visualization` and `create_dashboard` exist as a pair only because a card
is a first-class Metabase object and a dashboard is a container for cards.
Nothing native needs that round trip, and the current tool description carries the
scar tissue: *"If you omit both, cards created earlier in this conversation are
used automatically"* (`apps/backend/internal/tools/create_dashboard.go:36`),
backed by `GetThreadCards` (`apps/backend/internal/tools/thread_cards.go:28`,
called from `create_dashboard.go`) reading a package-level in-memory map. That map
does not survive a worker restart and is wrong the moment there are two workers.

Keep the name `create_dashboard`; take panels inline; delete
`create_visualization.go` and `thread_cards.go`. A three-panel dashboard becomes
one tool call instead of four.

New `Description()` — this is prompt engineering, so it is part of the ticket:

```
Create a live dashboard from one or more panels and return a URL the user can open.
Each panel carries its own SQL, a chart type, and which columns to plot — the
dashboard re-runs those queries every time somebody opens it, so it stays current
without being rebuilt. Call this ONCE with every panel the user asked for; there is
no separate step for individual charts. Run each panel's SQL with run_sql first and
look at the column names it actually returns — 'map' must name columns from that
result, and a name the query does not produce is the most common way this call
fails. If an axis is time (date, month, week, quarter), the SQL MUST ORDER BY that
column ascending so the chart reads left to right; never rely on unspecified row
order. Add a 'filters' entry for anything the user should be able to change — a date
range above all — and reference it in each panel's SQL as {{from}} / {{to}} or
{{your_filter_name}}. Those are bound as query parameters, so write them bare:
WHERE created_at >= {{from}}, never quoted and never concatenated. Returns
dashboard_id, url, and per-panel warnings. Give the user the url as a markdown link
with descriptive text, never the raw URL.
```

Tolerate malformed arguments the way `parseCardEntries` already does
(`apps/backend/internal/tools/create_dashboard.go:168`): accept `panels` or
`cards`, `map.series` as a bare string or an array, `viz` with spaces or hyphens
normalised, and default the layout to a sensible flow when absent — a model asked
for grid coordinates will produce overlapping ones.

### `T-D12` Prompt and wiring — 0.5d

⚠ **Correction.** The original read "Tools are constructed inline in
`cmd/worker/main.go`; there is no registry file to edit." There is:
`apps/backend/internal/tools/registry.go`. Edit it there.

Remove the `create_visualization` construction and its prompt text, rewrite the
`create_dashboard` blurb, and check `apps/backend/config/agents.yaml` for tool
allowlists that name the removed tool.

**Keep the "do not build a chart nobody asked for" guidance** and re-point it at
`create_dashboard`. Deleting the tool it currently names would delete the rule,
and the failure would come back as an unrequested *dashboard*, which costs more
than an unrequested card did.

### `T-D13` Sharing — 2d

*(Renumber — `026` is taken by `026_agent_actions_request_id`.)*

A public page has no tenant context but must run tenant SQL. The rule: **the
share row carries the company id, and everything the page reads is scoped by what
comes back from that row rather than by anything the request said.** The resolver
never reads a company, source, dashboard or panel id off the request. The token
is the only thing the visitor supplies.

```sql
CREATE TABLE IF NOT EXISTS dashboard_shares (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id           UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dashboard_id         UUID NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    token_hash           TEXT NOT NULL UNIQUE,
    locked_params        JSONB NOT NULL DEFAULT '{}'::jsonb,
    allow_filters        BOOLEAN NOT NULL DEFAULT false,
    password_hash        TEXT,
    max_refresh_per_hour INTEGER NOT NULL DEFAULT 60,
    created_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,
    view_count           INTEGER NOT NULL DEFAULT 0,
    last_viewed_at       TIMESTAMPTZ
);
CREATE INDEX idx_dashboard_shares_token ON dashboard_shares(token_hash);
```

⚠ **Correction — reuse, do not invent.** The original read "Token minting is new
— `internal/auth/` has no share-token file." It has one:
`apps/backend/internal/auth/sharetoken.go`, shipped for report shares
(`apps/backend/migrations/control/050_report_shares`, handler at
`apps/backend/cmd/api/router.go:71`). Read it
before writing anything; the design below may already be implemented there.

The intended design, for comparison: 32 random bytes, base64url, stored as a
SHA-256 hash and looked up by that hash in one indexed read, so nothing ever
compares a presented secret in Go. SHA-256 rather than a KDF because the input is
256 uniformly random bits: a KDF would slow a dictionary attack that cannot exist
while handing anyone who types a wrong token a way to burn CPU on every page view.
`password_hash` is the opposite case and uses Argon2id via
`apps/backend/internal/auth/password.go` — that input is human-chosen and does
have a dictionary behind it.

**`locked_params` are locked, never merged.** A dashboard shared with `region`
pinned to Jakarta shows Jakarta, and a visitor who edits the query string still
sees Jakarta, because request parameters on a share are *ignored* rather than
merged. Merging is the obvious implementation and it turns every declared filter
into a dimension a stranger may enumerate.

`?refresh=1` is ignored on a share. A bearer link that can spend a customer's
warehouse without limit is a leaked link that costs money forever.

Route `GET /share/:token`, unauthenticated, `no-store`, `noindex`,
`X-Frame-Options: DENY`. The original noted this would be "the first frame policy
this product has ever set" — re-check against the embed surface
(`apps/backend/internal/auth/embedkey.go`), which may have set one since.

**Gate: a written security review of the share path before this ticket ships**,
because it is the only place in the product where an unauthenticated request
causes a query against a customer's production database.

---

## Track E — Decommission (2d backend) · one release after Track D

### `T-D15` Remove Metabase — 1.5d

All locations re-verified and corrected — the original draft's line numbers were
taken from `1115e90` and none of them survived.

| Delete | Location (verified on `main`) |
| --- | --- |
| The client | `apps/backend/internal/metabase/` (client, DSN translators, tests) |
| Warehouse sync | `apps/backend/internal/app/metabase_sync.go`; call sites at `apps/backend/internal/app/company_service.go:50,124` |
| Wiring | `apps/backend/cmd/api/bootstrap.go:28` (import), `:172-177` (client + sync construction), `:189` (passed to `NewCompanyService`), `:343-344` (`Metabase`, `MetabaseSource`) |
| The reverse proxy | `apps/backend/cmd/api/router.go:296-300` (`/metabase/*path`) — **not `:71-78`, which is the report-share handler** |
| Config | `apps/backend/internal/config/config.go:107-109` (fields) and `:414-416` (env reads). `MetabaseAPIKey` is confirmed dead — declared at `:109`, read at `:416`, consumed nowhere |
| Repo accessor | `MetabaseDatabaseIDForSource`, `apps/backend/internal/adapters/postgres/connection_repo.go:124` |
| Metering | `apps/backend/internal/app/usage_service.go:21-22` (pricing fields), `:38-39` (defaults), `:118-135` (`RecordMetabaseCard`, `RecordMetabaseDashboard`) |
| Infra | the `metabase` service, its env blocks and volume in `apps/backend/docker-compose.yml`; `apps/backend/docker/postgres-docker-init-metabase.sql`; `apps/backend/scripts/setup_metabase.sh`; the Makefile targets |
| Dead code | `apps/backend/internal/cache/` |

Keep `saved_dashboards` and its read path through the deprecation window so the
archived list still renders.

> **Check `T-H5` in the hardening roadmap first.** It proposes hardening Metabase
> — its own read-only DSN, a scoped API key, a pinned version. If this
> decommission is going ahead, `T-H5` is wasted work. Only one should be built.

### `T-D16` Drop the Metabase columns — 0.5d

*(Renumber — `027` is taken.)*

`ALTER TABLE db_connections DROP COLUMN metabase_database_id` plus its unique
index, and drop `saved_dashboards`. The column is introduced in
`apps/backend/migrations/control/004_metabase_tenant_connections.up.sql` and is
also read by `apps/backend/internal/eval/tenant.go` and
`apps/backend/internal/domain/connection.go` — check all three before dropping.

**This must land a release after `T-D15`**, not with it: a running binary that
still reads the column would fault. The down migration re-adds the column
nullable — the data is gone, but the schema round trips, which is what a down
migration is for here.

### Cutover

| Release | State |
| --- | --- |
| N | Native dashboards live. Proxy still registered, Metabase still running, every existing public URL still works. |
| N+1 | Notice in the archived list naming the shutdown date. |
| N+2 | `T-D15` and then `T-D16`. Old URLs 404. |

**Gate N+2 on evidence, not the calendar:** a recorded check that no
`/metabase/*` request was served in the preceding 30 days. If one was, find out
who is still using it first.

**Rollback.** Everything through Track D rolls back by redeploying the previous
image — native dashboards live in tables the old binary never reads, and Metabase
is still running. `T-D15`/`T-D16` are the irreversible step, which is why they
are gated.

---

## ~~The frontend is not in this repo~~ — retracted

The original section read: *"`apps/dashboard/` contains `dist/`, `node_modules/`
and two tsbuildinfo files. No source. No `.tsx` file exists anywhere in this
checkout."*

**This was wrong, and the reason matters.** It was written from a working tree in
a broken intermediate state — the monorepo migration had been partially applied,
leaving build output without sources. On `main` there are **105 files under
`apps/dashboard/src/`**, including the feature directories this roadmap's UI work
would live beside.

The consequence: **the UI work can be estimated normally**, and Track E's "2d
backend" qualifier should be dropped. The design constraints recorded then are
still right and worth keeping:

- **One request for the whole dashboard**, not one per panel. Twelve requests ×
  twenty viewers every 30s is 480 rpm and twelve auth checks for data the server
  already gathers in parallel. Per-panel independence lives in the *response*.
- **Do not refetch in a backgrounded tab.** A minimised dashboard must not bill
  the tenant.

There is also a design system to build against — `packages/design-tokens` with a
`make tokens-check` drift gate, and a validated chart palette behind
`make palette`. Panel charts must take their colours from it; the palette gate
already checks greyscale and simulated CVD, and a hand-picked series colour would
fail it.

---

## Risks

| # | Risk | Mitigation |
| --- | --- | --- |
| 1 | **A share link is a stored, replayable query against a customer's production database, triggered by a stranger.** Wider than a Metabase public link, which ran on Metabase's own connection with its own limits. | `locked_params`; `allow_filters` false by default; request params ignored, never merged; no `refresh=1`; per-share hourly budget; row caps; 15s deadline; `ValidateStatement` at save *and* resolve; every miss logged. Security review gates `T-D13`. |
| 2 | **Query load against tenant warehouses.** Refresh × viewers × panels is a pattern this product has never generated, and a customer's replica falling over will look like our fault. | Cache is the default path; singleflight; server-side floor on `refresh_secs`; four concurrent panels. **Instrument in Track C, not later** — the first production week has to produce the number that says whether these defaults are right. |
| 3 | **SQL Server has no read-only transaction.** Already recorded as a finding in `03-security-hardening-roadmap.md` and re-verified at `sqlserver/conn.go:33-35`. Dashboards multiply the exposure because a panel re-runs on every view. | `T-D2` runs at save and at resolve. Consider making SQL Server panels refuse anything but a `WITH`/`SELECT` at a stricter threshold, and fix the driver under the hardening roadmap rather than this one. |
| 4 | **The agent writes worse dashboards than it wrote cards, silently.** Metabase inferred the chart type and column roles; the spec makes the model state them, so a `Map` naming a column that does not exist is a new failure class — and a chart with the wrong series draws without complaint. | Execute every panel at save; answer a missed mapping with the columns that would have worked; the tool description names this as the most likely failure and says to run the SQL first. |
| 5 | ~~**This roadmap is larger than it looks**~~ — **inverted.** It is *smaller* than it looks: `T-D1` is shipped, and the metric registry, chart renderer, audit table, share tokens, route policy and tool registry all exist. | The [What changed](#what-changed-since-this-was-written) table is the correction. The new risk is the opposite one — see 6. |
| 6 | **Planning against a stale tree.** This document was written against a non-ancestor commit *and* a half-migrated working tree, and asserted thirteen absences of which nine were wrong. Both roadmaps in this folder cite line numbers that drift with every merge. | Re-verify citations before acting on any ticket. `git merge-base --is-ancestor <commit> main` before writing "verified against". Prefer symbol names to line numbers where the prose allows. |

---

## Out of scope

Visual query builder · SQL editor UI · cross-filtering · drill-through ·
multi-select filters · per-viewer row scoping · dashboard folders · alerting on a
panel · emailed dashboards · dashboard versioning · drag-to-rearrange · automatic
conversion of existing Metabase dashboards.

⚠ **Server-side chart rendering and dashboard-to-PDF: re-open this.** The
original declared them out of scope on the grounds that *"That plan assumed a
chart renderer existed to reuse. It does not."* It does:
`apps/backend/internal/report/chart/` renders charts today and is already wired
into PDF and PPTX export. Whether `T-D4`'s `Resolved` should feed it is now a
design choice rather than a blocked one, and the original concern — two
definitions that drift — argues *for* sharing the one spec, not against it. The
`types.go:6` comment about extending Section/Sheet types predates the chart
package and should be reconciled.
