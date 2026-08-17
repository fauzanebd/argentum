# The loop after the answer — what was built

`T-Q10`, `T-U13`, `T-D22` and `T-D23`: the four tickets in
[`../plan/05-next-steps-and-dashboard-revision.md`](../plan/05-next-steps-and-dashboard-revision.md).
Two tracks, one missing loop — the product ended every turn at a full stop, and
a dashboard the agent built could only be created or deleted.

> **Status: gated live 2026-08-17, the same day it was built.** The stack-only
> half and the model-spend half of §1f both ran, in one sitting, for about
> **$0.12** of spend. **Two defects**, one of them in this build and one older
> and worse than anything this build touched. §6 is the record; the paragraph
> below is what it replaced, kept because the prediction was right.
>
> ~~Nothing here has been run against a live stack or a real model. That is the
> half this repo's own delivery log says finds the defects — six of the nine on
> the quality roadmap were found by running it rather than by writing it — so
> read §5 before reading the ticks.~~ **The eighth sitting paid out twice.**

---

## 1. What landed

| Ticket | What | Where |
| ------ | ---- | ----- |
| `T-D22` | `update_dashboard`: a **patch** against a stored dashboard, resolved to this thread's when no id is given, refusing a source change, merged through `DashboardService.Update` so validation and the zero-row warning stay one code path | `internal/tools/update_dashboard.go`, migration `057`, six template cards, `agents.yaml` |
| `T-Q10` | One light-model pass after every answered turn, narrowed server-side, persisted on `messages.metadata` and published on the `final` event | `internal/app/nextsteps.go`, `domain.NextStep`, `chat_runner.go`, `thread_service.go` |
| `T-U13` | The chips, and the only evidence they work: a pick writes a row | `features/chat/next-steps.tsx`, migration `058`, `POST /api/messages/:id/suggestion-picked` |
| `T-D23` | "Ask for a change" from the dashboard itself, prefilled and never sent | `features/dashboards/dashboard-view.tsx`, `store/composer.ts` |

Two migrations, `057_agent_tools_update_dashboard` and `058_suggestion_picks`.
Both are additive, and both were applied, reversed and re-applied against the
real control database on 2026-08-17 — §6.1 and §6.2.

---

## 2. Four decisions that departed from the ticket

Each of these was written down because the ticket said something else, and in
three of the four the ticket's version is wrong rather than merely different.

### The digit rule would have deleted the best suggestions

`T-Q10` says: *"drop any step containing a digit run of 4+ (a suggestion is not
a place to restate a figure)"*. A year is a digit run of four. That rule deletes
*"compare with 2024"* and *"how did Q4 2025 finish"* — which are among the most
useful suggestions this feature can make and the ones a business user is most
likely to click.

The rule's actual job is to stop the agent restating a figure it computed, so
`restatesAFigure` draws the line where a figure is distinguishable from a period:
a grouped or decimal number is a figure (nothing writes a period as `1,234`),
five or more digits is a figure, and exactly four digits is a year if it is
inside 1900–2099 and a figure otherwise. **A test is what found this** — the
first version of the rule was the ticket's, and `TestRestatesAFigureTellsPeriodsFromResults`
failed on `Compare with 2024`.

### The `next_steps` usage event would have been a second, wrong number

`T-Q10` asks for *"one `usage_event` per pass, kind `next_steps`, with the token
counts"*. There already is one: the pass calls the tenant's `MeteredLLM`, which
records an `llm_call` with the real token counts, and `UsageService.append`
stamps `metadata.feature` on it — the mechanism `T-B2` built for exactly this
question. A second event beside it would either double-count the cost or record
a zero-cost row next to the real one, and a free-looking LLM pass in the usage
table is the shape of the C-2 defect this product has already shipped once.

So: one event, correctly priced, `feature = next_steps`, separable in the usage
table without a migration. The ticket's requirement is met in substance and not
in wording.

### Held tools are read off the built agent, not off the roster row

`T-Q10` says the held tools come *"from `agentscope`, not from a constant"*.
`agentscope.Scope` carries sources and MCP servers, not tool names. The roster
row's `allowed_tools` is empty for an unrestricted agent and says nothing about
what this *deployment* registered, and a constant would be this *release's* list.
`agent.GetTools()` is the only answer that is true for the turn — it is
post-filter, post-MCP-append — so `heldToolNames` reads it there.

### `update_dashboard` is not on the MCP surface, and the omission is commented

`T-D22` says not to add it to `cmd/mcp`, and it is not. The reason is now written
where somebody will ask it, next to `create_dashboard`, which *is* exposed:
creating a dashboard adds something nobody was depending on yet; editing one
changes what a stored URL already serves.

---

## 3. What the build argued about, and settled

**`update_dashboard` takes a patch, not a re-submission.** A model re-emitting a
twelve-panel spec to change one axis turns the cheapest edit in the product into
the most expensive call in the registry, and every re-emission is a chance for a
panel that was right to come back subtly wrong. `TestUpdateLeavesUnnamedPanelsAlone`
is the test that pins it: a `viz` change leaves the panel's id, SQL, mapping and
layout byte-identical.

**A panel is addressed by title, not by index.** A model that counts panels gets
it wrong the moment one is removed. A duplicate title is refused rather than
resolved to the first match — silently editing one of two identically named
panels is the failure nobody can see in the result.

**No id and no thread dashboard returns a RESULT, not a Go error.** The
2026-08-14 finding stands: a Go error in answer to a caller mistake made
deepseek re-send the identical call seven times until the iteration budget ended
the turn. The tool answers with the five most recent dashboards, `row_count: 0`
and a sentence asking which.

**A pick is an event, not a verdict.** `suggestion_picks` has no unique key,
which is the deliberate difference from `message_feedback`, whose
`(message_id, actor_kind, actor_ref)` key exists so pressing the button again
*replaces* a rating. Somebody who clicks two of three chips has told us both
were worth clicking.

**The pick's label and `recommended` flag are read off the stored message, never
taken from the request.** The browser sends only an index. This table's whole
value is that it says what actually happened, and a stale tab could otherwise
write a row saying somebody pressed a chip that was never on screen.

**The chips render under the newest assistant message only, and not while a turn
is in flight.** Chips on every historical bubble turn a transcript into a wall of
buttons; chips beside a streaming answer invite a click that queues a second
question behind the first.

---

## 4. The latency trade, stated so nobody re-litigates it silently

`suggestNextSteps` runs between the empty-reply rescue and `completeWith`, which
delays the `final` event by the length of the pass. The alternative — publish
`final`, run the pass, then a second event and an `UPDATE` of the message row —
needs a `MessageRepository.Update` that does not exist and a second event type
every consumer (dashboard, widget, `/v1`, MCP) would have to learn.

Take the latency, fail open, and **revisit if the measured p95 of the pass
exceeds 1s**.

**It does. The measurement exists now and it is 12,962 ms** (§6.4), so the trade
above is settled against itself and the decision is the owner's. The timeout is
`NEXT_STEPS_TIMEOUT_SECS`, default 8s — at which this deployment's light model
never finishes, which is stated in a `Warn` line rather than left as an absence.

The pass is skipped entirely when the turn called `ask_clarification`, when a
guardrail replaced the reply, on a report turn, on a schedule or watcher fire, on
an empty answer, and when the company's credit verdict is anything but `ok`.

---

## 5. What is owed

**Everything that needs a stack or a model.** This is the section that matters
more than §1.

> **Written before the gate ran, and kept as written.** §6 is what happened.
> Everything in "needs the stack" below was run on 2026-08-17 and passed, and
> two of the three model-spend items ran too — including the two numbers this
> section says do not exist, which now do. The struck items are closed; what is
> left un-struck is genuinely still owed.

**~~Needs the stack, nothing else~~ — run 2026-08-17, all three pass (§6.1–§6.3):**

- Migrations `057` and `058` up, down and up again against a real Postgres,
  `058` against a populated table. `057` is a backfill and its assertion is that
  it adds `update_dashboard` to exactly the agents that hold `create_dashboard`
  and to no unrestricted agent — the `allowed_tools <> '{}'` clause is the one
  that would be catastrophic to get wrong, because it would narrow an
  every-tool agent to one tool.
- `POST /api/messages/:id/suggestion-picked`: a 404 for another tenant's message,
  a 400 for an index the message's suggestions do not have, and a row written
  with the label and the `recommended` flag read off the message rather than off
  the request.
- `GET /api/suggestions/summary` against a member session — expect 403 — and
  against an admin.

**Needs model spend:**

- `T-D22`'s gate as the ticket writes it: build the two-panel dashboard from the
  08-17 gate, then in the same thread ask for the window change, a panel swapped
  to a line chart, and a third panel added — three turns, `update_dashboard`
  each time, the same dashboard id throughout. Then a fresh thread asking to
  change "the revenue dashboard" with no id, showing the list-and-ask path
  rather than a retry loop.
- `T-Q10`'s turns — **partly run 2026-08-17.** Four turns produced the metadata,
  the `final` event and the kill-switch comparison (§6.4). What did not run is
  the scoped-agent arm: an agent holding only `get_schema` + `run_sql`, showing
  the chart suggestion narrowed away. That arm is the one covering
  `needsMissingTool`, which today is proven by unit test only.
- ~~**The cost and the p95 of the pass.**~~ **Both run 2026-08-17, and together
  they settle the design question against the ticket: 607 µUSD (≈3% of the turn)
  and 12,962 ms.** Cheap and slow, where the ticket assumed the opposite. §6.4.
- The `next_steps` eval category — parse, cap, allowlist, no-figures, and the
  clarification negative — added to `internal/eval` and `golden.yaml`, scored
  with the pass on and off. The set has a ±2-case noise band (delivery log,
  Phase 2s §4), so a one-case delta is not a result.

**Needs a browser:** the chip row in both light and dark mode; that clicking the
recommended chip fills the composer and starts no turn; that an older message in
the same thread has no chips under it; the `suggestion_picks` row afterwards; and
`T-D23`'s prefilled composer and the panel grid before and after one edit turn.

**Not built, deliberately:** no suggestions analytics page — the summary route is
admin-only JSON, and what that page should show depends on what the first week
of real picks looks like. No chips in the widget (its config already carries a
static `suggested_prompts` list, and the two have to be reconciled rather than
stacked). No text channels: three appended lines on every WhatsApp answer is
noise this repo has not measured, and the metadata is on the message when
somebody wants it.

**Left for `T-D13`:** an edit silently changes what a share link serves. There is
a `TODO(T-D13)` at the merge point in `update_dashboard.go` saying so, and the
tool result has to name the number of live shares when that ticket lands.

---

## 6. The live gate, 2026-08-17

Run against the compose stack, `moonshotai/kimi-k2.6` as the primary and
`openai/gpt-5-nano` as the light tier, on a gate tenant created for the sitting
(`Gate 1f 0817`, one connection to the demo warehouse). About **$0.12** of model
spend across four turns.

**It opened with the lesson this file's own backlog already records.**
`docker ps` answered *"client version 1.43 is too old"* and reads as "Docker is
not running". The daemon was up; the `docker` first on `PATH` was a nix-profile
24.0.5 whose API predates the daemon's minimum. Docker Desktop ships a current
client at `/Applications/Docker.app/Contents/Resources/bin/docker`. That is the
second time this exact message has cost this project a sitting, so it is now
written in the place somebody will be standing when it happens again.

### 6.1 Migration `057` — pass, and the clause that mattered held

The control database was at version 56 with **45 real agent rows** already
carrying all three shapes the gate needed, so nothing was seeded:

| | before | after |
| --- | --- | --- |
| unrestricted (`allowed_tools = '{}'`) | 39 | 39 |
| scoped, holds `create_dashboard` | 4 | 4 |
| scoped, does not | 2 | 2 |
| holds `update_dashboard` | 0 | **4** |

The load-bearing assertion is the first row, and it was checked by content
rather than by count: `md5(string_agg(id || allowed_tools))` over the 39
unrestricted rows is `f96223b080e55db0c1d4f3c75f1d8d87` before the migration,
after it, and again after a full `56 → 58 → 56 → 58` round trip. **No
every-tool agent was narrowed to one tool**, which is the outcome that would
have been catastrophic and silent.

Exactly the four agents holding `create_dashboard` gained the new tool;
`Ops2` and `People Ops`, scoped without it, were untouched. Re-running the
`UPDATE` by hand reports `UPDATE 0` and no array gained a duplicate entry, so
the `NOT allowed_tools @> '{update_dashboard}'` guard works.

`057`'s down is the documented no-op: after `migrate down`, the four agents
**keep** `update_dashboard`. That is the decision `043` made and this migration
copied — a down that strips a capability also strips it from the agent an
administrator ticked by hand.

### 6.2 Migration `058` — pass, including down against a populated table

Table created with 7 columns, the `(company_id, created_at DESC)` index, both
foreign keys cascading, and — the deliberate part — **no unique constraint**.
Proven rather than read: two picks were inserted for the same message and both
persisted, which `message_feedback`'s `(message_id, actor_kind, actor_ref)` key
would have collapsed to one.

Then `migrate down 1` against that populated table, `down 1` again through
`057`, and `up` back to 58: `schema_migrations` reads `58 / dirty = f`, the
table returns empty, and the agents checksum above is unchanged.

The summary SQL was exercised against real rows, and the JSONB predicate it
leans on (`metadata ? 'next_steps'`) works: `offered 1, picks 2, picked 1`.
`picked` counting DISTINCT messages is what keeps a pick rate a rate — two
clicks on one answer is one answer acted on.

### 6.3 The pick endpoint — pass on every item

| Case | Expected | Got |
| ---- | -------- | --- |
| Valid pick, index 1 | 200, row carries the message's own label and flag | 200, `{"idx":1,"recommended":true,"label":"Compare with last year"}` |
| Index the message does not have | 400 | 400 `this message has 2 suggestions, so index 7 names none of them` |
| Another tenant's message | 404, not 403 | 404 `no such message` |
| Member POSTs a pick | 200 | 200 |
| Member GETs the summary | 403 | 403 `admin only` |
| Admin GETs the summary | 200 | 200 `{"offered":1,"picked":1,"picks":3,"pick_rate":1,"recommended_picks":1}` |

**The property the table's value rests on was tested adversarially**, not just
happily. A client posting `{"index":0,"label":"SOMETHING THAT WAS NEVER ON
SCREEN","recommended":true}` got back a row reading `"By region"` /
`recommended: false` — both read off the stored message, the invented pair
discarded.

### 6.4 `T-Q10` — the pass works, and its first live turn found a defect

**Defect 1: the ticket's 5s timeout is three times too small for this
deployment's light tier, so the feature was switched on and did nothing.**

The first real turn logged:

```
next-steps pass failed; the answer is unchanged
error=failed to generate text: error reading response body: context deadline exceeded
```

Timed directly, three calls with the real prompt shape against
`openai/gpt-5-nano` through OpenRouter took **12.5s, 16.6s and 15.7s**. The
5s budget `T-Q10` specifies could never have been met here. Fail-open behaved
exactly as designed — the answer was unchanged and the turn was otherwise
untouched — which is the good half. The bad half is that this is the C-2 shape:
a feature that is on, bills nothing, and does nothing, and says so only at
`Info`.

Three changes came out of it:

- The timeout is now `NEXT_STEPS_TIMEOUT_SECS`, default **8s**. Eight is a
  compromise and not a measurement: raising the default to cover 17s would put
  17 seconds in front of every answer, which the ticket's own rule — *revisit if
  the p95 exceeds 1s* — forbids outright.
- **Exhausting it now logs at `Warn`** and names both numbers, because a
  deployment whose light model cannot make the budget needs to be told rather
  than left to infer it from an absence.
- **The pass is timed**, and the elapsed is on the success line too. `T-Q10`'s
  entire design rests on a latency budget and there was no way to read the
  latency; now `elapsed_ms` is in the log on both paths.

Re-run at `NEXT_STEPS_TIMEOUT_SECS=30`, the mechanism is correct end to end:

```
next-step suggestion pass finished  steps=3  elapsed_ms=12962
```

and the persisted message carries exactly the designed shape — three steps, one
`recommended`, `why` present on that one and stripped from the others:

```json
{"next_steps":[
 {"label":"Break down by region","prompt":"Break that down by region",
  "recommended":true,"why":"to reveal regional contributions to the top channel"},
 {"label":"Show sales by channel over time","prompt":"Show sales by channel over time","recommended":false},
 {"label":"Identify the top region for each sales channel","prompt":"Identify the top region for each sales channel","recommended":false}]}
```

The answer that produced it contained **$12,729,714,500.00** and no chip
restates it, so the grounding rule held on a real turn with a large figure in
front of it. A later turn produced *"Compare December 2024 to December 2023"* —
two four-digit years, both correctly read as periods rather than results, which
is the exact case §2's correction to the ticket exists for, now confirmed live
rather than only in a test.

**The `final` event carries them**, beside `latency_ms` and with no new event
type, read straight off the Redis bus:

```
metadata keys: ['latency_ms', 'next_steps']
```

**The two numbers `T-Q10` asks for and never had:**

| | |
| --- | --- |
| Cost per pass | **607 µUSD** (`gpt-5-nano`, 320 in / 1478 out) — about **3%** of the turn beside it (19,958 µUSD) |
| Latency of the pass | **12,962 ms** measured; 12.5–16.6s across four samples |

So the pass is **cheap and slow**, which is the opposite of what the ticket
assumed, and it is the latency that decides its future rather than the cost.
The output-token count says why: 1,478 output tokens for three short
suggestions is `gpt-5-nano` reasoning. **This is now the owner's call**, and the
options are the ones the ticket already wrote down: accept ~13s in front of
every answer (it should not), point the light tier at a non-reasoning model, or
take the design `T-Q10` rejected — publish `final`, run the pass, then a second
event and a message `UPDATE`. The measurement is what makes that a decision
rather than a preference.

**The kill switch is a kill switch.** With `NEXT_STEPS_ENABLED=false`:

| | with the pass | with the switch off |
| --- | --- | --- |
| `final` metadata keys | `latency_ms`, `next_steps` | `latency_ms` |
| persisted `messages.metadata` | 3 steps | `NULL` |
| `next_steps` usage events | +1 | +0 |
| suggestion lines in the worker log | 1 | 0 |

### 6.5 Defect 2 — a fabricated figure in a persisted answer, and it is not this build's

**This is the more serious of the two, it is older than this build, and the
turn that exposed it was running with `NEXT_STEPS_ENABLED=false`** — so the
control for "did today's work cause it" is in the transcript.

Asked *"How many transactions were there in November 2024?"*, the stored answer
of record is:

> There were **1,667 transactions** in November 2024.  There were **1,667
> transactions** in November 2024. There were **300 transactions** in November
> 2024.

The agent's own `run_sql` — `SELECT COUNT(DISTINCT transaction_id) FROM
fact_sales WHERE created_at >= '2024-11-01' AND created_at < '2024-12-01'` —
returns **300**, which is correct. `1,667` appears nowhere in the warehouse:
`fact_sales` holds 1,348 rows in total. The December turn has the same shape,
with the same invented 1,667 in front of the true 310.

The mechanism is in the stream, and the delta events say so directly. That turn
carried `iteration: 2` and 44 `delta` events whose concatenation **is** the
final content: the model wrote a sentence with an invented figure *before*
calling the tool, wrote it again, then wrote the true one after the result came
back — and `runStream` accumulates every iteration's prose into one reply.

**Why the guardrail did not stop it.** `guardrails.CheckFabrication` grounds a
reply on `TurnEvidence.DataRows > 0`. A data tool did return a row, and the
reply does contain a tool-derived figure, so the check is satisfied — while a
figure no tool ever produced sits in the same paragraph. The check asks *"is
there evidence?"* and the failure needs *"is every figure evidenced?"*. That is
the wrong-but-nonempty class `02-agent-quality-roadmap.md` names as uncovered
and assigns to `T-Q9`, arriving one door further out than that ticket looked.

**Not fixed here, deliberately.** It is not this build's defect, and both
plausible fixes are decisions rather than edits: dropping pre-tool prose would
also drop legitimate narration, and making the fabrication check evidence every
figure rather than any figure is a change to the guardrail that has blocked
correct answers before. It is filed as a finding with a reproduction, which is
what this sitting owes it.

### 6.6 What is still owed

`T-D22`'s four-turn edit gate and `T-Q10`'s eval category still need model
spend and did not run today. The browser bucket (§3a of
[`live-gate-backlog.md`](live-gate-backlog.md)) is untouched — no chip has been
looked at, only read out of Postgres and off the Redis bus.
