# Freshness, the metric layer, and a way to reach people — `T-F1` → `T-F7`

Written 2026-09-11 against `main` @ `16bd0fc`, from
[`../research/07-feature-candidates.md`](../research/07-feature-candidates.md)
§3, which the owner accepted in full. Seven tickets, **~11.0 days — ~8.5
backend, ~2.5 frontend** — across three tracks.

**Why `F`.** `grep -rhoE "T-[A-Z]" docs/` finds `A B C D G H K M N P Q R S U V
X Y`, and `grep -rhoE "\b[A-Z]-[0-9]+\b"` finds bare findings under `B C E O P
Q S`. `F` collides with neither.

**One thing here is not like the others: `T-F1`+`T-F2` close a wrong-answer
class.** Everything else on this roadmap adds a surface. A source that has not
loaded produces an answer that is grounded, cited, non-fabricated and wrong —
and every accuracy mechanism this product owns will vouch for it, because they
all check the answer against the result set and none of them can see that the
result set is a day old. That is why the freshness track is first and why its
gate is the one that matters.

> **§1d of the research — closing the correction loop — is deliberately not
> here.** [`../research/05-hermes-self-learning.md`](../research/05-hermes-self-learning.md)
> §10 priced it: a wrong lesson is durable and self-reinforcing, and the write
> path is an unfenced route from warehouse content to persistent instruction.
> It needs a measurement and a threat model, not a ticket.

---

## 1. Decisions (locked — do not re-litigate inside the tickets)

**1. Freshness is configured, never inferred.** The tenant supplies a
single-value `SELECT` that returns the moment their data was last loaded. Not
guessed from `information_schema`, not `max(created_at)` off a table this
product picked. A guessed "as of" is a confident wrong claim about currency in
a product whose pitch is that its numbers can be trusted, and the guess would
be wrong exactly where warehouses are most interesting — a `dim_` table that
legitimately has not changed in a month.

**2. Empty means unchecked, and unchecked says nothing.** Every source that
exists today has no freshness expression, so every one of them keeps behaving
exactly as it does now. No backfill, no default expression, no migration that
writes data. This is `078`'s rule and the reason it is stated again.

**3. Two thresholds, and they mean different things.** `warn_after` and
`stale_after`, minutes, the shape dbt's `source freshness` settled on. Under
warn: nothing is said. Between: the answer carries a dated line. Over
`stale_after`: the answer carries a notice **the server wrote**, not the model.

**4. The model is told, and the server says it anyway.** The tool payload
carries the freshness block so the model can reason about it and phrase it
naturally. The deterministic notice fires regardless, on the same seam
`CheckFabrication` uses (`internal/guardrails/fabrication.go:91`). This is
`T-16`'s division of labour and its argument: a claim the product's
trustworthiness rests on is not left to a model's discretion.

**5. Never refuse on staleness.** A stale source answers with a caveat. An
operator's ETL being late must not take the product offline, and a refusal
teaches a user that the freshness feature is the thing standing between them
and their answer. `ChatRunner.companyContext`'s rule: context makes an answer
better and is never what makes one possible.

**6. A probe that fails is *unknown*, not *stale*.** A freshness query that
errors, times out, or returns a non-timestamp says nothing at all and is logged
once at `Warn`. Announcing staleness because a probe broke would put a false
caveat on a correct answer, and a caveat users learn to ignore is worse than no
caveat.

**7. Freshness is a property of the source; a metric inherits it.** No
per-metric expression in v1. `metric_definitions` already names a source, so
`query_metric` gets freshness for free and there is one place a tenant
configures it.

**8. The probe is cached and is never on the hot path.** One probe per source
per TTL (default 60s), in-process. A turn that runs five queries against one
source pays for one probe, and a turn that runs none pays for nothing.

**9. Metric coverage is computed, not stored.** `agent_actions` already carries
`tool_name` and `message_id`. Coverage is a `GROUP BY`, so it needs no
migration, cannot drift from the truth, and is retroactive to every turn this
deployment has ever run.

**10. A turn is classified, not a call.** Three buckets — **certified** (the
turn's data tools were `query_metric` only), **ad hoc** (`run_sql` only),
**mixed**. Counting calls would score one turn that ran `query_metric` five
times above five turns that each ran `run_sql` once, which is backwards.

**11. A proposed metric is never auto-promoted, and never shown unproven.** It
is validated by the existing `MetricService.Test` against the real warehouse
before an admin ever sees it, and an admin approves or discards. A wrong metric
that gets certified is worse than no metric: from then on every answer cites it.

**12. Email is delivery, not a channel.** Argentum sends; nothing is received.
Inbound email is a new untrusted-input surface with its own threat model
(`T-H8`'s argument), and it is not in this track.

**13. One `email.Sender` interface, SMTP behind it.** The shape
`whatsapp.Provider` already established. SMTP first because every tenant and
every operator can point it somewhere without an account, and because a
provider SDK can be added behind the same interface later without touching a
caller.

---

## 2. What already exists, and is the reason this is 11 days

| Mechanism | Where | Why this track needs no new version of it |
| --- | --- | --- |
| Bound single-value reads | `Conn.ExecuteReadOnlyParams`, `internal/adapters/db/driver.go:42` | A freshness probe *is* a bound single-value read. Built for `T-06`, all three drivers implement it |
| Per-turn evidence | `guardrails.TurnEvidence`, `fabrication.go:12` | Freshness rides it exactly as `DataRows` does |
| A tracker that reads tool payloads | `agentbudget.Tracker.Observe`, `budget.go:387` | It already parses `rowCount` off a result's JSON. Freshness is one more field, no signature change |
| Payload decorators | `attachProbe` / `attachRedaction`, `internal/tools/run_sql.go` | `attachFreshness` is the third, and the pattern is established |
| Output guards | `CheckFabrication`, `CheckEmptyReply`, `CheckToolCallLeak`, applied at `chat_runner.go:1153`, `:1293`, `:1336` | The staleness notice is a fourth in the same row |
| Metric validation against the warehouse | `MetricService.Test`, `internal/app/metric_service.go` | Runs a candidate without storing it — exactly what `T-F5` needs to never show an unproven proposal |
| Per-tenant harvesting from `agent_actions` | `T-Q8`'s cookbook, `internal/app/` | `T-F5` is the same mine, one step further |
| Watcher scheduling, dry-run, cooldown, delivery | `T-08`/`T-09` | `T-F3` is a new watcher *kind*, not a new scheduler |

---

## 3. The tickets

### Track A — A number you can date (4.0d) · do first

#### `T-F1` A source can say when it was last loaded — **built 2026-09-11**
**Repo:** BE · **Size:** 1.5d · **Deps:** none · **Migration:** `079`

> **Built and unit-gated 2026-09-11.** The record is
> [`../coverage/freshness.md`](../coverage/freshness.md). The settings sheet the
> feature is unusable without was **not in this ticket and should have been** —
> it shipped with `T-F2`, and this line is here rather than a quiet edit because
> the omission is the finding: a ticket that specifies a route and no form
> describes a capability an operator has and a tenant does not.

##### Do
- `079_source_freshness`: three nullable columns on `db_connections` —
  `freshness_sql TEXT`, `freshness_warn_after_mins INT`,
  `freshness_stale_after_mins INT`. Additive, no backfill (decision 2).
- `domain.SourceFreshness` on `domain.DBConnection`, plus the zero value
  meaning unchecked.
- `internal/freshness`: `Probe(ctx, conn, expr) (time.Time, error)` running the
  expression through `ExecuteReadOnlyParams` with no args, requiring **exactly
  one row, one column, parseable as a timestamp**. Anything else is an error,
  and an error is *unknown* (decision 6).
- Validation on save, borrowed from `metric.ValidateTemplate`'s rule: a single
  `SELECT`/`CTE`, no mutating keyword. A freshness expression is tenant-supplied
  SQL that this product runs on a schedule, so it goes through `sqlguard` like
  everything else.
- A `Verdict` type: `Unknown | Fresh | Warn | Stale`, with the observed
  timestamp and the age. One function, pure, fully table-testable.
- `PUT /api/connections/:id/freshness` (admin) and a `POST …/freshness/test`
  that probes without saving — the same Test-then-Save pairing `T-06` built for
  metrics, for the same reason.
- A TTL cache keyed by source id (decision 8).

##### Acceptance
- [x] A source with no expression probes nothing and reports `Unknown`
- [x] An expression returning one timestamp inside `warn_after` is `Fresh`
- [x] Between the thresholds is `Warn`; beyond `stale_after` is `Stale`
- [x] A probe that errors, times out, returns two rows, two columns, or an
      unparseable value is `Unknown` — never `Stale` — eight paths, one table test
- [x] A mutating freshness expression is refused on save
- [x] Two probes inside the TTL run one query — and a *failed* probe is cached
      too, which the ticket did not ask for and a warehouse that is down needs
- [ ] The `079` round-trip against a real Postgres — owed,
      [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §3d

##### Gate
`go test ./internal/freshness/... ./internal/app/... -race`, then `make check`.
The migration round-trip is owed live —
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §3d.

---

#### `T-F2` The turn knows, and the answer says so — **built 2026-09-11**
**Repo:** BE + FE · **Size:** 1.5d · **Deps:** `T-F1` · **Migration:** none

> **Built and unit-gated 2026-09-11**, plus `T-F1`'s settings sheet. The
> dashboard half of this ticket turned out to be nothing: the notice is appended
> to the reply text server-side, so it renders as the markdown it already is.
> What the dashboard actually needed was the *configuration* form, which is why
> it is here.

##### Do
- `attachFreshness` on the `run_sql` payload, beside `attachProbe`. The block
  carries the verdict, the observed timestamp and the age in words.
- Same on `query_metric`, resolved through the metric's source (decision 7).
- `agentbudget.Tracker.Observe` reads the block off the result JSON — the same
  way it reads `rowCount` — into the `Snapshot`, and `TurnEvidence` carries the
  worst verdict any data tool in the turn saw. **Worst, not last**: a turn that
  queried a fresh source and a stale one has a stale answer in it.
- `guardrails.CheckStaleness(reply, TurnEvidence) (string, bool)` — appends a
  dated notice when the turn's worst verdict is `Stale` and the model did not
  already say so. Applied beside the other three output guards.
- One sentence in `bootstrap.SystemPrompt` about reading a freshness block.
  **This is a prompt change, so `make eval` before and after is mandatory**
  (`../agents/verification.md`).
- The dashboard renders the dated line under an answer that carries one.

##### Acceptance
- [x] A `run_sql` result against a source with freshness configured carries the
      block; against one without, the payload is byte-identical to today's —
      asserted by marshalling the same result twice and comparing bytes
- [x] A turn that queried a stale source gets the notice appended, deterministically
- [x] A turn that queried a fresh source gets nothing appended
- [x] A turn that queried two sources, one stale, is treated as stale — five
      orderings, and the *note* travels with the verdict it belongs to
- [x] `Unknown` appends nothing (decision 6)
- [x] The notice is not duplicated when the model already stated the date — and
      a vague hedge with no date does **not** suppress it, which is the second
      half nobody asked for and the reason the first half is safe
- [ ] `make eval` at or above [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md); both numbers pasted — **owed, costs model spend**

##### Gate
`go test ./internal/guardrails/... ./internal/agentbudget/... ./internal/tools/... -race`,
`make check`, `pnpm --filter dashboard lint`, then `make eval` paired.

---

#### `T-F3` A watcher that fires on staleness itself
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-F1` · **Migration:** `080`

##### Do
- A watcher `kind`: `metric` (every existing row, backfilled by the migration's
  default) or `freshness`. A freshness watcher names a source, not a metric.
- Evaluation reuses `T-F1`'s probe and `T-08`'s cooldown, dry-run-before-enable
  and channel delivery unchanged.
- The breach message says which source, when it last loaded, and how late that
  is against the threshold.

##### Acceptance
- [ ] Existing watchers are `kind = metric` after `080` and behave identically
- [ ] A freshness watcher breaches when the source passes `stale_after`
- [ ] It does **not** breach on `Unknown` (decision 6 — a broken probe must not
      page somebody at 03:00)
- [ ] Cooldown suppression works the same as a metric watcher's

##### Out of scope
- Alerting on the freshness *trend*. A source getting slowly later is a real
  signal and it is a second ticket.

---

### Track B — The layer that is supposed to be the moat (4.0d)

#### `T-F4` Metric coverage, measured — **built 2026-09-11**
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** none · **Migration:** none

> **Built and unit-gated 2026-09-11**; the record is
> [`../coverage/metric-coverage.md`](../coverage/metric-coverage.md). **The
> route is admin, not member.** This ticket said member "beside the feedback
> routes `T-Q16` added" and those routes are admin (`policy.go:283-284`) — so
> the ticket was wrong about its own precedent, and matching the precedent was
> the smaller surprise.

##### Why
[`../research/03-gap-analysis.md`](../research/03-gap-analysis.md) §"The
metric-definition gap" argued the registry is the moat. `T-06`/`T-07` built it
and the agent prefers it. **Nothing measures whether it is accumulating**, so
nobody can say whether a tenant's answers are certified or re-derived — which
is the question the market now uses to decide whether an AI answer can be
trusted unattended.

##### Do
- `CoverageService.ForCompany(ctx, companyID, window)` over `agent_actions`,
  grouping by `message_id` and classifying each turn `certified` / `ad_hoc` /
  `mixed` (decision 10). Turns with no data tool are excluded and counted
  separately rather than silently dropped.
- `GET /api/quality/metric-coverage?days=30`, member-readable, beside the
  feedback routes `T-Q16` added.
- A panel on `/quality`: the three counts, the percentage, and the **top ad-hoc
  questions** — which is the part that tells an admin what to define next.

##### Acceptance
- [x] A turn calling only `query_metric` is `certified`; only `run_sql` is
      `ad_hoc`; both is `mixed` — and a mixed turn counts as *covered* in the
      percentage while keeping its own column
- [x] A turn with no data tool is in neither bucket and is reported separately
- [x] Blocked and errored calls do not make a turn certified — `result_status =
      'ok'` throughout
- [ ] The query is scoped `WHERE company_id = $1` and a second tenant's turns
      cannot appear — **the scoping is in the SQL and is not asserted by a
      test**, because the repository has never run against a database. Honest
      status: reviewed, not proven. The arm is one read,
      [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §3d
- [ ] The panel read against real data, which is also the number that decides
      whether `T-F5` is worth building

---

#### `T-F5` Metrics the product proposes
**Repo:** BE + FE · **Size:** 3.0d · **Deps:** `T-F4` · **Migration:** `081`

##### Why
A tenant's registry grows only when an admin writes SQL into a form. `T-Q8`
already mines this deployment's own `agent_actions` and `messages` into
question→SQL pairs behind four quality gates. The same mine, one step further,
says: *this question was asked eleven times this month with five different SQL
shapes, here is the metric nobody has written.*

##### Do
- `metric_proposals`: company, proposed name, description, source, SQL template,
  value column, grain, the evidence (how many turns, over what window, example
  question ids), a `validated_at`, and a status of `pending | approved |
  discarded`.
- A daily sweep clustering `T-Q8`'s harvested pairs by question similarity,
  proposing one metric per cluster above a threshold.
- **Every proposal is run through `MetricService.Test` before it is stored**,
  and one that fails validation is never written (decision 11).
- `GET /api/metric-proposals`, `POST …/:id/approve` (creates the real
  `metric_definition` through the existing service, so validation is one code
  path), `POST …/:id/discard`. Admin.
- A card in Settings → Metrics: the question it came from, the SQL, the test
  result, Approve and Discard.

##### Acceptance
- [ ] A proposal that fails `Test` is never stored
- [ ] Approve creates a metric through `MetricService`, not through the repo
- [ ] Discard is remembered — the sweep does not re-propose it next day
- [ ] Nothing is auto-approved, asserted by a test that runs the whole sweep and
      checks the registry is unchanged
- [ ] Clustering never crosses a tenant

##### Notes for the implementer
- The clustering is the risky half. If question embeddings are unavailable
  (they need the credential `T-Q8` is still waiting on —
  [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2),
  fall back to normalised-question exact match and **say so in the proposal's
  evidence**, rather than silently proposing off a weaker signal.

---

### Track C — A way to reach people (3.0d)

#### `T-F6` Email, and invites that arrive
**Repo:** BE · **Size:** 2.0d · **Deps:** none · **Migration:** none

##### Why
`grep -rin "smtp\|sendgrid\|mailgun\|resend\|postmark"` over `apps/backend`
returns nothing. `T-04`'s invite link is *"handed to the inviting admin"*, so
onboarding a colleague is a copy-paste step in a product that sells automation;
`T-H6`'s export and erasure records have no way to reach the person who asked
for them; and a tenant with no team chat cannot receive a push at all.

##### Do
- `internal/email`: a `Sender` interface (`Send(ctx, Message) error`), an SMTP
  implementation, and a `nopSender` for a deployment with nothing configured —
  which logs once at startup that email is off, rather than failing at the first
  invite (`T-P8`'s embedder finding: a disabled dependency that returns no error
  reads as a working one).
- Config: `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`,
  `SMTP_FROM`, `EMAIL_ENABLED`. Added to `.env.example`, which `T-H6`'s gate
  found to be missing variables the process refuses to boot without.
- One plain-text-plus-HTML template pair, rendered from the tenant's branding
  (`domain.Branding` already exists for reports).
- `T-04`'s invite sends, and the route still returns the link — an email that
  bounces must not make the invite unrecoverable.

##### Acceptance
- [ ] A deployment with no SMTP config boots, logs once, and invites still
      return a link
- [ ] An invite with email on sends exactly one message to the invitee
- [ ] A send failure does not fail the invite
- [ ] No credential appears in any log line — asserted
- [ ] The recipient address is never logged at `Info`

---

#### `T-F7` Watchers and reports arrive by email
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-F6` · **Migration:** `082`

##### Do
- `email` as a watcher delivery channel beside Slack/Discord/Lark/WhatsApp,
  addressed to company members by role or to an explicit list.
- `send_message` gains an email target so the backlog's *Scheduled branded
  report delivery* can land a PDF in an inbox as well as in Lark.

##### Acceptance
- [ ] A watcher configured for email delivers one message per fire, not one per recipient per retry
- [ ] An email delivery failure is recorded on `WatcherDelivery` like every other channel's
- [ ] Delivery to a company with no members with email is a no-op, not an error

---

## 4. Cut order

| # | Cut | Saves | What is lost |
| - | --- | ----- | ------------ |
| 1 | `T-F5` | 3.0d | The registry keeps growing only when an admin writes SQL. `T-F4` still says whether that is a problem |
| 2 | `T-F7` | 1.0d | Email exists but only invites use it |
| 3 | `T-F3` | 1.0d | Staleness is visible in an answer but nobody is told when nobody asked |
| — | **Floor** | **6.0d** | `T-F1`, `T-F2`, `T-F4`, `T-F6` — the wrong-answer class closed, the moat measured, and invites that arrive |

**`T-F1` and `T-F2` are never cut.** They are the only pair here that stops the
product being confidently wrong.

## 5. What needs no ticket

- **Per-source cost of the probe.** One cached query per source per minute
  against a tenant's own warehouse is below the noise floor of a single turn.
- **Freshness on the dashboards.** `T-D13`'s panels read the same sources; once
  `T-F1` exists, a dated line on a panel is a render change, not a feature.
- **Freshness in the MCP surface.** `run_sql` over MCP returns the same payload
  the in-process tool does, so `T-F2`'s block reaches an MCP client for free.
