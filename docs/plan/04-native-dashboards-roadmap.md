# Native dashboards roadmap — replacing Metabase with a live query layer

Re-planned 2026-08-11 against `main` @ `ae66e8f`. Sixteen tickets, **~25.5 days**
across six tracks — 17.5 backend, 8.0 frontend. Ticket ids are `T-D1` → `T-D16`
plus a new Track F; `D` is unused elsewhere and does not collide with the `T-H…`
hardening tickets in `03-security-hardening-roadmap.md`, the `T-Q…` quality
tickets in `02-agent-quality-roadmap.md`, or the `S-n` finding codes.

> **This document replaces a superseded draft rather than amending it.** The
> first draft was written against `1115e90` — a commit that is **not an ancestor
> of `main`** — from a working tree in a broken intermediate state, and asserted
> thirteen absences of which nine were wrong. A first re-verification pass
> against `cc06dc7` caught those nine and left the ticket bodies alone. This pass
> re-checked every citation a third time, file by file, and found **eleven
> further claims wrong**, including two the *re-verification itself* introduced.
> All of it is in [Correction log](#correction-log) at the end; everything above
> that section is already corrected.
>
> **Status, 2026-08-17 — most of this roadmap is now built.** Eight commits
> (`105ad5b`→`12ba63e`) landed `T-H4` step 1, `T-D3`→`T-D7`, `T-D10` and
> `T-D11`, plus three surfaces of Track F: the chart primitive and its dark ramp
> (`T-D17`/`T-D18`), the `/dashboards` routes and page (`T-D19`), the data
> plumbing and generated types (`T-D20`), and one surface this roadmap never
> carried — a dashboard drawn inside the chat transcript. Gated live the same
> day: migration `056` up/down/up against the real control database, one
> authored dashboard from a real turn, and the first panel ever opened in a
> browser. **Three defects, all fixed in the sitting**
> ([`../coverage/native-dashboards.md`](../coverage/native-dashboards.md),
> [`../coverage/delivery-log.md`](../coverage/delivery-log.md) Phase 2t).
>
> **Revised 2026-08-18 — `T-D22`'s edit gate ran, and `update_dashboard` is
> sound.** Six turns, $0.119: a patch leaves unnamed panels byte-identical, the
> id and URL hold, a wrong panel title and an invalidated mapping are both
> refused with errors the model corrected from, `dryRun` caught a mapping naming
> a column the SQL never returned, and the no-id path listed the candidates and
> asked instead of looping. **The sitting's P0 is not in this roadmap's code:**
> two turns claimed edits they never made, because a budget-refused call is
> remembered as one that ran (`T-Q12`, on the quality roadmap). What *is* this
> roadmap's is `T-D24` below — a dashboard cannot default to the closed period
> it is about, so the gate's own request saved a dashboard that draws nothing on
> open. Record: [`../coverage/native-dashboards.md`](../coverage/native-dashboards.md) §4.
>
> ~~**What is left:** `T-D8` (panel cache) and `T-D9` (query log) in Track C…~~
>
> **Status, 2026-08-25 — the track is built except `T-D16`.** `T-D8`, `T-D9`,
> `T-D13`, `T-D21` and `T-D15` all landed; `T-D14` does not exist and never did,
> the numbering skips it. Records in
> [`../coverage/native-dashboards.md`](../coverage/native-dashboards.md).
>
> **`T-D16` is held deliberately, not forgotten.** It must land a release after
> `T-D15` or a rolling deploy faults on `SELECT metabase_database_id` in the
> previous binary, on every connection read. `T-D15` removed every reader — the
> SELECT, the scan, the UPDATE, the dead accessor and the domain field — which
> is precisely what makes the drop safe *next* release and unsafe in this one.
>
> ⚠ **And its evidence gate cannot be run as written.** The Cutover table gates
> `T-D16` on *"a recorded check that no `/metabase/*` request was served in the
> preceding 30 days"*, sourced from `internal/apiobs`. **apiobs never saw that
> route, for two independent reasons.** `middleware.RecordAPIRequests` is
> mounted on the `/v1` group alone (`cmd/api/router.go:268`) and its own comment
> says it "observes every `/v1` response"; the Metabase proxy was registered on
> the root router. And `api_request_stats.api_key_id` is `NOT NULL REFERENCES
> api_keys(id)`, while the proxy carried a browser session and no API key — so
> the row could not have existed even had the middleware been there.
>
> The evidence has to come from the ingress or gin access log instead
> (`msg:"request"`, `path:"/metabase/…"`), which is an operator's check against
> production traffic rather than a query anyone can run from this repo. **It is
> also now largely moot:** `T-D15` deleted the proxy, so the route already
> 404s and anyone still depending on it has been told by their own client. The
> gate's remaining value is naming *who* that was, and only the access log can
> answer it.
>
> **What moved between `cc06dc7` and that build:** nothing in the code.
> `git diff --stat cc06dc7 HEAD` is two files, both of them roadmaps in this
> folder. Sprint 3's `T-U1`→`T-U11` UI work landed at `c5f979b` and earlier,
> which is an *ancestor* of `cc06dc7` — so it was already inside the previous
> pass's window and is not new. The eleven corrections below are re-reads of the
> same tree, not drift.
>
> **The headline number went the wrong way.** The superseded draft's risk 5 said
> this roadmap was "*smaller* than it looks" because `T-D1` had shipped. That is
> false. It is **larger**: `~17d` → `~25.5d`. Two tracks were never sized (the
> frontend, because of the broken checkout) and one ticket's blast radius was
> counted at one file when it is twenty-one. See
> [Re-estimate](#re-estimate-old-numbers-beside-new).
>
> **Path convention.** Every path outside quotation marks is at its
> post-monorepo location under `apps/backend/`. Paths *inside* `"…"` quote the
> original draft verbatim at its old pre-monorepo spelling, so a correction reads
> as a correction.

Metabase is not the analytics engine here. It is a chart renderer, a dashboard
host and a link server bolted onto a product that already owns the hard parts:
schema introspection, read-only execution against three dialects, tenant
connection pooling, a validated metric registry, a CVD-gated chart palette, and
an agent that writes the SQL. What we are removing is the last mile.

| Claim we want to make | State today |
| --------------------- | ----------- |
| A dashboard shows current numbers | **False.** A Metabase card is created once and re-executed by Metabase on its own connection; Argentum has no idea when or whether it ran. |
| A dashboard link can be revoked or expired | **False for Metabase dashboards.** `GetPublicDashboardURL` mints a Metabase public link with no expiry, no revocation and no viewer identity. *Report* shares have exactly this machinery — see `T-D13`. |
| We can answer "who read this customer's warehouse" | **False for Metabase.** Metabase queries never touch our audit path. An audit table exists for tool calls (`023_agent_actions`); Metabase does not write to it, and cannot — the recorder decorates `interfaces.Tool` (`internal/tools/audit.go:46`). |
| Charts match the product's look | **False.** They are Metabase's chrome inside an anchor tag that opens a new tab — `apps/dashboard/src/components/layout/generated-dashboards.tsx:51-65`. |
| A saved query is bounded | **False for saved charts.** `run_sql` caps rows; a Metabase card does not, because Metabase runs it. |
| Tenant DSNs live in one place | **False.** Every registered DSN is mirrored into Metabase via `UpsertWarehouse` (`internal/app/metabase_sync.go:67`). |

`docs/coverage/feature-coverage.md:138` is the row this roadmap flips:
*"Public/embeddable dashboards 🟡 — Metabase URLs are shareable; no
Argentum-native embedding."*

---

## What is true on `main` today

Verified file by file against `main` @ `ae66e8f`. This table supersedes both the
original draft's "What is actually here to build on" and the previous pass's
"What changed since this was written".

| Capability | Original claim | Verified at `ae66e8f` |
| --- | --- | --- |
| **Parameterised query execution** | ❌ Absent — "`Conn` exposes only `ExecuteReadOnly`; no args variant and `Dialect` has no `Placeholder`" | ✅ **Shipped.** `ExecuteReadOnlyParams` on the `Conn` interface (`internal/adapters/db/driver.go:42`), implemented in all three dialects; `Dialect.Placeholder(n int)` at `driver.go:80`. Landed under T-06/T-07. **`T-D1` is done.** |
| **Statement validation** | ❌ Absent; re-verification said "❌ **Confirmed absent.** No `ValidateStatement`, no `sqlguard` anywhere" | ⚠️ **Both wrong — it is shipped under a different name.** `metric.ValidateTemplate` (`internal/metric/template.go:75`) refuses anything but a single SELECT/CTE, having first scrubbed comments and string literals — which is the exact behaviour, and the exact justifying argument, `T-D2` proposes as net-new. See [Decision: who owns the validator](#decision-who-owns-the-statement-validator). |
| **Parameter template binding** | ❌ Implied absent (`T-D3`, 1d, "still net-new") | ⚠️ **Half shipped.** `metric.Render(template, placeholder, from, to)` (`internal/metric/template.go:29`) already walks `{{token}}`s left to right, emits one dialect placeholder per occurrence and appends the value — `T-D3`'s `Render` with the token set frozen to `{{from}}`/`{{to}}`. What is genuinely new is arbitrary declared tokens, the five filter kinds and the presets. |
| **Metric / semantic layer** | ❌ Absent — "no `internal/metric`, no `metric_definitions`" | ✅ **Shipped, and narrower than "semantic layer" implies.** `migrations/control/039_metric_definitions.{up,down}.sql`, `internal/app/metric_service.go` (409 lines), `internal/metric/` (5 files), `internal/tools/metric_tools.go`, six HTTP routes, and a settings tab. **One scalar, one window, one source** — see [Decision 2, retaken](#decision-2-retaken). |
| **Chart rendering of any kind** | ❌ Absent — "No chart package anywhere" | ✅ **Shipped server-side.** `internal/report/chart/` (7 files), wired into `report/pdf` and `report/pptx`. Seven types, an 8-colour palette gated against deuteranopia, protanopia and greyscale in CI (`make palette`), documented in `docs/coverage/report-charts.md`. `internal/report/` now holds **16 subpackages**, not the single flat renderer the draft assumed. |
| **Chart rendering in the browser** | ❌ Never assessed (broken checkout) | 🟡 **One chart exists.** `apps/dashboard/src/components/ui/chart.tsx` is 127 lines and exports exactly `BreakdownChart` + `BreakdownDatum` — a horizontal bar breakdown with one consumer. `recharts ^2.15.4` is a dependency, lazy-loaded (~390 kB, deliberately code-split). No `ChartContainer`, no `ChartConfig`, no legend, no line/area/pie/stacked component. |
| **Chart palette in CSS** | ❌ Never assessed | ✅ **Shipped.** `--chart-1` … `--chart-8` in `apps/dashboard/src/tokens.generated.css:62-69`, generated from `packages/design-tokens/tokens.json` and read by `useChartPalette()` (`chart.tsx:33-41`, `SERIES_COUNT = 8`). **Gap:** no dark-mode variant — the eight hexes serve both themes, and series 2 (`#1C3A62`, L\* 24.2) and 7 (`#713F1C`, L\* 32.1) are dark on dark. |
| **Share tokens, public share pages** | ❌ Absent — "`internal/auth/` is `jwt.go` + `password.go`" | ✅ **Shipped for reports, with the exact design `T-D13` specifies.** `internal/auth/sharetoken.go`, `migrations/control/050_report_shares.{up,down}.sql`, `handlers.NewReportShareHandler` at `cmd/api/router.go:71`, and a **keyless `/share` group at `router.go:188-192`** with its own rate limiter. `internal/auth/` also holds `apikey.go`, `embedkey.go`, `invite.go`. |
| **Audit log** | ❌ Absent — "No `agent_actions` table, no audit package" | ✅ **Shipped.** `migrations/control/023_agent_actions` and `026_agent_actions_request_id`; `internal/tools/audit.go`; `handlers.NewAuditHandler` at `cmd/api/router.go:72`. It already carries `actor_kind` (`023_agent_actions.up.sql:25`), which the previous pass said it did not. |
| **Declarative route RBAC** | ❌ Absent — "No `cmd/api/policy.go`" | ✅ **Shipped, and load-bearing.** `cmd/api/policy.go` (24 kB) + `policy_test.go`, `internal/transport/http/middleware/rolepolicy.go`. **Unlisted routes are denied** (`rolepolicy.go:34-39`). `T-D10` must add its routes or the build fails. |
| **Tool registry** | ❌ Absent — "tools wired inline in `cmd/worker/main.go`" | ✅ **Shipped.** `internal/tools/registry.go`, with a `MetabaseSource` field at `:49-52` that exists solely for `create_visualization`. |
| **The dashboard frontend** | ❌ "not in this repo — no `.tsx` file exists anywhere in this checkout" | ✅ **Present.** 105 files under `apps/dashboard/src/` — 82 `.tsx`, 21 `.ts`, 2 `.css`. Eleven feature directories. **No `dashboards` feature, no `/dashboards` route.** |
| `internal/cache/` | ❌ Dead code | ✅ **Deleted 2026-08-14.** It was dead in every pass: no Go file imported it, `InvalidateSQLCache` was a `return nil` (`redis.go:294-296`) and `InferQueryType` string-matched `"2023"`…`"2020"` (`redis.go:126`). The comment at `internal/app/credits.go:86` that named it has been rewritten. |
| Read-only execution, 3 dialects, timeout | ✅ Shipped | ✅ Confirmed — `internal/adapters/db/driver.go:28-47` |
| Tenant connection pool, DSN-rotation detection | ✅ Shipped | ✅ Confirmed — resolver returns a `version` token (`pool.go:23`), pool compares it on every hit (`pool.go:92`) |
| Schema introspection | ✅ Shipped | ✅ Confirmed — `driver.go:47` (`ExtractSchema`) |
| Source resolution by company | ✅ Shipped | ✅ Confirmed — `internal/tools/source_resolve.go` |
| Redis, asynq queue, cron scheduler | ✅ Shipped | ✅ Confirmed — `internal/queue/` (8 files) |
| Event bus for pushing to clients | ✅ Shipped | ✅ Confirmed — `internal/transport/eventbus/redis.go` |
| `internal/dashboard/` package | ❌ Absent | ❌ **Confirmed absent** — 0 files. Tracks A–D's new packages are net-new. |
| `@tanstack/react-table` | ❌ Never assessed | ❌ **Absent** across `apps/` and `packages/`. `apps/dashboard/src/components/ui/table.tsx` (120 lines) is presentational only: no sorting, pagination, column defs, selection or virtualisation. |

---

## The three decisions this roadmap is built on

Decision 2 is retaken below. Decisions 1 and 3 survive re-verification.

| Decision | Consequence for this plan |
| --- | --- |
| **Existing Metabase dashboards are abandoned, not converted.** | No converter, no dual-write. `saved_dashboards` rows survive read-only through the deprecation window and are dropped in `T-D16`. *Unaffected.* |
| **Panels carry SQL *and* metric keys, with the boundary at row cardinality.** | **Retaken — see below.** The original ("SQL, not only registry metrics") rested on there being no registry. There is one, and it answers a narrower question than the original decision assumed. |
| **Embedding is inside Argentum's own app only.** | No cross-origin iframe story, no per-share origin allowlist, no Go-served HTML shell. *Unaffected, and now supported by precedent:* `handlers.ShareHandler`'s docblock (`share_page.go:15-24`) argues the same thing for the report player — "There is no HTML here. The page itself is the dashboard's SPA route… An HTML template in Go would be a second frontend." An embed surface has shipped since (`internal/auth/embedkey.go`), but it is the *widget's*, not a dashboard's, and it has its own origin allowlist. |

### Decision 2, retaken

**Recommendation: both — a panel carries `metric_key` *or* `sql`, never neither
and never both, with the boundary drawn at how many rows the panel needs.**

Accept or reject in one line. The three sentences of argument:

1. The registry answers exactly one shape of question — `MetricService.evaluate`
   runs with `maxRows 2` and refuses anything but a single row
   (`internal/app/metric_service.go:254,258-262`), and `039_metric_definitions.up.sql`
   states the narrowing is deliberate ("*No dimensions, no joins, no DSL — those
   turn the registry into a semantic layer, which is a multi-week design problem
   and a Sprint-2 item at the earliest*"), so of `T-D4`'s eight viz kinds the
   registry can express precisely one: `kpi`.
2. For that one, the registry is strictly better than SQL and should be
   *required*, not merely allowed — `metric.Result` already carries `Primary`,
   `Comparison`, `Delta` and `DeltaPct` (`internal/metric/result.go:24-32`) plus
   `Unit`, `Currency` and `HigherIsBetter` for formatting and arrow direction,
   and a re-derived KPI is the precise fabrication the registry was built to
   stop (`docs/coverage/metric-registry.md` §2: two threads, two revenues).
3. For the other seven, SQL is the only option that exists, and pretending
   otherwise means shipping no chart at all until a dimensional registry is
   built — which is the multi-week cost `039` explicitly deferred and which
   nobody has paid.

**The alternatives, and why they lose.**

*Registry-only* buys number-consistency across every thread and dashboard, at
the price of a v1 that renders KPI tiles and nothing else. That is not a
Metabase replacement; it is a scorecard. To make it a replacement you must first
add dimensions to `metric_definitions`, which changes the `evaluate` contract,
the `query_metric` payload, the watcher evaluation path and the eval golden
set — a multi-week change gating a one-week one.

*SQL-only* — the original decision — ships every chart and re-opens the exact
divergence the registry closed. A dashboard whose revenue KPI is agent-written
SQL and a chat answer that came from `query_metric` will disagree, and the
dashboard is the one an executive reads every Monday.

*Both, unstructured* (a panel may use either, agent's choice) is the worst of
the three: it has the cost of two code paths and none of the guarantee, because
nothing makes the agent prefer the registry when a metric exists.

**The governance boundary, stated so it can be enforced.**

| Panel viz | Source | Enforced by |
| --- | --- | --- |
| `kpi` | `metric_key` — required when a metric of that meaning exists | Tool description ranks it, matching `bootstrap.SystemPrompt` guideline 5 ("prefer a defined metric over `run_sql`"); the turn already injects the metric catalog via `ChatRunner.withMetricsContext` |
| `kpi`, no metric covers it | `sql` | Allowed, and the save-time warning names the gap so an admin can promote it to a metric |
| everything else | `sql` | The registry cannot express it |
| any | **never both `metric_key` and `sql`** | `spec.Validate` refuses, the way it already refuses `Series` + `SeriesBy` together |

Governance is **execution control, not authorship provenance** — that half of
the original decision survives intact and applies identically to both paths:
bound parameters, a read-only transaction, statement validation at save *and* at
resolve, row caps, a 15s statement deadline, and one audit row per warehouse
read. A metric-backed panel gets all of it because `evaluate()` is the same
code path `query_metric` and validate-on-save already run through.

**Deferral trigger.** When a dimensional metric registry ships — one group-by
dimension is enough — `series_by` panels become expressible and the boundary
moves from "one row vs many" to "one dimension vs many". Revisit this decision
in the ticket that adds the dimension, not before.

**Cost of the recommendation:** two panel source kinds in the spec and two
branches in the resolver. Sized at +0.5d on `T-D4` and +0.25d on `T-D7` below.

### Decision: who owns the statement validator

`T-D2` (0.5d, this roadmap) and `T-H4` (2.0d, `03-security-hardening-roadmap.md`)
propose the same capability at two sizes in two documents with no cross-reference
between them. Neither knows that **a third implementation already shipped**.

`internal/metric/template.go:75` — `ValidateTemplate` — refuses anything but a
single SELECT or `WITH … SELECT`, having first stripped block comments, line
comments and single-quoted string literals so that `status = 'deleted'` and
`REPLACE()` are not false positives. That is `T-D2`'s specification, including
the sentence `T-D2` uses to justify itself. It has 128 lines of table-driven
tests (`internal/metric/template_test.go`) and nine live-gated refusals recorded
in `docs/coverage/metric-registry.md` §4.

**Call: `T-H4` owns it. `T-D2` is deleted and becomes a dependency.**

Real scope of `T-H4`, restated in three steps rather than one:

1. **Promote, don't rewrite — ~0.25d.** Move `ValidateTemplate` into
   `internal/sqlguard` as `ValidateStatement(sql string, require ...Token) error`,
   with the "must reference `{{from}}` and `{{to}}`" rule made a caller-supplied
   requirement instead of a hardcoded one — it is metric-specific, and a
   dashboard panel declares different tokens. `metric.ValidateTemplate` becomes a
   one-line call that passes `from`, `to`. Pure move plus one parameter; the
   existing tests move with it.
2. **Then the parser upgrade — 2.0d, as `T-H4` already specifies.**
   `pg_query_go` for Postgres, `vitess` sqlparser for MySQL, the promoted lexer
   as the SQL Server arm because no credible Go parser exists there. Both
   consumers upgrade at once because there is only one implementation.
3. **Wire the third caller — 0.25d.** `run_sql.Execute` passes `params.SQL`
   straight to the driver today (`internal/tools/run_sql.go:138`); that is the
   hole `T-H4` was written to close, and it is a different call site from either
   the metric or the dashboard one.

**What the other ticket then depends on.** `T-D2` becomes *"call
`sqlguard.ValidateStatement` at save and at resolve"* — 0.25d, folded into `T-D6`
and `T-D7`, no separate ticket.

**The dashboard track does not block on the parser.** Step 1 alone unblocks it,
and step 1 is a refactor of shipped tested code. This matters because step 2
introduces cgo for `pg_query_go`, which `T-H4` already records as touching
`apps/backend/Dockerfile.api` and the release build — a dashboard ticket must
not be the thing that introduces cgo to the release build.

**Rejected alternative:** let `T-D2` ship a second lexer as a stopgap and have
`T-H4` replace both later. Two validators drift, and the one that drifts is the
one on the *unauthenticated* path (`T-D13`'s share page), because that is the
path nobody exercises during development. There is no version of "temporarily
two" that is cheaper than one move.

---

## Migration numbering

⚠ **All four numbers the original draft claimed are taken.** It said "the last
applied migration is `023_thread_slack`" — true on `1115e90`, where the Slack
migrations were `021`–`023`. On `main` those same migrations are `047`–`049`.

`make migration-next` at `ae66e8f` reports the last three, which is the whole of
what it does (`Makefile:195-197` — `ls migrations/control/ | tail -3`):

```
054_message_feedback.up.sql
055_query_examples.down.sql
055_query_examples.up.sql
```

So the highest applied is **`055_query_examples`** and the next free number is
**056**.

| Original | Name | Ticket | Now |
| --- | --- | --- | --- |
| 024 | `dashboards` | `T-D5` | → **056** |
| 025 | `dashboard_query_log` | `T-D9` | → **057** |
| 026 | `dashboard_shares` | `T-D13` | → **058** (`026` is `agent_actions_request_id`) |
| 027 | `drop_metabase_columns` | `T-D16` | → **059** |

**Re-run `make migration-next` at the moment you write the file, not from this
table.** Two other agents are working this tree; the number moves, and only one
agent may claim it (`docs/AGENTS.md` §6). The working tree was clean at
`ae66e8f` when this was checked, which is a fact with a five-minute shelf life.

**No fifth migration is needed for the tool removal** — see `T-D12`, where this
was checked against `043_agent_tools_backfill.up.sql` rather than assumed.

Each migration carries a 20–40 line header comment stating the decision and the
alternative rejected, matching `003_metering.up.sql`,
`013_drop_table_embeddings_ivfflat` and `039_metric_definitions`.

---

## Track A — The query engine (3d → 1.5d → **0.75d**)

### ~~`T-D1` Bound parameters across all three dialects — 1.5d~~ ✅ SHIPPED

Delivered under T-06/T-07. `internal/adapters/db/driver.go:36-42`:

```go
// ExecuteReadOnlyParams is ExecuteReadOnly with bound query parameters. It
// exists for the metric registry (T-06/T-07): a metric's window bounds are
// passed as args and referenced in the SQL through the dialect's placeholder
// syntax, so a `'; DROP …` in a window value is data the driver escapes
// rather than SQL it runs. args are positional and match the placeholders
// Dialect.Placeholder produced, left to right.
ExecuteReadOnlyParams(ctx context.Context, sql string, args []any, maxRows int) (*QueryResult, error)
```

`Dialect.Placeholder(n int) string` is at `driver.go:80`. `ExecuteReadOnly`
remained as the no-args path, exactly as this ticket proposed. Nothing to do.

**The SQL Server caveat still stands** and is re-verified at
`internal/adapters/db/sqlserver/conn.go:33-35`:

```go
// SQL Server has no read-only tx mode; the mssql driver rejects
// TxOptions.ReadOnly with "read-only transactions are not supported".
// Read-only enforcement is the customer's db_datareader login.
```

Statement validation is what stands in front of it.

### ~~`T-D2` Statement validation — 0.5d~~ → **reassigned to `T-H4`; 0.25d of call sites remains**

See [Decision: who owns the statement validator](#decision-who-owns-the-statement-validator).
`internal/dashboard/sqlguard.go` is **not** written. `T-D6` and `T-D7` call
`sqlguard.ValidateStatement` at save and again at resolve — the stored spec is
not trusted just because it was validated once, which is the one design point
from the original `T-D2` that survives whole and is the reason it runs twice.

The 0.25d is inside `T-D6`/`T-D7`'s estimates, not a line of its own.

### `T-D3` The parameter template binder — 1d → **0.5d**

`internal/dashboard/template.go`. Half of this shipped as `metric.Render`
(`internal/metric/template.go:29`), which already walks `{{token}}`s left to
right, emits one dialect placeholder per occurrence and appends the value —
including the "one placeholder per occurrence rather than reusing a marker, so
MySQL's positional `?` needs no reuse" decision, which is the subtle part.

What is new is the token set and the coercion:

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

`metric.Render` already gets this right for its own two tokens — an unknown
token is an error naming it (`template.go:45`). Generalise rather than
re-derive; the live gate found the one thing it does *not* do
(`docs/coverage/metric-registry.md` §4: an unknown token fails at render with a
500 rather than at validate with a 400, because `ValidateTemplate` checks
presence and never absence). **Fix that in the promotion, not here** — it is one
line and it belongs with the code, not with a second copy of it.

| Kind | Request form | Coerced to |
| --- | --- | --- |
| `date_range` | preset name, or `from`/`to` as `YYYY-MM-DD` | two `time.Time` in the dashboard's zone |
| `date` | `YYYY-MM-DD` | `time.Time` |
| `enum` | string | string |
| `number` | decimal string | `float64` |
| `bool` | `true`/`false` | `bool` |

Presets: `last_7d`, `last_30d`, `mtd`, `qtd`, `ytd`, `last_month`, resolved
server-side at request time. **A default is stored as a preset name, never as
two timestamps** — that is the entire difference between a live dashboard and a
snapshot. None of the six exist anywhere in the tree today; `metric/window.go`
has only `ValidationWindow` (trailing 7 days) and `Shift` (comparison windows),
so the preset table is genuinely net-new.

An `enum` value is deliberately **not** checked against its option list. Options
are a UX affordance; the security boundary is the binding shipped in `T-D1`. A
value outside the set returns no rows, which is the correct outcome. Adding a
check would suggest the check is what makes it safe, and the day someone adds a
filter kind without one, that belief is what breaks.

⚠ **Correction — the tzdata note was wrong.** The original read *"`_ "time/tzdata"`
must be imported wherever presets resolve — the deployed images carry no
zoneinfo."* A blank import of `time/tzdata` embeds the database for the **whole
process**, not per package, and `internal/app` already blank-imports it twice
(`scheduled_task_service.go:25`, `watcher_service.go:23`), so both `cmd/api` and
`cmd/worker` already carry zoneinfo. Adding it again in `internal/dashboard` is
harmless defence for a future binary that does not link `internal/app`; it is
not the load-bearing step the draft implied. The comment at
`internal/app/scheduled_cron_test.go:110` is the one to copy the reasoning from.

---

## Track B — The dashboard model (5d → **5.75d**)

### `T-D4` The spec package — 1.5d → **2d**

`internal/dashboard/spec/`. A panel stores a **question and a column mapping**,
not values. The +0.5d is [decision 2](#decision-2-retaken)'s second panel kind.

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

    // Exactly one of MetricKey and SQL is set. A kpi panel prefers MetricKey:
    // the registry's number is validated on save and re-checked on every read,
    // and a KPI the agent re-derived is the divergence 039 exists to prevent.
    // Every other viz needs more than one row, which the registry refuses by
    // construction (metric_service.go:258), so it carries SQL.
    MetricKey string `json:"metric_key,omitempty"`
    SQL       string `json:"sql,omitempty"` // single SELECT, {{tokens}} bound at run time

    Map Mapping `json:"map"`
    Fmt string  `json:"fmt,omitempty"` // text|number|currency|percent|date
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

A `metric_key` panel needs no `Mapping` at all — `metric.Result` already names
its own value, comparison, delta and unit. `Project` short-circuits for it.

`Project(panel, *db.QueryResult) (*Resolved, error)` turns a result set into what
the browser draws. It refuses a spec that sets both `Series` and `SeriesBy`
rather than picking one silently — and, by the same rule, both `MetricKey` and
`SQL`. A `Map` naming a column the result lacks returns **the column names that
would have worked**, the same repair-instruction shape
`internal/tools/sql_error_hint.go` uses when a query fails.

Series after the long-form pivot are capped at 8. That number is not a taste
call and must not be re-derived here: it is the length of the palette in
`packages/design-tokens/tokens.json`, and `docs/coverage/report-charts.md`
records why a ninth series wraps onto the first one's red. `SeriesTruncated` is
a different fact from `Truncated` ("there is more data") and reads differently
to a person.

⚠ **The original's closing line is retracted.** It read: *"Because there is no
chart renderer in this repo, `Resolved` is JSON for a browser only."* There is
one — see [Out of scope](#out-of-scope), where server-side rendering is
re-opened as a design choice rather than a blocked one.

### `T-D5` Migration `dashboards` — 0.5d

*(**056**. See [Migration numbering](#migration-numbering).)*

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
   at a Metabase object. Its `thread_id` is `NOT NULL … ON DELETE CASCADE`
   (`006_saved_dashboards.up.sql:7`) — which would delete a dashboard an
   executive opens every Monday because somebody tidied the chat thread that
   created it. Here `thread_id` is nullable provenance and does not cascade.
3. **`source_id` is `ON DELETE RESTRICT`.** Deleting a connection a dashboard
   reads should fail loudly at the delete, not silently empty the dashboard.
   Note this differs from `039_metric_definitions`, where `source_id` cascades —
   deliberately, and the comment should say so: a metric is one definition an
   admin can rewrite, a dashboard is a dozen panels somebody's Monday depends on.

Forward-compatibility (`docs/AGENTS.md` §2, `workspace-context.md` §6): new
table only, nothing a running binary reads is touched.

### `T-D6` Domain, repository, service — 1.5d

Rewrite `internal/domain/dashboard.go` and
`internal/adapters/postgres/dashboard_repo.go`.

**Fix a live tenancy defect while doing it — re-verified, still present.**
`DashboardRepository.GetByID(ctx, id)` takes no company id
(`internal/domain/dashboard.go:23`) and the service compares ownership in Go
afterwards. Every method on the new repository takes `companyID` beside the id,
so the isolation is in the `WHERE` clause and not in a comparison somebody can
forget. `domain.MetricRepository` is the shape to copy — `GetByID(ctx, companyID, id)`
is what `MetricService.Get` calls (`internal/app/metric_service.go:101`).

On save, every distinct `source_id` in the spec is intersected with the
company's own connections. A stored dashboard must not be a latent cross-tenant
read waiting for a resolver bug. `MetricService.evaluate` already does the
belt-and-braces version of this and explains why
(`metric_service.go:234-239`: *"The metric was read scoped to the company, so
this should be impossible; refusing rather than trusting keeps a mis-scoped read
from running one tenant's SQL on another's word."*) — copy the check and the
comment.

Save-time validation — **refuse on structure, warn on execution**:

| Failure | Outcome |
| --- | --- |
| `sqlguard.ValidateStatement` fails | Refuse the whole save, name the panel |
| Both `metric_key` and `sql` set, or neither | Refuse, name the panel |
| `metric_key` names no enabled metric | Refuse, and **list the keys that would have worked** — `query_metric` already does exactly this (`docs/coverage/metric-registry.md` §3) |
| A `{{token}}` no filter declares, or a filter no panel binds | Refuse, name the token |
| `Map` names a column the result lacks | Refuse that panel, save the rest, return the columns that would have worked |
| Execution times out or errors | **Save with a warning** on that panel |

The asymmetry is deliberate: a dashboard is a dozen statements an agent wrote in
a turn that is about to end, and losing eleven good panels because one hit a cold
cache is the worse failure.

**One inherited trap, recorded because the metric registry hit it.**
Validate-on-save runs the panel. `docs/coverage/metric-registry.md` §4 found that
`SUM(x)` over an empty window returns NULL and refuses a *correct* definition
for the state of last week's data — "every metric in this gate needed the
workaround". A dashboard panel is more likely to hit this, not less, because a
panel is often scoped to a preset the author has not thought about. So the
execution failure is a **warning**, never a refusal, and the warning text names
the window it ran over.

### `T-D7` The resolver — 1.5d → **1.75d**

`internal/dashboard/resolve.go`. Loads the row company-scoped, coerces params
against the declared filters, then fans panels out. The +0.25d is the
`metric_key` branch, which calls `MetricService.Query` rather than the pool.

| Cap | Value | Reason |
| --- | --- | --- |
| `maxRows` chart | 2 000 | `run_sql`'s cap is 100, tuned for LLM context — far too small for a chart |
| `maxRows` table | 500 | Past this a browser table wants pagination, which v1 does not have |
| `maxRows` KPI | 2 | Enough to tell "exactly one row" from "more than one" — the same value and the same reason as `metric_service.go:252-254`, so an SQL-backed KPI and a metric-backed one agree about what "one row" means |
| series after pivot | 8 | The palette is eight long, and `report-charts.md` records what a ninth does |
| per-panel deadline | 15s, `DASHBOARD_PANEL_TIMEOUT` | So a browser waiting on twelve panels has a bounded worst case |
| concurrent panels | 4 | Twelve simultaneous connections into a customer's production replica is a load pattern they did not agree to |

A panel that fails fills its own `error` field; the response is still `200` and
the other panels render. One timing out must not blank the eleven that answered.

**Filter options never get their own route.** They resolve inside the dashboard
response. A `GET /api/dashboards/:id/filters/:name/options` endpoint would be a
generic query executor reachable by anyone who can open the dashboard —
including, after `T-D13`, a stranger holding a share link.

---

## Track C — Caching and accountability (2d → **2.25d**)

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
`ConnectionResolver.Resolve` returns it (`internal/adapters/db/pool.go:23`) and
the pool compares it on every hit (`pool.go:92`) — it is simply not reachable by
callers. Add:

```go
func (p *TenantConnPool) ForWithMeta(ctx context.Context, companyID, sourceID string) (Conn, ConnMeta, error)
```

`For` keeps its signature and delegates, so no existing caller changes — and
there are callers outside this track: `MetricService` depends on a narrowed
`MetricConnResolver` with exactly `For` (`internal/app/metric_service.go:43-45`),
which is the interface that would break if `For` changed.

A `metric_key` panel keys on the metric id and window instead of rendered SQL,
because `MetricService.Query` owns its own rendering. Same TTL, same
singleflight.

TTL `DASHBOARD_PANEL_CACHE_TTL`, default 60s. Write a small Redis helper next to
the resolver. `internal/cache/` is no longer an option to reject: it was deleted
on 2026-08-14, having been imported by nothing for the whole life of this
document.

**No stale-while-revalidate.** The failure that is real on day one is the
thundering herd: twenty people open the same dashboard at 09:00 and twelve panels
each run twenty times against a customer's warehouse. A per-process in-flight map
— `map[string]*call` behind a mutex, about forty lines — collapses that. Hand
this rather than adding `golang.org/x/sync`: the module does not depend on it
today and this is the only consumer.

**No per-process L1 cache.** A dashboard's whole value is that the number is
current; an in-process copy would let two API replicas show two different figures
for the same URL. Redis is the only layer. (This is also `workspace-context.md`
§1's standing warning arriving in a new place: two processes, two caches, no
shared invalidation.)

### `T-D9` The query log — 1d → **1.25d**

*(**057**.)*

⚠ **Premise corrected twice.** The original read *"There is no audit table in
this repo, so one is created rather than reused."* There is one. The
re-verification then said the separate-table argument still holds because
"`actor_kind` has no analogue in `agent_actions`" — **that is also wrong**.
`agent_actions.actor_kind` exists at `023_agent_actions.up.sql:25` and its
comment explicitly anticipates new kinds:

```sql
-- user | schedule | watcher (T-08) | api_key (T-13). Text rather than an
-- enum type: T-13 and T-19 each add a kind, and ALTER TYPE ... ADD VALUE
-- cannot run inside the transaction golang-migrate wraps a migration in.
actor_kind     TEXT NOT NULL,
```

**Decision: a new table, on two arguments neither previous draft made.**

1. **`WithAudit` decorates `interfaces.Tool`** (`internal/tools/audit.go:46`),
   and its own comment says the decorator exists so that "a tool added next year
   is audited without its author knowing this package exists." A share-page
   render and a scheduled refresh are **not tool calls** — there is no `Tool` to
   decorate. Writing into `agent_actions` from the resolver would be a second
   write path into a table whose entire design is one row per tool execution
   written in one place, which is the property that makes the audit endpoint
   trustworthy.
2. **The retention obligations are opposite, and this is the decisive one.**
   `agent_actions.args_redacted` holds redacted arguments by design
   (`023_agent_actions.up.sql:30-33`), and that is exactly why `T-H6` in the
   hardening roadmap exempts audit rows from erasure: they hold no tenant
   content, so they should outlive conversations. `dashboard_query_log.sql_text`
   stores the rendered statement **verbatim**, literals included. Putting it in
   `agent_actions` forces one of two bad outcomes — redact it and lose the
   question "what ran against my database last month", or keep it and void
   `T-H6`'s exemption for the entire table.

**Rejected alternative:** rows in `agent_actions` with `tool_name =
'dashboard_panel'`. It is one fewer table and it is wrong for reason 2 alone.

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

`actor_kind` deliberately mirrors `agent_actions`' vocabulary so the two logs can
be read side by side, and it is `TEXT` for the same stated reason.

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

**Retention ships with the table, not after it** (+0.25d). It cannot inherit
`agent_actions`' exemption — that is argument 2 above — so it needs its own
prune from day one. Reuse `internal/apiobs`'s shape: a 30-day default and an
hourly at-most-once prune per process (`internal/apiobs/recorder.go:39,256`),
which `T-H6` names as the pattern to generalise rather than reinvent.

Metering, on the same cache-miss rule: a new `dashboard_query` usage event priced
at the existing SQL-query cost. **Keep `UsageEventMetabaseCard` and
`UsageEventMetabaseDashboard` in `internal/domain/usage.go:14-15` permanently**,
with a comment saying nothing writes them any more and they exist so historical
rows still render a label — `apps/dashboard/src/features/usage/labels.ts:14-15`
maps them to "Charts" and "Dashboards" and would render raw identifiers without
them. Delete `MetabaseCardCost` / `MetabaseDashboardCost` from the pricing struct
(`internal/app/usage_service.go:21-22`, defaults at `:38-39`) once the tools are
gone.

---

## Track D — The agent surface and sharing (5d → **5.75d**)

### `T-D10` HTTP routes — 0.5d → **0.75d**

| Route | Policy | Notes |
| --- | --- | --- |
| `GET /api/dashboards` | `RoleMember` | list |
| `GET /api/dashboards/:id` | `RoleMember` | definition |
| `GET /api/dashboards/:id/data` | `RoleMember` | resolve; `?refresh=1` bypasses the cache |
| `DELETE /api/dashboards/:id` | `RoleAdmin` | |

Registered on the existing authenticated group in `cmd/api/router.go` beside
`NewDashboardHandler` (`router.go:94`).

⚠ **Correction — this is a build-breaker.** The original read "There is no
declarative role policy in this repo to update." There is: `cmd/api/policy.go`,
enforced by `internal/transport/http/middleware/rolepolicy.go`, and **unlisted
routes are denied** — `rolepolicy.go:34-39` aborts with 403 and returns, before
any role is read. `TestEveryAuthedRouteIsClassified` in `cmd/api/policy_test.go`
fails the build in both directions: a route with no entry, *and* an entry whose
route no longer exists (which matters for `T-D15`, deleting the proxy).

The read-member / write-admin split above follows the metric registry's
classification (`docs/coverage/metric-registry.md` §1: "read=member,
write+test=admin"). Rationale for the whole table lives in
`docs/coverage/rbac.md` and this ticket adds to it.

`docs/AGENTS.md` §3 also requires `apps/backend/docs/` to be updated for
anything under the dashboard's `/api`. These are `/api` routes, not `/v1`, so
`apps/backend/openapi/v1.yaml` is **not** in scope — worth stating, because the
parity test that fails for a missing `/v1` entry does not fire here and the
absence of a red build is not evidence the docs are current.

### `T-D11` Collapse the two tools into one — 1.5d → **3d**

`create_visualization` and `create_dashboard` exist as a pair only because a card
is a first-class Metabase object and a dashboard is a container for cards.
Nothing native needs that round trip, and the current tool description carries the
scar tissue: *"If you omit both, cards created earlier in this conversation are
used automatically"* (`internal/tools/create_dashboard.go:36`), backed by
`GetThreadCards` (`internal/tools/thread_cards.go:28`) reading a package-level
in-memory map. That map does not survive a worker restart and is wrong the moment
there are two workers — which `workspace-context.md` §2 states as a standing rule
("a tool must be stateless"), so this is a live violation, not a style
preference.

⚠ **Doubled from 1.5d. `create_visualization` is named in twenty-one Go files,
not one.** The original scoped this as "delete `create_visualization.go` and
`thread_cards.go`". The full set, from a whole-tree search:

| Site | What breaks |
| --- | --- |
| `internal/tools/create_visualization.go`, `create_dashboard.go`, `thread_cards.go` | the deletions themselves |
| `internal/tools/registry.go:49-52` | the `MetabaseSource` dependency field exists only for this tool |
| `internal/tools/list_sources.go`, `source_resolve.go` | reference the tool by name |
| `internal/mcpserver/server.go` | **`cmd/mcp` serves `create_visualization` as one of eight MCP tools** under the `write:visualizations` scope (`docs/coverage/feature-coverage.md:132`). An MCP client is a third-party integration; removing a tool it can call is a contract change, not a refactor. |
| `internal/bootstrap/system_prompt.go` + `system_prompt_test.go`, `report_turn_prompt_test.go`, `agent_factory_test.go` | `TestEveryRegisteredToolHasAPromptLine` (conventions §"Agent tools" 7) |
| `internal/transport/http/handlers/agents.go` | `agentToolLabels`, or the checkbox renders a raw identifier |
| `internal/app/chat_runner.go`, `report_directive.go`, `agent_service_test.go` | turn composition |
| `internal/agenttemplates/golden_test.go` | a golden test over the template vocabulary |
| `internal/eval/tenant.go`, `score.go`, `cmd/eval/main.go`, `testdata/eval/golden.yaml` | the eval harness — see `T-D15` |
| `config/agents.yaml:35`, `config/agent_templates.yaml:36,70,91,112,133,155,177` | six template rows plus a comment |
| `internal/tools/audit_test.go`, `internal/transport/http/handlers/v1_reports_test.go` | fixtures |

Keep the name `create_dashboard`; take panels inline; delete
`create_visualization.go` and `thread_cards.go`. A three-panel dashboard becomes
one tool call instead of four.

**Decide the MCP contract explicitly, in this ticket.** Options: drop
`create_visualization` from `cmd/mcp` and leave `write:visualizations` gating
`create_dashboard` alone; or retire the scope. The second is a breaking change
for any key that holds it. Recommendation: keep the scope, re-point it — a scope
is a permission name, not a tool name, and renaming it costs every integrator a
key rotation to buy nothing.

New `Description()` — this is prompt engineering, so it is part of the ticket:

```
Create a live dashboard from one or more panels and return a URL the user can open.
Each panel carries either a metric_key from the metric registry (preferred for single
numbers — call list_metrics first) or its own SQL, plus a chart type and which columns
to plot. The dashboard re-runs those queries every time somebody opens it, so it stays
current without being rebuilt. Call this ONCE with every panel the user asked for;
there is no separate step for individual charts. For an SQL panel, run its SQL with
run_sql first and look at the column names it actually returns — 'map' must name
columns from that result, and a name the query does not produce is the most common way
this call fails. If an axis is time (date, month, week, quarter), the SQL MUST ORDER BY
that column ascending so the chart reads left to right; never rely on unspecified row
order. Add a 'filters' entry for anything the user should be able to change — a date
range above all — and reference it in each panel's SQL as {{from}} / {{to}} or
{{your_filter_name}}. Those are bound as query parameters, so write them bare:
WHERE created_at >= {{from}}, never quoted and never concatenated. Returns
dashboard_id, url, and per-panel warnings. Give the user the url as a markdown link
with descriptive text, never the raw URL.
```

Tolerate malformed arguments the way `parseCardEntries` already does
(`internal/tools/create_dashboard.go:168`): accept `panels` or `cards`,
`map.series` as a bare string or an array, `viz` with spaces or hyphens
normalised, and default the layout to a sensible flow when absent — a model asked
for grid coordinates will produce overlapping ones.

Return `row_count` in the tool payload. `docs/coverage/metric-registry.md` §"What
the gate found" records what happens when a data tool omits it:
`guardrails.CheckFabrication` grounds the reply on `TurnEvidence.DataRows > 0`,
`query_metric` did not emit `row_count`, and **every metric-only answer was
suppressed as a fabrication** while the audit row logged `rows_returned = NULL`.
A dashboard tool that returns a URL and no row count is the same half-wiring.

### `T-D12` Prompt and wiring — 0.5d

⚠ **Correction.** The original read "Tools are constructed inline in
`cmd/worker/main.go`; there is no registry file to edit." There is:
`internal/tools/registry.go`. Edit it there.

The four edits conventions §"Agent tools" 7 requires are all in `T-D11`'s table
above; this ticket is the prompt and config half.

**Keep the "do not build a chart nobody asked for" guidance** and re-point it at
`create_dashboard`. Deleting the tool it currently names would delete the rule,
and the failure would come back as an unrequested *dashboard*, which costs more
than an unrequested card did. Note this rule is also about to be *measured*:
`T-Q1` in `02-agent-quality-roadmap.md` adds a `no_chart_wanted` eval category
that "asserts `create_visualization` was **not** called". **Cross-reference
required** — if `T-D11` lands first, that category's assertion names a tool that
does not exist. Whichever ships second owns the rename.

⚠ **Correction to the correction — `agents.yaml` is not the only allowlist.**
The re-verification said to "check `config/agents.yaml` for tool allowlists".
`create_visualization` appears there once (`:35`) and **six times in
`config/agent_templates.yaml`** (`:70,91,112,133,155,177`), which is the file
that decides what a newly created agent gets.

**No backfill migration is needed, and this was checked rather than assumed.**
Conventions §"Agent tools" 8 warns that `agents.allowed_tools` is a frozen copy
and a capability added to a card reaches nobody without a backfill.
`043_agent_tools_backfill.up.sql:41-43` already added `create_dashboard` to every
agent that held `create_visualization`:

```sql
   SET allowed_tools = allowed_tools || '{create_dashboard}'
 WHERE allowed_tools <> '{}'
   AND allowed_tools @> '{create_visualization}';
```

So the set of agents that could make a card is exactly the set that can already
make a dashboard, and no tenant loses a capability. The stale
`create_visualization` string left in their arrays names a tool `filterTools`
will simply not find. **Do not write a migration to strip it** — conventions §8
says why: nothing records which rows were touched, so the strip would also take
the name from an agent an administrator ticked by hand.

### `T-D13` Sharing — 2d → **1.75d** · **gated live 2026-09-03, and the gate found a P1 the ticket did not ask about**

> **Every arm the ticket specifies passed** — a forged token, a revoked token,
> injected query-string filters and `refresh=1` all behaved
> ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1s).
>
> **What it did not specify, and what the sitting found: the share payload
> carried the panel SQL.** The route has no session behind it and was returning
> the whole stored `Dashboard`, so an unauthenticated visitor received
> `SELECT … FROM dim_customers` for every panel, plus `source_id` and
> `company_id`. Fixed with `Dashboard.PublicCopy()`; the authenticated route is
> unchanged. The ticket's threat model was **the token** — forgery, expiry,
> revocation, parameter tampering — and it never asked what a *valid* visitor is
> handed.

*(**058**.)*

A public page has no tenant context but must run tenant SQL. The rule: **the
share row carries the company id, and everything the page reads is scoped by what
comes back from that row rather than by anything the request said.** The resolver
never reads a company, source, dashboard or panel id off the request. The token
is the only thing the visitor supplies.

⚠ **Correction — reuse, do not invent.** The original read "Token minting is new
— `internal/auth/` has no share-token file." It has one, and it implements the
design this ticket specifies, line for line. `internal/auth/sharetoken.go`:
`NewShareToken` mints 32 random bytes as 43 base64url characters and returns
`HashShareToken(token)`; the hash is SHA-256 hex; there is no constant-time
compare **because there is nothing to compare** — the lookup is
`WHERE token_hash = $1` on an indexed column. Its docblock already makes this
ticket's KDF argument verbatim: *"the input is 256 uniformly random bits, so a
KDF slows down a dictionary that does not exist while costing 64 MiB on every
page view of a public URL — which is a denial-of-service handed to anyone who can
type a wrong token."*

**Call `auth.NewShareToken` / `auth.HashShareToken`. Write no token code.**
That is where the −0.25d comes from.

`050_report_shares.up.sql` supplies five of the twelve columns below
(`token_hash UNIQUE`, `created_by`, `expires_at`, `revoked_at`, `view_count`,
`last_viewed_at`) and the reason `company_id` is denormalised even though
`document_id` implies it. Copy that header comment's third bullet directly.

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

Genuinely new against `report_shares`: `locked_params`, `allow_filters`,
`password_hash`, `max_refresh_per_hour`. `password_hash` uses Argon2id via
`internal/auth/password.go` — that input is human-chosen and *does* have a
dictionary behind it, which is the opposite case from the token and the reason
the two use different primitives.

⚠ **New finding — the route collides.** The original specified
`GET /share/:token`. **That path is taken.** `cmd/api/router.go:188-192`
registers a keyless `/share` group behind its own rate limiter
(`middleware.NewRateLimiter(d.rdb, 60, 1.0)`), served by
`handlers.NewShareHandler`, and `cmd/api/policy.go:394` lists `"/share/:token":
true` in `unpolicedPaths` with a comment explaining the exemption. Gin will
panic on a conflicting wildcard at that position. Pick a distinct path —
`/share/dashboard/:token` inside the existing group is the smallest change, and
it inherits the rate limiter and the exemption pattern for free. The
`unpolicedPaths` entry is still required, with its own comment; "this route
authenticates nobody" is meant to stay a decision somebody wrote down.

**`locked_params` are locked, never merged.** A dashboard shared with `region`
pinned to Jakarta shows Jakarta, and a visitor who edits the query string still
sees Jakarta, because request parameters on a share are *ignored* rather than
merged. Merging is the obvious implementation and it turns every declared filter
into a dimension a stranger may enumerate.

`?refresh=1` is ignored on a share. A bearer link that can spend a customer's
warehouse without limit is a leaked link that costs money forever.

**Headers.** `no-store` and `noindex` are **not** new — `share_page.go:74-75`
already sets `Cache-Control: private, no-store, max-age=0` and `X-Robots-Tag:
noindex, nofollow, noarchive`, with `share_page_test.go:57-61` asserting both.
Copy them. The **frame policy is** new: a whole-tree search for
`X-Frame-Options` and `frame-ancestors` returns nothing in Go, so this really
would be the first one this product sets. Set `X-Frame-Options: DENY` and
`Content-Security-Policy: frame-ancestors 'none'`, and say in the comment that
the widget's iframe story (`internal/auth/embedkey.go`) is a *different* surface
with its own origin allowlist and is deliberately not generalised here.

**No HTML in Go.** The page is the dashboard's SPA route, the way the report
player's is (`apps/dashboard/src/routes/index.tsx:63-70`, `/s/$token`, outside
the `AppShell`). `ShareHandler`'s docblock argues this at `share_page.go:35-40`
and the argument is unchanged: an HTML template in Go is a second frontend drawn
with a second set of tokens.

**Gate: a written security review of the share path before this ticket ships**,
because it is the only place in the product where an unauthenticated request
causes a query against a customer's production database. Nothing else in this
roadmap carries that property.

---

## Track E — Decommission (2d "backend" → **3d**) · one release after Track D

### `T-D15` Remove Metabase — 1.5d → **2.5d**

All locations re-verified. **Five of the original draft's citations were still
wrong after the first re-verification pass** — see the correction log.

| Delete | Location (verified at `ae66e8f`) |
| --- | --- |
| The client | `internal/metabase/` — `client.go`, `mysql_dsn.go`, `postgres_dsn.go`, `sqlserver_dsn.go` + 2 tests |
| Warehouse sync | `internal/app/metabase_sync.go`; call sites at `internal/app/company_service.go:183-184`, `:227-228`, `:347-348` — **not `:50,124`**, which contain no Metabase reference at all |
| Wiring | `cmd/api/bootstrap.go:28` (import), `:172-177` (client + sync construction, gated on `MetabaseURL` **and** admin email **and** password at `:174`), `:189` (passed to `NewCompanyService`), `:343-344` (`Metabase`, `MetabaseSource`) |
| The reverse proxy | `cmd/api/router.go:296-307` — the whole `if cfg.MetabaseURL != ""` block, **not `:296-300`**, which stops mid-handler |
| Config | `internal/config/config.go:106-111` (five fields) and `:413-418` (five env reads) — **not `:107-109`/`:414-416`**, which omits `MetabaseAdminEmail` and `MetabaseAdminPassword`, the two `bootstrap.go:174` actually gates on. `MetabaseAPIKey` is confirmed dead — declared `:109`, read `:416`, consumed nowhere |
| Repo accessor | `MetabaseDatabaseIDForSource` at `internal/adapters/postgres/connection_repo.go:127` (doc comment `:124`) — and the column also appears at `:18` (SELECT list), `:39` (scan), `:105` (UPDATE), which `T-D16` must handle |
| Metering | `internal/app/usage_service.go:21-22` (pricing fields), `:38-39` (defaults), `:118-135` (`RecordMetabaseCard`, `RecordMetabaseDashboard`) |
| Infra | the `metabase` service (`docker-compose.yml:99-119`), its env blocks (`:193`, `:310`) and volume (`:423`), the init mount (`:13`); `docker/postgres-docker-init-metabase.sql`; `scripts/setup_metabase.sh`; the Makefile targets |
| Dead code | `internal/cache/` |

⚠ **The original scoped this as "2d backend". It is not backend-only.** Six
frontend sites, none of them previously listed:

| Delete / change | Location |
| --- | --- |
| The sidebar dashboard list | `apps/dashboard/src/components/layout/generated-dashboards.tsx` (81 lines) — queries `GET /dashboards`, renders each as `<a href={d.public_url} target="_blank">`, and confirms deletion with `"…It will be removed from Metabase as well."` (`:27`) |
| Metabase link styling in chat | `apps/dashboard/src/features/chat/markdown-renderer.tsx:19-20` — detects `/metabase/public/dashboard/` and `/metabase/public/card/` hrefs and renders them as a primary button |
| Dev proxy | `apps/dashboard/vite.config.ts:72` |
| Prod proxy | `apps/dashboard/nginx.conf:25` |
| Env comment | `apps/dashboard/.env.example:1` |
| Usage labels | `apps/dashboard/src/features/usage/labels.ts:14-15` — **keep these**, per `T-D9`; historical rows still need a label |

⚠ **And it breaks the eval harness, which nothing previously recorded.**
`internal/eval/tenant.go:292-303` has `syncToMetabase`, whose own comment says it
was added because without it "*the three `chart_dashboard` cases were scoring*"
wrong, and `docs/coverage/metric-registry.md` §5 lists "a chart case with no
Metabase running" as one of its 23 failures. Decommissioning Metabase deletes
three golden cases and the harness code that supports them
(`internal/eval/tenant.go:47,89,260,289,292-303`, `internal/eval/score.go`,
`testdata/eval/golden.yaml`). **Replace them, do not just delete them** — the
native dashboard tool needs eval cases at least as much as the Metabase one did,
and `eval-baseline.md` rule 1 (an unmeasured change is an unshipped change)
applies. Coordinate with `T-Q1`, which is rewriting this set anyway.

Keep `saved_dashboards` and its read path through the deprecation window so the
archived list still renders.

> **Check `T-H5` in the hardening roadmap first.** It proposes hardening Metabase
> — its own read-only DSN, a scoped API key, a pinned version. If this
> decommission is going ahead, `T-H5` is wasted work. Only one should be built,
> and `03-security-hardening-roadmap.md` §"What is owed" already lists this as a
> decision the owner must make. **This roadmap's position: build `T-D15`, skip
> `T-H5`** — but that position is not a decision until the owner takes it, and
> `T-H5` is 1.0d that becomes 0 the moment they do.

### `T-D16` Drop the Metabase columns — 0.5d · **half built and gated 2026-09-03; the table's drop is owed one release later**

*(**059** as filed; landed as **073**, and the second half is **074**.)*

> **Status, 2026-09-03.** `metabase_database_id` and its partial unique index are
> dropped by `073_drop_metabase_database_id`, gated up/down/up against the real
> control database (column and index 0/0 → 1/1 → 0/0, `db_connections` intact at
> 5 rows), and a **previous-release binary boots and serves 200 against the new
> schema** — the ordering claim measured rather than asserted.
>
> **The ticket is one migration, and it had to become two.** `saved_dashboards`
> is still read by the release this lands in: the archived-list handler, its two
> routes, and the thread-delete cascade on both `/api` and `/v1`. Those readers
> are removed in the *same* commit as `073`, which is exactly why the table
> cannot go with it — `workspace-context.md` §6 is that during a rolling deploy
> the new schema meets the old binary. Proven rather than reasoned: with the
> table dropped inside a transaction, the statement `SavedDashboardRepo.ListByCompany`
> runs returns `ERROR: relation "saved_dashboards" does not exist`. So the drop
> is **`074_drop_saved_dashboards`, to be landed one release after this one**:
>
> ```sql
> -- 074_drop_saved_dashboards.up.sql
> DROP TABLE IF EXISTS saved_dashboards;
> -- down: recreate per 006_saved_dashboards.up.sql (schema round trips, data does not)
> ```
>
> It is deliberately **not** in `migrations/control/` yet, because `cmd/api`
> applies whatever is in that directory on boot — a migration written early is a
> migration applied early.
>
> **Finding: this ticket's cutover gate names an instrument that cannot answer
> it.** The Cutover section below gates N+2 on *"a recorded check that no
> `/metabase/*` request was served in the preceding 30 days… `internal/apiobs`
> records request rows and is where that number comes from."* It does not.
> `apiobs` is installed as `v1.Use(middleware.RecordAPIRequests(…))`
> (`cmd/api/router.go:271`) and instruments the `/v1` group only, while the proxy
> was `r.Any("/metabase/*path")` on the **root** router — so its traffic was
> never eligible for a row. `api_request_stats` is also empty in this deployment:
> **0 rows, ever.** The evidence this cutover was gated on does not exist and
> cannot be reconstructed after the fact. It is moot here only because T-D15
> already deleted the proxy route, which is a different argument from the one the
> ticket makes.

`ALTER TABLE db_connections DROP COLUMN metabase_database_id` plus its unique
index, and drop `saved_dashboards`. The column is introduced at
`migrations/control/004_metabase_tenant_connections.up.sql:3` with a partial
unique index at `:6-8`.

⚠ **Correction — the "check all three" list was wrong.** The original said the
column "is also read by `internal/eval/tenant.go` and
`internal/domain/connection.go`". `internal/eval/tenant.go` does **not** read the
column — it takes a `metabaseHostPort` string and calls `stack.MetabaseSync`. The
real readers are:

- `internal/domain/connection.go:26-28` — the `MetabaseDatabaseID *int` field
- `internal/adapters/postgres/connection_repo.go:18` (SELECT), `:39` (scan),
  `:105` (UPDATE)
- `internal/app/company_service.go:188`, `:232`, `:347`

**This must land a release after `T-D15`**, not with it: a running binary that
still reads the column would fault on the SELECT at `connection_repo.go:18`,
which is on every connection read. That is `workspace-context.md` §6
("migrations self-apply on API boot… the new schema meets old code") as a
concrete failure rather than a general rule. The down migration re-adds the
column nullable — the data is gone, but the schema round trips, which is what a
down migration is for here.

### Cutover

| Release | State |
| --- | --- |
| N | Native dashboards live. Proxy still registered, Metabase still running, every existing public URL still works. |
| N+1 | Notice in the archived list naming the shutdown date. |
| N+2 | `T-D15` and then `T-D16`. Old URLs 404. |

**Gate N+2 on evidence, not the calendar:** a recorded check that no
`/metabase/*` request was served in the preceding 30 days. `internal/apiobs`
records request rows and is where that number comes from. If one was served,
find out who is still using it first.

**Rollback.** Everything through Track D rolls back by redeploying the previous
image — native dashboards live in tables the old binary never reads, and Metabase
is still running. `T-D15`/`T-D16` are the irreversible step, which is why they
are gated.

---

## Track F — The native dashboard UI (**8d**) · never previously sized

The original draft's §"The frontend is not in this repo" is retracted whole. It
read: *"`apps/dashboard/` contains `dist/`, `node_modules/` and two tsbuildinfo
files. No source. No `.tsx` file exists anywhere in this checkout."* That was an
artifact of a half-applied monorepo migration, not a fact about the repo.

`apps/dashboard/src/` has 105 files across eleven feature directories. This track
is what the previous draft's "2d backend" qualifier was hiding.

**What is already done and must not be redone:**

- **The palette.** `--chart-1` … `--chart-8` are emitted from
  `packages/design-tokens/tokens.json` into
  `apps/dashboard/src/tokens.generated.css:62-69`, and `make palette` gates them
  in CI against greyscale (ΔL\* floor 5), deuteranopia and protanopia (CIE76
  ΔE\*ab floor 12) using Brettel/Viénot/Mollon 1999. A hand-picked series colour
  fails the gate. `docs/coverage/report-charts.md` records that finding a
  compliant green was **impossible** and series 8 had to leave the red-green axis
  entirely — so this is not a palette anyone should re-litigate in a panel
  component.
- **`useChartPalette()`** already reads the eight variables
  (`components/ui/chart.tsx:33-41`, `SERIES_COUNT = 8`). It is not exported;
  exporting it is the first edit of this track.
- **The table shell** — `components/ui/table.tsx` (120 lines), from `T-U11`.
- **`recharts ^2.15.4`** is in the tree, lazy-loaded behind a `React.lazy` at
  `features/usage/overview-tab.tsx:23-25` because it is ~390 kB. **Keep that
  discipline** — a dashboard route that pulls recharts into the initial bundle
  undoes `T-U6`'s six-lazy-chunk result.

**The chart layer must not become a second answer to the questions
`internal/report/chart/` already answered.** `docs/coverage/report-charts.md`
records eight decisions — the category cap of 40 with an "Other" bucket, that the
cap does **not** apply to line charts because an x-axis is a sequence and the
smallest twelve days of a month are not "other days", that series over the cap
are dropped except in a stack where the total must still reconcile, that every
cap writes a sentence into the caption, that bar charts are forced to a zero
baseline and line charts are not, that a flat series still gets a range, and the
two states that are not a chart. A web panel that silently re-decides any of
these produces a PDF and a dashboard that disagree about the same data. **The
normalisation rules belong in one place**, which argues for `T-D4`'s `Resolved`
carrying the already-normalised shape — see [Out of scope](#out-of-scope).

| Ticket | What | Size |
| --- | --- | --- |
| `T-D17` | Generalise the chart primitive: export `useChartPalette`, add `ChartContainer` + `ChartConfig` + a shared tooltip and legend, and line / bar / grouped / stacked / pie / donut / kpi components over the 8-colour palette. `components/ui/chart.tsx` is 127 lines and provides one horizontal bar chart; the container, config and legend layer does not exist. | 3d |
| `T-D18` | Dark-mode chart tokens. `--chart-*` has **no** `.dark` variant — the same eight hexes serve both themes, and series 2 (`#1C3A62`, L\* 24.2) and 7 (`#713F1C`, L\* 32.1) are dark on dark. Needs a second ramp in `tokens.json` and a re-run of `make palette` against it, plus fixing `useChartPalette`'s `useMemo(…, [])`, which reads the variables once at mount and would not re-read on a theme toggle. | 0.5d |
| `T-D19` | The dashboard route: `/dashboards`, `/dashboards/$id`, a feature directory, the 12-column grid, per-panel loading / error / empty states, and the filter bar. No `dashboards` feature or route exists today. | 2.5d |
| `T-D20` | Data plumbing: a `DashboardsResponse` type in `packages/api-types` (there is **none** — `generated-dashboards.tsx:21` hand-declares `{ dashboards: SavedDashboard[] }` inline, which conventions §Frontend calls a review finding), the TanStack Query wiring, and **no refetch in a backgrounded tab** — a minimised dashboard must not bill the tenant. | 1.5d |
| `T-D21` | The share page as an SPA route beside `/s/$token`, reusing `SharePage`'s shape. | 0.5d |

**One request for the whole dashboard**, not one per panel. Twelve requests ×
twenty viewers every 30s is 480 rpm and twelve auth checks for data the server
already gathers in parallel. Per-panel independence lives in the *response*.

**Deferred, with triggers:** sorting and pagination in the panel table need
`@tanstack/react-table`, which is not a dependency and would be the first table
library in the tree — defer until a tenant asks for a table panel over 500 rows,
which is `T-D7`'s cap and therefore the natural trigger. Virtualisation defers
behind the same one.

---

## Re-estimate: old numbers beside new

| Track | Original | After first re-verification | **Now** | Why it moved |
| --- | --- | --- | --- | --- |
| A — query engine | 3.0 | 1.5 | **0.75** | `T-D1` shipped; `T-D2` reassigned to `T-H4`; `T-D3` halved because `metric.Render` exists |
| B — dashboard model | 5.0 | 5.0 | **5.75** | +0.5 `T-D4` (metric-key panel kind), +0.25 `T-D7` (registry resolve branch) |
| C — cache + accountability | 2.0 | 2.0 | **2.25** | +0.25 `T-D9` retention, which cannot inherit `agent_actions`' exemption |
| D — agent surface + sharing | 5.0 | 5.0 | **5.75** | +0.25 `T-D10` policy + docs; **+1.5 `T-D11`** (21 files, MCP contract, eval set); −0.25 `T-D13` (token minting reused) |
| E — decommission | 2.0 "backend" | 2.0 | **3.0** | +1.0 `T-D15`: six frontend sites and the eval harness, neither previously counted |
| **F — the native UI** | *not sized* | *not sized* | **8.0** | The frontend is in the repo. Chart container/legend layer, dark tokens, routes, plumbing, share page |
| **Total** | **~17.0** | ~17.0 | **~25.5** | 17.5 backend + 8.0 frontend |

`T-H4` step 1 (0.25d) is a prerequisite and is **not** counted here — it belongs
to the hardening roadmap.

**Sequencing.** `T-H4` step 1 → Track A → Track B → Track C ∥ Track D →
one release → Track E. Track F can start at Track B's spec freeze and run
alongside C and D; only `T-D20` needs `T-D10`'s routes to exist.

---

## Risks

| # | Risk | Mitigation |
| --- | --- | --- |
| 1 | **A share link is a stored, replayable query against a customer's production database, triggered by a stranger.** Wider than a Metabase public link, which ran on Metabase's own connection with its own limits. | `locked_params`; `allow_filters` false by default; request params ignored, never merged; no `refresh=1`; per-share hourly budget; row caps; 15s deadline; statement validation at save *and* resolve; every miss logged. Security review gates `T-D13`. |
| 2 | **Query load against tenant warehouses.** Refresh × viewers × panels is a pattern this product has never generated, and a customer's replica falling over will look like our fault. | Cache is the default path; singleflight; server-side floor on `refresh_secs`; four concurrent panels. **Instrument in Track C, not later** — the first production week has to produce the number that says whether these defaults are right. |
| 3 | **SQL Server has no read-only transaction.** Recorded as a finding in `03-security-hardening-roadmap.md` and re-verified at `sqlserver/conn.go:33-35`. Dashboards multiply the exposure because a panel re-runs on every view. | `sqlguard.ValidateStatement` at save and at resolve. Consider a stricter threshold for SQL Server panels, and fix the driver under the hardening roadmap rather than this one. |
| 4 | **The agent writes worse dashboards than it wrote cards, silently.** Metabase inferred the chart type and column roles; the spec makes the model state them, so a `Map` naming a column that does not exist is a new failure class — and a chart with the wrong series draws without complaint. | Execute every panel at save; answer a missed mapping with the columns that would have worked; the tool description names this as the most likely failure and says to run the SQL first; `T-D11` returns `row_count` so the fabrication guardrail can ground the turn. |
| 5 | ~~**This roadmap is larger than it looks**~~ → ~~*inverted, it is smaller*~~ → **larger after all, by 50%.** The first re-verification concluded "smaller" from nine shipped capabilities and never re-sized the two tracks it had no visibility into. | The [Re-estimate](#re-estimate-old-numbers-beside-new) table. A capability being shipped shrinks the ticket that builds it and says nothing about the ticket that removes the thing it replaces. |
| 6 | **Planning against a stale tree.** Written against a non-ancestor commit *and* a half-migrated working tree; asserted thirteen absences of which nine were wrong; then a re-verification pass introduced two new wrong claims of its own. | `git merge-base --is-ancestor <commit> main` before writing "verified against". **Search for the capability, not for the symbol name you were about to invent** — that single habit would have caught `metric.ValidateTemplate`, which is the most expensive miss in this document's history. Prefer symbol names to line numbers in prose. |
| 7 | **Two other agents are in this tree.** Migration numbers, `cmd/api/policy.go` and `internal/tools/registry.go` are all single-writer files that this roadmap and the hardening roadmap both touch. | `make migration-next` at write time, not from this document. `docs/AGENTS.md` §6: one agent per app per task, migrations serialized. |

---

## Out of scope

Visual query builder · SQL editor UI · cross-filtering · drill-through ·
multi-select filters · per-viewer row scoping · dashboard folders · alerting on a
panel · emailed dashboards · dashboard versioning · drag-to-rearrange · automatic
conversion of existing Metabase dashboards · table sorting, pagination and
virtualisation (trigger in Track F).

⚠ **Server-side chart rendering and dashboard-to-PDF: re-opened, and the case
for it is now stronger than "possible".** The original declared them out of scope
because *"That plan assumed a chart renderer existed to reuse. It does not."* It
does: `internal/report/chart/` renders seven types today and is wired into PDF
and PPTX.

The argument has changed shape. The original worry was two definitions that
drift. Track F makes that worry concrete: `report-charts.md` documents eight
normalisation decisions (the category cap, the line-chart exemption, the stacked
"Other" band, the caption sentence, the forced zero baseline, the flat-series
range, the no-data panel, the single-point dot), and a recharts panel that
re-decides them silently produces a dashboard and a PDF that disagree about the
same data in front of the same customer. **That argues *for* sharing one
normalisation, not against it.**

Concretely: `T-D4`'s `Resolved` should carry the *already-normalised* series —
caps applied, "Other" bucketed, `Note` text populated — so the browser draws what
the PDF would draw. Whether the Go renderer also produces the PNG is then a
separate and much smaller question. Size it when Track F starts, not now.

The comment at `internal/tools/document/types.go:5` — *"Adding charts later means
extending Section/Sheet types without touching the tool contract"* — predates the
chart package and should be reconciled. **Note it is at
`internal/tools/document/types.go:5`, not `internal/report/types.go:6`**, which
does not exist; `internal/report/` has no top-level Go files, only 16
subpackages.

---

## What is owed

Nothing in this document has been run. It is a read of shipped code and two
read-only commands (`make migration-next`, `git diff --stat`).

**Needs the stack:**

- `T-D6`/`T-D7` — a dashboard saved and resolved against the demo warehouse
  (`postgres://demo:demo@localhost:5433/demo_analytics`), including one panel
  that legitimately times out, to prove eleven panels still render.
- `T-D8` — twenty concurrent opens of one dashboard, counting rows in
  `dashboard_query_log`. The singleflight claim is arithmetic until it is a
  count.
- `T-D13` — the share path, forged and replayed: an expired token, a revoked
  token, a token with edited query-string filters, and a `refresh=1` that must be
  ignored.
- `T-D16` — migrate up, down, up against local Postgres, with a binary from the
  previous release running against the new schema to prove the ordering claim.

**Needs model spend:** `T-D11`'s new tool description, scored on the eval set
against the old two-tool pair. Note every published quality number for this
project is `deepseek/deepseek-v3.2`; a tool-choice rate is model-specific.

**Needs a decision, not code:**

- **Accept or reject decision 2** as recommended (both, boundary at row
  cardinality). One line.
- **Accept or reject the validator ownership call** (`T-H4` owns it; `T-D2`
  deleted). One line. This is the only item that blocks the first day of work.
- **`T-H5` versus `T-D15`.** Hardening Metabase and deleting Metabase are both
  planned; only one should be built. Already listed as owed in
  `03-security-hardening-roadmap.md`.
- **The MCP contract for `write:visualizations`** — re-point the scope or retire
  it (`T-D11`). Retiring it is a breaking change for existing keys.
- **Who owns the `no_chart_wanted` eval category** when `create_visualization`
  disappears — `T-Q1` or `T-D12`.

**Not planned, deliberately:** no dimensional metric registry. It is the right
long-term shape for panels and the wrong next step — `039` deferred it as a
multi-week design problem, and decision 2's boundary is what buys the time to
find out whether it is worth paying for.

---

## Correction log

Two passes of re-verification against `main`. Pass 1 was against `cc06dc7` and
found nine wrong absences; pass 2 is against `ae66e8f` and re-checked everything
pass 1 asserted, including pass 1's own new claims. Everything above is already
corrected; this records what moved so the diff is reviewable.

`cc06dc7..HEAD` touches only `docs/plan/03-…` and `docs/plan/04-…`, so **no
correction below is drift** — all eleven are re-reads of the same tree.

### Pass 1 — 2026-08-11 against `cc06dc7` (retained)

| # | First draft | Corrected to |
| --- | --- | --- |
| 1 | `1115e90` cited as the verification base | Not an ancestor of `main`. A pre-monorepo branch whose work reached `main` by another route. |
| 2 | "the dashboard frontend source is not in this checkout at all" | 105 files under `apps/dashboard/src/`. The checkout was half-migrated. |
| 3 | `T-D1` net-new, 1.5d, "nothing else can start without it" | Shipped under T-06/T-07 with the exact proposed signature. |
| 4 | "There is no metric registry in this repo to route panels through" | `039_metric_definitions` + `internal/app/metric_service.go`. Voided decision 2's premise. |
| 5 | "No chart package anywhere" | `internal/report/chart/`, wired into PDF and PPTX. |
| 6 | "`internal/auth/` is `jwt.go` + `password.go`" | Also `sharetoken.go`, `apikey.go`, `embedkey.go`, `invite.go`. |
| 7 | "There is no audit table in this repo" | `023_agent_actions` + `internal/tools/audit.go`. |
| 8 | "There is no declarative role policy in this repo to update" | `cmd/api/policy.go`; unlisted routes are **denied**. A build-breaker, not a nicety. |
| 9 | "tools wired inline in `cmd/worker/main.go`; there is no registry file" | `internal/tools/registry.go`. |
| 10 | "the last applied migration is `023_thread_slack`"; 024–027 claimed | Those are `047`–`049` on `main`; highest is `055`. |
| 11 | `internal/cache/redis.go:124` | `:126`. |

### Pass 2 — 2026-08-11 against `ae66e8f` (new)

| # | Claim as it stood | Corrected to |
| --- | --- | --- |
| 12 | **"Statement validation ❌ Confirmed absent. No `ValidateStatement`, no `sqlguard` anywhere. `T-D2` is still net-new."** *(pass 1's own claim)* | **Shipped as `metric.ValidateTemplate`** (`internal/metric/template.go:75`) with the literal-scrubbing behaviour `T-D2` proposes as its distinguishing feature, 128 lines of tests, and nine live-gated refusals. Pass 1 searched for the *symbol name the draft was about to invent* rather than for the capability. This is the most expensive miss in the document's history and is why risk 6 now names the habit. |
| 13 | `T-D3` "still net-new", 1d | Half shipped as `metric.Render` (`internal/metric/template.go:29`), including the one-placeholder-per-occurrence decision. → 0.5d. |
| 14 | **"`actor_kind` has no analogue in `agent_actions`"** *(pass 1's own claim, offered as the argument for a separate table)* | It has one — `023_agent_actions.up.sql:25`, `TEXT`, with a comment anticipating new kinds. `T-D9`'s decision now rests on the decorator shape and on the `T-H6` retention conflict instead. |
| 15 | `T-D13` route `GET /share/:token` | **Taken.** `router.go:188-192` + `policy.go:394`, the report player. Gin would panic. Needs a distinct path. |
| 16 | `T-D13` "the first frame policy this product has ever set" (for the whole header set) | True for `X-Frame-Options`/`frame-ancestors` only. `no-store` and `noindex` already ship at `share_page.go:74-75` and are asserted at `share_page_test.go:57-61`. |
| 17 | `T-D3` "`_ "time/tzdata"` must be imported wherever presets resolve" | A blank import is process-wide, and `internal/app` already has two (`scheduled_task_service.go:25`, `watcher_service.go:23`). Defence, not a load-bearing step. |
| 18 | `metabase_sync` call sites at `company_service.go:50,124` | `:183-184`, `:227-228`, `:347-348`. Neither `:50` nor `:124` mentions Metabase. |
| 19 | Config fields `config.go:107-109`, env reads `:414-416` | `:106-111` and `:413-418`. The original range omits `MetabaseAdminEmail` and `MetabaseAdminPassword`, which `bootstrap.go:174` gates on — deleting the cited range alone breaks the build. |
| 20 | Reverse proxy at `router.go:296-300` | `:296-307`. `:300` is mid-handler. |
| 21 | `metabase_database_id` "also read by `internal/eval/tenant.go` and `internal/domain/connection.go`" | `eval/tenant.go` does not read the column; it takes a `metabaseHostPort` string. The real readers are `domain/connection.go:26-28`, `connection_repo.go:18,39,105` and `company_service.go:188,232,347`. The eval harness *is* affected, but through `syncToMetabase` and three `chart_dashboard` golden cases — a bigger problem than the one claimed. |
| 22 | `T-D4` quoted "`types.go:6`" for the Section/Sheet comment | `internal/tools/document/types.go:5`. `internal/report/types.go` does not exist; `internal/report/` has 16 subpackages and no top-level Go files. |
| 23 | `T-D11` scoped as deleting two files; `T-D12` as "check `agents.yaml`" | 21 Go files name `create_visualization`, including `internal/mcpserver/server.go` (a published MCP tool under `write:visualizations`) and the eval golden set. `agent_templates.yaml` names it six times. → `T-D11` 1.5d → 3d. |
| 24 | `T-D15` "2d backend" | Six frontend sites plus the eval harness. → 1.5d → 2.5d, and the "backend" qualifier is dropped. |
| 25 | Risk 5, "inverted — it is *smaller* than it looks" | Larger. ~17d → ~25.5d. |
| 26 | `rolepolicy.go:34-37` for the deny path | `:34-39`. The `return` at `:38` is the load-bearing line; `:37` is a closing brace. |
| 27 | `connection_repo.go:124` for `MetabaseDatabaseIDForSource` | `:127`; `:124` is the doc comment. The column is also touched at `:18`, `:39`, `:105`. |

**Re-verified unchanged in pass 2:** `driver.go:28-47,42,47,80`; `pool.go:23,92`;
`router.go:71,72,94`; `sqlserver/conn.go:33-35`; `domain/dashboard.go:23`;
`create_dashboard.go:36,168`; `thread_cards.go:28`;
`006_saved_dashboards.up.sql:7`; `credits.go:86`; `cache/redis.go:126,294`;
`bootstrap.go:28,172-177,189,343-344`; `usage_service.go:21-22,38-39,118-135`;
`usage.go:14-15`; `004_metabase_tenant_connections.up.sql:3,6-8`;
`audit.go:31,46`; the absence of `internal/dashboard/`; and the 105-file count
under `apps/dashboard/src/`.

---

# Added 2026-08-18 — `T-D24`, from `T-D22`'s edit gate

`T-D22`'s gate ran on 2026-08-18 ([`../coverage/native-dashboards.md`](../coverage/native-dashboards.md)
§4). The tool passed every property it was written to prove. This is the one
thing the sitting found that is a **product decision** rather than a defect, and
it was found by asking for something entirely ordinary.

## `T-D24` A dashboard cannot default to the period it is about — 0.5d–1d
**Repo:** BE (+ FE if the filter UI gains a form) · **Deps:** none · **Priority:** P1 · **Migration:** none

> **Decided and built 2026-08-18 — option 1.** A `date_range` default may now be
> `{"from": "2024-10-01", "to": "2024-12-31"}`, every preset behaves exactly as
> before, and a stored default that is neither is refused by name.
> `internal/dashboard/spec/{window,validate,spec}.go`,
> `internal/dashboard/params.go`, both tool descriptions and
> `create_dashboard`'s filter parser.
>
> **The decision was less open than this ticket knew.** `update_dashboard`'s
> tool description has been promising `{from, to}` since it shipped, its parser
> already built that exact shape, and `TestUpdateResolvesTheThreadsOwnDashboard`
> — the test written from the sentence *"just make it 2024"* — asserts it. All
> of it ran against a fake service, so `spec.Validate` never saw the value it
> would have refused. Option 2 or 3 would have meant deleting a capability the
> product had already told the model it had.
>
> **The FE bullet needed no code, and why is worth recording.** There is no
> filter control anywhere in the dashboard — `dashboard-view.tsx:89` prints
> `applied_filters` as text and nothing else — so a fixed window renders as
> `period: 2024-10-01…2024-12-31` and reads correctly. It also means the gate's
> transcript, where the model told the user to *"change the Period filter when
> you open the dashboard"*, was advice about a control that does not exist. That
> is a separate gap and it belongs with `T-D8`/`T-D9` rather than here.

### Why

The gate's first turn asked for a dashboard *"with the period defaulted to the
fourth quarter of 2024"* — a closed quarter, which is what most dashboards
anybody builds on purpose are about. `create_dashboard` refused it:

```
invalid input: filter "period": a date_range default must be a preset name
(one of last_7d, last_30d, mtd, qtd, ytd, last_month), not a stored date
```

The rule is deliberate and `validate.go:135` calls it *"the rule this whole
ticket turns on"*: a dashboard with dates baked in is a screenshot that ages
silently. That reasoning is right about a *live* dashboard and says nothing
about a dashboard whose subject is a period that has ended.

**What the product does instead is worse than refusing.** The model saved with
`qtd`, which in August 2026 is Q3 2026, where every panel returns nothing. Both
panels warned at save (`dryRun` working exactly as `T-D11`'s defect 2 intended),
and the model relayed it to the user honestly:

> The dashboard opens with a default date filter of "Current Quarter" because
> the system requires preset defaults. To see the Q4 2024 data, simply change
> the **Period** filter to **Oct 1, 2024 – Dec 31, 2024** when you open the
> dashboard.

So the stored artefact draws nothing on open, forever, and the instructions for
making it useful live in a chat message that scrolls away. **It also cost the
turn its budget**: two of the eight iterations went on the refusal and the
retry, which is what left no room for the edit the same turn went on to attempt
— the first half of the `T-Q12` chain.

### The decision

Three options, and the recommendation is the first:

1. **Let a `date_range` default carry an absolute window** — `{"from":
   "2024-10-01", "to": "2024-12-31"}` — while keeping every preset. The
   validator already parses both shapes; it refuses one of them on purpose.
   The ageing argument is answered by the fact that a fixed window is *the
   point* of a closed-period dashboard, and by the filter still being editable
   on the page.
2. **Refuse the request at the tool, with a sentence the model can act on.**
   Cheaper, and honest: *"a dashboard's default window must be relative; ask
   the user whether they want a live dashboard or a report for a fixed
   period"*. Leaves the user without the thing they asked for.
3. **Leave it, and make `dryRun`'s zero-row warning fatal for a brand-new
   dashboard.** Saving something that draws nothing is the actual harm; this
   removes it without adding vocabulary. Also leaves the user without the thing
   they asked for, and turns a working warning into a refusal.

Option 1 is what a user means. Options 2 and 3 are both defensible and both
change a warning into a refusal somewhere.

### Do (if option 1)

- Accept an object default on `KindDateRange` in `spec.Validate`, requiring both
  bounds, `from <= to`, and the same parse the filter's runtime path uses.
- `resolveWindow` (or whatever `params.go` calls it) returns it unchanged rather
  than computing from `now()`.
- `create_dashboard`'s and `update_dashboard`'s tool descriptions gain one
  clause naming the absolute form, because a capability the model cannot see is
  a capability the product does not have.
- The filter UI must render an absolute default as the dates, not as a preset
  name it will not find in its list.

### Acceptance

- [x] A dashboard saved with an absolute default resolves to exactly that window on open, in a later month, with no drift — `TestBindResolvesAClosedWindowDefault` binds the same spec under two clocks eighteen months apart
- [x] Every existing preset behaves identically — the six names, and a spec written before this change, byte-identical
- [x] The gate's own request (*"default the period to Q4 2024"*) produces a dashboard whose panels return rows on first open — proven at the unit level in both tools and the binder; **the turn itself is owed** ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2)
- [x] A malformed absolute default (`to` before `from`, one bound missing, a non-date string) is refused by name, at save **and** at bind — a row can reach the binder from a database an earlier release wrote

### Gate

One turn. Ask for the closed-quarter dashboard the 2026-08-18 gate asked for,
open it without touching the filter, and read the panels: rows, not an empty
grid. Then re-open a dashboard created before the change and confirm its preset
still computes from today.
