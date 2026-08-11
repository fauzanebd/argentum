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

Nothing here is blocked on writing code. Every item needs the stack up, model
spend, or a browser — the three buckets
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) sorts by,
and its own opening claim applies unchanged: *the live half found something the
unit tests could not, on every ticket where it was run.*

**Needs the stack, nothing else** — the bucket that gets run, and the one that
has produced eleven findings across three sittings:

- Migrations `054` and `055` up **and** down against a real Postgres.
- One `POST /api/messages/:id/feedback` round trip, plus the refusals: a user's
  own message (400), another tenant's message (404), a second vote replacing
  the first rather than duplicating.
- A two-turn conversation with `PRIOR_WORK_TURNS=3`, confirming the second turn
  does not call `get_schema` — and the same with `=0`, confirming it does. That
  pair is the T-Q6 measurement; the write-but-do-not-read setting exists for it.
- A thread past 20 messages, confirming the summary block appears and that
  hydration now carries the recent turns rather than the opening. This is
  defect 1 above, and it needs a long thread to see at all.
- `POST /api/cookbook/harvest` against a tenant with history, then a turn that
  retrieves an example. Then the gate: thumbs-down an answer, re-harvest, and
  confirm `skipped_negative` moved and the example was not written.
- A zero-row query against a real warehouse, confirming `available_values`
  comes back with the column's actual contents.

**Needs model spend:**

- `make eval` on the 55-case set. **The number this roadmap predicts is 70–85%,
  and that prediction is the point** — if it comes back above 95%, the new
  cases are too easy and the honest response is to harden them, not to bank it.
  Note that four cases are written to FAIL until their ticket works end to end
  (`follow-up-breakdown-no-reschema` needs T-Q6; `ambiguous-headcount` and
  `dirty-ask-rather-than-guess` need the model to actually reach for
  `ask_clarification`).
- `make eval-matrix MODELS=…` across 2–3 models. Every quality number this
  project has ever published is `deepseek/deepseek-v3.2` and nothing else.
- A before/after on `T-Q3`, which is a prompt change with an argument behind it
  and no number — exactly the shape `docs/coverage/eval-baseline.md` rule 1
  exists to stop shipping unmeasured.

**Needs a browser:** the feedback control in the chat transcript — the thumbs,
the reason box after a thumbs-down, and that `role: tool` rows stay invisible.

**Not built, deliberately:** no dashboard UI for the feedback list or the
cookbook. Both are admin-only JSON routes today. The tuning list wants to be a
page, and what that page should show depends on what the first week of real
verdicts looks like — which is an argument for building it after the gates
above, not before.
