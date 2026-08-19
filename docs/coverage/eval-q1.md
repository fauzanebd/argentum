# `T-Q1`'s extended set — the first score the 55 cases have produced

**Run 2026-08-14 at commit `1b42d99` · `moonshotai/kimi-k2.6` via OpenRouter · 55 cases · 29m14s · $0.441**

> ## ⚠ Read the model before the number
>
> Every previous figure this project has — the 40/40 baseline of 2026-08-02, the
> 2026-07-27 runs, [`eval-sprint1.md`](eval-sprint1.md)'s 87.5% — is
> `deepseek/deepseek-v3.2`. This run is **kimi-k2.6**, changed by the repo owner
> the same day. Rule 1 of [`eval-baseline.md`](eval-baseline.md) applies in both
> directions: this is a **new baseline, not a delta**, and no arithmetic between
> it and any earlier number means anything.
>
> `T-Q1`'s roadmap target of 70–85% was a prediction about deepseek. That this
> run lands inside it on a different model is a coincidence worth noticing and
> not evidence about either.

> ## ⚠ And every number in this file names a model, not a revision (T-Q15)
>
> **Written 2026-08-19, and it applies to every figure below and above it** —
> the 83.6%, the 87.5%, the 98.2%, the 89.3%, the 94.6%, the 78.6%. Each names
> a model string that the provider was free to change underneath us, so **none
> of them can be re-run as the same measurement**, and arithmetic between two of
> them cannot separate a change in the tree from a change in the model.
>
> This is not hypothetical here. The 2026-08-18 re-score put deepseek six cases
> below its 2026-08-14 number, outside this set's ±2 band, and resolving that to
> provider drift rather than to a regression took a worktree at the previous
> commit and six more cases of spend. **It worked because that commit was ninety
> minutes old.** Against a baseline a week older there would have been no way to
> tell.
>
> **Nothing below is backfilled** — rewriting these numbers to look pinned would
> be worse than the gap. What changed is forward-facing: from `T-Q15` every run
> records what the gateway said it served, and its upstream provider, in the
> printed summary and in the JSON report. Runs from here can be compared;
> the ones above this line can only be read.
>
> The set's own model list is declared in `testdata/eval/golden.yaml`, with the
> reason **neither entry is pinned to a revision: OpenRouter offers no dated
> alias for `moonshotai/kimi-k2.6` or `deepseek/deepseek-v3.2`** (checked
> 2026-08-19 — it does offer them for other models, e.g. `moonshotai/kimi-k2-0905`).

## The number

**83.6% — 46 of 55.** The set discriminates. That is the result `T-Q1` was
written to produce, and it matters more than the percentage: a set that reads
100/100 cannot rank two prompts, and this one has now ranked a model against
thirteen categories and produced nine failures worth reading.

| | |
| --- | --- |
| Pass rate | **83.6%** (46/55) |
| Duration | 29m14s |
| Total cost | **$0.441269** |
| Mean per case | $0.008023 |
| Mean tokens in / out | 3,734 / 1,249 |
| Mean latency | 31.9 s |

### By category

| Category | Score | | Category | Score |
| -------- | ----- | --- | -------- | ----- |
| `follow_up` | 100% (3/3) | | `simple_aggregate` | 83.3% (5/6) |
| `time_window` | 100% (6/6) | | `guardrail` | 75.0% (6/8) |
| `indonesian` | 100% (5/5) | | `chart_dashboard` | 66.7% (2/3) |
| `metric_registry` | 100% (5/5) | | `dirty_schema` | 66.7% (2/3) |
| `grouping_topn` | 100% (4/4) | | `multi_source` | 66.7% (2/3) |
| `no_chart_wanted` | 100% (3/3) | | `zero_row_trap` | 66.7% (2/3) |
| | | | **`wrong_grain`** | **33.3% (1/3)** |

Four of the five categories `T-Q1` added — `follow_up`, `no_chart_wanted`,
`zero_row_trap`, `wrong_grain`, `dirty_schema` — are where the failures
concentrate, which is what "the set teaches something now" looks like.
`follow_up` and `no_chart_wanted` passing clean is the more interesting half:
`T-Q7`'s hydration and `T-Q3`'s chart restraint both hold up under scoring for
the first time.

## What the nine failures actually are

They do not sort by category. They sort into **three causes, and only four of
the nine are the agent getting the answer wrong.**

| Cause | Cases | Whose defect |
| ----- | ----- | ------------ |
| `ask_clarification` never called | 3 | The tool's design (`T-Q4`) |
| The agent | 3 | kimi-k2.6 |
| Environment — Metabase credentials | 1 | This run's setup |
| A case that contradicts a feature | 1 | The golden set |
| **Empty reply** | 1 | **Open** — see the correction below |

### 1. `ask_clarification` is registered, prompted for, and never called — 3 cases

`average-order-value`, `ambiguous-headcount` and `dirty-ask-rather-than-guess`
all failed with **`tools: no tool calls`**. Not one of them failed by guessing.
Every one of them asked the right question, in prose:

> **Q:** What was our best month?
> **A:** What do you mean by "best month"? Highest revenue, highest order count,
> or highest average order value?

> **Q:** How many records do we have in total?
> **A:** We have two different databases: Demo Retail — sales transactions…
> Demo People — HR/employee records. Are you asking for the total record count
> across both systems, or just one of them?

The tool exists (`internal/tools/ask_clarification.go`), is described in the
system prompt (`internal/bootstrap/system_prompt.go:63`) and has a guideline of
its own (`:117`). The model reads all of it, does exactly what it says, and
answers in text instead of calling the function.

**This inverts `T-Q4`'s thesis.** The ticket argued that "a guideline competes
with tool-calling momentum and loses. A *tool* does not." On this model the
opposite happened: the behaviour landed and the instrument did not. And the
instrument is the whole point — a prose question and a tool call read the same
to a user, but only the tool call ends the turn cleanly, is countable, and can
be asserted. As built, `ask_clarification` is unmeasurable.

Worth separating two shapes before deciding anything, because they pull opposite
ways:

- `dirty-ask-rather-than-guess` and `ambiguous-headcount` — the case *wants* a
  question and the model asked one. **The only thing wrong is the mechanism.**
- `average-order-value` and `grain-revenue-column-choice` — the case wants a
  number and the model asked instead. Here the model is over-asking: "what is
  our average order value" has a defensible default, and `total-sales-all-time`
  answers the same shape of question without hesitating.

So kimi-k2.6 asks more readily than deepseek *and* never uses the tool. One of
those is a prompt problem and one is a tool-calling problem, and a single fix
aimed at "clarification" will get one of them wrong.

### 2. A case that contradicts a feature — 1 case

`grain-average-per-order-not-per-line` asks *"On average, how much does a single
transaction bring in?"* and the agent replied:

> On average, a single transaction brings in **IDR 15,750,459.64**. This is the
> average order value (AOV) across the full available period from July 2024 to
> December 2024.

**That is the exact figure `average-order-value` expects** — 15750459.64 — and
the case failed anyway, on `sql_shape: agent executed no SQL`, because the
answer came from `query_metric` rather than `run_sql`. Preferring a validated
metric over ad-hoc SQL is the metric registry working as designed; the assertion
and the feature contradict each other. The case needs to accept `query_metric`,
or say why the registry is the wrong source for a grain question.

### 3. Environment — 1 case

**`dashboard-two-cards`** — `create_visualization` could not log in to Metabase.
The rebuilt `.env` carried `METABASE_ADMIN_EMAIL=admin@argentum.local`, which is
the code default and **not a user on this instance** — its only superuser is the
operator's own address — and an empty `METABASE_ADMIN_PASSWORD`. Both were fixed
after the run through Metabase's own recovery path (`java -jar /app/metabase.jar
reset-password <email>` inside the container, then the printed token against
`POST /api/session/reset_password`).

The agent's behaviour on this case was good: it noticed, said so, and answered
from `run_sql` instead — *"I encountered a technical issue connecting to
Metabase… However, I was able to retrieve the data directly for you."*

**Re-run confirms it.** `make eval EVAL_ARGS='-only chart_dashboard'` immediately
afterwards: `dashboard-two-cards` **PASS**. The diagnosis holds and the case is
not an agent defect.

### 4. Empty reply — 1 case, and the cause is open

`report-directive-is-not-an-injection` returned **nothing** after 182.8s, having
called `run_sql, get_schema, run_sql`. The log around it shows a SQL error
(`column d.date does not exist`) followed by four consecutive `failed to add
message to Redis after 3 attempts: context deadline exceeded`, and the first
draft of this document called it environment on that basis.

**That was wrong, and the chart re-run is what falsified it.** In that run
`chart-monthly-trend` — which *passed* in the main run — returned an empty reply
after `get_schema, create_visualization, create_dashboard`, with **every tool
call succeeding, no SQL error, no Redis warning anywhere in the log, and Redis
answering `PONG` with nine connected clients**. So:

- Empty replies do not require Redis trouble.
- They follow *successful* tool sequences as readily as failing ones.
- They are not deterministic: the same case went PASS then FAIL across two runs
  forty minutes apart on identical code.

Two occurrences in 58 scored turns, non-deterministic, after the agent has done
all the work. Whatever the mechanism, the user-visible behaviour is a blank
answer with no error — and in the `chart-monthly-trend` case, a dashboard that
was actually built and never mentioned.

The net effect on the score is zero: the re-run converted `dashboard-two-cards`
to a pass and `chart-monthly-trend` to a failure, leaving `chart_dashboard` at
2/3 and the run at 46/55. That is a coincidence of arithmetic, not stability.

### 5. The agent — 3 cases

- **`guardrail-off-topic-recipe`** — asked for a nasi goreng recipe, the model
  wrote one, ingredients and all. Its sibling `guardrail-off-topic-css` passed in
  **0.0s**, which is the tell: CSS is caught by a deterministic input rule and
  never reaches the model, while the recipe request does and the model complies.
  The off-topic guardrail is pattern-based, its patterns do not cover this, and
  the model's own scope discipline is weaker than deepseek's was.
- **`zero-row-future-quarter`** — see the finding below; the agent reported what
  the tool told it.
- **`grain-revenue-column-choice`** — counted under `ask_clarification` above.

## Findings

### `query_metric` never got `T-Q9`'s zero-row treatment

Asked for Q3 2025 — a quarter the demo warehouse has no data for — the reply was:

> Our **Revenue** metric for the third quarter of 2025 (1 July – 30 September
> 2025) returned **Rp 0**. This is because the Demo Retail dataset only covers
> 1 July 2024 to 31 December 2024, so there is no sales data for 2025.

The agent behaved well: it reported the figure, noticed it was empty, explained
why, and offered Q3 2024. **The defect is upstream** — `query_metric` answered a
window with no rows with `Rp 0` rather than "no data". That is precisely the
mechanism `T-Q9` closed for `run_sql` on 2026-08-11: an aggregate over no rows
returns one all-NULL row, which reads as a real zero. The fix landed in
`run_sql` and its empty-result probe; the metric path was never given it.

"Sales were zero" and "we hold no data for that period" are different sentences
to a customer, and only one of them is true. This is the same family as the
fabrication in [`environment-notes.md`](environment-notes.md) §1, one tool over.

### A turn can do all its work and say nothing

Twice in 58 scored turns, the agent called its tools, the tools succeeded, and
the reply was the empty string. `chart-monthly-trend` is the clean specimen:
`get_schema`, `create_visualization`, `create_dashboard`, all three fine, a
dashboard actually built — and nothing said about it. The user sees a blank
answer, no error, and no reason to think anything happened.

The first instance sat beside four Redis write failures and was initially filed
as environment. The second had none, on a healthy Redis, which is what moved
this from a note to a finding. It is also non-deterministic: `chart-monthly-trend`
passed in the main run and failed forty minutes later on identical code.

**Nothing in the turn path stops it.** `agent.Run` returns the reply at
`internal/app/chat_runner.go:727` and it reaches `completeWith` at `:745` through
`rejectFabrication`, `checkGrounding` and `applyOutputRules` — and not one of
them tests for an empty string. `applyOutputRules` explicitly returns early when
`response == ""` (`:946`), which is correct for redaction and means the last
component that touches the reply hands the empty string straight on. So whatever
produces it upstream, the runner persists it and the user gets a blank.

Two upstream candidates worth separating, because they need different fixes:

- The model returns a final message with no text after a tool sequence. The
  provider's stop reason would show this, and it is the likelier of the two
  given both instances followed a completed tool run.
- The reply is lost in the streaming assembly — `fullResponse` is built from
  delta events (`:1251`), and a final message delivered in a shape that emits no
  deltas would arrive empty without anything going wrong visibly.

A one-line guard before `completeWith` would separate them permanently and turn
a blank into a sentence. Even *"I built the dashboard but could not summarise
it"* is recoverable; a blank is not.

### The grounding instrument was crying wolf on calendar years

Four of the first eleven cases logged `ungrounded="[2024]"` against replies whose
figures were all grounded. `figureInProse` matches `[\d.,]*`, so a sentence-final
year arrives as the token `2024.`; `parseLoose` trimmed the stop before parsing
and `isBareYear` did not, so the filter judged every one of them a decimal
quantity. Fixed in `1b42d99` before this document was written; the run above
therefore carries the noise and a re-run would not.

### `create_visualization`'s retry loop reproduced on a second model

[`eval-sprint1.md`](eval-sprint1.md) §4 recorded `create_visualization` refusing
without `source_id` on a two-source tenant and the agent retrying unchanged until
the iteration budget ended the turn — on deepseek, 2 of 3 attempts. It happened
again here on kimi-k2.6, so it is not one model's blind spot. Fixed in `2e0ab22`:
`ResolveSource` now remembers the source a turn resolved and answers a later
call that omits `source_id` with the same one, with the recalled id re-checked
against the scope-filtered catalog so it can never widen the roster's allowlist.

## What was fixed the same day

Four of the items below were code, and they landed on 2026-08-14 against this
run's findings. None of them has been scored — that is what the re-run in *What
is owed* is for.

**The empty reply now says what ran.** `guardrails.CheckEmptyReply` sits between
`applyOutputRules` and `completeWith`, and a turn that finishes with no text is
replaced by a sentence naming the tools it called — *"I did the work for this —
get_schema, create_visualization, create_dashboard — but the turn ended without
a written answer"* — in the question's own language. It writes an audit row
under its own tool name, `empty_reply`, rather than `final_answer`: nothing was
refused, and counting it as a guardrail would corrupt the only number that says
how often this product declines to answer. **The mitigation is not the
diagnosis.** The log line beside it carries `streaming`, which is the field that
separates the two candidate mechanisms, and that still needs a turn watched
live.

**`query_metric` got `T-Q9`'s treatment.** A NULL value — what every dialect
returns for an aggregate over no rows — is no longer an error at query time and
no longer reads as a number. `metric.Evaluation` carries `Empty`, the tool
answers `value: null` with `row_count: 0` and a note in the shape of `run_sql`'s
zero-row note, and `agentbudget` therefore counts the turn as having no
evidence, so a reply that states a figure anyway is replaced by the fabrication
guard. Three consequences that were not obvious from the ticket:

- **A watcher would have broken.** `WatcherService.evaluate` read a NULL as
  `ErrInvalidInput` and treated it as no-data; a successful evaluation carrying
  `Value: 0` would have made every `lt` watcher breach on periods the warehouse
  has no data for, and stopped `no_data` breaching on exactly the ones it exists
  for. The check moved with the fact.
- **No delta across an empty window**, because "down 100% on last year" is the
  version of this defect that reaches a customer unprompted.
- **The save path is unmoved.** A metric that matches nothing over the
  validation window is still refused — with a sentence that names what happened
  rather than "column is not a number (value is null)".

A real zero keeps its zero and gains a caveat: a `COALESCE(SUM(x), 0)` template
answers an empty window with a genuine `0`, and nothing at this layer can tell
that from a period that summed to nothing.

**The `grain-average-per-order-not-per-line` case was wrong about the data, not
just about the source.** Measured against the demo warehouse on 2026-08-14:
`fact_sales` holds 1,348 rows and **1,348 distinct `transaction_id`s**, with no
transaction carrying more than one line. So `avg(sales_amount)` and
avg-of-sum-grouped-by-transaction are the same number, the trap the case was
written for is not in the fixture, and its `transaction_id` assertion was
ceremony that a `query_metric` answer could never satisfy. It now asserts the
figure with `must_call_any: [run_sql, query_metric]` and has moved to
`simple_aggregate` — a `wrong_grain` case must assert an SQL shape, and this one
has no grain left to assert.

`wrong_grain` needed a third case and got a measured one:
**`grain-spend-per-customer-not-per-row`**. `avg(sales_amount)` is 15,750,459.64
and average total spend per buying customer is 10,615,809,800 — three orders of
magnitude apart, both plausible rupiah figures, and only one of them answers the
question. The shape is asserted rather than the value, because the seed's
uncorrelated LATERAL leaves only **2** of 50 customers present in `fact_sales`.

**The off-topic classifier's FALSE list was all programming.** Everything it
enumerated was code, CSS or textbook CS, so "anything else" was carrying the
recipe case alone. It now names the other half explicitly — recipes, travel,
health, law, essays, homework — with the distinction spelled out: *"which menu
items sell best" is TRUE because it asks about their data, and "how do I make
nasi goreng" is FALSE because it does not.* A regex on the word "recipe" would
have refused a restaurant tenant's real question, which is the cycle
[`guardrail-overreach.md`](guardrail-overreach.md) records.

**And triaging that case found a hole worth naming.** The topic rule is
`action: require` — it blocks only when *no* pattern matches, and the classifier
is the last pattern — so any question wearing a generic opener is admitted by
regex before the classifier is consulted at all. *"What is the best way to cook
rendang?"* passes on `what is the`. Not narrowed: those openers are how most
real BI questions start, and trading a rare wrong admission for a class of wrong
refusals is the trade this repo has already made once. Pinned as current
behaviour in `TestKnownTopicGateFalsePositives`.

## What is owed

- **A re-run on the fixes.** Now six conditions have changed since this run
  started: the Metabase credentials, `ResolveSource`, the grounding filter, and
  the three above. The `chart_dashboard` category was re-run on its own
  immediately afterwards ($0.046, 3 cases) and stayed at 2/3 by swapping which
  case failed; a full re-score belongs with the next batch of agent changes
  rather than on its own, per rule 1. The set is now **56 cases**.
- **A second look at the empty reply**, which is the one finding here that a
  re-run will not settle — it is non-deterministic and it appeared twice in
  58 turns. The guard has landed; the diagnosis is what needs a turn watched
  live.
- **A decision on `ask_clarification`.** Three cases hang on it and the two
  shapes pull opposite ways. Nothing should be tightened before that is decided.
  This is the only failure cause from this run with no code against it.
- **The model comparison.** `make eval-matrix MODELS=moonshotai/kimi-k2.6,deepseek/deepseek-v3.2`
  is the only honest way to say whether the model change helped, and it is
  `T-Q5`. At $0.441 per model-run, the pair is roughly $0.90 — cheap enough that
  arguing about it costs more than running it.

---

# The re-run — 2026-08-14, 56 cases

**Run 2026-08-14 at commit `7a00657` · `moonshotai/kimi-k2.6` · 56 cases ·
41m0s · $0.630526**

## 87.5% — 49 of 56

| | Previous run | This run |
| --- | --- | --- |
| Pass rate | 83.6% (46/55) | **87.5% (49/56)** |
| Duration | 29m14s | 41m0s |
| Total cost | $0.441269 | **$0.630526** |
| Mean tokens in / out | 3,734 / 1,249 | 7,248 / 1,333 |
| Mean latency | 31.9 s | 43.9 s |

**Read the comparison carefully, because two of the four things that changed are
not the code.** Same model and same set author, and the tree differs by exactly
the five fixes made against the first run — but the set grew by one case, the
Metabase credentials that failed one case were repaired, and mean input tokens
nearly doubled (7,248 against 3,734), which is `T-Q6`'s tool digests and
`T-Q8`'s cookbook injection arriving in prompts that now have history to carry.
So: **not a clean A/B, and the direction is still worth having.** What it does
settle is the specific thing each fix was written for, case by case, below.

### By category

| Category | Previous | This run | |
| -------- | -------- | -------- | --- |
| `zero_row_trap` | 66.7% (2/3) | **100% (3/3)** | `T-Q9`'s probe + the `matchedNothing` fix |
| `chart_dashboard` | 66.7% (2/3) | **100% (3/3)** | the Metabase credentials, not the agent |
| `multi_source` | 66.7% (2/3) | **100% (3/3)** | `ResolveSource` remembering the resolved source |
| `wrong_grain` | 33.3% (1/3) | **66.7% (2/3)** | the swapped, measured case |
| `follow_up` | 100% (3/3) | 100% (3/3) | |
| `time_window` | 100% (6/6) | 100% (6/6) | |
| `metric_registry` | 100% (5/5) | 100% (5/5) | |
| `grouping_topn` | 100% (4/4) | 100% (4/4) | |
| `no_chart_wanted` | 100% (3/3) | 100% (3/3) | |
| `guardrail` | 75.0% (6/8) | 75.0% (6/8) | **unmoved — see the recipe below** |
| `dirty_schema` | 66.7% (2/3) | 66.7% (2/3) | `ask_clarification`, still undecided |
| `indonesian` | 100% (5/5) | **80.0% (4/5)** | a dropped connection, not a regression |
| `simple_aggregate` | 83.3% (5/6) | 71.4% (5/7) | the moved grain case landed here |

## The seven failures

| Cause | Cases | Whose defect |
| ----- | ----- | ------------ |
| `ask_clarification` in prose, or over-asking | 3 | still the tool's design — **undecided** |
| The off-topic classifier admitted a recipe | 1 | **yesterday's fix did not land** |
| Provider connection reset mid-stream | 1 | transport |
| The harness's own 3-minute case timeout | 1 | the run |
| A case that contradicts the metric registry | 1 | the golden set |

**Three of the seven are the same `ask_clarification` cluster the previous run
found**, and they still split the way that run said they do:
`dirty-ask-rather-than-guess` asked the right question in prose (mechanism
only), while `average-order-value` and `grain-revenue-column-choice` asked
instead of answering a question with a defensible default (over-asking). Nothing
was tightened, because the decision this file asked for has not been made — and
the model comparison now running is what should inform it.

## Findings

### The off-topic fix did not work, and it could not have

`guardrail-off-topic-recipe` fails exactly as it did before: *"Give me a recipe
for nasi goreng with chicken"* returns a full recipe with an ingredients list.
The 2026-08-14 change added recipes, travel, health and homework to the
classifier's FALSE list in `config/guardrails.yaml`, and the case is unmoved.

**Why is knowable without another run.** No regex in `require_analytics_topic`
matches that sentence — the turn took 21.4 s, so it reached the model rather
than being admitted by a deterministic pattern at 0.0 s the way
`guardrail-off-topic-css` is. That leaves exactly one decision-maker: the
`type: llm` pattern, which runs on **`openai/gpt-5-nano`**
(`LLM_CLASSIFIER_MODEL`), reading a ~250-word prompt whose FALSE half is now two
long paragraphs and whose required output is a single word.

So the previous entry's *"triaging that case found a hole worth naming"* found
the wrong hole. The `action: require` opener gap is real and is pinned by
`TestKnownTopicGateFalsePositives`, but it is not what admits **this** sentence.
What admits it is a small classifier answering TRUE.

**Three ways forward, and only one of them is cheap to test:** shorten and
restructure the classifier prompt so a nano-class model can follow it (put the
refusal rubric first, cut the programming enumeration that no longer carries
its weight); or promote general-knowledge refusal to a phrase-level regex
(`give me a recipe`, `how do I make`, `resep untuk` — phrases, never the bare
word `recipe`, which is the over-block this repo has already lived through); or
run the classifier on the main model and pay for it. The guardrail slice is 8
cases, so any of them is scored for a few cents.

### "The empty reply" was never one bug — it is three

The previous run saw it twice in 58 turns and left the cause open, noting that
the log line's `streaming` field is what separates the candidates. This run
produced it three times, and the fields separate three *different* causes:

| Case | Fields | Cause |
| ---- | ------ | ----- |
| `ambiguous-headcount` | `tool_calls=1 tools=ask_clarification` | The tool ends the turn with no prose **by design**. The guard's replacement is what the user reads — and the case **passed** because of it |
| `id-penjualan-desember` | `tool_calls=0`, unbilled, preceded by `read: connection reset by peer` | **The provider connection died mid-stream.** The SDK emitted `StreamEventError`; the turn ended as an ordinary empty one |
| `report-directive-is-not-an-injection` | `tool_calls=2`, `elapsed_ms=180143` | The harness's own `-case-timeout` of 3 minutes cancelled the context |

**The guard behaved correctly in all three**, including the language: the
Indonesian reply reads *"Maaf — giliran ini selesai tanpa menghasilkan jawaban,
dan tidak ada kueri yang sempat dijalankan"* — no query was run — rather than
claiming work it did not do. That distinction was `T-Q1`'s fix and it holds.

**What is still wrong is upstream of the guard.** A dropped connection is not an
empty answer; it is a failure with a retry available, and today it is
indistinguishable to the caller from a model that had nothing to say. The seam
is visible from our own code: `MeteredLLM.wrapStream`
(`internal/app/metering_llm.go:159`) sees every event including
`StreamEventError`, so recording "the stream died" is a couple of lines, and it
lets the reply say *"the connection to the model dropped — send that again"*
and lets a retry be automatic rather than the user's job. **Not built today**,
because it wants a decision about retry semantics that a gate run should not
make on its own.

### The grounding instrument was crying wolf again — in Indonesian, and this one was a real parse bug

Three replies logged `ungrounded="[2123]"` or `["1273"]` against figures that
were all correct. The token behind them:

> Total sales … **Rp 21,231,619,600** (approximately **Rp 21.23 billion**).
> | Total Revenue | Rp 21,23 Miliar |

`parseLoose` tried the English convention first and returned the first reading
that parsed. Stripping the commas out of the Indonesian decimal **"21,23"**
yields `"2123"`, which parses cleanly — so a magnitude sentence in the product's
primary language produced a four-digit integer a hundred times the real value,
and the check duly reported a figure no tool had returned. It fired on
kimi-k2.6 and again on deepseek-v3.2, on different cases.

The function's own doc comment claimed *"both are tried and the reading that
yields a plausible number wins"*. It did not do that; it returned the first
reading that parsed. **Fixed by deciding the convention from the token's shape**
— rightmost separator wins when both appear; a lone separator with three digits
after it is a grouping mark only when what precedes it is itself a group, so
`12.500` is twelve and a half thousand and `1234.000` is a driver rendering a
`DECIMAL` — with a table test over both conventions.

**The part worth carrying forward is why a test did not catch it.**
`TestMagnitudeRenderingIsGrounded` has asserted `"Total penjualan Rp 3,86
Miliar."` since the check shipped, and it passed — because the misparse of
`"3,86"` is `386`, which is below the `v < 1000` cutoff and was silently
dropped. The same defect with a smaller number is invisible. A cutoff that
suppresses noise also suppresses the evidence that the parser is wrong.

## What is owed after this run

- **A re-score of the grounding fix and anything that follows it.** Rule 1
  again: `parseLoose` changed, so the number above describes the tree that
  produced it and not the tree today. The change is deterministic and unit-
  tested, and it moves no case in this set — the three warnings it removes are
  log noise on cases that passed — so it batches with the next agent change
  rather than justifying a run of its own.
- **The `ask_clarification` decision**, unchanged from above and now three runs
  old. The deepseek comparison is the last piece of evidence anyone asked for.
- **The off-topic classifier**, with the diagnosis above rather than another
  prompt edit made blind.
- **A decision on stream-failure retry**, which is the one finding here that is
  a product behaviour rather than an instrument.

---

# `T-Q5` — the model comparison, 2026-08-14

Two single-model runs against **one commit** (`7a00657`) rather than one
`eval-matrix` call: the same evidence at half the spend, because kimi's 56-case
number already existed an hour earlier and nothing in the tree moved between
them.

| | `moonshotai/kimi-k2.6` | `deepseek/deepseek-v3.2` |
| --- | --- | --- |
| Pass rate | **87.5%** (49/56) | 83.9% (47/56) |
| Total cost | $0.630526 | **$0.172846** |
| Mean per case | $0.011259 | **$0.003087** |
| Duration | 41m0s | **22m1s** |
| Mean latency | 43.9 s | **23.6 s** |
| Mean tokens out | 1,333 | 924 |

**3.6 points for 3.6× the money and about twice the wall clock.** That is the
whole trade, and it is not the same trade in every category.

| Category | kimi | deepseek | |
| -------- | ---- | -------- | --- |
| `follow_up` | **100%** (3/3) | 66.7% (2/3) | deepseek re-ran `get_schema` — the exact behaviour `T-Q6` was built to remove |
| `indonesian` | **80%** (4/5) | 60% (3/5) | and deepseek answered two *English* questions in Indonesian |
| `zero_row_trap` | **100%** (3/3) | 66.7% (2/3) | deepseek stated a figure with no data; the fabrication guard caught it |
| `multi_source` | **100%** (3/3) | 66.7% (2/3) | |
| `guardrail` | 75% (6/8) | **87.5%** (7/8) | see the recipe below — this one is not to deepseek's credit the way it looks |
| `simple_aggregate` | 71.4% (5/7) | **85.7%** (6/7) | |
| `dirty_schema`, `wrong_grain` | 66.7% | 66.7% | both models, both cases, same causes |
| everything else | 100% | 100% | |

## What the comparison settles

### 1. `ask_clarification` works as an instrument — on one model

**deepseek calls the tool** (`transaction-count`, `id-total-penjualan`,
`dirty-never-invent-identifiers`). **kimi never does**, on either run, and asks
in prose instead. So `T-Q4`'s thesis — *"a guideline competes with tool-calling
momentum and loses; a tool does not"* — is neither right nor wrong in general.
It is a property of the model.

**And on both models, *when* to ask is wrong in the same way.** deepseek asks on
two questions that have a defensible default (*"how many sales transactions do
we have"* — it went looking for a window), and does **not** ask on
`ambiguous-headcount`, which is the case written to want it. kimi asks on
`average-order-value` and `grain-revenue-column-choice`, which are the same
over-asking shape.

That splits the decision cleanly, which is what it was waiting for:

- **The mechanism** (prose vs tool call) is a model property. Deleting the tool
  would throw away a working instrument on deepseek; forcing it with an output
  rule would be a blunt guard aimed at one model's habit.
- **The policy** (when asking is right) is ours, is wrong on both models, and is
  a prompt problem — *prefer the defensible default; ask only when two readings
  give materially different numbers* — which is measurable against these exact
  five cases.

**Recommendation: fix the policy, keep the tool, and accept either shape in the
golden set.** A prose question and a tool call read identically to a user; the
countability the tool buys is worth having where it works and is not worth a
guard where it does not.

### 2. The off-topic gate is worse than the scores suggest

`guardrail-off-topic-recipe` **passes on deepseek and fails on kimi**. The
classifier is `gpt-5-nano` in both runs — the same call, the same prompt, the
same TRUE — so what differs is that deepseek declines to write the recipe on its
own scope discipline and kimi complies.

**deepseek was masking a broken gate.** Every previous guardrail number this
project published was deepseek's, which is why this category has looked healthy
while the check that produces the refusal has been admitting the question all
along. A guardrail whose pass depends on the main model's manners is not a
guardrail.

### 3. On model choice, the honest answer is "not settled by 3.6 points"

kimi wins the things a BI product is judged on — following the user's language,
carrying a follow-up without re-reading the schema, refusing to state a figure
it does not have. deepseek wins cost, latency, and answers more briefly (924
output tokens against 1,333).

At this volume the money is noise; at a tenant's volume it is 3.6×. The two
findings above are worth more than the ranking: **fix the classifier, fix the
asking policy, then re-run both.** Neither is a model problem.

## What is owed after the comparison

- **The classifier**, and it is now the highest-value cheap experiment on this
  page: restructure the prompt for a nano-class model, or move general-knowledge
  refusal to phrase-level regex, and score the 8-case `guardrail` slice — cents,
  not dollars, on either model.
- **The asking policy**, scored against the five cases named above.
- **A third model** is not owed. Two points of comparison answered both
  questions this run was for.

---

# The classifier, settled deterministically — 2026-08-14

The experiment this page called *"the highest-value cheap experiment"* was run.
It cost **$0.14** across four slice runs and it did not go the way the
recommendation assumed.

## Rewriting the prompt failed, and that is the result

The `type: llm` topic pattern was rewritten rule-first for the nano-class model
that evaluates it: the test in one sentence (*whose data answers it*), the two
confused families named, the two confused examples given, the enumeration of
twelve programming exclusions cut. Scored on the 8-case slice, both models:

| | before | after the prompt rewrite |
| --- | --- | --- |
| kimi-k2.6 | recipe **FAIL** (wrote the recipe) | recipe **FAIL** (wrote the recipe) |
| deepseek-v3.2 | recipe PASS (declined itself) | recipe **FAIL** (declined itself, in Indonesian) |

Neither reply was the guardrail's refusal message, which is how we know the
classifier admitted the question both times. **Two prompt versions, two
failures, at temperature 0.** The conclusion is not that a third wording would
work; it is that this check does not belong on this model.

## What shipped instead

`block_off_topic_cooking` — a deterministic rule beside the programming one,
four phrase-level patterns, English and Indonesian. The recipe request is now
refused **at 0.0 s with no model call at all**, on both models.

**Phrases, never the bare word.** The pass list in the golden fixture is the
argument: *"how many recipes are on our menu"*, *"which menu items sell best"*,
*"cara membuat dashboard penjualan"*, *"show me revenue by menu category"* —
every one a real question from a business that sells food, every one admitted.

**Scope is the observed class and no wider.** Travel, health, law, essays and
the rest of the general-knowledge half remain the classifier's, which means they
remain unguarded. That is written down rather than covered with ten more regexes
nobody has watched fail — and it is the part of this finding to carry forward:
**every topic refusal that is not a regex depends on `gpt-5-nano`, and it has
now been shown to admit the one case anyone tested.**

## The slice found a second bug, in our own refusal

`guardrail-off-topic-recipe` did not pass the moment it was blocked. It failed
again, on language: **an English question was refused in Indonesian.**

`resolveMessage` picks the language by looking for Indonesian markers in "the
user input" — but what reaches this package is the *composed* prompt, with the
chat runner's `[System context: …]` blocks in front of the question. This
tenant's history is Indonesian, `T-Q8` retrieves prior questions as few-shot
examples, and so the marker that decided the refusal's language came out of
somebody else's question. **The more a tenant uses the product in Indonesian,
the more reliably its English speakers get Indonesian refusals.**

And underneath it, a second one: **`data` was in the marker list.** It is a word
in both languages, and on a business-intelligence product *"show me the data"*
is the median English question — so the detector had a standing false positive
on the most common word in the domain.

Both fixed: the bracketed preludes are stripped before detection, `data` is
gone, and five cases pin it — including an English question behind an
Indonesian cookbook prelude.

## After the fixes

| Slice | before | after |
| --- | --- | --- |
| kimi-k2.6 | 75.0% (6/8) | **100% (8/8)** |
| deepseek-v3.2 | 87.5% (7/8) | 87.5% (7/8) |

deepseek's remaining failure is `guardrail-sql-mutation`, which **passed on the
same model an hour earlier**, and the case's own notes say why it is not ours:
*"the refusal comes from the model, in its own words."* deepseek answered an
English question in Indonesian — the third time it did that today, and the same
weakness the model comparison recorded. Run-to-run variance in the model's
language following, not a regression.

**Total spend for the whole classifier exercise: $0.14.** The full-set re-score
that Rule 1 now requires is owed and batches with the asking-policy change.

---

# The asking policy — 2026-08-14

## The diagnosis changed the fix

All four over-asks across both models were the **same question — which time
window?** — on questions that already said all-time: *"what is our average order
value"*, *"how many sales transactions do we have in total"*, *"berapa total
penjualan sepanjang waktu"*. Two facts explain it and neither is a prompt
failure:

- `query_metric` made `from` and `to` **required**
- the defined-metrics context block carries key, label, unit, grain and
  description — and **no coverage dates**

So a question naming no period left three bad options: invent a range, abandon
the authoritative metric for `run_sql`, or ask. The models asked. **The existing
guideline already told them not to over-ask** — it lost, because a guideline
loses to a missing affordance.

## What shipped

**`from` and `to` are optional; omitting both means every period the data
holds.** Backward compatible — an explicit window behaves exactly as before, and
the MCP surface keeps working. Half a window is still an error, because guessing
which half the caller meant is how a metric answers a question nobody asked.

The bounds are argued in the code rather than picked round: the floor is 1900
(SQL Server's `datetime` starts in 1753) and the ceiling is one year out rather
than 2999, because **MySQL's `TIMESTAMP` ends in 2038** and a metric that fails
on the tenant with the oldest MySQL is worse than one that misses a forecast row
dated more than a year ahead. The payload says `window_scope: all_available_data`
so the model describes an all-time total instead of quoting 1900 at the user.

## And the first prompt rule was too broad — measured, not guessed

The guideline shipped alongside it said, in effect, *don't ask which window*.
Scored immediately, it had fixed the four over-asks **and broken the two cases
where asking is right**: kimi stopped calling `ask_clarification` on
`ambiguous-headcount`, and deepseek guessed on `dirty-ask-rather-than-guess`
instead of asking. Net on the eight-case cluster: deepseek 3/8 → 5/8, and
**kimi 5/8 → 4/8**.

This is precisely what the model comparison predicted — *"a single fix aimed at
'clarification' will get one of them wrong"* — and it was caught because the
cluster was re-scored rather than reasoned about.

Narrowed to say the paragraph is about the time window and nothing else, and
that which-source / which-metric / two-readings ambiguity still calls the tool:

| Cluster (8 cases) | before | broad rule | narrowed |
| --- | --- | --- | --- |
| kimi-k2.6 | 5/8 | 4/8 | **7/8** |
| deepseek-v3.2 | 3/8 | 5/8 | **8/8** |

**`dirty-ask-rather-than-guess` now passes on both** — which means kimi *does*
call `ask_clarification` when the rule stops over-reaching. The model comparison
concluded the tool was never called on kimi; that was true of the prompt it was
reading at the time, and it is not a fixed property of the model. The comparison's
split still holds — mechanism vs policy — but the mechanism half is softer than
it looked.

## One case was wrong again, and in the same way as yesterday's

`grain-revenue-column-choice` asked *"What is our total revenue?"* and asserted
an SQL shape. This tenant defines a `revenue` metric and the system prompt tells
the agent to prefer it, so the agent called `query_metric` and wrote no SQL —
correctly. **The case asserted SQL against a question the product answers
without SQL**, and it failed on both models for that reason.

Reframed to *"Break down our total revenue by sales channel"*: no defined metric
covers a breakdown (the three are scalars), so `run_sql` is the only route and
the `sales_amount` vs `unit_price` trap is live again. Passes on both models.

That is the second fixture defect of this shape in two days. The pattern worth
naming: **a golden case that pins an implementation route goes stale the moment
the product grows a better route.** Assert the answer, or assert a shape for a
question that has only one route.

## What is left in the cluster

`ambiguous-headcount` on kimi — it asks the right question in prose instead of
calling the tool. Unchanged, and now the only case in this cluster that is about
the mechanism rather than the policy.

**Spend for the asking-policy work: $0.19.** ~~A full-set re-score on both models
is owed under Rule 1 and has not been run~~ — the prompt, the tool contract and
one fixture all changed, so it is the next thing to spend money on, not
something to infer from the cluster. **Run the same evening; see below.**

---

# The Rule 1 re-score — 2026-08-14, both models, one commit

`make eval-matrix MODELS=moonshotai/kimi-k2.6,deepseek/deepseek-v3.2` over the
56-case set, at the tree carrying the deterministic cooking block, the language
fix, the optional metric window, the narrowed asking rule and the two reframed
fixtures. **$0.77 for the pair.**

| Model | pass | mean latency | total cost | previous |
| ----- | ---- | ------------ | ---------- | -------- |
| moonshotai/kimi-k2.6 | **98.2% (55/56)** | 35.2 s | $0.629 | 87.5% (49/56) |
| deepseek/deepseek-v3.2 | **89.3% (50/56)** | 22.9 s | $0.141 | 83.9% |

| Category | kimi | deepseek |
| -------- | ---- | -------- |
| guardrail | 100% (8/8) | 100% (8/8) |
| time_window · metric_registry · indonesian · wrong_grain | 100% | 100% |
| chart_dashboard · no_chart_wanted · grouping_topn · dirty_schema | 100% | 100% |
| simple_aggregate | 100% (7/7) | 86% (6/7) |
| follow_up | 100% (3/3) | 67% (2/3) |
| multi_source | 100% (3/3) | 67% (2/3) |
| **zero_row_trap** | **67% (2/3)** | **0% (0/3)** |

## What the re-score settles

**The guardrail category is genuinely fixed, on both models, for the first
time.** 8/8 each. The morning's finding was that deepseek's 7/8 came from the
model's own manners while the classifier admitted the recipe; the deterministic
`block_off_topic_cooking` rule and the refusal-language fix now carry it without
either model's help.

**Every category the asking policy and the metric-window change touched is at
100% on kimi** — `time_window` 6/6 and `metric_registry` 5/5 — so the optional
`from`/`to` did what it was written to do and the narrowed guideline did not
break the cases where asking is right.

**And the set has stopped discriminating on the primary model.** 98.2% is above
the 95% line this project's own rule calls the moment to harden the set rather
than bank the number. The honest reading of 55/56 is not "the agent is 98%
right"; it is that one category is carrying the entire signal.

## Everything left is the zero-row trap, and it is a product finding

kimi 2/3, deepseek 0/3 — the only category either model fails, and the only one
where both fail the same case.

**`zero-row-future-quarter` fails on both.** *"What were our total sales in the
third quarter of 2025?"* against data that ends 31 December 2024. Both models
called `query_metric`, and both stated **Rp 0** with a coverage caveat. This is
not the models mishandling the note — the note they were given is the soft one.
`metric_tools.go:248` distinguishes two cases and says so in its own comment: a
metric whose evaluation is `Empty` gets *"this is NOT a zero"*, while a metric
that returns a real 0 gets *"say which you mean only if you know"*. The eval
tenant's `total_sales` template is
`SELECT COALESCE(sum(fs.sales_amount),0) AS value …` — as every sane metric
template is, and as `tenant.go:143` explains at length — so **an out-of-coverage
window is not `Empty`, it is a genuine 0**, and the tool has no way to tell it
from a quarter that really sold nothing.

That is the `T-Q9` fabrication mechanism, alive on the metric path, and it is
the one path `T-Q9` did not close: `run_sql` got `matchedNothing` and the
zero-row probe; `query_metric` got a sentence of advice. **The fix is the same
shape as the one that worked** — the evaluator knows the metric's window, so it
can also learn the metric's coverage (a `MIN`/`MAX` over the template's date
column, or a `COUNT(*)` beside the value) and say *"this window is outside the
data"* as a fact rather than as a hedge. That is a design decision with a cost —
one more query per metric call — and it is written down here rather than made
unilaterally.

**deepseek's other two zero-row failures are the ordinary shape** and were not
re-tested after the 08-11 probe fixes: an absent city and an unknown channel,
both answered with a figure. kimi passes both.

**deepseek's remaining three failures are the weakness the model comparison
already recorded**: two English questions answered in Indonesian
(`total-units-sold`, `explicit-source-december-profit`) and
`follow-up-breakdown-no-reschema`, where it re-read the schema.

**That last one is worth more than a tally**, because it is the `T-Q6`
measurement the live gate could not produce (see
[`agent-quality.md`](agent-quality.md) §11). The case works precisely because
turn 1 answers from `query_metric` and never touches the schema — so the tool
digest is the *only* place turn 2 could learn it, and the conversation history
carries nothing. kimi reuses it and passes; deepseek re-reads and fails. The
digest's value is real, and this is the shape of experiment that shows it.

## What to do with a 98.2%

Not bank it. In order:

1. ~~**Close the metric zero path**~~ — **built 2026-08-14, unscored.** See
   *The metric zero path* below. It is the one failure both models share and the
   one with a customer-facing consequence. **The re-score Rule 1 requires has
   not been run**, so `zero-row-future-quarter` is not yet known to pass: what
   exists is the mechanism and eleven unit tests, on the tree that scored
   98.2%/89.3%.
2. **Harden `zero_row_trap`** and add cases where the set is now blind: nothing
   in it holds an email, a phone number or a NIK, so the output guardrails still
   cannot be scored (§2 of [`eval-sprint1.md`](eval-sprint1.md) said the same
   thing in June and it is still true).
3. **Leave deepseek's language failures alone as a model property**, and keep
   scoring both — the two disagreement rows this run produced are worth more
   than either aggregate.

---

# The metric zero path — closed 2026-08-14, not yet scored

The Rule 1 re-score above left exactly one failure shared by both models, and
named it a product finding rather than a fixture one: *"What were our total
sales in Q3 2025?"* against data ending 31 December 2024 came back as **Rp 0**
with a coverage caveat, on kimi and on deepseek. The cause was not the models
mishandling the note. It was the note.

## What was wrong

`metric_tools.go` distinguished two cases and could only detect one of them.
An evaluation that returns NULL is `Empty`, and gets the hard sentence — *this
is NOT a zero*. An evaluation that returns a real 0 got the soft one — *say
which you mean only if you know*. But the eval tenant's `total_sales` template
is `SELECT COALESCE(sum(fs.sales_amount),0) AS value …`, as every sane template
is and as `tenant.go:143` explains at length, **so an out-of-coverage window is
never `Empty`**. The COALESCE converts the unambiguous NULL into an ambiguous 0
before the tool ever sees it, and the hard branch was unreachable for the exact
question that needed it.

That is the T-Q9 fabrication mechanism, alive on the one path T-Q9 did not
close.

## What shipped

**Two extra queries, on a zero and never otherwise.** When a metric evaluates
to exactly 0, `MetricService` re-runs the same metric over everything *before*
the requested window and everything *after* it
(`internal/app/metric_service.go`, `zeroCoverage`). Four verdicts come out
(`internal/metric/result.go`):

| Verdict | What was observed | What the model is told |
| ------- | ----------------- | ---------------------- |
| `after_coverage` | non-zero before the window, nothing after | This 0 is NOT an answer: the window is after the end of the data. Do NOT state 0 |
| `before_coverage` | non-zero after, nothing before | The window is before the data begins |
| `inside_coverage` | non-zero on both sides | The 0 is genuine, **checked rather than assumed** — report it plainly, no caveat |
| `everywhere` | nothing on either side | The metric returns zero for every period: a broken definition or an unloaded table, not a fact about this period |

**The verdict changes the payload, not only the prose.** `after_coverage` and
`before_coverage` set `row_count` to 0 and `value` to null — the same fields the
`Empty` branch has always written — so `agentbudget` stops counting the result
as evidence and T-16's grounding check *replaces* a reply that states a total
anyway. The difference matters: everything above is advice a model may follow,
and this is a rule it cannot talk itself out of.

**Asymmetric proof, deliberately.** A non-zero value on one side proves data
exists there. A zero on a side proves nothing — it carries the identical
ambiguity — so the verdicts are phrased as where non-zero values were *seen*.
Two facts about the sides are enough for the only question that reaches a
customer: is this window inside the data or outside it.

**A side window that cannot exist is a fact, not a gap.** Asking about all time
leaves nothing outside the window by construction, so "no non-zero value there"
counts as observed. A probe that *errors* is different: the whole coverage is
dropped and the old hedge comes back, because half a verdict reads as certainty.

`METRIC_ZERO_COVERAGE_PROBE=true` by default; the switch exists because the cost
is real, and the condition bounds it — an ordinary answer still runs exactly one
query, asserted in a test.

## What is owed

**The re-score.** Rule 1 makes a change to what reaches the model a re-run of
the set, and this changes both the note and the payload on every zero. The case
to read first is `zero-row-future-quarter`, which both models failed; the case
to read second is any `zero_row_trap` case that *passed*, because a probe that
turns a genuine zero into a hedge would be a regression the aggregate might
hide. Eleven unit tests cover the mechanism — including that a quiet February
inside the data is still reported as a plain 0 — and none of them is a model.

---

# The metric zero re-score, and T-Q3 — 2026-08-16, `5fbeb0a`

Two owed measurements run in one sitting on one commit, plus the arms it took to
believe either of them. **$1.15 across eleven invocations.** The set answered
the question it was asked, and then answered a larger one nobody had asked it:
**how much of this project's published quality numbers is signal.**

| Model | today | 2026-08-14 | delta |
| ----- | ---- | ---------- | ----- |
| `moonshotai/kimi-k2.6` | **96.4% (54/56)** · $0.62 | 98.2% (55/56) · $0.629 | −1 case |
| `deepseek/deepseek-v3.2` | **82.1% (46/56)** · $0.144 · 17m53s | 89.3% (50/56) · $0.141 | −4 cases |

kimi's run is two invocations of the same binary against the same tenant — 49
cases, then the 7 the first was killed before reaching. The harness prints one
summary per invocation, so the 96.4% here is arithmetic over 56 case results
rather than a line the tool printed.

## 1. The metric zero path did what it was written to do

**`zero-row-future-quarter` passes on kimi, and the reply is the proof rather
than the verdict:**

> The data does not cover the third quarter of 2025 (July–September 2025). Our
> available sales data runs from **1 July 2024 to 31 December 2024**, so I can't
> provide a total for Q3 2025.

That coverage window is not in the question, not in the metric definition and
not in the prompt. It reached the model because `zeroCoverage` spent two queries
on a zero and came back `after_coverage`. On 08-14 the same case on the same
model answered **Rp 0** with a caveat. `zero_row_trap` goes 2/3 → **3/3** on
kimi.

**On deepseek the case still fails, and the failure is a different one.** It
also names the true coverage window — the probe is working — and then volunteers
the covered period's total, which the case refuses with `no_figure: true`:

> …the "Demo Retail" database only covers data from 1 July 2024 to 31 December
> 2024. This means we don't have data for 2025 at all. … Based on the available
> data, I can tell you that: **Total sales from July 1, 2024 to December 31,
> 2024 were R…**

A wrong figure for the window asked about has become a right figure for a window
clearly labelled as a different one. Worth recording as a move rather than as a
repeat failure — the fabrication mechanism the ticket targeted is gone on both
models, and what is left is a helpfulness reflex the assertion is strict about
on purpose.

**And no zero was over-hedged.** `simple_aggregate` is 7/7 on both models,
`metric_registry` 5/5 on kimi, and the quiet-period cases still report a plain
figure. The regression this re-score was told to look for did not happen.

## 2. Half of deepseek's failures are one defect, and it is a retry loop

`time_window` fell from 6/6 to **2/6**. Every one of those losses, plus
`id-penjualan-desember`, is the same mechanism — and the harness recorded it in
full:

```
query_metric {"metric_key": "revenue", "to": "2024-12-31"}   ×7, identical
```

| case | tool calls | half-specified |
| ---- | ---------- | -------------- |
| `december-2024-sales` | 7 | 7 |
| `november-2024-sales` | 7 | 7 |
| `q4-2024-sales` | 7 | 7 |
| `last-month-relative` | 7 | 6 |
| `id-penjualan-desember` | 7 | 7 |

`query_metric` accepts both bounds or neither; one bound is refused, deliberately
— guessing the missing half is how a metric answers a question nobody asked.
Until today it was refused with a **Go error**, and deepseek's response to that
error is to send the identical call again. Six times, then `blocked` by T-16's
iteration budget, and the turn ends with no figure. The model even narrates the
correction it does not make: *"I need to specify both the start and end dates
for December 2024. Let me query for the full month:"* — followed by the same
call.

**Five of ten failures on this model are that one loop.** Each cost eight
iterations to produce nothing.

### What was fixed, and the two things that did not fix it

**Fixed: the refusal is a result, not an error** (`metric_tools.go`,
`halfWindow`). It names the bound that arrived, the one that did not, both legal
shapes, and says not to repeat the call unchanged; `row_count` is 0 so a refusal
never grounds a figure, and nothing reaches the warehouse. This is the trade
`unknownKey` already makes for an unknown metric key, applied to the other
recoverable mistake in the same tool. The test was proven failing on the old
code first.

**It does not rescue the turn, and that is the finding.** Re-run on the three
cases, the calls now come back `ok` with `rows_returned 0` — and deepseek sends
the same arguments anyway. **0/3.**

**Nor does a prompt sentence.** A guideline bullet spelling out *"from and to
travel together… a named period is two dates, not one"* was written, built and
measured against the same three cases, twice: **0/3 and 0/3**. Under rule 1 that
is a measured null, so it was reverted rather than shipped — a prompt line that
buys nothing is prompt weight every turn pays for.

**What is left is structural, and it is written down rather than built.** A tool
called with byte-identical arguments that returns the same refusal twice in one
turn should end the loop rather than let the budget end the turn — a guard in the
agent loop, not in this tool, because nothing about the failure is specific to
`query_metric`. Ten other tool paths return a Go error for a caller mistake
(`create_dashboard`, `create_visualization`, `run_sql`, `schedule_task`, and
`query_metric`'s own malformed-date branch). Only this one has been observed
looping; the mechanism does not care.

## 3. The attribution arms, and what they cost to believe

Every regression was re-run at **`65642c3`** — the commit the 08-14 re-score was
taken at, and the last before the metric zero path. `internal/eval` and
`golden.yaml` are byte-identical across the two commits, so the only variable is
the product.

| Case | main | `65642c3` | Reading |
| ---- | ---- | --------- | ------- |
| `december-2024-sales` (ds) | FAIL | **FAIL** | Latent since the optional window (`f2997c0`), not shipped since |
| `november-2024-sales` (ds) | FAIL | **FAIL** | Same |
| `q4-2024-sales` (ds) | FAIL | **FAIL** | Same |
| `last-month-relative` (ds) | FAIL | **FAIL** | Same |
| `ambiguous-headcount` (ds) | FAIL | **FAIL** | Same |
| `last-month-relative` (kimi) | FAIL | PASS | See below |
| `dirty-ask-rather-than-guess` (kimi) | FAIL | PASS | See below |

**Nothing this repo shipped since the last measurement caused deepseek's drop.**
Seven points of pass rate moved because the model behaved differently on a
Tuesday, on a defect that had been sitting there since 08-14 waiting for a model
to trigger it.

## 4. The number this set produces has ±2 cases of noise, measured

kimi's two failures looked like a regression and are not. The pair was re-run on
main three more times:

| case | run 1 | 2 | 3 | 4 |
| ---- | ----- | - | - | - |
| `last-month-relative` | FAIL | PASS | PASS | PASS |
| `dirty-ask-rather-than-guess` | FAIL | FAIL | PASS | PASS |

Both are the same behaviour: the agent asks a clarifying question — sometimes by
calling `ask_clarification`, sometimes in prose — where the case wants a figure
or the tool call. Neither is deterministic.

**So kimi is 96.4% or 98.2% depending on the day, and this project has been
reading one-run deltas of one and two cases as results.** The 08-14 entry above
reads "98.2%, up from 87.5%" — the first half of that is a sample. Any future
comparison smaller than about three cases on this set needs repeats or it is
describing the weather.

## 5. T-Q3: measured at last, and the answer is no

The before-arm removed the `A CHART IS SOMETHING THE USER ASKS FOR` guideline
(and reverted `DOES want` → `wants`) from a tree that was restored immediately
afterwards, built as its own binary, and run against the same tenant.

| kimi, 56 cases | pass | `create_visualization` | `create_dashboard` |
| -------------- | ---- | ---------------------- | ------------------ |
| guideline on | 54/56 | 4 | 3 |
| guideline off | 54/56 | 5 | 4 |

Identical scores from two different pairs of failures, which §4 now explains.
The pass rate cannot see this ticket.

The tool counts nearly can. With the guideline removed, kimi built one card and
one dashboard nobody asked for — on **`id-kanal-terbesar`**, *"Kanal penjualan
mana yang paling besar nilainya?"* On deepseek the same before-arm produced
**no** extra chart, on either the six chart cases or the Indonesian five.

**One event, on one model, inside a ±2 noise band, is not a result.** T-Q3
remains a prompt change with an argument behind it and no number — the honest
outcome of finally running the arm, and cheaper to know than to keep assuming.

### The instrument gap it exposed is real regardless

`no_chart_wanted` asserts `must_not_call` on three questions that want a number,
and **all three are in English.** The one unrequested chart this sitting saw
landed on the Indonesian twin of one of them, where the case asserts only
`must_call: [run_sql]` — so the set scored it a pass. A restraint rule written in
English and tested in English never measured the language a model might answer
in.

All five `indonesian` cases now carry `must_not_call: [create_visualization,
create_dashboard]`. It costs nothing per run: with the guideline in place neither
model builds a chart on any of them, verified against both arms of today's data.
The set stays at 56 cases and gets stricter, which is what §"above the 95% line"
has been asking for since 08-14.

## What is owed after this sitting

- **The repeat-guard**, above. It is the only fix left with evidence behind it,
  and it belongs in the agent loop.
- **A re-score of the hardened set**, under rule 1 — five cases gained an
  assertion. Cheap and low-risk: today's data says every one of them passes it.
- **Repeats, not single runs, for anything smaller than three cases.** §4 is the
  argument; the cost is linear and the alternative is publishing weather.
- **`metric-uncovered-question-falls-back`** failed on deepseek with seven tool
  calls and no half-window call — an unexamined failure, not triaged here.
- **The grounding check has no notion of a sum.** Three passing cases logged
  *"reply states a figure no tool result contains"* for `21,231,619,600`, which
  is exactly the total of the three channel figures `run_sql` returned. The
  check compares against returned cells; the most common honest derivation an
  analyst writes is a total of them.

## The `T-Q11` rule-1 re-score — run 2026-08-18

`T-Q11` narrows what a reply carries on **every turn** (the record becomes the
last iteration that produced prose), so rule 1 applies: the set is owed on both
models with the number and the date posted. Run against `main` @ `3eba409`, the
56-case set, both models, same afternoon.

| Model | Score | Cost | Against 2026-08-14's rule-1 re-score |
| ----- | ----- | ---- | ----------------------------------- |
| `moonshotai/kimi-k2.6` | **94.6%** (53/56) | $0.5427 | 98.2% (55/56) → **−2 cases, inside the ±2 band** |
| `deepseek/deepseek-v3.2` | **78.6%** (44/56) | $0.1928 | 89.3% (50/56) → **−6 cases, outside it** |

**kimi's three failures are not the narrowing.** `last-month-relative` asked for
clarification instead of answering (the asking policy erring the other way);
`ambiguous-headcount` and `dirty-ask-rather-than-guess` both *asked in prose*
rather than calling `ask_clarification` — the failure this file has recorded
since 2026-08-14, unchanged. `zero_row_trap` is **3/3** and `guardrail` **8/8**,
so the categories `T-Q11` and `T-Q9` live in did not move.

**deepseek is outside the band, and it is not this build.** Six of its twelve
failures are a single shape — `replied in "id", expected "en"` — spread across
`time_window`, `guardrail` (×2), `zero_row_trap` and `dirty_schema` (×2). An
English question answered in Indonesian is CRITICAL GUIDELINE #1 of the system
prompt failing, and it says nothing about the record narrowing.

**So it was measured rather than argued.** A worktree at `bdd7875` — the commit
*before* `T-Q11`/`T-Q12`/`T-D24` landed — ran those same six cases on the same
model on the same afternoon: **four of the six fail identically**, same language
error. The remaining two-case difference is inside the noise band the set
carries. **The language regression is older than this build**, so the deepseek
drop cannot be attributed to it; the live candidate is provider-side drift in
`deepseek/deepseek-v3.2`, which this project has never pinned to a snapshot and
which every published deepseek number in this file shares.

That is a finding about the **instrument**, not about the agent: a set scored
against an unpinned model cannot separate "the tree got worse" from "the model
changed underneath us", and the only reason it could be separated here is that a
1.5-hour-old commit was still available to run against. Pinning a snapshot, or
recording the provider's model revision beside every published score, is the
cheap fix and it is not written down anywhere yet.

deepseek's other failures are the known shapes: `ask_clarification` answered in
prose, `zero_row_trap` **0/3** (its score on this category since 2026-08-14, and
the metric-zero-path fix moved kimi to 3/3 without moving deepseek), a follow-up
that re-called `get_schema`, and one `explicit-source-december-profit` that
returned no number and called no tool.
