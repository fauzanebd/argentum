# Feature candidates — what is worth adding next, and what already has a home

Written 2026-09-11 against `main` @ `16bd0fc`, after `T-N1`→`T-N4` landed. The
question asked was *"what can we add to the feature roadmap"*, and the first
job of this document is to **not** answer it with things that already have an
entry, a trigger and an estimate — [`../plan/backlog.md`](../plan/backlog.md)
holds 40-odd of those, and proposing one back is noise dressed as research.

So the method was subtraction. Every `❌` and `🟡` row in
[`../coverage/feature-coverage.md`](../coverage/feature-coverage.md) was
checked against the backlog, the nine roadmaps and the rejected list. What
survives is in §1. What did not, and why, is in §4 — which is the more useful
half for anybody who reads this next and has the same idea.

**No number in this document was measured by running anything.** The
production figures quoted are Phase 3ae's, read on 2026-09-11 from 437 real
turns ([`../coverage/delivery-log.md`](../coverage/delivery-log.md) Phase 3ae).
Every size is a guess and is labelled as one.

---

## 1. The four candidates

### 1a. An answer that says how fresh the data is · **recommended**

**The gap.** This product has no concept of data recency anywhere.
`grep -rin "freshness\|loaded_at\|data_as_of"` over `apps/backend/internal`
returns exactly one hit, and it is about how recent a *watcher's dry-run* must
be (`internal/app/watcher_service.go:41`). Nothing asks when the warehouse was
last loaded, nothing tells the user, and nothing refuses.

**Why the existing accuracy work does not cover it.** `CheckGrounding(reply,
returned []float64)` (`internal/guardrails/grounding.go:96`) checks that every
figure in the answer appears in the rows the query returned.
`CheckFabrication` (`fabrication.go:91`) checks that a turn had evidence at
all. Both are about the *relationship between the answer and the result set*.
Neither can see that the result set is a day old. A nightly ETL that failed at
02:00 produces an answer that is grounded, cited, non-fabricated, and wrong —
and it is wrong in the most expensive direction, because everything this
product built to make numbers trustworthy will vouch for it.

**Why it matters here specifically.** The pilot is a supermarket chain reading
yesterday's sales ([`../coverage/gelael-pilot.md`](../coverage/gelael-pilot.md)).
"Revenue yesterday" off a load that has not run is the single most likely wrong
answer this deployment can give, and `T-08`'s watchers make it worse rather
than better: a watcher that fires on a metric computed from a stale source
sends a confident alert about a number that did not move because nothing was
loaded.

**The shape, and it is mostly assembly.** The standard form outside this repo
is dbt's `source freshness`: a `loaded_at_field` per source and `warn_after` /
`error_after` thresholds, run on a schedule
([dbt](https://www.getdbt.com/), [Datafold](https://www.datafold.com/blog/dbt-source-freshness/)).
The BI-side habit that pairs with it is a *"data as of 08:42"* line on anything
a human reads, taken from the maximum timestamp in the data rather than from
render time ([Basedash](https://www.basedash.com/blog/data-freshness-how-current-your-dashboard-data-really-is)).
Argentum has the pieces for both: `db_connections` is the place for a freshness
expression, `metric_definitions` already carries a source and a window, and
`Conn.ExecuteReadOnlyParams` already runs a bound single-value query — which is
exactly what a freshness probe is.

**Guessed size: 3d.** A freshness expression per connection (optional, empty =
unchecked, so no existing tenant changes behaviour); a probe on the
`query_metric` / `run_sql` path with the result on the turn's evidence; one
sentence in the answer when the data is older than the threshold; and a watcher
kind that fires on staleness itself. The last of those is the part with real
product value and is the part to cut if 3d turns out to be 5.

**Trigger.** It does not need one. A trigger is for a capability a customer may
never want; this is a wrong-answer class in a product whose whole pitch is that
its numbers are trustworthy. If a trigger is wanted anyway: the pilot going
live against a scheduled load.

### 1b. Metric coverage, and metrics the product proposes

**The gap.** [`03-gap-analysis.md`](03-gap-analysis.md) §"The metric-definition
gap (most underrated)" argued that the metric layer is the moat — *"a
competitor can clone the chat UI in a week; they cannot clone a customer's
accumulated, curated metric layer."* `T-06`/`T-07` built the registry and the
agent does prefer it. **Nothing measures whether the layer is actually
accumulating**, and nothing helps it accumulate. A tenant's registry grows only
when an admin sits down and writes SQL into a form.

**What the outside world now calls this.** The market's stated test for whether
an AI answer can be trusted unattended is whether it traces back to a certified
metric definition or re-derives SQL from raw tables
([Cube](https://cube.dev/articles/best-ai-powered-bi-tools-2026)). That is a
number Argentum can already compute and does not: `agent_actions` carries
`tool_name` per turn, so *what fraction of answered turns went through
`query_metric` rather than `run_sql`* is a `GROUP BY`. Phase 3ae's long-turn
tail suggests it is not small — 85 `query_metric` calls against 19 `run_sql` —
but that is 28 turns chosen for being long, not the 437.

**Two features, and the second is the one worth building.**

1. **Coverage on `/quality`** — one number per tenant, next to the feedback
   `T-Q16` already surfaces. Guessed size: **1d**, and most of it is the panel.
2. **Proposed metrics.** `T-Q8` already mines this deployment's own
   `agent_actions` and `messages` into question→SQL worked examples, per
   tenant, behind four quality gates ([`../coverage/agent-quality.md`](../coverage/agent-quality.md) §3).
   The same mine, one step further, produces a *metric definition*: the same
   question asked eleven times this month with five different SQL shapes is a
   metric nobody has written yet. `MetricService.Test` already validates a
   candidate against the warehouse without storing it, so a proposal can be
   proven to run before an admin ever sees it, and the admin's action is
   approve-or-discard rather than authoring. Guessed size: **3d**.

**The honest objection.** The second one learns from ad-hoc SQL the model
wrote, which is the input `T-Q8`'s four gates exist to filter. A proposed
metric that is subtly wrong and then *certified* is worse than no metric,
because from then on every answer cites it. That argues for the approval step
being real — a human reads the SQL and the test result — and against ever
auto-promoting one.

**Trigger.** For coverage: none, it is a query and a panel. For proposals:
coverage coming back low on a tenant who uses the product daily. Measure
before building, which is the same order
[`09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)
§8 argued for.

### 1c. Email, as a thing this product can send

**The gap.** There is no email anywhere.
`grep -rin "smtp\|sendgrid\|mailgun\|resend\|postmark"` over `apps/backend` and
`docs` returns nothing, and the coverage matrix's Channels table has carried
`Email ❌ Not implemented` since it was written.

**What that costs today, in three places that already exist:**

- `T-04`'s team invites have no delivery. The matrix's own row says the link is
  *"handed to the inviting admin"* — so onboarding a colleague is a
  copy-paste-into-WhatsApp step in a product that sells automation.
- `T-H6`'s data export streams NDJSON to whoever called the route. An erasure
  or export under UU PDP has no way to reach the person who asked for it.
- Watchers deliver to Slack, Discord, Lark and WhatsApp, and not to the one
  address every business person has. The tenant who does not run a team chat —
  which is a great many Indonesian SMBs — cannot receive a push at all.

**What it does *not* unblock, stated so nobody over-sells it.** The backlog's
*Scheduled branded report delivery* (1.5d) targets a channel and `T-12a`'s
`send_message` already takes an `attach_document_id`, so that item ships
without email. Email makes it better; it is not the blocker there.

**Guessed size: 2d** for the primitive (one provider behind an interface, the
shape `whatsapp.Provider` already establishes) plus invites, and a day per
consumer after that.

**Trigger.** It has effectively fired: the invite flow is shipped and
incomplete. The reason to hold it anyway is that it is table stakes rather than
differentiation, and this project has never been short of differentiation to
build.

### 1d. Closing the correction loop

`Learning from a correction` is `❌` in the matrix and the row already states
the position: `T-Q16` made a verdict visible to a human, and *"whether anything
automatic should follow is an open trust question"*, argued in
[`05-hermes-self-learning.md`](05-hermes-self-learning.md) §10 against a
product that does close it.

**Nothing new to add, and that is the finding.** That §10 already priced it —
a wrong skill is durable and self-reinforcing, ~30K tokens per review event,
and an unfenced injection path to persistent instruction that Hermes can accept
because it is single-user and we cannot because untrusted warehouse rows reach
the model on every turn. It is listed here only so the next person who notices
the open loop finds the analysis instead of redoing it.

---

## 2. The thing to do before any of this

**Read `create_dashboard`'s `error_text`.** Not a feature; one query, already
written, in [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §3c.

Phase 3ae found the tool failing **7 times for every 2 successes** in long
turns — 14 errors and 4 blocked against 2 ok — and concentrated in exactly the
turns that run longest. That is a shipped, advertised capability failing most
of the time it is used, and it is worth more than anything in §1. The cause is
unknown because reading `error_text` was refused by this environment's
permission layer, so it is a row in the backlog's hygiene table sized `?`.

It is also the discipline this document is trying to follow: the market's
answer to a tool that fails is a *critic agent* that statically reviews
generated SQL ([Querio](https://querio.ai/articles/best-text-to-sql-query-tools-2026-comparison-features-benchmarks)),
and building one here would be a guess. Fourteen error strings would say
whether it is one bad argument shape or fourteen different problems, and the
answer changes what gets built by a lot.

---

## 3. Recommendation

**Build `1a`, data freshness.** It is the only candidate here that closes a
*wrong-answer* class rather than adding a surface, it is 3 guessed days, it
needs no permission and no model spend to build or to gate, and everything it
needs already exists in `db_connections`, `metric_definitions` and
`ExecuteReadOnlyParams`. The pilot is a retailer on a nightly load, which is
the exact deployment where the gap bites first.

Order after that: `1b`'s coverage number (1d, it is a query and a panel, and it
tells you whether `1b`'s second half is worth 3d), then §2's query, then `1c`.

**Do not start `1b`'s proposals or `1d` without a measurement first.** Both
learn from model output, and both are the kind of feature that is very hard to
withdraw once a tenant's registry or an agent's behaviour depends on it.

---

## 4. Checked, and deliberately not proposed

The useful half. Each of these looked like a candidate until it was read.

| Idea | Where it already lives |
| --- | --- |
| Telegram channel | [`../plan/backlog.md`](../plan/backlog.md). Note it is **advertised on the landing page** and not implemented, which makes it a truth-in-marketing item rather than a feature choice |
| Anomaly detection / forecasting | Backlog, *Statistical anomaly detection for watchers* |
| Metric dimensions and drill-down | Backlog, *Metric registry v2* |
| Per-agent user grants | Backlog, with a trigger, and [`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md) §7 records why a room makes it likelier |
| Internal planner + specialist agents | Backlog, *Multi-agent architecture*. Three different things have now been read into this entry; its own note says so |
| Recurring questions ("ask this every Monday") | **Already shipped** — the `schedule_task` tool (`internal/tools/schedule_task.go`) |
| CSV / XLSX export | **Already shipped** — `generate_document` takes PDF, PPTX, XLSX and CSV (`internal/agentbudget/budget.go:537`) |
| Certified-metric provenance on an answer | The registry exists and the agent prefers it (`T-06`/`T-07`). What is missing is the *measurement*, which is §1b |
| BigQuery / Snowflake / ClickHouse drivers | Backlog, 3d each, trigger is a named prospect's warehouse. Building drivers nobody asked for is inventory |
| Google Sheets / REST sources | Backlog, and its note explains why tenant MCP servers are not it |
| SSO, row-level policy, on-prem | Backlog, *Enterprise readiness*, each with a deal-shaped trigger |
| Credit top-up without SQL | Partly the backlog's *Plans, quotas and checkout*; the matrix's `Credit balance 🟡` row is the narrower half |
| Per-message cost attribution | Backlog, 1d, trigger is a disputed bill |
| Error tracking (Sentry) | Backlog hygiene table |
| A critic agent reviewing generated SQL | Premature — §2 |
| Relaxing `run_sql`, dropping topic guardrails, in-house charting | **Rejected**, not deferred — backlog's *Explicitly rejected* table |

---

## 5. What this document did not do

- **No production query.** The two numbers that would sharpen §1b and §2 —
  metric coverage across all 437 turns, and `create_dashboard`'s error strings
  — both need a read against the production control plane, which this
  environment's permission layer refuses
  ([`../coverage/environment-notes.md`](../coverage/environment-notes.md)).
  They are one command each for somebody who can run them.
- **No customer contact.** Every "why it matters" above is argued from this
  repository and from the pilot's shape, not from a tenant saying so. That is
  the same weakness [`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)
  §8 admitted about itself, and it is admitted here for the same reason.
- **No ticket written.** Nothing here is scheduled, and §1's sizes are guesses
  that have not survived contact with the code they would change.
