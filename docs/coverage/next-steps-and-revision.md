# The loop after the answer — what was built

`T-Q10`, `T-U13`, `T-D22` and `T-D23`: the four tickets in
[`../plan/05-next-steps-and-dashboard-revision.md`](../plan/05-next-steps-and-dashboard-revision.md).
Two tracks, one missing loop — the product ended every turn at a full stop, and
a dashboard the agent built could only be created or deleted.

> **Status: code-complete and unit-gated, 2026-08-17. Nothing here has been run
> against a live stack or a real model.** That is the half this repo's own
> delivery log says finds the defects — six of the nine on the quality roadmap
> were found by running it rather than by writing it — so read §5 before reading
> the ticks. `make vet`, `make test`, `make lint-go` (0 issues), `make types-check`,
> `tsc -b` and `pnpm build` are all clean.

---

## 1. What landed

| Ticket | What | Where |
| ------ | ---- | ----- |
| `T-D22` | `update_dashboard`: a **patch** against a stored dashboard, resolved to this thread's when no id is given, refusing a source change, merged through `DashboardService.Update` so validation and the zero-row warning stay one code path | `internal/tools/update_dashboard.go`, migration `057`, six template cards, `agents.yaml` |
| `T-Q10` | One light-model pass after every answered turn, narrowed server-side, persisted on `messages.metadata` and published on the `final` event | `internal/app/nextsteps.go`, `domain.NextStep`, `chat_runner.go`, `thread_service.go` |
| `T-U13` | The chips, and the only evidence they work: a pick writes a row | `features/chat/next-steps.tsx`, migration `058`, `POST /api/messages/:id/suggestion-picked` |
| `T-D23` | "Ask for a change" from the dashboard itself, prefilled and never sent | `features/dashboards/dashboard-view.tsx`, `store/composer.ts` |

Two migrations, `057_agent_tools_update_dashboard` and `058_suggestion_picks`.
Both are additive; neither has been applied to a real database.

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

Take the latency. 5s timeout, fail-open, and **revisit if the measured p95 of the
pass exceeds 1s** — which is a measurement nobody has taken yet (§5).

The pass is skipped entirely when the turn called `ask_clarification`, when a
guardrail replaced the reply, on a report turn, on a schedule or watcher fire, on
an empty answer, and when the company's credit verdict is anything but `ok`.

---

## 5. What is owed

**Everything that needs a stack or a model.** This is the section that matters
more than §1.

**Needs the stack, nothing else:**

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
- `T-Q10`'s three turns — one plain aggregate, one that builds a dashboard, one
  that asks for clarification — with the `next_steps` metadata pasted for each.
  Then the same on an agent scoped to `get_schema` + `run_sql`, showing the
  chart suggestion gone.
- **The cost and the p95 of the pass.** Both are the numbers that decide whether
  this feature keeps its place in front of the `final` event, and neither exists.
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
