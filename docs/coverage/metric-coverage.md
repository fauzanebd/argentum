# Metric coverage — is the layer that was supposed to be the moat accumulating?

The plan is
[`../plan/10-freshness-metrics-email-roadmap.md`](../plan/10-freshness-metrics-email-roadmap.md)
Track B (`T-F4`, `T-F5`); the argument is
[`../research/07-feature-candidates.md`](../research/07-feature-candidates.md)
§1b. **One ticket of two is built.**

| Ticket | Status |
| --- | --- |
| `T-F4` Metric coverage, measured | **built 2026-09-11, unit-gated. No migration. Never run against real data — §5** |
| `T-F5` Metrics the product proposes | Not built |

---

## 1. The question, and why nobody could answer it

[`../research/03-gap-analysis.md`](../research/03-gap-analysis.md) §"The
metric-definition gap (most underrated)" made the argument before the registry
existed:

> Every question re-derives its own SQL. Ask "what was revenue last month" twice
> in two threads and you can get two different queries, two different join paths,
> and two different numbers — both defensible, neither authoritative. […] It
> becomes the moat. A competitor can clone the chat UI in a week; they cannot
> clone a customer's accumulated, curated metric layer.

`T-06` built the registry, `T-07` gave the agent `list_metrics` and
`query_metric`, and the system prompt tells it to prefer a definition over
re-deriving. All of that shipped and was gated live on 2026-08-02.

**And then nothing measured whether the layer was accumulating.** A tenant's
registry grows only when an admin sits down and writes SQL into a form, and
this product has never been able to say — for any tenant, over any window —
what share of its answers stood on a definition. The moat was an argument, not
a number.

It is also the question the market has settled on for whether an AI answer can
be trusted unattended: *can it be traced back to a certified definition, or was
it re-derived from raw tables?* (`07-feature-candidates.md` §1b carries the
source.)

## 2. What was built

| Piece | Where |
| --- | --- |
| `domain.MetricCoverage`, `Percent`, `Answered`, `AdHocQuestion` | `internal/domain/metric_coverage.go` |
| The classification query, and the ad-hoc list | `internal/adapters/postgres/metric_coverage_repo.go` |
| Window clamping, best-effort list | `internal/app/metric_coverage_service.go` |
| `GET /api/quality/metric-coverage?days=30` (admin) | `internal/transport/http/handlers/metric_coverage.go` |
| `MetricCoverageResponse` on the wire, generated into `api-types` | `handlers/wire.go`, `tygo.yaml` |
| The panel, above the verdicts on `/quality` | `apps/dashboard/src/features/quality/metric-coverage-panel.tsx` |

**No migration, and that is the design.** `agent_actions` has carried
`tool_name`, `message_id` and `result_status` since `023`, so coverage is a
`GROUP BY`. Three consequences follow: it cannot drift from the truth the way a
counter incremented at write time can; it needs no backfill; and it is
**retroactive to every turn this deployment has ever run**, including the 437
that Phase 3ae counted.

## 3. Three decisions worth the words

**A turn is classified, not a call.** Counting calls would score one turn that
ran `query_metric` five times above five turns that each ran `run_sql` once,
which is backwards — the unit a customer experiences is the answer, not the
query. So each `message_id` falls into exactly one of four buckets: *certified*
(metric calls only), *ad hoc* (`run_sql` only), *mixed*, or *no data tool*.

**A mixed turn counts as covered, and is still shown separately.** It did reach
the registry. But a turn that read a metric and then joined something to it is a
different thing from one that ignored the registry, and folding it into either
neighbour would be a judgement this number should not be making silently.

**A turn that called no data tool is not in the denominator.** A workspace that
uses the product to edit charts and search documents would otherwise read as
badly covered, which says nothing about whether the registry covers the
questions people actually ask. `NoData` is reported beside the others rather
than dropped, because a denominator that quietly excludes a bucket makes the
percentage unreadable.

**Only `result_status = 'ok'` counts.** A blocked or errored `query_metric` call
did not put a certified number in front of anybody, and counting it would let a
tenant whose metrics are all broken read as fully covered — the most
comprehensively wrong answer this panel could give.

## 4. What the build found

**The number and the list it explains had to come from one definition.** The
first version had the count and the "top ad-hoc questions" as two independent
queries, which is how a panel ends up saying "25 ad-hoc turns" above a list
adding up to something else. Both now derive from one `coverageTurns` CTE.

**Nothing in this schema links an answer back to what was asked.** The ad-hoc
list has to resolve the question with the same `LATERAL` the feedback list uses
(`message_feedback_repo.go`), taking the nearest preceding `user` message in the
thread. It is an approximation — a thread where two questions were asked before
either was answered will attribute the wrong one — and that is tolerable here in
a way it would not be in an audit: this is a prompt for an admin deciding what
to define, not a record of anything. `T-Q16` hit the same wall and this is the
second feature to pay for it; a third should probably fix the schema instead.

**Grouping is on the trimmed, lower-cased question — not on an embedding.**
Tempting, since `T-Q8` already embeds questions. But this is the panel that says
*"you asked this eleven times"*, and a grouping a human cannot verify by reading
the list underneath it is worse than a strict one that under-counts. It also
does not need the embedding credential that `T-Q8`'s loop is still waiting on.

**The panel refuses to be read as a measurement below about twenty turns.**
Eight turns at 50% and eighty turns at 50% are different claims, and a screen
that renders them identically invites an admin to reorganise their registry off
a coin flip. Below the threshold the percentage is shown and *labelled*, and the
"define this next" list is withheld — a prompt to go define something off four
turns is the same over-reading in a more expensive form.

**The route is admin, not member.** The roadmap said member "beside the feedback
routes `T-Q16` added"; those routes are admin (`policy.go:283-284`). Matching
them was the smaller surprise, and the list underneath the number quotes the
questions the company's own people have been asking.

**The wire shape had to move into `handlers/wire.go`.** The service's return
embedded `domain.MetricCoverage`, and tygo does not resolve an embedded
cross-package struct — it would have generated `unknown`. The repo already has
the rule (`wire.go`'s package comment: a response that is neither an entity nor
an event lives there), plus a `type_mappings` entry for `domain.AdHocQuestion`.
The hand-written alternative is what shipped two P1s on the embed surface on
2026-09-10.

## 5. What is owed

**The query has never run against real data.** Everything above is unit-proven
against a fake repository; not one of the three SQL statements has touched a
Postgres. This is the arm that matters, and it is cheap:

```sql
-- The number this whole ticket exists to produce, for one tenant, 30 days.
WITH turns AS (
  SELECT message_id,
         count(*) FILTER (WHERE tool_name = 'query_metric' AND result_status = 'ok') AS certified,
         count(*) FILTER (WHERE tool_name = 'run_sql'      AND result_status = 'ok') AS ad_hoc
    FROM agent_actions
   WHERE company_id = $1 AND created_at >= now() - interval '30 days'
     AND message_id IS NOT NULL
   GROUP BY message_id)
SELECT count(*) FILTER (WHERE certified > 0 AND ad_hoc = 0) AS certified,
       count(*) FILTER (WHERE certified = 0 AND ad_hoc > 0) AS ad_hoc,
       count(*) FILTER (WHERE certified > 0 AND ad_hoc > 0) AS mixed,
       count(*) FILTER (WHERE certified = 0 AND ad_hoc = 0) AS no_data
  FROM turns;
```

It is a read, it costs nothing, and **it is the number that decides whether
`T-F5` is worth three days.** Filed in
[`live-gate-backlog.md`](live-gate-backlog.md) §3d beside the other production
reads this environment's permission layer refuses.

**The one hint that already exists** is Phase 3ae's, and it is not this number:
across the 28 *long* turns it sampled, `query_metric` ran 85 times and `run_sql`
19. Those turns were selected for being long, not sampled, so they say nothing
about the other 409 — but they are the only evidence in the repository either
way, and they point at coverage being *high* rather than low. If that holds
across all 437, `T-F5` is solving a problem this deployment does not have, and
the honest thing is to say so before spending three days on it.

**`T-F5` is not built**, so the registry still grows only when an admin writes
SQL into a form. That is Track B's cut #1 and it is deliberately gated on the
measurement above.
