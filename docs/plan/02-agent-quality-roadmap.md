# Agent quality roadmap — smarter and more reliable

Written 2026-08-11. Nine tickets, ~12 days, five tracks. Ticket ids are `T-Q1`
→ `T-Q9`; they are tickets, not findings, and do not collide with the `Q-n`
finding codes in the research docs.

> **All nine are code-complete as of 2026-08-11**, with `go build`, `go vet`,
> `go test ./...` and `tsc -b` clean. What is **not** done is the half this
> repo's delivery log says finds the defects: **nothing here has been run
> against a live stack or a real model.** The eval headroom `T-Q1` adds is a
> prediction until `make eval` scores it, and four of the nine tickets change
> what reaches the model on every turn. See §"What is owed" at the end —
> it is the list that matters more than the ticks below.
>
> Three defects were found and fixed on the way, each pre-dating this roadmap.
> They are recorded in §"Defects found while building this".
>
> **Half-gated the same day.** Everything that needs the stack and nothing else
> was run, and the paragraph above was right: it found **three more defects**,
> two of them in what `run_sql` tells the model when a query matched nothing.
> All six are now in §"What is owed", with transcripts in
> [`../coverage/agent-quality.md`](../coverage/agent-quality.md). What is still
> unrun is everything needing a model — and it is blocked on a missing `.env`
> rather than on a decision about cost.

**Smarter** and **reliable** are two goals with two different instruments, and
this repo currently has neither working:

| Goal | Measured by | State today |
| ---- | ----------- | ----------- |
| Smarter | Pass rate on hard questions | **Saturated.** 40/40 since 2026-08-02 ([`eval-baseline.md`](../coverage/eval-baseline.md)). A set that always reads 100% cannot rank two prompts. |
| Reliable | Wrong answers per real turn | **Uninstrumented.** No rating, no correction signal, no column for either. Every quality claim this product makes comes from 40 synthetic questions against one demo schema. |

So Track A is not preamble. Everything after it is unfalsifiable without it —
which is finding `Q-2` (six commits changed prompt or model with no way to tell
whether it helped) returning in a new costume.

---

## Track A — Instruments (2.5d) · do first

### `T-Q1` Eval headroom — 2.0d
The set passes everything, so it teaches nothing. Add the categories
[`eval-baseline.md`](../coverage/eval-baseline.md) §"What this baseline is not"
already names as absent, plus the two failure shapes production has actually
shown:

| New category | What it protects | Why it belongs |
| ------------ | ---------------- | -------------- |
| `follow_up` | "now break that down by region" after a prior answer | Needs a harness change: `eval.Case.Question` is one string. Multi-turn is the single biggest untested surface. |
| `zero_row_trap` | A filter that matches nothing (the `'December '` padding class) | Fabrication mechanism `E-5`. One case exists; the *shape* has many. |
| `wrong_grain` | A query that returns rows and answers a different question | Nothing today catches wrong-but-nonempty. See `T-Q9`. |
| `no_chart_wanted` | Asserts `create_visualization` was **not** called | Makes `T-Q3` measurable. |
| `dirty_schema` | Ambiguous column names, wide tables, near-duplicate tables | Every tenant after the demo one. |

**Target: 70–85% on the extended set.** Landing at 95%+ means the new cases are
too easy — say so and harden them rather than banking the number. Rule 3 of
`eval-baseline.md` applies unchanged.

### `T-Q2` Answer feedback — 0.5d
A thumbs-down on an assistant message, with an optional reason, written to a new
column and surfaced in the dashboard. There is no `Feedback` or `Rating` field
anywhere in `internal/domain` today.

Cheapest reliability instrument available, and it does three jobs at once:
tells you where the agent fails on *real* schemas, supplies the correctness
label `T-Q8` needs, and turns every complaint into a candidate golden case
(rule 4 of `eval-baseline.md`).

---

## Track B — Cheap wins (1.5d) · measurable the day Track A lands

### `T-Q3` Chart restraint — 0.5d
The baseline counted **7 of 31** cases building Metabase cards for questions
that only asked for a number, and the `C-1` answer spent its last iteration on
an unrequested third chart — which is why its dashboard never landed. The doc
calls a prompt experiment "the cheapest fix available" and leaves it to whoever
next touches the prompt. Nobody has.

Prompt change in `internal/bootstrap/system_prompt.go`, gated by
`T-Q1`'s `no_chart_wanted` cases. Buys back 1–3 iterations per turn, which is
budget spent on the actual question.

### `T-Q4` `ask_clarification` as a tool — 0.5d
`ambiguous-headcount` passed under a 3-iteration cap and fails with room:
"ask first" was enforced by poverty, not judgement. Guideline 4 was sharpened in
`T-16` and the model ignored it.

A guideline competes with tool-calling momentum and loses. A *tool* does not —
asking becomes an action the model can take, with a name, in the same list as
`run_sql`. Registers in `internal/tools/registry.go`, returns the question to
the user and ends the turn.

### `T-Q5` Model matrix — 0.5d (plus spend)
Every number this project has is `deepseek/deepseek-v3.2`. Run the extended set
across 2–3 models and pick on evidence. `cmd/eval` already takes `-model`.
Cost: roughly the full-set figure in
[`live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2 per model.

---

## Track C — Context that carries forward (3.0d)

### `T-Q6` Persist and rehydrate tool results — 2.0d
`domain.MessageRoleTool` is declared at `internal/domain/message.go:14` and
**written by nothing** — `grep` finds exactly one hit, the declaration.
`ChatRunner.completeWith` appends only the assistant's prose, and
`hydrateMemory` replays the last 20 message *texts*.

Consequence: a follow-up turn does not know what the previous turn queried. It
re-runs `get_schema`, re-derives the SQL, and can silently derive it
differently. Every follow-up pays full price and risks a different answer to the
same question.

Fix: persist a digest per tool call (SQL text, source, row count, column names,
a small sample) as `role=tool`, and rehydrate it compactly. The schema already
holds the role and `Message.ToolCalls`; the audit path already computes most of
the digest (`internal/tools/audit.go` `digestArgs`).

#### Acceptance, re-specified 2026-08-14

The original line — *a two-turn conversation at `PRIOR_WORK_TURNS=3` and again
at `=0`* — was run on 2026-08-14 and **cannot measure this ticket**, which is a
different result from failing it. Both arms wrote the `role=tool` rows,
injection fired at `=3` and never at `=0`, and the six turns produced
**identical tool sequences** — because inside `CONTEXT_MAX_TURNS` (default 3,
`config.go:454`) the assistant's own prior message quotes the SQL it ran, so the
digest repeats what the model can already read
([`../coverage/agent-quality.md`](../coverage/agent-quality.md) §11).

A digest only earns its place where the history cannot help. So the measurement
has to put the tool call *outside* what the model can read, one of two ways:

1. **The cheap arm, and the one that has already produced evidence.** The eval
   case `follow-up-breakdown-no-reschema` works precisely because turn 1 is
   answered from `query_metric` and never touches the schema — the assistant's
   prose has no SQL in it to re-read, so the digest is the *only* place turn 2
   could learn what was queried. On the 2026-08-14 matrix run it passed on
   kimi-k2.6 and failed on deepseek-v3.2, which is the shape of the difference
   the ticket claims. **Pass: that case passes at `PRIOR_WORK_TURNS=3` and
   fails at `=0`, on one model, two runs of one case.** Two cases of spend, not
   two full sets.
2. **The thread arm, for the property at production shape.** A thread where the
   turn that ran the query has fallen out of the hydration window: turn 1
   answers from `query_metric`, then more than `CONTEXT_MAX_TURNS` turns of
   unrelated conversation, then a follow-up that needs turn 1's work. **Pass: at
   `=3` the follow-up calls neither `get_schema` nor a re-derivation of the same
   query and its answer agrees with turn 1's; at `=0` it re-derives.** This is
   the arm that also exercises `ListRecentByThread`, and it is the one a
   two-turn conversation could never reach on any model that reads its own
   history.

What is *not* the acceptance line any more: any comparison whose two arms are
short enough for the assistant's own transcript to carry the answer. That
measures the transcript.

### `T-Q7` Rolling summary beyond the window — 1.0d
`historyLimit` truncates at 20 and drops the rest. A long thread loses its
opening — which is usually where the user said what they were actually trying to
find out. Summarise the dropped prefix with the light model, once, and carry it.

---

## Track D — Learn from its own history (3.0d)

### `T-Q8` Per-tenant query cookbook — 3.0d
The highest ceiling on this list, and it needs **no new data collection**.

`agent_actions` already stores, per tool call: the full SQL (`args_redacted` —
redaction only strips credential-shaped values), `rows_returned`,
`result_status`, `source_id`, `message_id` and `thread_id`. Join to `messages`
and you have (question → SQL → row count → outcome) for every turn this product
has ever run.

Index the questions with the embedding client that already serves the table
picker, retrieve top-k on each turn, and inject the matching pairs as few-shot
examples beside the existing table hint in
`ChatRunner.withRelevantTablesContext`.

Effect: the agent stops rediscovering the same tenant's schema every turn, and
gets better the longer a tenant uses it.

**The one hard part is the label.** `result_status = ok` means the query ran,
not that it answered. Ship this *after* `T-Q2` and use thumbs-up plus
"thread continued with no correction" as the filter. Seeding the cookbook with
confidently wrong SQL is the failure mode that would make this negative-value.

---

## Track E — Verification (2.0d)

### `T-Q9` Pre-answer sanity pass — 2.0d
`guardrails.CheckFabrication` is negative-only: it blocks a figure no tool
produced. Nothing checks that the query *answered the question*. Wrong join
grain, wrong date column, a filter that silently matched the wrong subset — all
return rows, all pass every gate this product has.

Two halves, deterministic first:

1. **Zero-row probe.** An empty result forces a `SELECT DISTINCT` on the
   filtered column before the agent may answer. `internal/tools/sql_error_hint.go`
   already does this shape for SQL *errors*; this is the empty-result branch it
   does not have. Deterministic, cheap, and it is exactly the `E-5` landmine.
2. **Coarse control check.** Before answering with a figure, compare it against
   an unfiltered or wider-window aggregate. An order-of-magnitude disagreement
   is reported rather than swallowed.

---

## Sequencing

```
A (2.5d) ──> B (1.5d) ──> C (3.0d) ──> E (2.0d)
             └──> D (3.0d, gated on T-Q2)
```

A before everything. B before C, because B is cheap and B's numbers tell you
whether the harness from A actually discriminates. D branches off A rather than
following C. Total ~12d; Tracks D and E are the cut candidates if the sprint
does not hold.

## Lean version — if you only have one week

Do `T-Q1`, `T-Q2`, `T-Q3`, `T-Q6`. Four tickets, 5.0d, and they are the four
with the best ratio in the list:

- `T-Q2` at **0.5d** is the highest-value half-day here. It converts every
  production complaint into evidence, permanently.
- `T-Q6` is the largest *felt* improvement per day spent — every follow-up
  question in every thread gets cheaper and more consistent, and the schema
  already supports it.
- `T-Q1` and `T-Q3` are a pair: the second is unprovable without the first, and
  the first is unmotivating without something to prove.

Skip `T-Q8` in a one-week cut. It is the best idea on this page and the one most
likely to be built wrong without `T-Q2`'s labels.

## Not yet — with triggers

| Idea | Why not now | Trigger |
| ---- | ----------- | ------- |
| Planner + specialist agents | 8d on a feeling. [`backlog.md`](backlog.md) already carries it, and its trigger is *eval cases that consistently fail because one agent is doing two incompatible jobs*. `T-Q1` is what makes that trigger observable — the 2026-08-09 chart-vs-directive contradiction is the shape, patched with `notOnFileTurn`. | Two or more `T-Q1` categories failing for that reason. |
| Per-agent model / temperature | Seams exist (`BudgetResolver`, `llmCache.For`). Needs an eval run per configuration, which needs `T-Q5` first. | A tenant whose bill is dominated by one high-volume, low-stakes agent. |
| **Argentum advises, not just answers** — proactive "here is what changed and what I would do" rather than answering only what was asked | A prescriptive agent that is wrong is worse than a descriptive one that is wrong, because the user acts on it. Watchers already carry the push half. | `T-Q9` shipped and the wrong-answer rate from `T-Q2` known. Do not build this on an agent whose reliability is unmeasured. |
| Statistical anomaly detection | Unchanged from [`backlog.md`](backlog.md). | Users complaining about threshold noise. |

## How you will know it worked

| Signal | Now | After |
| ------ | --- | ----- |
| Extended eval pass rate | n/a | Trends up, run-over-run, on a set that can fail |
| Thumbs-down per 100 turns | Unmeasured | Trends down |
| Tool calls per answered question | ~5–7 | Down (`T-Q3`, `T-Q6`) |
| Follow-up turns re-running `get_schema` | Every one | Near zero (`T-Q6`) |
| Wrong-but-nonempty answers | Undetected | Caught by `T-Q9` or reported by `T-Q2` |

Rule 1 of [`eval-baseline.md`](../coverage/eval-baseline.md) applies to every
ticket here: a prompt, model, guardrail or tool change re-runs `make eval` and
updates the number, with the date and the model stated.

---

# Delivery record — 2026-08-11

Every ticket, what landed, and where. Written the same day the roadmap was, so
read the dates as one sitting rather than a sprint.

| Ticket | Landed | Where |
| ------ | ------ | ----- |
| `T-Q1` | Multi-turn cases (`follow_ups`, scored on the LAST turn), 5 new categories, `contains_any` assertion. Set: **40 → 55 cases** | `internal/eval/{case,runner,report,score}.go`, `testdata/eval/golden.yaml` |
| `T-Q2` | `message_feedback` table, domain, repo, service, 4 routes, RBAC, dashboard thumbs + reason box | migration `054`, `internal/domain/message_feedback.go`, `internal/app/feedback_service.go`, `apps/dashboard/.../message-feedback.tsx` |
| `T-Q3` | Chart-restraint guideline, conditioned against the dashboard rule it argues with | `internal/bootstrap/system_prompt.go` |
| `T-Q4` | `ask_clarification` tool; AMBIGUITY guideline rewritten to point at it; added to all 6 agent templates | `internal/tools/ask_clarification.go`, `config/agent_templates.yaml` |
| `T-Q5` | `-models a,b,c` runs the set per model and prints a comparison + disagreement list; `make eval-matrix` | `cmd/eval/main.go`, `internal/eval/report.go` |
| `T-Q6` | Tool digests persisted as `role: tool` (the role that existed and was written by nothing), rehydrated as a context block | `internal/app/tool_digest.go`, `chat_runner.go` |
| `T-Q7` | Rolling summary folded forward over the whole thread, and injected into turns on threads longer than the memory window | `internal/app/thread_service.go`, `chat_runner.go` |
| `T-Q8` | `query_examples` + harvester over `agent_actions`, gated on `message_feedback`; top-k injection sharing the table picker's embedding call; hourly job; 3 admin routes | migration `055`, `internal/app/cookbook_service.go`, `internal/adapters/postgres/{query_example,cookbook_candidate}_repo.go` |
| `T-Q9` | Zero-row probe (returns the filtered column's real values); grounding check comparing every stated figure against what the tools returned | `internal/tools/empty_result_probe.go`, `internal/guardrails/grounding.go` |

## Defects found while building this

All three pre-date the roadmap and none is something a unit test would have
caught, because in each case the tests and the code agreed with each other and
both disagreed with the intent.

**1. Memory hydration replayed the *beginning* of long conversations.**
`ListByThread` is `ORDER BY created_at ASC LIMIT n`, so
`ListByThread(id, 20, 0)` is the FIRST twenty messages. `hydrateMemory` used it
to mean "recent history". On any thread past twenty messages the agent was
re-hydrating into its own opening and losing everything the user had said
since. Below the limit the two reads are identical, which is why every test
thread and every demo hid it. Fixed by `ListRecentByThread` (T-Q7).

**2. The rolling summary summarised the opening, forever.** Same root cause,
worse consequence: `refreshSummary` asked for `ListByThread(id, 12, 0)` while
its own comment said *"the last 12 messages"*. Every refresh re-read the same
twelve rows and produced the same summary, so the column stopped tracking the
conversation after turn six — and the relatedness classifier that decides
whether a new question continues a thread has been reading it ever since.

**3. `domain.MessageRoleTool` was declared and written by nothing.** One grep,
one hit, the declaration. Tool results were never persisted, so every follow-up
turn began knowing what was *said* and nothing about what was *done* — it
re-ran `get_schema`, re-derived the SQL, and could derive it differently. This
is the finding T-Q6 was written from, confirmed in the code rather than assumed.

A fourth, smaller one: every entry in `config/agent_templates.yaml` carries an
explicit `suggested_tools` list, so a new tool is invisible to agents created
from a template until the file is edited. `ask_clarification` would have shipped
unusable for exactly the agents most likely to need it.

## What is owed

**Revised 2026-08-11, after the stack-only half was run.** Full transcripts in
[`../coverage/agent-quality.md`](../coverage/agent-quality.md); the backlog entry
is [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1b.

The prediction at the top of this file was right, and the price of being right
was **three more defects** — bringing this roadmap's total to six, of which the
three below were found by running it rather than by writing it. All three are
the same shape as the original three: *the tests and the code agreed with each
other, and both disagreed with production.*

| Found | What it was | Where |
| ----- | ----------- | ----- |
| **4** | **`T-Q8`'s verdict gate could never fire.** A verdict is filed against the *assistant* message (`Rate` refuses anything else); a candidate is keyed by the *user* message (717 of 717 real rows). `negative[c.MessageID]` looked a question up in a table of answers, so `skipped_negative` was structurally zero and **every turn a human marked wrong was learned from anyway** — the exact failure this page names as making T-Q8 negative-value. Fixed with `AnswerMessageID` + `VerdictKeys()` | `internal/domain/query_example.go`, `cookbook_candidate_repo.go`, `cookbook_service.go` |
| **5** | **`T-Q9`'s probe never ran on a real query.** The WHERE clause was located with `strings.Index(lower, " where ")` — a literal space on both sides. Models write multi-line SQL, so nothing ever probed. Every unit case was one line | `internal/tools/empty_result_probe.go` |
| **6** | **The fabrication mechanism's own question shape was uncovered.** An aggregate over no rows returns ONE all-NULL row, not zero rows, so `SELECT SUM(…) WHERE <no match>` — the `C-1` question, the `E-5` landmine — got no zero-row note and no probe. Fixed with `matchedNothing`; `COUNT(*) = 0` is deliberately still data | `internal/tools/run_sql.go` |

**~~Needs the stack, nothing else~~ — run 2026-08-11.** Four of the six passed
or were fixed and re-proven:

- ~~Migrations `054` and `055` up **and** down against a real Postgres.~~
  **Pass**, including down against populated tables. §1.
- ~~A second vote replacing the first rather than duplicating.~~ **Pass** at the
  storage layer. §2. ~~**The 400 and the 404 are still owed**~~ — **run
  2026-08-14 with the API booted, and it is defect 7.** Both 404s pass, and a
  foreign message id is indistinguishable from a nonexistent one. But a missing
  or out-of-range `rating` came back **500**: the validation errors were bare
  `fmt.Errorf` values and `feedbackFail` maps the unrecognised to 500, so the
  one failure the handler's own comment calls "a client bug" was reported as a
  server fault. The unit test asserted `err != nil` and agreed with the code.
  Wrapped in `domain.ErrInvalidInput`, mapped to 400, tests tightened to the
  sentinel, re-proven over HTTP.
- ~~A thread past 20 messages, confirming hydration carries the recent turns.~~
  **Pass** on a real 58-message thread: **zero overlap** between the old window
  and the new one. §5. The summary *block* reaching a prompt still needs a turn.
- ~~A zero-row query confirming `available_values` returns the column's real
  contents.~~ **Pass after two fixes.** §4.
- `POST /api/cookbook/harvest` — the candidate query is proven on **121 real
  candidates** and the negative gate is fixed and re-proven, but the harvest
  that *writes* an example needs an embedding call. §3.

**Blocked on a missing file, which is new and worse than a cost.** There is no
`.env` in this tree — only a stale `.env.example`. `LLM_API_KEY`,
`ARGENTUM_JWT_SECRET`, `ARGENTUM_DSN_KEY` and `DB_PASSWORD` are all absent, so
neither `cmd/api` nor `cmd/worker` boots and **model spend on 2026-08-11 was
$0.00** — not declined, unavailable. Everything below waits on that one file.
(The control Postgres volume was initialised with a `metabase` role rather than
the `argentum` `docker-compose.yml` declares; a recreated `.env` must match it.)

**Needs model spend:**

- ~~`make eval` on the 55-case set.~~ **Run twice on 2026-08-14** — 83.6%
  (46/55) at `1b42d99`, then **87.5% (49/56)** at `7a00657` after the five
  fixes the first run produced, both on `moonshotai/kimi-k2.6`, $1.07 the pair.
  The prediction held: the set landed inside 70–85% on its first run and did not
  need hardening. Triage in [`../coverage/eval-q1.md`](../coverage/eval-q1.md).
  **What the second run settled and what it did not:** `zero_row_trap`,
  `chart_dashboard` and `multi_source` reached 100%, while `guardrail` did not
  move at all — the off-topic fix was a prompt edit to a classifier running on
  `gpt-5-nano`, and it still admits the recipe. The original wording is kept
  below because its logic is what made the run worth doing.
  **The number this roadmap predicts is 70–85%,
  and that prediction is the point** — if it comes back above 95%, the new
  cases are too easy and the honest response is to harden them, not to bank it.
  Note that four cases are written to FAIL until their ticket works end to end
  (`follow-up-breakdown-no-reschema` needs T-Q6; `ambiguous-headcount` and
  `dirty-ask-rather-than-guess` need the model to actually reach for
  `ask_clarification`).
- ~~`make eval-matrix MODELS=…` across 2–3 models.~~ **Run 2026-08-14 twice:
  once as two single-model runs, and once for real after the classifier and
  asking-policy work — kimi-k2.6 **98.2% (55/56)** / $0.629 against
  deepseek-v3.2 **89.3% (50/56)** / $0.141, $0.77 for the pair.** `guardrail` is
  8/8 on both for the first time. The set is now **above the 95% line this file
  calls the moment to harden rather than bank**, and the reason is that one
  category carries the whole signal: `zero_row_trap` (kimi 2/3, deepseek 0/3) is
  the only category either model fails. The case both fail is a product finding
  — a `COALESCE(sum(…),0)` metric template makes an out-of-coverage window a
  genuine 0, so `query_metric` cannot say "this is NOT a zero" and both models
  answered Rp 0 for a quarter the data does not reach
  ([`../coverage/eval-q1.md`](../coverage/eval-q1.md)).
- ~~A before/after on `T-Q3`~~ — **run 2026-08-16, and it found nothing.** Same
  model, same tenant, differing in the prompt only: **54/56 with the guideline
  and 54/56 without**, from two different pairs of failures. deepseek's
  before-arm produced no unrequested chart on the six chart cases or the
  Indonesian five. kimi's off-arm produced exactly one — a card and a dashboard
  for `id-kanal-terbesar` — and the same sitting measured a **±2-case
  run-to-run noise band on this set**, which is larger than the effect. So T-Q3
  is still a prompt change with no number behind it; what changed is that this
  is measured rather than assumed.
  **The ticket did expose a real gap in the instrument.** All three
  `no_chart_wanted` cases are in English, and the one violation landed on an
  Indonesian question wanting a channel name and a figure — the exact shape the
  guideline's own text uses as its example — where the case asserted only
  `must_call: [run_sql]`. All five `indonesian` cases now carry
  `must_not_call: [create_visualization, create_dashboard]`
  ([`../coverage/eval-q1.md`](../coverage/eval-q1.md) §5).
- ~~The `T-Q6` pair itself (`PRIOR_WORK_TURNS=3` vs `=0`) and the `T-Q7` summary
  block~~ — **both run 2026-08-14, eight turns, ~$0.30.**
  **`T-Q7` passes**: `thread summary injected summary_chars=202 message_count=60
  history_window=20` on the 58-message thread, and the reply reconstructs the
  opening alert. That log line did not exist — the function had four silent exits
  and no success log, so an injected summary and a skipped one were
  indistinguishable, and one of the exits disables the feature on every thread.
  **`T-Q6`'s mechanism passes and its acceptance line does not measure it.** The
  `tool` rows exist now (the baseline below is closed), and injection fires at
  `=3` and never at `=0` while the rows are written at both — but the two arms
  produced *identical* tool sequences over three turns each. Inside
  `CONTEXT_MAX_TURNS` the assistant's own prior message quotes the SQL it ran, so
  the digest is repeating what the model can already read; T-Q6 only earns its
  place once the tool turn has fallen out of the window. **A two-turn
  conversation cannot produce this difference on any model that reads its own
  history**, which makes the acceptance line as written unmeasurable rather than
  unmet. ~~Re-specify it against a thread longer than the memory window.~~
  **Re-specified 2026-08-14** — see §T-Q6 "Acceptance, re-specified" above. The
  cheap arm is two runs of one eval case (`follow-up-breakdown-no-reschema` at
  `PRIOR_WORK_TURNS=3` and `=0`), which is the measurement the matrix run
  produced by accident and this line now asks for on purpose.
- The `T-Q8` harvest that writes an example, and a turn that retrieves one. One
  embedding call per example; everything above `client.Embed` is proven.

**One item is owed that was not owed this morning.** The 2026-08-11 gate changed
`run_sql`'s payload on both no-data paths (defects 5 and 6). Rule 1 of
`eval-baseline.md` makes that a re-run of the set — so the eval above now
answers two questions, not one, and the `zero_row_trap` category is the one to
read first.

**Needs a browser:** the feedback control in the chat transcript — the thumbs,
the reason box after a thumbs-down, and that `role: tool` rows stay invisible.

**Not built, deliberately:** no dashboard UI for the feedback list or the
cookbook. Both are admin-only JSON routes today. The tuning list wants to be a
page, and what that page should show depends on what the first week of real
verdicts looks like — which is an argument for building it after the gates
above, not before.

---

# Added 2026-08-17 — `T-Q11`, from a gate rather than from a plan

The 2026-08-17 live gate found a persisted answer stating a figure no tool ever
returned, **in front of the true one**, on a turn deliberately run with that
sitting's new feature switched off. It is older than the build that exposed it,
it is the worst thing in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md), and §3b
of that file files it as *"not a gate that is owed; a defect that needs a
ticket"*. This is the ticket.

It belongs on this roadmap because it is the **wrong-but-nonempty** class this
page assigns to `T-Q9` — arriving one door further out than `T-Q9` looked.

## `T-Q11` A reply carries one iteration's figures, and an ungrounded one is counted — 1.5d
**Repo:** BE · **Deps:** none · **Priority:** P0 · **Migration:** none

> **Built 2026-08-18** — `internal/app/turn_answer.go` (new), `chat_runner.go`,
> `internal/metrics/{collector,prometheus}.go`, `internal/tracing/tracing.go`.
> The record is now the last iteration that produced prose, and an ungrounded
> figure increments a counter, lands on the turn's span and appears on the
> turn's completion line. **The live half is owed** — the two November/December
> questions re-asked, and the 56-case re-run rule 1 demands
> ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2).
>
> **One thing the build found that this ticket did not predict.** The SDK can
> withhold intermediate content and replay it *after* the last iteration
> (`filterIntermediateContent`), and its final synthesis call is tagged
> `final_call` with no iteration number at all. Choosing "the last prose that
> arrived" would therefore have stored the narration on one path and nothing on
> the other, so the choice is made on the iteration number with the synthesis
> call winning outright. This deployment sets `IncludeIntermediateMessages:
> true` (`bootstrap/stack.go:680`) and so takes neither path today — which is
> exactly why it had to be handled: the setting is one line from changing.

### Why

Asked *"How many transactions were there in November 2024?"*, the answer of
record reads:

> There were **1,667 transactions** in November 2024. There were **1,667
> transactions** in November 2024. There were **300 transactions** in November
> 2024.

`300` is correct and is what the turn's own `run_sql` returned. `1,667` is in no
table — `fact_sales` holds 1,348 rows altogether. The December turn has the same
shape in front of its true 310. Reproduction and the event-stream evidence:
[`../coverage/next-steps-and-revision.md`](../coverage/next-steps-and-revision.md)
§6.5.

**Two independent failures produced one answer, and each is worth fixing on its
own.**

**1. The reply is every iteration's prose concatenated.** `runStream` appends
each `AgentEventContent` to one builder (`internal/app/chat_runner.go:1218`), and
the turn carried `iteration: 2`. The model guessed a figure, called the tool, and
wrote the true figure once the result came back — and all three sentences were
stored as the answer. The same loop **already reads the iteration number** off
the event metadata two lines above (`:1205`, for the `iteration` progress event),
so the information needed to fix this is in the function and is thrown away.

**2. The detector exists and only whispers.** `checkGrounding`
(`chat_runner.go:1358`) runs `guardrails.CheckGrounding` on exactly this
question — *"is THIS number one of the numbers a tool returned?"* — and 1,667
against a result set holding 300 is precisely what it reports. It writes one
`Warn` line and returns. Nothing counts it, nothing surfaces it, and no gate in
this repository has ever read it. So the product had both the evidence and the
detector, and still stored the wrong answer.

`T-Q9` chose to report rather than block for a good reason that still holds — an
analyst's reply legitimately contains numbers no query returned. This ticket does
not overturn that. It fixes the *mechanism* that put an unevidenced figure in the
reply, and turns the report into something that can be read without grepping a
worker log.

### Do

- **Keep prose per iteration in `runStream`.** One builder per iteration key
  rather than one for the turn, using the `iterationOf(evt.Metadata)` the loop
  already computes. The answer is the **last** iteration that produced content.
  Earlier iterations' prose is a working note, not an answer: the model that
  wrote it had not yet seen the tool result it went on to call for.
- **Fall back to the concatenation when the iteration number is absent.** Not
  every provider stamps it, and a turn whose events carry no iteration must
  behave exactly as it does today — this ticket must not empty a reply on a
  provider it was not measured against.
- **Do not drop prose from an iteration that called no tool.** A model that
  writes two paragraphs in one iteration, or narrates across a turn that never
  called anything, is not the failure; the failure is prose written *before* a
  tool result and kept *beside* it.
- **Count the grounding report.** A counter — `ungrounded_figures_total`, beside
  the existing turn/tool counters in `internal/metrics` — plus the count on the
  turn's span. One `Warn` line per occurrence is what made this invisible for a
  week.
- **Say it in the log line the answer already carries**, not in a second one: the
  number of ungrounded figures belongs on the turn's completion fields so a turn
  can be found by it.

### Notes for the implementer

**The contradiction shape is the one to test.** A reply containing both an
ungrounded figure *and* a grounded one for the same quantity is not an analyst
being loose with a derived number — it is a reply that disagrees with itself. It
is also exactly what iteration-scoped prose removes, so the test for the fix is
the transcript above: the same event sequence must store the last sentence only.

**Do not make `CheckGrounding` a gate in this ticket.** The overreach cycle this
repo has lived through is documented in
[`../coverage/guardrail-overreach.md`](../coverage/guardrail-overreach.md), and a
check that replaces answers needs the counter above to exist for a week first.
Blocking is a separate decision with a number behind it.

**`rescueEmptyReply` sits after this.** A turn whose last content-bearing
iteration is empty must reach the rescue exactly as it does today, not with an
earlier iteration's guess.

### Acceptance

- [x] A turn whose first iteration writes prose, calls a tool, and writes prose again stores **only** the post-tool prose
- [x] A turn with a single iteration is byte-identical to today, on every provider
- [x] A turn whose events carry no iteration metadata is byte-identical to today — the fallback is keyed on whether *any* content event was tagged, so a provider that stamps nothing gets one bucket and the concatenation this replaced
- [x] The streamed `delta` events are unchanged — the reader watches the model think; the *record* is what this ticket narrows
- [x] An ungrounded figure increments a counter and lands on the span, in addition to the existing `Warn` — `ungrounded_figures_total` and `ungrounded_replies_total`, `argentum.ungrounded_figures` on `agent.turn`, and `ungrounded` on the new `turn completed` line
- [x] The November transaction transcript, replayed as a fixture, stores `300` and not `1,667` (`TestAnswerKeepsOnlyThePostToolIteration`)

### Gate

`make vet` / `make test`, then the stack: re-ask the two questions from the
2026-08-17 transcript (November and December 2024 transaction counts) on
`moonshotai/kimi-k2.6` and read the persisted `messages.content` — one sentence,
one figure, and it is the tool's. Then `curl` `/metrics` and show the counter
moved on a turn engineered to state a derived figure. Rule 1 applies: this
changes what reaches the user on every turn, so re-run the 56-case set and post
the number with the model and the date.

### Out of scope

Making the grounding check block or rewrite a reply. Per-figure attribution in
the UI. The eval category for it (`T-Q1`'s set already has `wrong_grain`; whether
this needs its own case is a question for after the counter has run for a week).

---

# Added 2026-08-18 — `T-Q12`, from `T-D22`'s edit gate

The `T-D22` gate ran on 2026-08-18 and the tool passed every mechanical
property it was written to prove. What failed was the turn around it: **two
consecutive turns told the user an edit was done, having called no tool at
all**, on a dashboard that never changed. Transcript, control and the timestamps
are in [`../coverage/native-dashboards.md`](../coverage/native-dashboards.md)
§4.2.

It belongs on this roadmap and next to `T-Q11` because it is the same class —
an unevidenced claim reaching the user of record — arriving through a mechanism
`T-Q11` does not touch. `T-Q11` is about a figure inside one turn's prose. This
is about what one turn tells the *next* turn it did.

## `T-Q12` A refused tool call is remembered as a call that ran — 1.0d
**Repo:** BE · **Deps:** none (shares a fixture with `T-Q11`) · **Priority:** P0 · **Migration:** none

> **Built 2026-08-18** — `internal/app/tool_digest.go`, `chat_runner.go`,
> `internal/agentbudget/budget.go`. A refused call carries
> `status: refused` and its reason, and renders as *"REFUSED, it did NOT run"*
> above a block that now says in words that refused work must be done rather
> than reported. **The live half is owed**: repeat the 2026-08-18 sequence and
> read `agent_actions` for the second turn — a tool call must appear.
>
> **Two things the build settled that the ticket left open.** The Go-error path
> *does* emit `AgentEventToolResult`, so no second construction site is needed —
> but the result is the plain string `"Error executing tool: …"` (`Error: …` on
> the Anthropic path), which is not JSON, so it had to be read from the raw
> event rather than from the parsed map. And `DedupeDigests` needed the outcome
> in its key: without it, a call that was refused and then made properly
> collapsed to the refusal, and the successful retry disappeared — the same
> defect wearing the opposite sign.

### Why

Turn 1 spent its iteration budget, so its last `update_dashboard` was refused by
`agentbudget` — correctly, and as a **result** rather than a Go error, which is
the design that stops a model looping on a refusal:

```json
{"budget_exhausted": true, "reason": "iteration budget spent (8 of 8)", "instruction": "…"}
```

`BuildToolDigest` (`internal/app/tool_digest.go:82`) decides a call failed by
looking for `result["error"]` or `result["err"]`. **That payload has neither.**
So the digest persisted as the thread's `role: tool` row read:

```json
{"tool":"update_dashboard","rows":-1}
```

which is byte-for-byte what a *successful* call with no row count looks like.
The next turn hydrates that as prior work, reads "update_dashboard already ran,
no error", and answers *"Done. The dashboard has been updated"* in one iteration
with zero tool calls. Then **its own confirmation becomes the next turn's
evidence**, and turn 3 repeats it.

`agentbudget.IsRefusal(result)` already exists for precisely this distinction —
`internal/agentbudget/budget.go:438`, whose comment says the audit log needs it
because *"both come back as a string with a nil error, because that is how the
model has to receive them"*. The audit table calls it and records
`result_status = blocked`. The memory the agent reads does not call it.

**The control is what makes this a diagnosis.** Same model, same tenant, the
same sentence (*"rename that dashboard to X"*), on a thread whose history holds
a genuine success: the tool was called and the rename landed. Two histories, two
behaviours, one differentiator.

**Three things are true at once and only one of them is the model's fault.** The
refusal is well designed. The audit row is correct. The digest is lossy in the
one direction that matters — it can only ever cause a turn to believe *more* was
accomplished than was.

### Do

- **Set `ToolDigest.Err` from the refusal payload.** `BuildToolDigest` calls
  `agentbudget.IsRefusal` (or reads `budget_exhausted` directly, if the import
  direction is wrong) and records the reason. A refused call must never render
  as a plain one.
- **Give the digest an explicit outcome rather than an inferred one.** `Err`
  being empty currently means "fine" *and* "we could not tell". Those are
  different, and the second one is what produced this. A three-state
  `status: ok | failed | refused` read from what the executor actually returned
  is the honest shape; `RenderPriorWork` renders it in one word.
- **Render a refused call as unfinished work, in words.** *"update_dashboard was
  refused (iteration budget spent) — it did NOT run"* is the sentence the next
  turn needs. `RenderPriorWork` today lists what was called, which is an
  invitation to assume it worked.
- **Do not drop refused calls from the digest.** The instinct is to omit them;
  it is wrong for the reason the digest exists — a turn that cannot see the
  refusal re-runs the expensive discovery that preceded it. Carry them, marked.
- **Check the Go-error path too.** The errored `create_dashboard` in the same
  turn left **no digest at all**, so the same "what happened is invisible"
  problem exists one door over, from the opposite cause. Confirm whether a tool
  that returns a Go error emits `AgentEventToolResult`; if it does not, the
  digest has to be built where the error is known.

### Notes for the implementer

**This is not fixable in the prompt.** A sentence telling the model to verify
before claiming competes with a context block asserting the call happened, and
the context block is evidence. Fix the evidence.

**The self-confirming half deserves a thought and probably not code.** Once a
turn has said *"Done"*, that sentence is ordinary transcript. Nothing here
proposes marking assistant prose as unverified — but the fix above is what stops
the first one, and no second one occurs without it.

**Consider whether a claimed mutation should be checkable at all.** Out of scope
here, and worth writing down: the write-shaped tools (`create_dashboard`,
`update_dashboard`, `schedule_task`, `propose_action`) are the ones where a
false *"Done"* is invisible, and a turn that names one in the reply without
having called it in that turn is a detectable shape. That is a `T-Q13`-sized
idea and it needs the counter from `T-Q11` to exist first.

### Acceptance

- [x] A digest built from a budget-refusal payload carries the refusal and its reason
- [x] `RenderPriorWork` renders that call as not having run, in a sentence a model cannot read as success
- [x] A digest for a call that errored carries the error (and one is produced at all) — read off the raw result, because a Go error never reaches the parsed map
- [x] A successful call's digest is byte-identical to today — `status` is omitted for `ok`, so a stored row is unchanged and `Outcome()` reads the absence
- [x] Unit test: the exact turn-1 payload above, asserted to produce a non-empty failure marker (`TestBuildToolDigestMarksABudgetRefusal`)
- [x] Regression fixture: a two-turn thread where turn 1's last call is refused, asserting turn 2's prior-work block says so (`TestARefusedCallReachesTheNextTurnAsUnfinishedWork`)

### Gate

`make vet` / `make test`, then the stack, and it costs about four turns: repeat
the 2026-08-18 sequence — a create turn engineered to exhaust the budget
(`AGENT_MAX_ITERATIONS=8` and a request needing schema discovery does it), then
an edit turn — and read `agent_actions` for the second turn. **A tool call must
appear.** The control arm is the same edit on a thread with a clean history,
which already passes today and must keep passing.

### Out of scope

Verifying that a claimed action occurred (the `T-Q13` idea above). Changing the
refusal payload's shape — it is read by the model and by `IsRefusal`, and both
work. Anything about the iteration budget itself: 8 was not too small for the
work, it was spent on two validator retries, and that is `T-D24`'s subject.

---

# Delivery record — 2026-08-18 — both P0s from the gates are built

`T-Q11` and `T-Q12` landed in one sitting, unit-gated, with `make vet`,
`go test ./...`, `make lint-go` (0 issues) and `make types-check` clean. They
were written from two different gates a day apart and they share one sentence:
**something reached the user of record that no evidence supports.** `T-Q11` is a
figure inside a turn; `T-Q12` is what one turn tells the next it did.

| Ticket | Landed | Where |
| ------ | ------ | ----- |
| `T-Q11` | Prose kept per tool-calling iteration; the record is the last iteration that produced any. The synthesis call wins outright; a turn no provider tagged concatenates exactly as before | `internal/app/turn_answer.go` (new, + tests), `chat_runner.go` `runStream` |
| `T-Q11` | `ungrounded_replies_total` / `ungrounded_figures_total`, `argentum.ungrounded_figures` on the turn span, and a `turn completed` log line carrying the count beside latency and tool calls | `internal/metrics/{collector,prometheus}.go`, `internal/tracing/tracing.go`, `chat_runner.go` |
| `T-Q12` | A three-state digest outcome — `ok` / `failed` / `refused` — read from what the executor returned rather than inferred from an absent `error` key | `internal/app/tool_digest.go`, `internal/agentbudget/budget.go` (`IsRefusalPayload`, `RefusalReason`) |
| `T-Q12` | The prior-work block says a refused call did not run, and tells the turn to do the work rather than report it | `internal/app/tool_digest.go` `RenderPriorWork` |

## Four things the build learned that the tickets did not know

**1. The Go-error path emits a tool-result event, but not a JSON one.**
`T-Q12` asked whether one is emitted at all. It is — and the payload is the
plain string `"Error executing tool: …"` (`Error: …` on the Anthropic path),
which unmarshals to an empty map. So the raw result now travels beside the
parsed one, and only the SDK's own two prefixes count as failure: a tool that
answers in prose is not a failed tool, and calling it one would tell the next
turn its work is undone.

**2. Dedupe had the same defect wearing the opposite sign.** `DedupeDigests`
keyed on tool, source, metric and query — so a call refused by the budget and
then made properly in the next turn collapsed to *the refusal*, and the
successful one vanished. Marking refusals without fixing the key would have
turned "it thinks it ran" into "it thinks it never ran".

**3. The SDK can replay withheld prose after the answer.**
`filterIntermediateContent` captures intermediate content and replays it once
the loop ends, so arrival order is not iteration order; the final synthesis call
is tagged `final_call` and carries no iteration number at all. This deployment
sets `IncludeIntermediateMessages: true` and meets neither path — and both are
one config line away, which is why the selection is made on the iteration number
rather than on what arrived last.

**4. The counter is written where nothing scrapes it.** A turn runs in
`cmd/worker`, which has no HTTP surface, so `/metrics` on `cmd/api` will not show
these move. That is T-17's debt rather than this ticket's, and until it is paid
the number is read from the `turn completed` line and the span. Written down
here because a gate that curls `/metrics` and sees zero would otherwise read as
a failed fix.

## What is owed

Both live halves, and both are model spend — filed in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2 the day
they were written rather than after, which is the mistake that file exists to
record. `T-Q11` additionally triggers rule 1: it changes what reaches the user on
every turn, so the 56-case set is owed on both models with the number and the
date posted.

---

# Added 2026-08-18 — from the gate that closed `T-Q11`, `T-Q12` and `T-D24`

The live halves of the 2026-08-18 build were run the same day
([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1g).
All three tickets passed what they were written to prove. The two tickets below
are what the sitting found *beside* them, and both are the same family as
`T-Q11`/`T-Q12`: something reached the user of record that no evidence supports.

## `T-Q13` A claimed action nobody performed, with no refusal behind it — 1.5d · **built 2026-08-18, unit-gated; live half owed**
**Repo:** BE · **Deps:** none (extends `T-Q12`'s ground) · **Priority:** P0 · **Migration:** none

### Why

`T-Q12` closed the *cause* it was written for — a refused call remembered as one
that ran — and the fix is proven live (§1g). **The failure it was written
against is still reachable, and this time nothing was refused at all.**

On a thread whose history holds one clean, successful `create_dashboard`, the
sentence *"Rename that dashboard to 'Q4 2024 Sales Review'."* produced:

> Done — your dashboard is now called **[Q4 2024 Sales Review](/dashboards/b410d600…)**.
> The URL stays the same, so any existing links will continue to work.

`agent_actions` holds **no row for that turn**. The stored `title` was still
`Q4 2024 Sales`. The worker line reads `tool_calls=0`, and the SDK's own log says
`iteration=1 … Skipping final synthesis call - already got complete response`.
No budget refusal fired — the turn simply never called a tool and reported the
work as done anyway.

**The control, run immediately after on the same thread with the same sentence**,
called `update_dashboard`, moved `updated_at`, and landed the title. A third
attempt under the same tight budget as the first was *honest* — it reported the
refusal and correctly named the current title. So this is **non-deterministic,
not systematic**, which is what makes it a detection problem rather than a prompt
problem: one turn in three claimed work it had not done, and the product shipped
that claim to the user of record with no signal attached.

**Why no guardrail sees it.** `CheckFabrication` asks whether the turn has
evidence. `CheckGrounding` asks whether every *figure* came from a tool — and
these replies contain no figure. The claim is an **action**, and nothing in this
product checks that a claimed mutation happened. `native-dashboards.md` §4.2 said
exactly this on 2026-08-18 and assigned no ticket to it; this is that ticket.

It is arguably worse than a wrong number: a wrong figure is visible to somebody
who knows the business, while *"Done"* about an edit that did not occur is
invisible until the dashboard is opened — possibly by somebody else, possibly
next quarter.

### Do

- A post-turn check in the same chain as `rejectFabrication` and `checkGrounding`
  (`chat_runner.go:753-757`), reading the turn's own snapshot rather than the
  reply's text: if the reply **claims a completed mutation** and the turn made
  **no successful mutating tool call**, the claim is not evidenced.
- The mutating set is a property of the registry, not a list in a guard: mark
  the tools that change stored state (`create_dashboard`, `update_dashboard`,
  `schedule_task`, `propose_action`'s execute path, `generate_document`) with a
  `Mutating() bool` on the tool interface, so a tool added later cannot be
  forgotten by a constant somewhere else. That is the `T-14`/`list_watchers`
  lesson: a promise kept in a second place drifts from the first.
- Detect the claim cheaply and conservatively. Past-tense completion language
  about a named artifact — *"Done"*, *"has been updated/renamed/created"*,
  *"is now"* — in the presence of zero successful mutating calls. Indonesian too
  (*"sudah"*, *"telah diubah"*, *"berhasil"*), which is the `T-Q3` lesson: the
  instrument was English-only and the violation landed in Indonesian.
- **Count first, block later.** `unevidenced_actions_total`, on the span and on
  the `turn completed` line beside `ungrounded`, exactly as `T-Q11` did it. This
  ships as an instrument — the wrong-but-nonempty rate cannot be tightened
  before it is counted, and a guardrail that replaces a correct reply is the
  failure this repo has lived through six times.
- Only once it is counted and the false-positive rate is known: the reply is
  rewritten to say what the turn actually did, the way `rejectFabrication`
  already replaces a reply rather than editing it.

### Notes for the implementer

**Do not read this off the prior-work digest.** That is `T-Q12`'s ground and it
is already correct; this turn's failure is in *this* turn's snapshot, which
`tracker.Snapshot()` already carries (`snap.Tools`, `snap.ToolCalls`).

**A turn that legitimately reports a *past* action must not trip it.** *"The
dashboard I built earlier is still called X"* is not a claim about this turn.
Scope the detection to completion language, not to any past tense — and when in
doubt, count and do not act, which is what shipping it as an instrument buys.

### Acceptance

- [ ] A turn claiming a completed edit with zero mutating calls is counted, and the count appears on the `turn completed` line and the span
- [ ] The control — the same sentence on a turn that *did* call `update_dashboard` — is not counted
- [ ] A reply reporting an action from an earlier turn is not counted
- [ ] The Indonesian completion phrasings are covered by the same table test as the English ones
- [ ] A turn whose mutating call was **refused** is counted (it claims work that was refused, which is `T-Q12`'s sequence seen from the other end)

### Gate

Repeat §1g's sequence — the create turn, then the rename on the same thread —
until one turn claims an unperformed edit (one in three at
`AGENT_MAX_ITERATIONS=2` on `kimi-k2.6`), and show the counter moving on that
turn and not on the control. **~$0.10, about six turns.** Rule 1 does not apply
while it only counts; it does the day it rewrites a reply.

## `T-Q14` A misquoted figure is inside the grounding tolerance — 0.5d · **built and gated 2026-08-18**
**Repo:** BE · **Deps:** none · **Priority:** P1 · **Migration:** none

### Why

Asked for Q4 2024 monthly revenue, a turn printed December as
**$3,860,405,700.00**. Its own `run_sql` returned **3,863,405,700.00**. The
figure is wrong by three million rupiah-scale units, it is wrong in a table the
reader would quote from, and **`CheckGrounding` reported the reply clean.**

The reason is `ungroundedTolerance = 0.01` (`internal/guardrails/grounding.go:42`).
The misquote is 0.078% off, so `closeEnough` matches it against the true value.
The same turn's *derived* quarter total was flagged, so the instrument was awake
— it simply cannot see a transcription error smaller than one percent, and one
percent of a billion is ten million.

**The tolerance is right for the reason it was added** and must not simply be
lowered: the system prompt *requires* magnitude rendering, so "Rp 3,86 Miliar" is
the correct way to write 3,863,405,700 and must keep reading as grounded. That is
what `sameMagnitudeRendering` is for.

**The fix is to stop asking one number to do both jobs.** A figure written at
full precision — grouped digits, decimals to the cent — is making an exact claim
and should be matched exactly. A figure written in magnitude units or visibly
rounded is making an approximate claim and keeps the tolerance it needs.

### Do

- Classify each stated figure at extraction time as *exact* or *rendered*:
  decimals present, or a full grouped integer with no magnitude word beside it,
  is exact; a magnitude suffix (Miliar/Juta/Triliun/bn/m) or an obviously
  rounded short form is rendered.
- Exact figures match with a tolerance near zero (float equality within 1e-9,
  which is what the parse can guarantee). Rendered figures keep `0.01` and
  `sameMagnitudeRendering` unchanged.
- The sum/difference pass in `grounded()` keeps the loose tolerance regardless:
  a derived total is arithmetic the model did, and rounding there is expected.

### Acceptance

- [ ] `$3,860,405,700.00` against a returned `3863405700` reports ungrounded
- [ ] `Rp 3,86 Miliar` against the same returned value still reports grounded
- [ ] `$3,863,405,700.00` against the same value reports grounded
- [ ] A derived total that is the sum of two returned values stays grounded at the loose tolerance
- [ ] The 56-case set does not move (this changes a log line, not a reply)

### Gate

A table test is most of it. The live half is one turn that prints a full-precision
table, which §1g already produced and stored — **$0.00**, because the reply is
already persisted and the check runs on stored text.

## `T-Q15` Every published score names a model nobody pinned — 0.5d · **built and gated live 2026-08-19**
**Repo:** BE (eval harness) · **Deps:** none · **Priority:** P1 · **Migration:** none

> **Status, 2026-08-19: built, unit-gated (16 new tests) and gated live for
> $0.0500.** Three cases re-run twice on `kimi-k2.6` plus one on
> `deepseek/deepseek-v3.2`, and the first run answered the ticket's own question
> in one line: **`served: moonshotai/kimi-k2.6 via Baidu`**, against
> **`deepseek/deepseek-v3.2 via AtlasCloud`**. Neither upstream is named in a
> single number this repo has published, and the upstream is the variable that
> moved on 2026-08-18.
>
> **One acceptance line is met differently than written.** *"`golden.yaml` pins
> a revision where the provider offers one"* — it offers none. OpenRouter's
> catalogue does carry dated aliases (`moonshotai/kimi-k2-0905`), and it has one
> for neither `moonshotai/kimi-k2.6` nor `deepseek/deepseek-v3.2`. The set now
> declares both ids with that check written beside them, and the revision the
> alias cannot carry is recorded on the *report* instead.
>
> Record: [`../coverage/delivery-log.md`](../coverage/delivery-log.md) Phase 3i.

### Why

The 2026-08-18 rule-1 re-score put `deepseek/deepseek-v3.2` six cases below its
2026-08-14 number, outside the ±2 band the set carries — and **half those
failures were an English question answered in Indonesian**, which no ticket in
this repo touches. It was resolved only by building a worktree at the commit from
ninety minutes earlier and re-running six cases: four failed identically, so the
regression predates the build and the live candidate is the provider changing
`deepseek-v3.2` underneath us.

**That resolution was luck.** It worked because the previous commit was hours
old. Every published number in [`../coverage/eval-q1.md`](../coverage/eval-q1.md)
— the 83.6%, the 87.5%, the 98.2%, the 89.3% — names a model string and no
revision, so none of them can be re-run as the same measurement, and a future
drop has no baseline that means anything. The set exists to tell whether *the
tree* got better or worse; against an unpinned model it cannot answer that at all.

### Do

- Record what the provider actually served beside every score: OpenRouter returns
  the resolved model in the response body, and the harness already reads that
  response. Put it in the JSON report, in the printed summary, and in the row
  anyone pastes into a coverage doc.
- Where the provider supports a dated or revision-pinned alias, use it in
  `golden.yaml`'s model list and say in the file why the alias is pinned.
- When a re-score moves more than the noise band, the harness should say what it
  can: print the previous report's resolved model beside this one's when `-out`
  points at a directory that already holds one.
- Backfill nothing. The published numbers stay as they are, with one sentence in
  `eval-q1.md` saying they name no revision — rewriting history to look pinned
  would be worse than the gap.

### Acceptance

- [ ] A run's JSON report and printed summary both carry the provider-resolved model identifier
- [ ] Two runs against different resolved revisions are visibly different in the report
- [ ] `golden.yaml` pins a revision where the provider offers one, with the reason in a comment
- [ ] The published-score sentence in `eval-q1.md` names the gap for every number older than this ticket

### Gate

One re-run of any three cases, showing the resolved identifier in the report.
**~$0.02.** No rule-1 implication: this changes what a report records, not what
reaches a user.
