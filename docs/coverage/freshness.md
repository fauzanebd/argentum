# Data freshness — what is built, and what it found

The plan is
[`../plan/10-freshness-metrics-email-roadmap.md`](../plan/10-freshness-metrics-email-roadmap.md)
Track A (`T-F1`→`T-F3`); the argument for building it is
[`../research/07-feature-candidates.md`](../research/07-feature-candidates.md)
§1a. **All three are built.**

| Ticket | Status |
| --- | --- |
| `T-F1` A source can say when it was last loaded | **built 2026-09-11, unit-gated. Migration `079` written, not applied — §5** |
| `T-F2` The turn knows, and the answer says so | **built 2026-09-11, unit-gated. `make eval` owed — §5** |
| `T-F3` A watcher that fires on staleness | **built 2026-09-11, unit-gated. Migration `080`, not applied — §5** |

---

## 1. The gap it closes, and why nothing else covered it

Every accuracy mechanism this product owns compares **the answer** to **the
result set**:

| Guard | What it asks |
| --- | --- |
| `CheckGrounding` (`internal/guardrails/grounding.go:96`) | Does every figure in the reply appear in the rows a tool returned? |
| `CheckFabrication` (`fabrication.go:91`) | Did the turn retrieve anything at all? |
| `CheckEmptyReply`, `CheckToolCallLeak` | Is this text an answer? |
| `T-Q9`'s zero-row note | Did the query match nothing? |
| `T-H4`'s `sqlguard` | Is this statement a read? |

**Not one of them can see that the result set is a day old.** A warehouse whose
nightly load failed at 02:00 answers "revenue yesterday" with a figure that is
grounded, cited, non-fabricated, correctly formatted and wrong — and every
guard vouches for it. The whole apparatus this project built to make numbers
trustworthy is, in that case, working against the reader.

`grep -rin "freshness\|loaded_at\|data_as_of"` over `apps/backend/internal`
returned exactly one hit before this ticket, and it was about how recent a
*watcher's dry run* must be (`watcher_service.go:41`).

**Why it matters on this deployment specifically.** The pilot is a supermarket
chain reading yesterday's sales
([`gelael-pilot.md`](gelael-pilot.md)). A nightly load is the norm, and a
failed one is the most likely wrong answer this product can give. `T-08`'s
watchers make it sharper rather than safer: a watcher that fires on a metric
computed from a source that did not load sends a confident alert about a number
that did not move because nothing moved it.

## 2. What was built

| Piece | Where |
| --- | --- |
| The policy: four verdicts, two thresholds, a pure `Classify` | `internal/freshness/freshness.go` |
| The probe, and the "exactly one row, one column, a timestamp" contract | same, `Probe` / `singleTimestamp` |
| Save-time validation through `sqlguard` | same, `Config.Validate` |
| Storage: three nullable columns, no backfill | `migrations/control/079_source_freshness.up.sql` |
| `domain.SourceFreshness` on `domain.DBConnection` | `internal/domain/connection.go` |
| Per-source TTL cache, tenant check, Test-without-saving | `internal/app/freshness_service.go` |
| `PUT /api/connections/:id/freshness`, `POST …/freshness/test` (admin) | `internal/transport/http/handlers/company.go` |
| The block on the tool payload | `internal/tools/freshness.go`, attached in `run_sql.go` and `metric_tools.go` |
| The turn's worst verdict | `internal/agentbudget/budget.go` — `Observe` / `Snapshot` |
| The deterministic notice | `internal/guardrails/staleness.go`, applied at `chat_runner.go` |
| One prompt rule | `internal/bootstrap/system_prompt.go` |
| The settings sheet, with Test | `apps/dashboard/src/features/settings/source-freshness-sheet.tsx` |

### The shape, borrowed rather than invented

dbt's `source freshness` is the settled form of this problem: a `loaded_at`
expression per source and `warn_after` / `error_after` thresholds over it. The
BI-side habit that pairs with it is a *"data as of 08:42"* line on anything a
human reads, taken from the data's own maximum timestamp rather than from
render time. Both are in
[`../research/07-feature-candidates.md`](../research/07-feature-candidates.md)
§1a with their sources.

## 3. Four decisions worth the words

**Nothing is inferred.** The tenant writes the expression. The tempting
alternative — read `information_schema`, take `max(created_at)` off whichever
table looks like a fact table — produces a *confident wrong claim about
currency*, and it would be wrong exactly where a warehouse is most interesting:
a dimension table that legitimately has not changed in a month is not stale, and
a product that said it was would be teaching its users to disbelieve the
feature.

**`Unknown` is not `Stale`, and that distinction is why the verdict type has
four values rather than being a bool.** A probe that errors, times out, returns
two rows, two columns, a NULL or an unparseable string says **nothing**. Eight
failure paths are pinned by one table test. The reasoning: announcing staleness
because a probe broke puts a false caveat on a correct answer, and a caveat
users learn to ignore is worse than no caveat — which would also destroy the
one case the feature exists for.

**An unknown verdict attaches nothing at all**, so a tool result on a source
nobody has configured is **byte-identical** to what that tool returned before
this ticket. That is asserted, not reviewed:
`TestAnUnknownVerdictLeavesThePayloadUntouched` marshals the same result twice
and compares the bytes. It is what makes the feature safe to have in the binary
on every deployment while nobody has opted in — there is no new sentence for a
model to over-read, and no behaviour to regress.

**The model is told, and the server says it anyway.** The payload carries the
block with guidance, so a well-behaved model phrases it better than a template
can. `CheckStaleness` appends the notice regardless. This is `CheckFabrication`'s
division of labour one layer out: guidance in a tool result is advice a model
may decline to take, and the failure this feature exists to prevent is precisely
the one where the model has no reason to doubt itself.

**It appends; it never replaces, and it never refuses.** A stale answer is still
the answer — the figures are real, they are just older than the reader may
assume. `CheckStaleness` is therefore the gentlest of the four output guards:
the other three replace a reply that must not be sent, and this one adds a
sentence to one that must. An operator's ETL being late is not allowed to take
the product offline.

## 4. What the build found

**The `warn` threshold nearly got the `stale` branch's language.** The first
version of the payload guidance told the model, at `warn`, not to describe a
collapsed recent period as a business result. That is right at three days and
wrong at six hours: applied to ordinary ETL lag it puts a hedge on every answer
from a perfectly healthy warehouse, which is the exact mechanism that makes a
caveat worthless. `warn` now carries a date and nothing else, and
`TestAWarnVerdictIsADateNotAWarning` asserts the stale branch's phrasing is
absent from it.

**A model that already dated its answer must not be made to say it twice, but a
vague hedge is not the same statement.** The duplicate check looks for the
`YYYY-MM-DD` the notice would carry, not for the *subject* of freshness. A reply
saying "the data may be somewhat out of date" has not told the reader *when* the
data is from, which is the whole claim — so the notice still fires. Both
directions are regression cases.

**The probe asks for two rows, not one.** Capped at one, "exactly one row" could
not be distinguished from "the first of many", and an expression accidentally
returning a row per table would have silently reported whichever row the
database happened to order first.

**A warehouse whose clock is ahead reads as fresh, not as negative.** Jakarta is
UTC+7 and a timezone-naive column is read as UTC, so a future load time is not
hypothetical. Age clamps at zero rather than going negative into a sentence
somebody reads.

**The one guess this feature makes is exposed rather than hidden.** A timestamp
with no zone is read as UTC. That is wrong by seven hours for a naive Jakarta
column — so the Test endpoint returns the **parsed instant**, and the settings
sheet prints it. An admin sees an age seven hours out immediately and fixes the
expression with a cast, instead of discovering it from a wrong caveat weeks
later.

**Failures are cached, not just successes.** The first version cached only a
good verdict, which meant a warehouse that was down waited out a connection
timeout on *every tool call* in every turn, in front of a query that was going
to fail anyway. Both are cached for `SOURCE_FRESHNESS_TTL_SECS` (60), and it
also turns a broken expression from one log line per query into one per minute.

**The turn carries the worst verdict, not the last.** A turn that queried a
fresh source and a stale one has a stale answer in it, and reading the last
verdict would have made which caveat the user gets depend on the order the model
happened to call its tools in. Five orderings are a table test.

**The verdict is read independently of the row count.** A query that matched
nothing against a source that has not loaded in three days is exactly the case
where the currency *is* the answer, so `Observe` reads the freshness block
before the data-tool row-count filter can return early.

**The guard runs last, after the empty-reply rescue.** Placed earlier, the
rescue would have written over an answer that already carried the notice — and
the case that would actually have happened is a stale turn that produced
nothing, getting a sentence about having no data with a caveat about currency
stapled to it: the second half of a contradiction.

**Two packages spell the verdicts as string literals**, because
`internal/agentbudget` and `internal/guardrails` both sit below
`internal/freshness` and importing it would cycle.
`internal/app/freshness_contract_test.go` is the pin — it lives in the one
package that imports all three and fails the moment a verdict is renamed without
its readers.

## 4a. `T-F3`, and the two things it refused to invent

A watcher gained a `kind`: `metric` (every row before `080`, backfilled by the
column default) or `freshness`, which names a **source** rather than a metric.
`kind`, not a second table — everything a freshness watcher needs is already on
`watchers` and already correct: the cron, the timezone, the cooldown, the
dedicated thread, the channel list, the dry-run-before-enable rule and the event
history. A second table would duplicate all of it to change the one column that
differs.

**It has no threshold of its own, and refusing to give it one is the decision
the ticket turns on.** The thresholds live on the source, so *one* configuration
decides both what an answer says about currency and when somebody gets told. Two
places to set the same number is two numbers that will disagree, and the
disagreement surfaces as an alert about data the product was happy to quote a
minute earlier. The three condition fields a metric watcher uses are left at
their zero values rather than given plausible defaults — a threshold of `1440`
would read like a setting somebody chose, and the next person to touch this
would wire it up in parallel.

**No model turn, and no budget check.** A metric breach enqueues an agent turn
because *"why did revenue drop"* is a question worth a model call. *"The sales
source has not loaded since Tuesday"* is not a question; it is a fact with one
action attached, so the sentence is composed in Go — immediate, free, and unable
to come out hedged. And because it spends nothing, there is nothing for the
credit check to refuse: **a tenant out of credits is still told their pipeline
is broken**, which is the moment they most need to know.

**A watcher on an unconfigured source is refused at save time.** Such a source
reports `unknown` forever, so the watcher would tick on its cron and never
breach — enabled, green, and structurally incapable of firing, which is the
worst row this table can hold. The message names the fix rather than the fault.

**`Unknown` does not breach**, which is decision 6 carried into the one place it
costs something to hold: a probe that broke at 03:00 must not page anybody, and
an alert that fires on its own instrument failing trains a team to mute it. It
is counted separately from `quiet` on the fire metric, because a watcher whose
probe is broken and one whose source is healthy both look silent and are very
different problems.

**The dry-run is honestly a different thing, and says so.** A metric watcher's
replays five complete periods. A source's load time has exactly one value — now
— and nothing records what it was yesterday, so this reports **one sample**: what
the watcher would do if it fired this instant. It is still worth requiring,
because it proves the probe works, and it **refuses on `unknown`** rather than
passing — a dry-run that vouches for a blind watcher is the refused row arriving
by the other door.

**A watcher cannot change kind.** Its threshold, its window and its whole event
history mean something else afterwards, and the dry-run that vouched for it
vouched for the old subject. The service refuses it, the repository's `UPDATE`
does not carry `kind` either, and the dashboard only offers the choice on
create — three places agreeing rather than one enforcing.

**The generated types caught the frontend half.** Making `metric_id` `omitempty`
turned it optional in `@argentum/api-types`, and `tsc` immediately failed in
three places where the dashboard assumed every watcher has a metric. That is the
whole argument for `make types` in one build: the hand-written version of this
type would have compiled and then rendered `undefined` in a row label.

## 5. What is owed

**`079` has not been applied anywhere.** Three nullable columns, no backfill, no
data write — the cheapest `up` in the tree, and still unproven against a real
Postgres for the same reason `077` and `078` are: the only control-plane
database on this machine is [production](environment-notes.md). Filed in
[`live-gate-backlog.md`](live-gate-backlog.md) §3d.

**`make eval` has not been run either side of the prompt change.** `T-F2` adds a
rule to `bootstrap.SystemPrompt`, and this repository's rule is that a prompt
change owes a paired score
([`../agents/verification.md`](../agents/verification.md)). It costs model spend
and it is **owed, not skipped** — the acceptance box stays unticked. The
prediction, recorded here so it can be checked rather than remembered: **no
movement**, because no source in the eval tenant has a freshness expression, so
no case can produce a block and the added prompt lines are inert text. A score
that *does* move would mean the four added lines changed behaviour on turns
they do not apply to, which is worth knowing.

**Nobody has seen a freshness block on a real turn.** Everything above is
unit-proven. The arm that would settle it is one source with a real expression,
one query, and the block in the tool result — which needs `079` applied first,
so it is the same arm.

**`080` has not been applied either**, and unlike `079` it is not purely
additive: it drops `NOT NULL` on `watchers.metric_id` and adds a CHECK
constraint in its place. Dropping `NOT NULL` widens what the column accepts and
invalidates nothing already in it, so the `up` is safe — but the `down`
**deletes every freshness watcher**, because `metric_id` goes back to `NOT NULL`
and a freshness watcher has none. The alternative, inventing a metric for it,
would bring the row back as a *metric* watcher pointed at something nobody
chose, on a cron, delivering to a channel. The file says so in its own comment.

**No freshness watcher has ever fired.** The fire path, the cooldown, the
`unknown` refusal and the dry-run are all unit-proven against a stubbed prober;
none of it has run against a real source on a real cron.

**The verdict is not recorded anywhere.** A turn that was dated logs one line at
`Info` and writes no row. Deliberate — filing it through `recordBlockedTurn`
would put an annotation into the count an operator reads to find out how often
this product had to *withhold* an answer — but it does mean "how often are we
answering off stale data" is currently a log query rather than a number. Worth a
row on `/quality` if the feature earns its space.
