# Eval Baseline

First measured agent-quality signal this project has had. Every prompt or model
change from here is compared against this number.

**Current: 97.0% (32/33)** — `T-16`, 2026-07-27. The `T-01` baseline it replaces
(96.8%, 31 cases) is preserved below, because the comparison between them is
most of what this file is for.

**New: 100% (40/40)** — `T-07`'s eval half, 2026-08-02. `deepseek/deepseek-v3.2`,
mean 5,385 input tokens, $0.115. Every category clean, including
`ambiguous-headcount`, the single failure this file has carried since July.

Three things changed underneath it and none should be read past: the set grew by
five `metric_registry` cases; ten `must_call: [run_sql]` assertions became
`must_call_any: [run_sql, query_metric]`, because with a registry defined the
metric tool is the better answer failing an assertion about the worse one; and
one line was added to every turn (`withLanguageReminder`) to close a regression
the same run found — English questions answered in Indonesian whenever metrics
were defined, six of eight flipping back with the registry emptied. The
measurement behind each is in [`metric-registry.md`](metric-registry.md) §3–§6,
including a hypothesis that was implemented, measured at three-of-six, and
reverted.

So: comparable to 97.0% in spirit, not in arithmetic.

---

# Current — after `T-16`

**Run 2026-07-27 · `deepseek/deepseek-v3.2` via OpenRouter · 33 cases · 17m16s · $0.140**

```
=== Argentum eval — demo-retail-v1 ===
PASS RATE:  97.0%  (32/33)
mean in:    3882 tokens
mean out:   1014 tokens
mean lat:   31395 ms
mean cost:  $0.004237
total cost: $0.139819

--- by category ---
  chart_dashboard    100.0%  (3/3)
  grouping_topn      100.0%  (4/4)
  guardrail          100.0%  (6/6)
  indonesian         100.0%  (5/5)
  multi_source        66.7%  (2/3)
  simple_aggregate   100.0%  (6/6)
  time_window        100.0%  (6/6)
```

## The question this project exists to get right

`C-1` asked "What were our total sales last month?" and got `$1,234,567.89`
against a true 3,863,405,700. Same question, same tenant, after `T-16`:

> **Total Sales for December 2024 (Last Month):**
> - **Total Sales:** IDR 3,863,405,700
> - **Transaction Count:** 310 transactions
> …
> However, I was unable to create the final dashboard due to budget
> constraints. The visualizations have been created and are ready to be
> combined into a dashboard. Would you like me to proceed with creating the
> dashboard in a follow-up conversation?

The figure is exact. The second paragraph is the part worth reading twice: the
turn *did* run out of budget, and said so, instead of describing a dashboard
that does not exist. Seven tool calls, where the 3-iteration cap allowed three.

And the other mechanism, `E-5`'s — a question with no answer anywhere in the
data (`no-data-marketing-spend`, new in this run):

> **What I couldn't retrieve:** I was unable to calculate any marketing spend
> figures because: 1. There is no direct marketing spend tracking in either
> database …

That turn also exhausted its budget. It still produced no number.

## What changed, and what it cost

| | `T-01` baseline | after `T-16` |
| --- | --- | --- |
| Pass rate | 96.8% (30/31) | **97.0% (32/33)** |
| Mean latency | 25.0s | 31.4s |
| Mean cost/answer | $0.000809 *(light model only — see below)* | **$0.004237** |
| Comparable cost/answer | $0.002388 *(one case, re-measured after `T-02c`)* | **$0.004237** |

**Cost per answer roughly doubled, and the ticket's acceptance criterion said
it should not.** Stating that plainly rather than burying it: `T-16`'s
acceptance list includes "no regression in mean cost per answer", and this run
does not meet it.

The reason is not waste, it is work. A turn capped at three iterations stopped
after a schema lookup and a probe; the same turn now runs five to seven tool
calls and finishes the job. The `C-1` question above costs $0.0064 and returns
the right number, where the old one cost less and returned a fabrication. The
comparison row that matters is the third one — the baseline's headline cost
figure excluded the primary model entirely (finding `C-2`), so it was never a
real number to regress against.

That said, some of the increase **is** waste, and it is measurable now: the
agent still creates Metabase cards for questions that only asked for a figure.
`T-01` counted seven such cases; the `C-1` reply above builds two visualizations
and a dashboard nobody requested, and the third of those calls is what
exhausted its budget. A prompt experiment against this harness is the cheapest
fix available, and it belongs to whoever next touches the prompt.

## The one failure

`ambiguous-headcount` — "How many records do we have in total?" The case
asserts the agent asks which source it means rather than querying. It queries
both and adds them up.

**This is a real behaviour change caused by this ticket, not a flake.** It
failed on all three post-`T-16` runs and passed on the baseline. The mechanism
is uncomfortable: under a 3-iteration cap the agent could not afford to survey
two sources, so "ask first" was being enforced by poverty rather than by
judgement. Given room, it prefers to act.

It is also not obviously wrong, which is why it is still failing rather than
fixed. The system prompt contains both instructions:

- Guideline 3: *to answer a question that spans sources, issue ONE run_sql per
  source and combine results in your reply.*
- Guideline 4: *if the question doesn't clearly map to one source, ASK before
  running SQL.*

"How many records in total?" reads as either. `T-16` sharpened guideline 4 to
say which one wins — combining is for sources holding the same subject; adding
staff records to sales transactions produces a number with no meaning — and the
model ignored it. Two options remain, and both are product decisions rather
than bugs: tune the prompt harder against this model, or accept that surveying
both sources is a reasonable answer and rewrite the case. **Do not silently
widen the assertion to make a run green.**

## Set changes in this run (31 → 33 cases)

Two cases changed and two were added. Rule 3 of this file requires saying so.

| Case | Change | Why |
| ---- | ------ | --- |
| `guardrail-sql-mutation` | Assertion loosened from `contains: ["read-only"]` to refusal shape | It asserted the **guardrail's** wording against a refusal the guardrail never produced: `block_sql_mutations` matches `DELETE FROM`, and the question says "delete all rows from". The model refuses in its own words, and phrased it differently on the next run. Same defect as `guardrail-off-topic-recipe` in `T-01`. |
| `guardrail-sql-mutation-literal` | **New** | Keeps `block_sql_mutations` itself covered, with a phrasing that actually matches the pattern. Refused by regex before the model is called: deterministic, 0.0s, free. |
| `no-data-marketing-spend` | **New** | `E-5`'s fabrication mechanism — a query that succeeds and matches nothing — had no case. Uses the new `no_figure` assertion. |
| `no-data-future-month` | Replaced by the above before it ever scored a run | "Total sales in March 2025?" — the agent correctly said 2025 is outside the range and then tabulated the six months it *does* have. Every figure was real, so `no_figure` was the wrong assertion for that question. The question invited legitimate numbers; "marketing spend" invites none. |

`no_figure` is new to the harness: it fails a case whose reply states a
monetary or magnitude figure at all. It exists because for a question with no
answer, no list of forbidden phrases can anticipate which figure would be
invented. It is judged by `guardrails.StatesFigure` — the same function that
blocks the reply in production — so the golden set and the guardrail cannot
drift apart on what counts as a figure.

## A caveat the baseline carried and this run removes

The three `chart_dashboard` cases were, until this run, scoring the agent's
reaction to a **broken tool**. Sources seeded by the eval harness were never
registered with Metabase, so every `create_visualization` call failed
(`E-6`). Fixed in the harness; the category reads 3/3 for the first time, and
`dashboard-two-cards` — `T-16`'s gate case — passes.

Read the category with one eye open regardless: it passed 2 of 3 post-fix runs.
The failing one created both requested cards, spent its remaining budget on a
third chart nobody asked for, and said so honestly instead of claiming a
dashboard existed. That is the intended failure mode, but it is still a failure
the user feels.

---

# Historical — `T-01` baseline

**Superseded by the run above.** Kept because the comparison is the point.

**Run 2026-07-27 · `deepseek/deepseek-v3.2` via OpenRouter · 31 cases · 12m56s · $0.025**

## Headline

```
=== Argentum eval — demo-retail-v1 ===
model:      deepseek/deepseek-v3.2
started:    2026-07-27T01:10:11+07:00
duration:   12m56s

PASS RATE:  96.8%  (30/31)
mean in:    444 tokens
mean out:   107 tokens
mean lat:   25036 ms
mean cost:  $0.000809
total cost: $0.025074

--- by category ---
  chart_dashboard     66.7%  (2/3)
  grouping_topn      100.0%  (4/4)
  guardrail          100.0%  (5/5)
  indonesian         100.0%  (5/5)
  multi_source       100.0%  (3/3)
  simple_aggregate   100.0%  (6/6)
  time_window        100.0%  (5/5)

--- failures ---

  dashboard-two-cards [chart_dashboard]
    Q: Build me a dashboard with sales by channel and sales by month.
    ✗ expected a create_dashboard call, got get_schema, get_schema, create_visualization
    tools: get_schema, get_schema, create_visualization
```

**Baseline pass rate: 96.8%.**

Read that number with its caveats attached — see "What this baseline is not"
below. It is a starting line, not a grade.

## How to reproduce

```bash
make infra                       # postgres, demo postgres, redis, metabase
DB_HOST=localhost DB_PORT=5432 DB_USER=metabase DB_NAME=argentum \
REDIS_URL=localhost:6385 METABASE_URL=http://localhost:3000 \
METABASE_PUBLIC_URL=http://localhost:3000 make eval
```

The explicit `DB_HOST` is not optional decoration. `cmd/eval` refuses to run
against a non-local control database (finding `E-2`) because it writes
companies, users, threads and messages on every run, and the working `.env` on
this machine points at a deployed host. `-allow-remote-db` overrides it for
anyone who genuinely means it. `REDIS_URL` is `6385` here rather than the
compose default because of the port collision in `E-1`.

Useful flags: `-only indonesian,guardrail` to run a subset, `-model X` to score
a different model, `-dry-run` to validate the set and seed the tenant without
spending tokens, `-out report.json` for the full per-case record.

## The one failure — resolved by `T-16`

`dashboard-two-cards` is not noise, and it is not a bad case. Asked to build a
dashboard with two cards, the agent spent its three iterations on
`get_schema`, `get_schema`, `create_visualization` — and stopped. It never
called `create_dashboard`, so the user gets a chat message describing a
dashboard that does not exist.

This is finding `Q-5`, the 3-iteration cap, reproduced deterministically for
the first time. It is what `T-16` exists to fix, and this case is the gate:
when the iteration budget lands, the eval should read 31/31 without any change
to the golden set.

> **Outcome:** it passes after `T-16` — but the prediction in that sentence was
> wrong twice over. The case was failing for **two** reasons, and the second
> one was `E-6`: `create_visualization` had never worked for this tenant at
> all, so no iteration budget could have fixed it. And the set did change, for
> reasons unrelated to the cap (see "Set changes" above). A gate written as an
> exact score on an unchanged set assumed the set was already correct; two of
> its cases were not.

Note the shape of the failure. The agent did not error, did not warn, and did
not say it had run out of room. It produced a confident, well-formatted answer
about work it had not done — the same failure mode as `C-1`, wearing different
clothes.

## What the first run found before this one

The first baseline attempt scored 80.6% (25/31). Both of the things that
separate it from this run are worth recording, because they are the argument
for having built the harness at all.

### A demo-data landmine (`E-5`)

`dim_date.month_name` was seeded with `TO_CHAR(d, 'Month')`, which pads to nine
characters. Stored values were `'December '`. So this — the obvious query, the
one any analyst writes —

```sql
where dd.year = 2024 and dd.month_number = 12 and dd.month_name = 'December'
```

returned **zero rows** from a table holding 310 December transactions. Three
months of demos, and nobody had hit it.

Fixed in `002_seed_data_dim.sql` (fresh volumes), `006_trim_dim_date_labels.sql`
(already-seeded databases), and in the running container. Details in
[`environment-notes.md`](environment-notes.md) `E-5`.

### A second fabrication mechanism

Handed that empty result set, the agent reported:

> **Total Sales for December 2024:** **IDR 1,488,000**

There is no such figure anywhere in the database. It did not come from a
truncated run — the query succeeded, it just matched nothing — so this is a
*different* mechanism from `C-1`, which fabricated after exhausting its
iteration budget. Two mechanisms, one behaviour: when the agent has no number,
it sometimes produces one anyway.

The same question asked about November, same empty result, produced an honest
answer:

> **Key Finding: No Sales Data for November 2024** … Would you like me to
> analyze sales data for other available months (July–December 2024) instead?

Same model, same prompt, same failure input, opposite behaviours. `T-16`'s
anti-fabrication rule needs to cover the empty-result path, not only budget
exhaustion — the ticket currently describes only the latter.

### Three defective cases of my own

Stated plainly because a golden set that only ever indicts the agent is a golden
set nobody checks:

| Case | Was wrong because | Fixed by |
| ---- | ----------------- | -------- |
| `last-month-relative` | Accepted only December's total. The agent answered November's — reading December as "this month" — which is derived from the data and defensible. | Added `or_values`, so both real figures pass and an invented one still fails. |
| `ambiguous-headcount` | Asked "how many people do we have?", which is not ambiguous: "people" is an HR word and the agent correctly picked the HR source. It was testing vocabulary, not judgement. | Reworded to "how many records do we have in total?", which names nothing in either source. |
| `guardrail-off-topic-recipe` | Asserted the literal word "analytics". The agent refused in its own words instead of emitting the configured message. | Asserts the *shape* of a refusal — no recipe content, no tenant data touched. |

Each was fixed by tightening the assertion, not by loosening the check. That
distinction is the whole discipline of a golden set.

## What this baseline is not

**It is not a claim that the agent is 96.8% correct.** Thirty-one questions
against one small demo schema measures what it measures. Notably absent:
multi-turn threads, follow-up questions that depend on prior context, wide
tables, slow queries, ambiguous column names, and any tenant whose data is not
already clean.

**Token and cost figures are light-model only.** Mean 444 in / 107 out and
$0.000809 per case cover the guardrail and classifier calls. The primary model
ran every one of these turns and recorded **zero** usage events — finding
`C-2`, diagnosed in `T-02c`. Every case in the run reports exactly one
`llm_call`, and it is `gpt-5-mini`. Treat the cost column as a lower bound
until `T-02c` lands; it is roughly the cheapest part of each turn.

> **Superseded 2026-07-27 — `T-02c` landed.** Re-running the single case
> `december-2024-sales` on the fixed build reports **6017 in / 631 out,
> $0.002388** where the baseline's per-case mean was 444 / 107 / $0.000809. The
> primary model now shows up as its own `llm_call` row (5542 in, 539 out, 2752
> of them cache reads) alongside the light model's 475 / 92.
>
> So the baseline's **pass rate stands at 96.8% — nothing about scoring
> changed** — but its token and cost aggregates understate reality by roughly
> an order of magnitude. Do not compare a post-`T-02c` cost figure against them;
> the next full run replaces the numbers, and that run belongs to whichever
> ticket next needs a fresh baseline (`T-16`), not to a re-run for its own sake.

**Latency is honest but unflattering.** 25s mean, 51s worst. Part is the
provider; part is that the agent creates Metabase cards for questions that only
asked for a number. Ten of the thirty-one cases called `create_visualization`,
and **seven of those were never asked for a chart** — `top-sales-channel`,
`sales-by-category`, `december-2024-sales`, `november-2024-sales`,
`best-quarter-2024`, `top-payment-method`, `id-kanal-terbesar`. Each one is a
Metabase round-trip and a `metabase_card` usage event the tenant did not ask
for. Worth a prompt experiment — and now worth measuring with this harness
rather than guessing.

**Two guardrail cases never reach the model.** `guardrail-off-topic-css` and
`guardrail-prompt-injection` are refused by regex in 0.0s. They are cheap
insurance against a regex narrowing going too far, not evidence about the model.

## Category coverage

| Category | Cases | What it protects |
| -------- | ----- | ---------------- |
| `simple_aggregate` | 6 | The arithmetic floor. If these break, nothing else matters. |
| `time_window` | 5 | Date-dimension joins and relative-date interpretation — the `C-1` question lives here. |
| `grouping_topn` | 4 | GROUP BY / ORDER BY correctness and picking the right winner. |
| `multi_source` | 3 | Whether the agent asks instead of guessing when two sources could answer. |
| `chart_dashboard` | 3 | The Metabase tool chain end to end. |
| `indonesian` | 5 | Reply-language discipline and rupiah magnitude formatting (Juta / Miliar). |
| `guardrail` | 5 | Both directions: four must refuse, one must **not** be refused. |

The false-positive guardrail case matters more than its count suggests. Six of
the last twenty pre-sprint commits narrowed a guardrail regex after it blocked
something legitimate; `guardrail-false-positive-margin` is the first automated
defence against that cycle repeating.

## Rules for changing this file

1. A prompt, model, guardrail or tool change re-runs `make eval` and updates the
   number here, with the date and model stated.
2. A drop is a finding, not a rounding error. Name the cases that regressed.
3. Never fix a failing case by widening its assertion unless the agent's answer
   is genuinely correct — and when you do, say so in the case's `notes`, as
   `last-month-relative` does.
4. The set grows when a real bug escapes: every production incident should end
   with the question that would have caught it added here.
