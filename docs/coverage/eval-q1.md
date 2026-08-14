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

## What is owed

- **A re-run on the fixes.** Three of this run's conditions have changed since it
  started: the Metabase credentials, `ResolveSource`, and the grounding filter.
  The `chart_dashboard` category was re-run on its own immediately afterwards
  ($0.046, 3 cases) and stayed at 2/3 by swapping which case failed; a full
  re-score belongs with the next batch of agent changes rather than on its own,
  per rule 1.
- **A second look at the empty reply**, which is the one finding here that a
  re-run will not settle — it is non-deterministic and it appeared twice in
  58 turns. The guard is cheap; the diagnosis is what needs a turn watched
  live.
- **A decision on `ask_clarification`.** Three cases hang on it and the two
  shapes pull opposite ways. Nothing should be tightened before that is decided.
- **`query_metric`'s zero-row story**, which is a code fix and not a gate.
- **The model comparison.** `make eval-matrix MODELS=moonshotai/kimi-k2.6,deepseek/deepseek-v3.2`
  is the only honest way to say whether the model change helped, and it is
  `T-Q5`. At $0.441 per model-run, the pair is roughly $0.90 — cheap enough that
  arguing about it costs more than running it.
