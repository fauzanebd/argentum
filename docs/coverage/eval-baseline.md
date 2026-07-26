# Eval Baseline — `T-01`

First measured agent-quality signal this project has had. Every prompt or model
change from here is compared against this number.

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

## The one failure

`dashboard-two-cards` is not noise, and it is not a bad case. Asked to build a
dashboard with two cards, the agent spent its three iterations on
`get_schema`, `get_schema`, `create_visualization` — and stopped. It never
called `create_dashboard`, so the user gets a chat message describing a
dashboard that does not exist.

This is finding `Q-5`, the 3-iteration cap, reproduced deterministically for
the first time. It is what `T-16` exists to fix, and this case is the gate:
when the iteration budget lands, the eval should read 31/31 without any change
to the golden set.

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
