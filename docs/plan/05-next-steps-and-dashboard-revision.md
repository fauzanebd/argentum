# Next steps and dashboard revision — the loop after the answer

Written 2026-08-17 against `main` @ `12ba63e`, post-monorepo. Four tickets,
**~5.5 days**, two tracks. Ticket ids are `T-Q10`, `T-U13`, `T-D22` and `T-D23`
— each is the next free number in its own series (`T-Q` ends at `T-Q9` in
[`02-agent-quality-roadmap.md`](02-agent-quality-roadmap.md), `T-U` at `T-U12`
in [`01-tickets.md`](01-tickets.md), `T-D` at `T-D21` in
[`04-native-dashboards-roadmap.md`](04-native-dashboards-roadmap.md)).

Two asks, one shape. **Track A**: after the agent answers, it should say what is
worth asking next, mark one of them as the one it recommends, and let the reader
pick it. **Track B**: a dashboard the agent built should be changeable by telling
the agent what is wrong with it, instead of being a thing that can only be
created and deleted.

Both are the same missing loop — the product currently ends every turn at a full
stop.

> **Status, 2026-08-17: all four are code-complete, and the stack-only half of
> the gate has run.** Migrations `057`/`058` are applied, reversed and
> re-applied against the real control database; the pick endpoint and its role
> split are proven over HTTP; the suggestion pass, the `final` event and the
> kill switch are proven on live turns. **Two defects came out of it**, one of
> them a correction to `T-Q10` below and one older and more serious than
> anything these tickets touch —
> [`../coverage/next-steps-and-revision.md`](../coverage/next-steps-and-revision.md) §6.
>
> **The measurement `T-Q10` asks for exists now, and it contradicts `T-Q10`.**
> The pass costs 607 µUSD (≈3% of the turn beside it) and takes **12,962 ms**.
> The ticket's own rule is to revisit above 1s, and its 5s timeout could never
> have been met by this deployment's light model — so as specified, the feature
> was on, billed nothing and did nothing. ~~Whether the pass moves behind `final`
> is the owner's call~~ — **decided 2026-08-17: `NEXT_STEPS_ENABLED` defaults to
> false.** The pass, the chips and the pick table are unchanged and one variable
> away; a deployment whose light model answers in about a second turns it on. The
> second-event design this ticket rejected stays the option for a slow tier, and
> stays unbuilt.
>
> ~~None has been run against a live stack or a real model.~~ `make vet` / `make test` /
> `make lint-go` (0 issues) / `make types-check` / `tsc -b` / `pnpm build` are
> clean. The record, the four
> departures from these tickets and the full list of what is owed are in
> [`../coverage/next-steps-and-revision.md`](../coverage/next-steps-and-revision.md).
>
> **One of those departures is a correction to this document rather than a
> deviation from it.** `T-Q10` below says to drop any suggestion containing a
> digit run of 4 or more. A year is a digit run of four, so that rule deletes
> "compare with 2024" — one of the most useful suggestions this feature can make.
> A test caught it. The shipped rule distinguishes a figure from a period:
> grouped or decimal numbers and runs of five or more are figures, four digits
> inside 1900–2099 is a year. The line below is left as written, because what it
> was reaching for is right and only its cheap enforcement was wrong.
>
> The other three: the `next_steps` usage event is `metadata.feature` on the
> `llm_call` the pass already bills (a second event would double-count, or record
> a free-looking LLM call, which is the C-2 shape); held tools are read off
> `agent.GetTools()` rather than `agentscope`, which carries sources and not tool
> names; and the two migration numbers came out `057`/`058` rather than the
> ticket's guesses, which is what `make migration-next` is for.

---

## What is true on `main` today

Every claim here was read in the tree before this document was written.

| Claim | State |
| ----- | ----- |
| The agent proposes something and a human decides | **True, for writes only.** `propose_action` → approval card → exactly-once execute (`internal/tools/propose_action.go`, `internal/app/action_service.go`, `features/chat/…/approval-card.tsx`). One concrete action, approve or reject. No ranking, no alternatives, no "recommended" marker |
| The agent suggests what to ask next | **False.** `StarterQuestions` (`apps/dashboard/src/features/chat/chat-page.tsx:647`) is per-agent static text from a template (T-B3) and renders on the new-chat screen only. The widget's `suggested_prompts` (`internal/domain/widget_config.go:29`) is tenant-typed config. Neither is written by the agent, and neither exists after the first turn |
| There is a transport for it | **Half-true, and the half that exists is legacy.** `AgentResponse.FollowUpQuestions` (`pkg/models/models.go:26`) is documented (`apps/backend/docs/api.md:219`) and rendered by WhatsApp (`internal/whatsapp/client.go:173-176`) — and **nothing anywhere populates it**. Its own struct comment says new callers should use the chat pipeline instead. The live transport is `domain.Message.Metadata` (`internal/domain/message.go:28`, persisted at `internal/adapters/postgres/message_repo.go:24,193`) and `ChatEvent.Metadata` (`internal/app/event_bus.go:57`) |
| The agent can build a dashboard | **True.** `create_dashboard` builds every panel in one call (`internal/tools/create_dashboard.go`, T-D11), stores it, and returns a URL; the chat transcript swaps that link for the live panels (`features/chat/markdown-renderer.tsx:31-58`); `/dashboards` and `/dashboards/$id` exist (`routes/index.tsx:143-155`) |
| The agent can change a dashboard | **False.** No `update_dashboard` tool — the registry's data tools are `list_sources`, `get_schema`, `list_metrics`, `query_metric`, `run_sql`, `create_dashboard`, `schedule_task`, `ask_clarification`, `propose_action`, `generate_document` (`internal/tools/registry.go:94-130`). The user's only route to a fix is "build me another one", which leaves the wrong dashboard in the list |
| The service could change one | **True, and unreachable.** `DashboardService.Update` is written, validated and dry-run gated (`internal/app/dashboard_service.go:82-100`), `DashboardRepository.Update` exists (`internal/domain/dashboard.go:43`), and **no route and no tool calls either**. `internal/transport/http/handlers/native_dashboards.go:22-25` says so deliberately: no second authoring surface "before there is a UI that needs it" |
| The agent can read a dashboard back | **False.** Nothing in the tool list can name what already exists |

The last three rows are the whole of Track B: the machinery is built, and the
agent has no door to it. `T-D22` is that door, and it deliberately does **not**
add the HTTP authoring route the handler comment refuses — authoring stays one
code path, and that path is the agent.

---

## Track A — The turn suggests its own next step (3.0d)

## T-Q10 · Next-step suggestions after every answer
**Repo:** BE · **Size:** 2.0d · **Deps:** none · **Priority:** P1
**Migration:** none — `messages.metadata` already exists and is already marshalled

### Why

Every turn ends at a full stop. The reader of an answer is the person least
equipped to know what this product can be asked next, and the agent is the one
that just discovered what the data supports. `feature-coverage.md` records the
same failure from the other side — an agent that was told it held nine tools
recommended work it could not do; the inverse is an agent that holds ten and
recommends nothing.

This is also the instrument the roadmap's Track A argument demands. Pick-rate on
a suggestion is the first signal this product would have about *what customers
actually want next*, as opposed to what forty synthetic eval questions ask.

### Do

- New `internal/app/nextsteps.go`, one exported entry point on `ChatRunner`:
  `suggestNextSteps(ctx, p, question, answer, held []string) []domain.NextStep`.
- New `domain.NextStep` in `internal/domain/message.go`:

  ```go
  type NextStep struct {
      Label       string `json:"label"`        // ≤ 48 chars, the chip's text
      Prompt      string `json:"prompt"`       // what goes into the composer
      Recommended bool   `json:"recommended"`  // at most one true, enforced server-side
      Why         string `json:"why"`          // one clause, shown on the recommended one only
  }
  ```

- Call it in `internal/app/chat_runner.go` **between line 749
  (`rescueEmptyReply`) and line 750 (`completeWith`)** — the same post-turn chain
  the fabrication gate, the grounding check and the output rules already run in,
  in that order and for the reasons the comments there give.
- One LLM call through the resolver the runner already holds
  (`ChatRunner.llmCache`, `chat_runner.go:106,260`), against the tenant's own
  client. Timeout **5s**, `context.WithTimeout`.
- What the prompt is given: the user's question, the final answer text, the
  names of the tools the turn *called*, the names of the tools the turn
  **held** (from `agentscope`, not from a constant), the metric keys if
  `list_metrics` ran, and the source names. **No rows and no figures.**
- What it must return: strict JSON, `{"steps":[…]}`, **at most 3**, at most one
  `recommended`. Parse failure, timeout, empty array and any error are all the
  same outcome — no suggestions, the turn is otherwise unchanged.
- Server-side narrowing after the parse, because the model is not trusted with
  this: drop any step whose prompt needs a tool the turn did not hold; drop any
  step containing a digit run of 4+ (a suggestion is not a place to restate a
  figure); truncate `Label` at 48; if two or more come back `recommended`, keep
  the first and clear the rest.
- Persist: extend `ThreadService.AppendAssistantMessage`
  (`internal/app/thread_service.go:593`) with a `metadata map[string]any`
  parameter and pass `{"next_steps": […]}` through it. The column and both
  marshal sites already exist; only the signature is missing.
- Publish: the same slice on the `final` event's `Metadata`
  (`chat_runner.go:1599-1606`), beside `latency_ms`. No new event type, and
  `/v1` chat consumers get it for free.
- Meter it: one `usage_event` per pass, kind `next_steps`, with the token counts.
- Skip the pass entirely when: the turn called `ask_clarification` (the agent
  already asked a question — chips would compete with it), the reply came from
  the fabrication gate or the empty-reply rescue, the company is at or near its
  budget floor (T-03 — an answer must never be delayed by a suggestion), or
  `NEXT_STEPS_ENABLED=false` (default `true`).

### Notes for the implementer

**Do not populate `models.AgentResponse.FollowUpQuestions`.** It is the legacy
WhatsApp shape, its own comment says so (`pkg/models/models.go:18-20`), and
writing to it would make two vocabularies for one idea. Leave it dead or delete
it in a separate commit.

**Do not copy `refreshSummary`'s metering.** `thread_service.go:705` and `:724`
make two unmetered LLM calls per thread today. That is a gap this ticket is not
fixing and must not widen — a per-turn call is a much larger one, and this
product's own delivery log names "metering shipped alongside every capability
that spends money" as a strength.

**The latency trade, stated so nobody re-litigates it silently.** Running before
`completeWith` delays the `final` event by the pass. The alternative — publish
`final`, then a second event, then `UPDATE` the message row — needs a
`MessageRepository.Update` that does not exist and a second event type every
consumer (dashboard, widget, `/v1`, MCP) must learn. Take the latency; revisit
if the measured p95 of the pass exceeds 1s.

**Grounding rules apply here too.** A suggestion is text this product puts in the
user's mouth. It may name a dimension, a period or a metric; it may not assert a
result. The digit-run rule above is the cheap enforcement; the eval case is the
real one.

### Acceptance

- [ ] A dashboard turn that answered a revenue question comes back with 1–3 steps, at most one `recommended`, each with a non-empty `prompt`
- [ ] The steps are on the persisted assistant message (`messages.metadata.next_steps`) **and** on the `final` event
- [ ] A turn on an agent whose allowlist is `get_schema` + `run_sql` produces **no** step that requires `create_dashboard` or `generate_document`
- [ ] A turn that called `ask_clarification` produces **no** steps at all
- [ ] Killing the suggester (unreachable model, or `NEXT_STEPS_ENABLED=false`) leaves the answer, the `final` event and the persisted message byte-identical to today
- [ ] No step contains a figure from the answer
- [ ] One `usage_event` of kind `next_steps` per pass, and none when the pass is skipped
- [ ] A reply the fabrication gate replaced carries no steps

### Gate

`make vet` / `make test`, then the stack: run three turns against the demo
warehouse on `moonshotai/kimi-k2.6` — one plain aggregate, one that builds a
dashboard, one that asks for clarification — and paste the `next_steps` metadata
for each. Re-run the second on an agent scoped to two tools and show the chart
suggestion gone. Add the eval category `next_steps` to `internal/eval` +
`golden.yaml` (parse, cap, allowlist, no-figures, and the clarification
negative), and post the score with the pass on and off — the set has a ±2-case
noise band (delivery log, Phase 2s §4), so a one-case delta is not a result.

### Out of scope

Text channels. WhatsApp / Slack / Discord / Lark replies are unchanged in this
ticket — three appended lines on every answer is noise this repo has not
measured, and the metadata is on the message when someone wants to.
Personalisation from history, per-agent suggestion style, and the widget's
rendering (its config already has a static list; the two must be reconciled, not
stacked).

---

## T-U13 · Next-step chips under the answer
**Repo:** FE · **Size:** 1.0d · **Deps:** T-Q10 · **Priority:** P1
**Migration:** `057_suggestion_picks` (renumber if T-D22 lands first)

### Why

`T-Q10` puts the steps on the message and nothing draws them. And a suggestion
nobody clicks is worse than no suggestion — the pick is the only evidence this
feature works, so the surface that renders them is also the one that measures
them.

### Do

- `MessageBubble` (`apps/dashboard/src/features/chat/chat-page.tsx:707`): render
  `message.metadata?.next_steps` **under the last assistant message only** —
  chips on every historical bubble turn a transcript into a wall of buttons.
  Sits below the `MessageFeedback` row (`:790-797`), same 1.5 gap.
- Reuse `StarterQuestions`' chip styling (`:656-666`) rather than a second chip
  look. The recommended one leads, carries a filled dot and `aria-label="Recommended"`,
  and its `why` is the `title`.
- **A click fills the composer; it does not send.** Same rule and same reason as
  the starter questions (`:640-646`): a turn that runs before the reader has read
  it teaches nothing and spends a credit.
- Type it properly: `next_steps` on the `Message` type in `packages/api-types`,
  generated from the Go struct by tygo like everything else (T-02b) — not
  hand-declared in the component, which conventions §Frontend calls a review
  finding.
- Telemetry: `POST /api/messages/:id/suggestion-picked` with `{index}`, routed
  beside the feedback endpoints (`internal/transport/http/handlers/feedback.go:32-35`)
  and stored in `057_suggestion_picks` (`message_id`, `company_id`, `idx`,
  `recommended bool`, `label`, `created_at`). Fire-and-forget from the client.
- The widget is **not** changed here.

### Notes for the implementer

`message.metadata` is `map[string]interface{}` on the wire. Validate the shape at
the boundary and render nothing on anything unexpected — a malformed metadata
blob must not blank a transcript.

Absent, empty or malformed `next_steps` renders exactly the screen as it is
today. Every branch returns `null`, like `StarterQuestions` does at `:654`.

### Acceptance

- [ ] Chips appear under the newest assistant message and nowhere else
- [ ] The recommended chip is visually distinct in both light and dark mode, and its reason is reachable without a mouse
- [ ] Clicking fills the composer and sends nothing; Enter then sends
- [ ] A pick writes exactly one row, with `recommended` recording whether the chosen chip was the marked one
- [ ] A thread whose messages carry no `next_steps` renders pixel-identically to today
- [ ] No raw Tailwind palette classes (T-U1 rule)

### Gate

`make web`, drive one turn that produces suggestions: screenshot the chip row in
both modes, click the recommended one and show the composer filled and no turn
started, then show the `suggestion_picks` row. Screenshot an older message in the
same thread with no chips under it.

### Out of scope

A "suggestions" analytics page. Pick-rate reporting. Widget chips. Keyboard
shortcuts for chips (`T-U10`'s command palette is the place for that argument).

---

## Track B — A dashboard the agent can revise (2.5d)

## T-D22 · `update_dashboard` — revise instead of rebuild
**Repo:** BE · **Size:** 1.5d · **Deps:** T-D11 (shipped) · **Priority:** P1
**Migration:** `057_agent_tools_update_dashboard` (backfill; renumber if T-U13 lands first)

### Why

The 2026-08-17 live gate (`../coverage/native-dashboards.md`) ends with a
dashboard whose default window matched no rows and a reply telling the user to
change the filter by hand. The obvious next sentence from a customer — "just make
it 2024" — has nowhere to land. `DashboardService.Update` is written, tested
through `validated` + `dryRun`, and reachable by nothing
(`internal/app/dashboard_service.go:82-100`).

Today the only fix is another `create_dashboard`, which leaves the wrong
dashboard in the list, breaks any link already sent, and pays the full build cost
to change one date.

### Do

- New `internal/tools/update_dashboard.go`, registered beside `create_dashboard`
  in `internal/tools/registry.go:113`.
- Consumer-declared interface, exactly like `DashboardCreator`
  (`create_dashboard.go:18-23`) and for the same import-cycle reason:

  ```go
  type DashboardReviser interface {
      Get(ctx context.Context, companyID, id string) (*domain.Dashboard, error)
      List(ctx context.Context, companyID string) ([]*domain.Dashboard, error)
      Update(ctx context.Context, companyID, id string, in dashboard.Input) (*dashboard.SaveResult, error)
  }
  ```

- Parameters: `dashboard_id` (optional), `title` (optional), `refresh_secs`
  (optional), `panels` (optional: `{op: add|replace|remove, title|index, …panel}`),
  `filters` (same three ops). **A patch, not a re-submission** — the model
  re-emitting a twelve-panel spec to change one axis is how a cheap edit becomes
  the most expensive call in the registry.
- Resolution when `dashboard_id` is omitted: the newest dashboard whose
  `thread_id` is this thread. That column is real and indexed (migration `056`,
  `dashboards_thread_id_fkey`) — this is **not** the package-level in-memory map
  the old `create_visualization` pair used, and `registry.go:109-112` records why
  that one was wrong.
- When there is no thread dashboard and no id: **return a result, not a Go
  error.** List the company's five most recent dashboards (id, title, created_at)
  with `row_count: 0` and a sentence asking which. The 2026-08-14 finding stands
  — a Go error to a caller mistake makes deepseek re-send the identical call
  seven times (roadmap 02, Phase 2s §2).
- Merge, then hand the whole thing to `DashboardService.Update` so validation,
  the source-ownership check and the zero-row `dryRun` warnings stay one code
  path (`dashboard_service.go:120-176`). Surface those warnings in the tool
  result the same way `create_dashboard` does — that is the 08-17 defect-2 fix
  and it must not be bypassed by the edit path.
- Resolve the source through `ResolveSource` (`internal/tools/source_resolve.go`)
  like every other data tool. That is 08-17 defect 1, and a new tool that skips
  the choke point re-creates it.
- **Refuse to change `source_id`.** Re-pointing a stored dashboard at another
  warehouse is a different act with a share-link blast radius; refuse with a
  sentence naming `create_dashboard` as the way to do it.
- Migration `057`: add `update_dashboard` to `agents.allowed_tools` wherever
  `create_dashboard` is present, mirroring `043`/`044`; add it to the templates
  that suggest `create_dashboard` in `config/agent_templates.yaml`.
- Prompt: one line in the tool catalog telling the agent to prefer editing the
  dashboard this conversation already produced over building a second one.

### Notes for the implementer

**Panels are addressed by title first, index second.** A model that counts panels
gets it wrong the moment one is removed; a title is what the user says out loud
("the pie chart"). Match case-insensitively on trimmed titles, refuse ambiguously
on a duplicate title rather than editing the first match.

**Sharing is not built yet (`T-D13`), and this ticket makes it harder.** When
share tokens exist, an edit silently changes what a stranger's link serves. Leave
a comment at the merge point saying so, and when `T-D13` lands the tool result
must name the number of live shares.

**Do not add the HTTP authoring route.**
`internal/transport/http/handlers/native_dashboards.go:22-25` refuses it
deliberately and the reason still holds — one authoring path, one set of
validation rules.

**Do not add this to `cmd/mcp`.** `T-14` leaves `propose_action` and friends off
the MCP surface because an MCP client is an agent we did not write. Editing a
dashboard somebody's Monday depends on belongs on the same side of that line.

### Acceptance

- [ ] "Change the period filter to 2024-07-01 → 2024-12-31" edits the dashboard from the same thread, with no `dashboard_id` in the call
- [ ] The edit is one `create_dashboard`-free turn, and `GET /api/dashboards/:id` shows the new spec with the same id
- [ ] A panel replaced by title keeps every other panel untouched, in order
- [ ] A call naming a panel title that does not exist is refused with the titles that do
- [ ] An edit that leaves a panel matching no rows returns the same zero-row warning `create_dashboard` returns, and the reply says so
- [ ] A call trying to change `source_id` is refused, and the stored `source_id` is unchanged
- [ ] With no id and no thread dashboard, the tool returns a list of recent dashboards and the model asks which — it does not retry the same call
- [ ] An agent whose allowlist lacks `update_dashboard` cannot call it, and `044`-style backfill left no agent holding create-without-update
- [ ] One `agent_actions` row and one `usage_event` per call, like every other tool

### Gate

`make vet` / `make test`, then the stack on `moonshotai/kimi-k2.6`: build the
two-panel dashboard from the 08-17 gate, then in the same thread ask for the
window change, a panel swapped to a line chart, and a third panel added — three
turns, `update_dashboard` each time, same dashboard id throughout, and the
`/dashboards/$id` page showing the result. Then open a fresh thread and ask to
change "the revenue dashboard" with no id and show the list-and-ask path. Apply
and roll back `057`.

### Out of scope

Undo / spec version history (a real want; needs a `dashboard_revisions` table and
its own ticket). Deleting a dashboard from chat. Editing over `/v1` or MCP.
Re-pointing the source. Panel layout beyond what `create_dashboard` already
accepts.

---

## T-D23 · Ask for a change from the dashboard itself
**Repo:** FE · **Size:** 1.0d · **Deps:** T-D22 · **Priority:** P2
**Migration:** none

### Why

`T-D22` makes the edit possible for a user who is *in the thread that built it*.
The person looking at a wrong chart is on `/dashboards/$id`, and their only route
back is to find the conversation. The feedback loop the ask describes closes
here: see the dashboard, say what is wrong with it, watch it change.

### Do

- `features/dashboards/dashboards-page.tsx` detail view: an "Ask for a change"
  action in the header.
- It opens chat with the composer prefilled with a reference the agent already
  understands — the dashboard's markdown link, which
  `features/chat/markdown-renderer.tsx:31` already parses back to a uuid — plus
  the user's cursor after it. Prefill, never send (T-U13's rule, same reason).
- After a turn in that thread calls `update_dashboard`, invalidate the
  `["dashboard", id]` query so the panels redraw without a reload.
- The chat-embedded panel view (`dashboard-view.tsx` inside a transcript) gets
  the same action, since that is where most dashboards are first seen.

### Notes for the implementer

No new backend plumbing and no `?dashboard=` state to persist — the id travels in
the message text, which is the one channel that already works in every surface
including the widget and `/v1`.

Do not auto-send the prefilled message. A dashboard edit that fires from a button
press is an action nobody approved.

### Acceptance

- [ ] From `/dashboards/$id`, one click lands in chat with the reference prefilled and nothing sent
- [ ] A change asked for this way edits that dashboard and the open page reflects it without a manual reload
- [ ] The same action works from a dashboard embedded in a transcript
- [ ] A user who may not edit (member vs admin, if the policy says so at the time) sees the same refusal the tool gives, not a broken button

### Gate

`make web`: screenshot the header action, the prefilled composer, and the panel
grid before and after one edit turn, in both modes.

### Out of scope

Inline panel editing (drag, resize, change chart type from the grid). A revision
history UI. Editing from the share page — a share viewer is not a tenant user.

---

## Sequencing

`T-Q10` → `T-U13`, and `T-D22` → `T-D23`. The two tracks share no code and can
run in either order or at once; `T-D22` is the one with a customer waiting for it
(the 08-17 gate ends on exactly the sentence it would answer), so it goes first
if only one fits.

| Ticket | Repo | Size | Priority |
| ------ | ---- | ---- | -------- |
| `T-D22` | BE | 1.5d | P1 |
| `T-D23` | FE | 1.0d | P2 |
| `T-Q10` | BE | 2.0d | P1 |
| `T-U13` | FE | 1.0d | P1 |
| **Total** | | **5.5d** | |

## Risks

| # | Risk | Mitigation |
| - | ---- | ---------- |
| 1 | **A per-turn LLM call is a per-turn bill.** Every answer in the product gets one more call | Metered as its own `usage_event` kind from day one, skipped under budget pressure, killable by env. Measure the cost per turn in the gate and put the figure in the coverage doc |
| 2 | **Suggestions are text this product puts in the user's mouth.** A confident wrong suggestion is a worse failure than a missing one | No rows in the prompt, digit-run rule, tool-allowlist narrowing server-side, and an eval category before the feature is called done |
| 3 | **Latency on `final`.** The suggestion pass sits in front of the event the browser is waiting for | 5s timeout, fail-open, and a p95 measurement in the gate. Above 1s, move to the second-event design and pay for the message `UPDATE` |
| 4 | **An edit changes what a link already sent serves.** Today that link is internal; after `T-D13` it is a share token | Comment at the merge point now, share count in the tool result when `T-D13` lands |
| 5 | **Chip fatigue.** Three buttons under every answer forever | Newest message only, ≤3, and `suggestion_picks` is what says whether they are earning the space. If pick-rate is under ~5% after a real week, cut the feature rather than tuning the prompt |
