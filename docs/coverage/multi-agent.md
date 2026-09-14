# Multi-agent conversations — what is built, and what it found

The plan is
[`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)
(`T-N1`→`T-N10`, ~18.5d); the reference it was checked against is
[`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md).
**Nine of the original ten are built** (`T-N1`→`T-N8` and `T-N10`), and so is
`T-N11`, filed and built since. This file records what landed, what it changed that the ticket did
not anticipate, and what is owed.

| Ticket | Status |
| --- | --- |
| `T-N1` Every assistant message says which agent wrote it | **built 2026-09-11, unit-gated. Migration `077` written, not applied — §4** |
| `T-N2` A conversation can hold more than one agent | **built 2026-09-11, unit-gated. Migration `078` written, not applied — §4** |
| `T-N3` Addressing — `@agent` decides who answers | **built 2026-09-11, unit-gated. One acceptance item struck as unachievable — §5** |
| `T-N4` The dashboard room | **built 2026-09-11. Visual gate run; three findings — §6** |
| `T-N5` A peer agent's words are untrusted input | **built 2026-09-13, unit-gated. `make eval` owed; one finding outside the ticket — §8** |
| `T-N6` `nudge_agent` — one participant asks another | **built 2026-09-14, unit-gated. Migration `086` written, not applied — the ticket said none. `make eval`, the live room and the screenshots owed — §11** |
| `T-N7` `hand_off_to_agent` — "this one isn't mine" | **built 2026-09-14, unit-gated. No migration. `make eval` and the live arms owed; four places the ticket was wrong — §13** |
| `T-N9` | Not built, not scheduled |
| `T-N10` `/v1`, the widget, the spec and the SDKs | **built 2026-09-14, unit-gated. No migration, no prompt change. The live room, the quickstart run and the query on a real Postgres owed; four places the ticket was wrong — §12** |
| `T-N8` The conversation budget and the loop guard | **ledger built 2026-09-14, unit-gated — §10. Its room half (the notice, the pass) built by `T-N6` — §11** |
| `T-N11` A room's history is a peer's words too | **filed 2026-09-13 from §8b; built 2026-09-14, unit-gated. `make eval` and the room arm owed; the ticket was short two leaks — §9** |

**As of `T-N3` a room routes.** One user message addressing two agents becomes
two `chat:run` turns. There is still no UI — that is `T-N4` — so a room can only
be assembled and addressed with `curl`.

---

## 1. The gap it closed

`031_thread_agent.up.sql` gave `agent_actions` an `agent_id` (`:24`) and
`usage_events` an `agent_id` and an index (`:30-34`). It gave `messages`
nothing. So since 2026-07-30 this product has been able to answer *which agent
ran this query* and *what did the Finance agent cost us* — and not *which agent
said that*, which is the only one of the three a customer reads.

In a thread holding one agent the gap is invisible: `conversation_threads.agent_id`
is the answer. It stops being invisible the moment a conversation can hold two,
which is `T-N2`.

**Two readers were affected, not one.** The transcript a person reads, and the
transcript `ChatRunner.hydrateMemory` (`chat_runner.go:2156`) replays into the
*next* turn's context. The second is the one that would have bitten first: three
personas flattened into one undifferentiated `assistant` voice is history the
model reasons over and gets wrong.

## 2. What was built

| Piece | Where |
| --- | --- |
| `messages.agent_id`, nullable, no FK, no index | `migrations/control/077_message_agent.up.sql` |
| Backfill of assistant rows from the thread's agent | same file |
| `domain.Message.AgentID` + `AgentName` | `internal/domain/message.go:19` |
| The roster join folded into the shared FROM clause | `internal/adapters/postgres/message_repo.go` — `messageColumns` + `messageFrom` |
| The agent read off the context, not a parameter | `internal/app/thread_service.go` — `AppendAssistantMessage` |
| One publish decorator stamping every event | `internal/app/chat_runner.go` — `ChatRunner.publish` |
| `ChatEvent.AgentID` / `AgentName` | `internal/app/event_bus.go:40` |
| Author label on assistant bubbles, multi-agent threads only | `apps/dashboard/src/features/chat/chat-page.tsx` |

### Three decisions worth the words

**The agent is read from `agentscope.FromContext`, not passed as a parameter.**
The ticket's *Do* said to pass `agentRow.ID` into `completeWith`. It turned out
not to be necessary: `agentscope.Scope` already carries `AgentID` and `Name` on
the context (`internal/agentscope/scope.go`), installed at
`chat_runner.go:783`, and it is *already* the value the audit decorator and the
usage recorder read from four packages down. Reading the same value in
`AppendAssistantMessage` makes the three rows a turn writes agree **by
construction** rather than by three call sites remembering to pass the same
thing. No signature changed.

**Twelve publish sites became one.** `ChatEvent` is built in twelve places in
`chat_runner.go`. Stamping the agent at each is twelve places to forget, and the
thirteenth publisher would have forgotten. `ChatRunner.publish` is the
decorator — the same argument `T-05` made for wrapping the tool registry rather
than each tool — and every site now goes through it. `r.bus.Publish` appears
exactly once in the file, inside the decorator.

**A `LEFT JOIN`, not a second column list.** `AgentName` is not persisted, so
every read joins `agents`. The join went into the shared `messageFrom` constant
rather than into the two or three queries that "need" it, because a second
agent-aware column list beside the existing one is two lists that drift and the
stale one is whichever the next reader does not notice. `LEFT` and not inner:
`agent_id` has no foreign key by design, so it can name a deleted agent, and an
inner join would make deleting an agent delete its messages from every
transcript — the exact outcome the missing FK exists to prevent.

## 3. What the build found

**The small-talk short-circuit completed before the agent was resolved.**
`ChatRunner.Run` resolved the agent at what was line 782 and short-circuited
greetings at what was line 748 — so a greeting went down `completeWith` with no
scope installed, and would have recorded no agent. The resolution moved above
the short-circuit. The cost is one indexed lookup on the cheapest path; the
short-circuit's own comment said it exists to skip *model* calls, and it now
says so precisely. **A greeting is still a thing an agent said**, and in a room
of three it is a thing one of three agents said.

**`agentscope.Scope.Name` said "carried for logs only. Nothing branches on it."**
It is now on every `ChatEvent` and therefore on a screen. Nothing branches on it
still, but a caller constructing a `Scope` by hand is now producing a label
somebody reads. The comment was updated rather than quietly falsified.

## 4. `T-N2`, and the design decision it forced

`conversation_threads.agent_id` says which agent a conversation *is*; membership
is a different question with a different cardinality. `thread_participants` is
the table, `ThreadParticipantService` owns the rules, and three routes hang off
the existing `ChatHandler`.

### The backfill that was written and then deleted

The ticket's *Do* specifies a backfill: one participant row per existing thread,
naming its `agent_id`. It was written that way, and then removed, because it
makes `thread_participants` the only answer to *who is in this conversation* —
and **threads are created by six paths**: the dashboard's `POST /api/threads`
and the WhatsApp, Discord, Lark, Slack and API enqueue paths. Every one of them
would have had to remember to write a row.

That is the exact shape of the defect
[`../plan/07-agentic-skills-roadmap.md`](../plan/07-agentic-skills-roadmap.md)
records against `T-K1`: a binding table written by one path and read by another,
invisible to every unit test, wrong for five days.

**So the default speaker is an implicit member with no row.**
`conversation_threads.agent_id` is already written by all six paths;
`ThreadParticipantService.List` returns the union of it and the rows, and the
table holds *the others*. A thread nobody has added an agent to is a room of one
with nothing in the new table — which is every thread that exists today, without
a backfill having touched any of them. `078` is purely additive: one table, one
index, no data write.

The cost is assembly in the service rather than one query, plus two checks that
would otherwise have come free from the unique index:

- **adding the default speaker** is `ErrAlreadyExists`, checked explicitly —
  there is no row for the index to collide with, and without the check the room
  would list one agent twice;
- **the cap counts the implicit seat**, or a room capped at four would hold five.

Both are tested.

### The rules, each with a test

| Rule | Answer |
| --- | --- |
| Another company's thread | `404`, never `403` — `RosterReader.GetByID`'s existence-oracle argument |
| Another company's agent, or one that never existed | `404`, indistinguishable |
| A **disabled** agent | `409` — disabling is how an admin stops an agent answering, and a room that could re-enlist one would make disabling a suggestion |
| The default speaker, removed | `409`, naming the fix: make another agent the default first |
| The cap | `409`, and the message names the limit |
| `THREAD_MAX_PARTICIPANTS` unset | `domain.MaxThreadParticipants` (4), applied in the constructor. Config defaults to `0` deliberately — repeating the number would be a second default able to disagree with the first |

Checks run cheapest-first, and the tenant check runs before all of them, so no
refusal below that line can leak whether an id exists.

### Two things chosen against

**Participants are not on the listing route.** `GET /api/threads` returns no
participants and issues no extra query; a sidebar needs a title, and joining a
second table per row to render one is a cost paid on every page load. Only the
detail read populates them, and a failure there is dropped rather than failing
the read — `ChatRunner.companyContext`'s argument, that context makes an answer
better and is never what makes it possible.

**`POST /api/threads` fails the whole request if a `participant_id` is refused.**
The thread is left behind rather than rolled back: it is a valid empty
conversation with a default speaker, and a compensating delete is a second write
that can also fail. The alternative — opening a conversation with quietly fewer
agents than the user picked — is discovered by addressing one and being told it
is not there.

## 5. `T-N3`, and the acceptance item that was struck

`@Finance what happened?` routes to Finance. Nothing addressed goes to the
thread's default speaker, which is what every message did before this ticket.
`@all` and `@everyone` address the room. Parsing is deterministic — **no router
LLM**, decision 3 — and `ParseAddressing` is a pure function of
`(text, participants)`, so the grammar is fully specified by its own test table.

### The two ways an `@` can fail need different answers

- **A name nobody has is text.** `@notanagent`, an email address, a handle.
  One turn, to the default speaker, message unchanged.
- **A name the company has and this conversation does not is a refusal.**
  `409`, naming the agent, **nothing enqueued**. Without this the user addresses
  a real agent, is silently answered by the default speaker, and finds out by
  reading a reply in the wrong voice — the failure `T-S3` refused to ship.

Telling those apart needs the roster, so `RoomReader` has a second method. It is
read **only** when an `@` matched no participant, which is off the hot path for
every ordinary message and is pinned by a test that counts the calls.

A **disabled** roster agent is text, not a refusal: it is unreachable, and
refusing would tell a user to add an agent they cannot add.

### What the build found, and what was narrowed

Three dependencies became consumer-declared interfaces, each one method wide:
`ChatRunEnqueuer`, `CompanyReader` and `RoomReader`. The first was not tidying.
The ticket's central claim is *one user message, N turns, one `UserMsgID`* —
and with a concrete `*queue.Enqueuer` that claim was checkable only against a
live Redis, **which is why nothing checked it**. It is now five tests, including
the fan-out order, the stripped `@` tokens, and a partial queue failure
reporting how far it got.

`@` tokens are stripped from what the model sees and kept in what is persisted —
`lark.StripMentions`' arrangement, and its reason: leaving them in the prompt
teaches the model that `@` is something it should produce.

Both degraded paths fail open. A participant lookup that errors, or a roster
lookup that errors, resolves the turn to the thread's agent rather than refusing
it — `ChatRunner.companyContext`'s argument, that context makes an answer better
and is never what makes one possible.

### The acceptance item that was struck rather than ticked

> *"A tenant with credit for one turn who addresses three gets one answer and a
> refusal naming the other two."*

**This is not achievable as written and was not quietly dropped.**
`UsageService.CheckBudget` (`credits.go:122`) is a *cached balance read*. The
decrement happens in the worker after a turn runs, and nothing reserves credit
at enqueue — so calling it three times returns the same verdict three times, and
the roadmap's "three `CheckBudget` calls" would have been three identical
answers dressed as enforcement.

Making it true needs a reservation at enqueue time. That is a credits ticket,
not an addressing one, and inventing one here would have put a spend-control
mechanism in the middle of a routing change.

**The exposure, stated plainly:** a tenant near zero who addresses N agents can
overshoot by up to N turns instead of one. It is bounded by
`THREAD_MAX_PARTICIPANTS` (4 by default), and the single-turn path already has
the same shape of overshoot — a turn is checked before it is queued and paid for
after it runs. So this widens an existing gap rather than opening a new one, and
the roadmap's `T-N3` now carries the struck item with this reasoning attached.

## 6. `T-N4`, and three things the screenshots found

The room on screen: a participant bar, `@` autocomplete, one streaming bubble
per agent, and an author on every answer.

![The room](assets/room-participant-bar.png)

### The state change that was the actual work

`liveAssistant` was a single `LiveTurn | null` — the right shape for a thread
that can only have one turn in flight. A room addressing two agents has two,
concurrently, **on one socket and under one job id**, because `T-N3` appends the
user message once and every payload carries its id. So the container became a
map keyed by `agent_id || job_id`, and three consequences followed that the
ticket did not name:

- **`final` no longer ends the turn.** It ends *one agent's* turn. A room
  publishes N `final` events, and the backstop poll and the "we have an answer"
  flag must wait for the last of them, or the other agents are stranded
  mid-answer with nothing polling for them.
- **`error` is the same shape.** A room where Finance errors must still deliver
  Ops's answer, so an error drops one turn and the banner names the agent —
  "Something went wrong" in a room of three does not say whose answer is
  missing.
- **The scroll effect** followed one turn's growth and now follows the combined
  progress of all of them.

### Three findings from actually looking at the screenshots

**1. The harness's Tailwind globs excluded `harness/`.** `tailwind.config.ts`
scanned `./src/**` only, so a utility used *only* in a screenshot scene was
absent from the generated CSS. The grayscale scene came out in **full colour**
and a height class collapsed — both silently, and both look exactly like product
defects rather than harness defects. This has been true since the harness was
written; nobody hit it because every earlier scene happened to use classes the
app also uses. `harness/**/*.{ts,tsx}` is now in the globs, which is the fix for
every future scene as well as these.

**2. `bg-accent` is the brand red in this design system.** `--accent` is
`#F25C5C` (`tokens.generated.css:27`), so the mention menu's active row rendered
as a solid red fill that swallowed the agent's own colour dot. `command.tsx` —
the closest analogue, a highlighted typeahead row — uses `bg-secondary`, and the
menu now does too. `dropdown-menu.tsx` pairs `bg-accent` with
`text-accent-foreground`; a component that borrows one without the other gets an
unreadable row.

**3. `tsc -b` catches what `tsc --noEmit -p` does not.** A use-before-declare in
`chat-page.tsx` passed the incremental check I was running after each edit and
failed the project build. The gate is `pnpm --filter dashboard lint`, which runs
`tsc -b`; nothing else is a check.

### What the grayscale scene is for

![The room, grayscale](assets/room-participant-bar-grayscale.png)

Colour is never the attribution — the name is, and the colour reinforces it.
Under a grayscale filter the three agents are still fully distinguishable, which
is the rule `T-R3`'s palette gate set for the report charts, applied to the one
other place this product colours things by category.

Colours come from the tokens' categorical ramp **by roster position, never
hashed from the id**. Two agents colliding on one hue in a room of four is
roughly a one-in-three coin flip on a hash, and the collision would be silent
and permanent for that tenant.

### What `AgentPicker` keeps

The picker still sets which agent a *new* conversation opens on and still
disappears after the first message. Its own comment argued that reinterpreting
history under a different persona is a decision rather than a widget, and that
is untouched: the participant bar adds a **reader**, not a reinterpretation, and
the picker's doc comment now says where the line is so the next reader does not
"fix" the apparent inconsistency.

## 7. What is owed

**Neither migration has been applied anywhere.** `077_message_agent` and
`078_thread_participants` are written with both directions and have had **no
round-trip against a real Postgres**. On
this machine that is not an effort problem — there is a Postgres — it is that
the only control-plane database here is
[**production**](environment-notes.md), serving a live pilot tenant. The arm is
filed in [`live-gate-backlog.md`](live-gate-backlog.md) §3d, the bucket for arms
owed on a permission rather than on effort, with the exact commands.

Until it runs, these acceptance boxes are unticked and stay unticked:

- **`T-N1`** — the round-trip is clean; the backfill sets every historical
  assistant row of an agent-pinned thread and leaves every `user` row `NULL`; a
  real turn writes an `agent_id` matching the `agent_actions.agent_id` of the
  tool calls in the same turn
- **`T-N2`** — the round-trip is clean; the `ON DELETE CASCADE` on `agent_id`
  actually removes participant rows and leaves the threads openable; the unique
  index refuses a duplicate that two concurrent adds would otherwise both win

`T-N2`'s "the backfill gives every agent-pinned thread exactly one participant"
is **not owed — it no longer exists**, and §4 is why: there is no backfill, and
the property it was checking is now structural.

**What *is* proven for `T-N2`**, by thirteen tests over the service: the implicit
default speaker, the union read, a thread with no default speaker, and every one
of the six refusals in §4's table. Plus the ticket's load-bearing negative —
**no turn behaves differently**, which is checkable rather than assertable:
`chat_enqueuer.go` and `internal/queue/` carry zero changes across both tickets.

**What is proven for `T-N3`**: 13 grammar cases over the pure parser (including
the two word-boundary regressions below), 11 over the resolution path, and 5
over the fan-out. The grammar table *is* the specification — Appendix A of the
roadmap and the test are the same list.

**A defect the tests found before the gate did.** The first parser matched a
participant by bare prefix, so an agent named `Ops` answered `@opsummary` and
`@allocation` addressed the whole room — `atTokens` deliberately over-reads to
the end of a plausible name, because otherwise a two-word agent like "Finance
Team" is unaddressable, and a prefix match over an over-read token matches the
wrong word. Both directions are now boundary-checked and both are regression
cases.

**What is proven for `T-N1`**, by tests that fail without the change (all four
verified red before green):

- an assistant message records the agent that wrote it
- a turn whose payload names a **deleted** agent records the agent it *ran as* —
  the default — and not the one the payload asked for
- a greeting records who greeted
- every published event carries the agent, and an unscoped turn's event is
  unchanged
- a user message carries no agent
- an unscoped turn writes no agent rather than failing

**One `T-N4` acceptance item is owed rather than met**: *"A single-agent thread
is pixel-identical to today — proven by a harness screenshot, not by
inspection"*. The harness mounts components, not the routed `ChatPage`, so there
is no before/after pair of the whole screen and producing one means either
mounting the router in the harness or exporting internals for it. What is proven
instead is structural, and saying which is the point: `ParticipantBar` returns
null below two participants with nothing addable, `showAuthors` is false, and
`PendingBubble` receives no author. That is an argument, not a photograph.

**Nobody has typed an `@` into this product.** The three scenes photograph the
room's controls against real components and real fixtures, which is what the
harness is for; none of them is a person using the feature against a running
stack. That arm needs the migrations applied first, and it is the same arm as
the `T-N3` live gate. The three routes are wired and policy-gated at
`RoleMember`, matching the thread routes beside them rather than the admin-only
`/api/agent-bindings`: a channel binding is routing configuration for a
company's shared rooms, whereas these act on one conversation the caller
already has.

**The dashboard's multi-agent branch has never rendered.** `showAuthors` is true
only when a thread's transcript holds two distinct `agent_id`s, which cannot
happen until `T-N2` and `T-N3`. That is deliberate — a flag hard-wired `false`
awaiting a later ticket is a branch nobody can see working, whereas this one
turns itself on the day the room exists. It is also, honestly, **untested in the
true case**, and the screenshot arm for it is owed with `T-N4`.

## 8. `T-N5`, and the path the ticket did not name

**Built 2026-09-13, unit-gated. The paired `make eval` is owed** (live-gate §7d).
No migration — for once the header was right.

`T-N5` is the receiving half of a peer turn: what a turn does when its payload says
another agent wrote the message. Nothing writes such a payload until `T-N6`, so every
test builds one by hand, as the ticket asked.

### 8a. What was built

| Piece | Where |
| --- | --- |
| `taint.KindAgent`, with what it is and what it is not | `internal/taint/taint.go` |
| `Tracker.Carry` (keeps the unnamed read) and `Tracker.Inherit` (copies, never shares) | same file |
| `guardrails.FencePeer`, and `PeerSourcePrefix` = `message from agent` | `internal/guardrails/fence.go` |
| `ChatRunPayload.Peer` → `queue.PeerOrigin{AgentID, Taint}` | `internal/queue/tasks.go`, `internal/queue/peer.go` |
| `receivePeer`: inherit, mark `agent` under the roster name, drop a directive | `internal/app/chat_runner.go` |
| `peerMessage`: the fence, applied where the model's input is composed | same file |
| `peer_agents` on the turn's completion line | same file |
| One bullet in the unconditional `T-H8` guideline | `internal/bootstrap/system_prompt.go` |
| `AgentAction.InputTaint`'s vocabulary comment, and its generated copy | `internal/domain/agent_action.go`, `packages/api-types/src/domain.ts` |

**The acceptance items, quoted back:**
- [x] *`KindAgent` is recorded on a turn that received a nudge (driven by a stub).*
  `TestAPeerTurnInheritsTheAuthorsTaintAndIsMarkedAgent`.
- [x] *A turn whose author was tainted `document` produces a recipient turn that is
  also tainted `document`, and `T-H9`'s approval gate fires on it.*
  `TestAPeerTurnWhoseAuthorReadADocumentGatesTheAction` goes through
  `ActionService.ProposeAction` itself, not a copy of its rule. On a workspace that
  auto-approves, approval is withheld, nothing executes, and the reason names
  `invoice-4471.pdf`. Its negative, `TestAPeerTurnFromAnUntaintedAuthorStillAutoExecutes`,
  shows `agent` alone gates nothing.
- [x] *Peer content arrives fenced and labelled with the author's agent name.*
  `TestAPeerMessageIsFencedUnderItsAuthorsName`, and end to end through `Run` in
  `TestAPeerTurnCannotSetTheDirective`.
- [x] *A nudge body containing the fence sentinel does not escape the fence.*
  `TestAPeerMessageCannotCloseItsFence` covers both markers, and
  `TestAnAgentNameCannotCloseTheFence` covers a hostile name.
- [x] *No path exists by which a nudge sets `Directive` — assert it.* Asserted in
  `Run`: a peer payload carrying a directive reaches the agent factory with an empty
  `SystemAddendum`.
- [x] *A nudge payload carrying scope-shaped fields does not widen the recipient's scope.*
  `TestAPeerPayloadCannotWidenTheRecipientsScope` decodes a payload with scope-shaped
  keys at the top level and inside `peer`. Scope, persona and tools come back equal
  to the recipient's row. `TestThePeerCarrierHoldsNothingThatDecidesATurn` holds the
  shape that keeps it true: `PeerOrigin` is exactly two fields, and `ChatRunPayload`
  may carry no field named like a scope.
- [ ] *`make eval` pass rate at or above baseline; both numbers pasted.* **Owed** — no
  model key here (live-gate §7d).

Also *"the audit row records the kind"*: `TestAuditRowRecordsWhatAPeerHandedTheTurn`
shows `input_taint = "agent,document"` and `document_tainted = true`.

### 8b. The finding: a room already hands one agent's words to another, unfenced

The ticket says the laundering path *"exists the moment `T-N6` ships unless this lands
first"*. **Half of it has existed since `T-N3`,** it shipped in `v1.7.0`, and no
nudge is involved.

- **Every agent in a room shares one conversation memory.** `buildMemory`
  (`bootstrap/stack.go`) builds a single `RedisMemory`. The SDK keys it
  `org:conversation` (`redis_memory.go:158`), and `ChatRunner.Run` sets those to the
  company and the **thread** (`multitenancy.WithOrgID`, `memory.WithConversationID`).
- **Every provider replays that buffer into its request** (`pkg/llm/openai/message_history.go:30`,
  and the same in the anthropic, deepseek and gemini providers).
- **So the next agent reads its colleague's reply as its own.** Ask Finance about a
  supplier PDF, then say `@Ops`. Ops' request carries Finance's reply as an
  **`assistant` message**, as though Ops had written it: no fence, no label, no `agent`
  taint and no `document` taint.
- **`hydrateMemory` is the cold path, and does the same.** It copies `m.Role` straight
  through. It runs only when the buffer is empty, so fixing it alone closes nothing.

**What that costs.** Finance's summary of the PDF, with whatever instruction the PDF
carried, reaches Ops with the trust of Ops' own reasoning. That is worse than decision
5's colleague's-request shape. `T-H9` does not gate Ops.

It is also the flattening `T-N1`'s *Why* named: *"three personas flattened into one
undifferentiated `assistant` voice"*. `T-N1` fixed the record and the transcript, not
the replay. §1 of this file reads as though it had.

**How much of it is new.** Less than it sounds. `T-H9` gates per turn, so a single
agent that read a PDF in one turn and is asked to "send it" in the next is not gated
either — its own summary sits in its history, untainted. What a room adds is a
*different* reader, with different tools and sources: Ops may hold `propose_action`
where Finance did not. That widens an existing gap rather than creating a new class.

**Not fixed here, and why.** Closing it means deciding what a peer's *history* is,
which the ticket did not ask.
- **The fence is mechanical:** a memory decorator that stamps the author on write and,
  on read, turns another agent's `assistant` message into a fenced user-role block.
  But it changes every room turn's context, so it owes its own eval pair.
- **The taint is a decision.** Inheriting from history would gate every room turn after
  anyone read a document — the off switch `T-H9` argues against. Not inheriting keeps
  the cross-turn gap a single agent already has.

Filed as `T-N11` in the roadmap and **recommended before `T-N6`**. Live-gate §7d's third
row is the *before*.

### 8c. Where the ticket was wrong, or silent

- **"Build against a stub" needed a carrier, and `T-N6` had specified one that could not
  work.** `T-N6` gave the payload `NudgeFromAgentID` and had `Run` install the inherited
  taint from it. An id cannot carry a taint: the author's tracker lives in another
  process. `T-N5` defines `Peer *PeerOrigin{AgentID, Taint}`, and `T-N6`'s bullet is
  revised to set it.
- **The author's name is resolved, not carried.** The ticket says the label carries the
  author's name. A name on the payload would be a label the sender types. The worker
  looks it up from `Peer.AgentID`, scoped to the turn's company. A deleted author, or
  another company's agent, is fenced and tainted with no name.
- **"Do not run peer text through the input guardrail classifier" is deliberately not
  done.** The classifier sees the whole composed message (`ProcessInput` over
  `agentMsg`), so the fenced body reaches it.
  - **The markers cannot be the signal.** A skip keyed on finding them would let a person
    bypass the input guardrails by typing one into the chat box.
  - **The payload is the safe signal** (`p.Peer != nil`), and a per-turn guardrail
    exception belongs to the ticket that enqueues peer turns. It moved to `T-N6`.
  - **Until then it fails closed.** A fenced peer question that trips
    `block_prompt_injection` is refused.
- **"The audit row records the kind" needed no change to the decorator.** `InputTaint` is
  `taint.Join`, so `agent` appears as soon as a turn is marked. The *names* — "the
  Finance agent, which had read invoice-4471.pdf" — cannot go on `agent_actions`, which
  has no sources column. They went on the completion line as `peer_agents`, beside
  `document_sources`, which now includes what a peer carried in.
- **`T-H9`'s reason is slightly off for an inherited taint.** It says *"this turn read the
  uploaded document invoice-4471.pdf"*, when the turn was handed the content. An approver
  can still act on it, so it was left alone. Making it exact means carrying *how* a taint
  arrived — a second field, for one sentence.
- **The prompt bullet is unconditional.** The ticket asks for a sentence in the shared
  prompt, and no tool delivers a peer message — it arrives in the user turn — so there is
  nothing to condition it on. Every prompt grows by one bullet: one cache miss at deploy.
  It sits inside an existing guideline, so no guideline number moves.

### 8d. Proven failing

Each mutation was applied, run, and restored, with the file `cmp`'d byte-identical before
the gate:
- **Inheritance removed:** three tests failed, including the `T-H9` gate test.
- **The directive kept:** `TestAPeerTurnCannotSetTheDirective` failed.
- **`peerMessage` returning the raw words:** the same test failed, on its fence assertion.
- **`Carry` dropping the unnamed read:** `TestAnUnnamedReadSurvivesTheCarry` failed.

The scope test did not fail under the directive mutation, and should not have: decoding
already discards a `directive` nested inside `peer`. The directive property is carried by
the `Run` test.

The gate's output is in [`delivery-log.md`](delivery-log.md) Phase 3ba.

### 8e. What is owed

- **The paired `make eval`** for the prompt bullet. Prediction, recorded in live-gate §7d:
  no movement beyond the set's ±2-case band. Both rates go here when it runs.
- **The real nudge from a document-tainted turn**, which needs `T-N6`.
- ~~**The room's history**~~ — built as `T-N11` on 2026-09-14, §9.

## 9. `T-N11`, and what one buffer per thread was carrying

**Built 2026-09-14, unit-gated.** No migration. The paired `make eval` is owed (live-gate
§7d), with a prediction that it cannot move.

The owner took §8b's recommendation:
- a colleague's earlier turns do **not** pass their document taint on;
- the reading turn records `agent`;
- the gap between turns is filed against `T-H9`, in roadmap 03.

### 9a. What the buffer held — read from the SDK, not assumed

§8b named the replies. Reading `agent-sdk-go` v0.2.56 turned up two more things in the same
buffer, both worse:
- **Every tool call and tool result.** Both providers (`pkg/llm/openai/client.go`,
  `pkg/llm/anthropic/client.go`) and the streaming path (`pkg/agent/streaming.go`) write
  them with the turn's own context: an empty assistant message carrying the calls, then a
  `tool` message carrying the result. So Ops' request carried Finance's query rows, from a
  source Ops' allowlist may not reach. **That is a scope leak, not only a trust one.**
- **Every composed prompt.** `agent.Run` and `RunStream` store their input as the user
  message. That input is `agentMsg`: the source catalog, metrics, actions, cookbook and
  prior-work blocks for *that* agent.

And one path outside the buffer. **The prior-work block** (`T-Q6`) was built from digest
rows written with no `agent_id`. It told Ops *"work already done earlier in THIS
conversation — reuse it"*, and listed Finance's SQL and source ids.

**The thread summary also mixes every agent** (`thread_service.go`). It is left alone: it
is one or two sentences, told to leave numbers out, and it names no source.

### 9b. What was built

| Piece | Where |
| --- | --- |
| `peermemory.Memory`: stamps each message on write, rewrites the read for the agent reading | `internal/peermemory/peermemory.go` |
| Both branches of the agent memory — Redis and in-process — behind it | `bootstrap/stack.go`, `buildMemory` |
| Replayed rows stamped from `messages.agent_id` | `app/chat_runner.go`, `hydrateMemory` |
| The person's words recorded beside the composed prompt | `chat_runner.go`, `peermemory.WithQuestion` before the agent runs |
| Digest rows written under their agent, and prior work filtered by it | `chat_runner.go`, `rememberToolWork` and `priorWork` |

**The rule, for a turn running as agent C.** The package comment carries the reasons.

| In the buffer | What C reads |
| --- | --- |
| C's own messages, a person's, anything unstamped, anything written by an unscoped turn | as written |
| A colleague's reply | a user-role message, fenced under the colleague's name; the turn records `agent` |
| A colleague's tool call or tool result | nothing |
| A colleague's composed prompt | the person's words it recorded — or nothing, if it recorded none |
| One question, written once by each of two agents' turns | the question, once |

A reader with no agent — the eval harness — reads the buffer unrewritten.

### 9c. The acceptance items, quoted back

- [x] *B's provider request carries A's reply fenced under A's name in a user-role message,
  and B's own earlier replies as `assistant`.*
  `TestAColleaguesTurnReachesTheModelFencedOnAWarmBuffer` drives `Run` with a stub model
  that reads `GenerateOptions.Memory` the way `message_history.go` does.
  `TestTheReadersOwnTurnsKeepTheirToolPairs` covers B's own turns.
- [x] *A single-agent thread's provider request is byte-identical to today's.*
  `TestASingleAgentReadsItsHistoryExactlyAsWritten`: the view returns the buffer
  `reflect.DeepEqual`.
- [x] *B's turn records `agent` with A's name.* Asserted in the package tests and the
  runner tests.
- [x] *Proven on both paths.*
  - Warm: `TestTheViewHoldsOverRealRedisMemory` puts `miniredis` behind the SDK's real
    `RedisMemory`, so the stamp survives its JSON encoding. The `Run` test covers it too.
  - Cold: `TestHydrationAttributesEachReplyToTheAgentThatWroteIt`.
- [ ] *`make eval` before and after.* **Owed** (live-gate §7d). **Prediction: identical.**
  Every golden case is one agent, and the eval harness runs unscoped. The view is identity
  on both, as proven above, so any delta is the set's noise band.
- [ ] *§7d's third arm re-run as the after.* **Owed** — and it never had a *before* either.
  §7d now runs both in one sitting.

**Beyond the ticket:**
- `TestPriorWorkLeavesOutAColleaguesQueries`
- `TestToolWorkIsWrittenUnderTheAgentThatDidIt`
- `TestTheAgentMemoryIsBehindTheRoomViewOnBothBranches`
- `TestTheWrapperKeepsConversationMemory`
- `TestAQuestionAddressedToTwoAgentsIsReadOnce`
- `TestUnstampedHistoryIsReadAsBefore`

### 9d. Where the ticket was wrong

The ticket was mine, written a day earlier from a grep rather than from reading the SDK.
- **It named one leak, and there were three,** plus prior work (§9a). Its *Do* would have
  fenced the replies and left a colleague's raw rows and source catalog where they were.
- **"Re-role an `assistant` message" was incomplete.** Most assistant messages in the
  buffer are tool-call carriers with empty content. Re-roling them would have produced
  empty fenced blocks and orphaned tool results — a request both OpenAI and Anthropic
  reject. They are dropped.
- **Its rule for unstamped messages — "as today in a thread of one, unattributed peer text
  in a room" — was not built,** because it needs a participant lookup inside memory. It
  isn't needed while no rooms exist. Production runs `1.6.0`, read from the deployment on
  2026-09-14, and has no rooms, so every unstamped buffer in existence is one agent's.
  **This holds only if `T-N11` ships no later than rooms do.** Deploying `v1.7.0` on its own
  would create unstamped room buffers, and an active conversation's 24-hour Redis TTL is
  refreshed on every write.
- **"Check that `RedisMemory` round-trips `Metadata`" — it does.** It JSON-encodes the whole
  `interfaces.Message`, and a test now holds that.
- **The prior-work limit is applied before the filter,** so a busy room gives an agent fewer
  of its own digests than `PRIOR_WORK_TURNS` allows. The cost is a re-read schema, not a
  wrong answer.

### 9e. Proven failing

Nine mutations. Each file was restored and `cmp`'d before the next.

| Mutation | Tests that failed |
| --- | --- |
| The view returns the buffer unrewritten | 6 package tests, the `Run` test, the hydration test |
| Hydration stamps nothing | the hydration test |
| The prior-work filter off | `TestPriorWorkLeavesOutAColleaguesQueries` |
| A digest row written with no agent | `TestToolWorkIsWrittenUnderTheAgentThatDidIt` |
| `buildMemory`'s in-process branch returned unwrapped | `TestTheAgentMemoryIsBehindTheRoomViewOnBothBranches` |
| An explicit stamp overwritten on write | the explicit-stamp test, the hydration test |
| The question de-duplication never matches | `TestAQuestionAddressedToTwoAgentsIsReadOnce` |
| A colleague's tool results kept | 4 package tests, the `Run` test |
| A colleague's composed prompt kept | 3 package tests, the `Run` test |

**The first de-duplication mutation proved nothing, and was redone.** It deleted the
variable's only read, which does not compile — a build failure is not a failing test. The
redone mutation compiles, and failed as shown.

The gate's output is in [`delivery-log.md`](delivery-log.md) Phase 3bb.

### 9f. What is owed, and what stays open

- **The paired `make eval`.** Prediction: identical.
- **§7d's room arm, as a before-and-after in one sitting.**
- **Deploy `T-N11` no later than rooms.** §9d's rule for unstamped history depends on it.
- **Open: the gap between turns.** It applies to a single agent as much as to a room, and
  is filed against `T-H9` in roadmap 03.
- **Open: consecutive user-role messages.** A question followed by a fenced reply is two
  user messages in a row. OpenAI-compatible endpoints accept that. The Anthropic interface
  already sends tool results as user messages, so the shape is not new there — but it has
  not been seen against a real Anthropic endpoint.

## 10. `T-N8`, and a loop guard with nothing to guard yet

**Built 2026-09-14, unit-gated.** No migration and no prompt change, so no `make eval` is
owed. Nothing asks another agent until `T-N6`, so in production the ceilings have nothing
to refuse yet. What goes live the day this deploys is the counting.

### 10a. What was built

| Piece | Where |
| --- | --- |
| `agentbudget.Conversation`: one ledger per person's message, in Redis, decided by two Lua scripts | `internal/agentbudget/conversation.go` |
| `Ceilings` — 6 agent turns, depth 2, 5 minutes — labelled as arithmetic, not measurement | the same file, `DefaultCeilings` |
| `Open`: a person's fan-out, counted before its first turn is queued | called from `ChatEnqueuer.Enqueue` |
| `Admit`: one ask, for `T-N6`'s nudge and `T-N7`'s hand-off | no caller yet |
| `Verdict.ToolResult` and `Verdict.Notice`: what the asking model reads, and what the room shows | the same file |
| `CONVERSATION_MAX_AGENT_TURNS`, `CONVERSATION_MAX_NUDGE_DEPTH`, `CONVERSATION_WALL_SECS` | `internal/config`, `.env.example` |
| `argentum_conversation_ceiling_hits_total{dimension}` | `internal/metrics` |
| The ledger on both processes that queue a person's turns | `cmd/api/bootstrap.go`, `cmd/discord/main.go` |

**The rules**, in the order the script checks them:

| Asked for | Answer |
| --- | --- |
| A person's message, to any number of agents | queued whole and counted — never refused, even past the ceiling |
| An ask deeper than `MaxNudgeDepth` | refused before Redis is read, so no ledger is created |
| An ask after `Wall` has passed since the first turn | refused |
| The same agent, asked the same question again in this conversation | a repeat: nothing queued, nothing counted, and not a refusal |
| An ask past `MaxAgentTurns` | refused |
| An ask when Redis cannot be reached | refused |

**The ledger** is one hash per company and message: `started`, `turns`, and one
`asked:<agent>:<digest>` field per question asked. Its TTL is `2 × Wall`, set when the hash is
created and never extended. **Depth is not stored.** It rides the asked turn's payload, so a
lost or expired ledger cannot reset it.

### 10b. Three decisions worth the words

- **A person's message is never refused by the loop guard.** The ticket counts the fan-out
  against the ceiling. That is right — an ask must know four turns are already running —
  but it does not say what happens when the fan-out alone is wider than the ceiling.
  Refusing part of an `@all` would be a loop guard overruling a person about their own
  message, after the credit check has agreed to it. So `Open` counts and never refuses, and
  a fan-out that wide simply leaves no asks.
- **`Open` fails open; `Admit` fails closed.** A Redis outage must not stop a person's
  message; the rate limiter and the share-refresh cap fail open for the same reason. An ask
  is a model spending another agent's budget on its own initiative, and admitting it blind
  is the unbounded spend this ticket exists to stop. The asking model gets a result it can
  act on either way.
- **A refusal carries `budget_exhausted`, deliberately.** That is `IsRefusal`'s key, so
  three readers that already exist get it right with no change:
  - the audit decorator records the call as refused (`T-05`);
  - the tool digest remembers it as refused (`T-Q12`);
  - `Observe` keeps it out of the calls that succeeded, so *"I asked Finance"* is an
    unevidenced claim (`T-Q13`).

  One test asserts all three. A **repeat** carries `already_asked` instead: the colleague
  *was* asked, and saying otherwise would have the model apologise for a question already
  on its way.

### 10c. The acceptance items, quoted back

- [ ] *A stubbed agent that always nudges terminates, and the room says why.* **The ledger's
  half is met; the room's half moved to `T-N6`** (§10d).
  `TestARoomThatAlwaysNudgesStops` runs a room of three in which every turn asks both
  colleagues something new. It is run once with each ceiling binding:
  - turns bind: 6 turns run;
  - depth binds: 21 turns run — 3, then 6, then 12, and nothing deeper.

  Each run's `Notice` names the question that went unasked. Nothing writes it into a room
  until `T-N6`.
- [x] *Depth 3 is refused; depth 2 runs.* `TestDepthTwoRunsAndDepthThreeIsRefused`, which also
  checks that a refused hop counts nothing and creates no ledger.
- [x] *The turn counter is shared across two worker replicas — proven with two processes, not
  one.* `TestTwoProcessesShareOneCounter` starts two copies of the test binary, each with its
  own client, against one Redis. Each tries 30 asks against a ceiling of 40. **They were
  admitted 20 and 20** (pids 1267296 and 1267297 on the run under `-race -v`), where two
  counters would have admitted 60. The Redis was `miniredis`; a real one is owed (§10f), and
  so is the arm with two *workers*, which needs `T-N6`.
- [ ] *Exhaustion produces a visible message naming the unasked question.* The sentence is
  built and tested (`Verdict.Notice`). Writing it into the room is `T-N6`'s.
- [x] *The per-turn budget is unchanged.* `budget.go`, `guard.go` and `checkpoint.go` carry
  zero changes: `internal/agentbudget` gained two new files and nothing else. The existing
  budget tests pass unchanged.
- [x] *A tenant at zero credit still gets the credit refusal, not this one.*
  `TestZeroCreditIsTheCreditRefusalAndTouchesNoLedger`: `ErrInsufficientCredits`, nothing
  queued, and no key written to Redis.
- [x] *The conversation key expires.* `TestTheLedgerExpiresAndIsNeverExtended`: 10 minutes at
  creation, 6 after an ask four minutes in (not pushed back out), gone at 10.
- [ ] *A pass ends a chain and renders as a settle.* **Moved to `T-N6`** (§10d).
- [x] *An agent addressed twice with nothing in between is enqueued once* — as a repeat, which
  is the form of the watermark this product can reach (§10d).
  `TestAnAgentAskedTheSameQuestionTwiceIsQueuedOnce` covers case and spacing, a new question,
  and another agent. `TestAMessagesFanOutIsOnTheLedgerBeforeAnyAsk` shows a colleague
  re-asking the person's own question is a repeat. One agent named twice in one message was
  already one turn: `ParseAddressing` has de-duplicated since `T-N3`.
- [x] *The ceilings are readable and overridable per company, not compiled in* — **per
  deployment**, as the ticket's own *Out of scope* says (§10d). Three env vars, zero meaning
  the default.

**Beyond the ticket:**
- `TestAPersonsFanOutIsCountedAndNeverRefused`
- `TestAMessagesFanOutIsOnTheLedgerBeforeAnyAsk` — the ordering: counted before any ask
- `TestAFanOutWiderThanTheBudgetStillReachesEveryoneAddressed`
- `TestALedgerThatCannotBeReachedRefusesAnAskAndNeverAPerson`
- `TestALedgerOutageDoesNotRefuseAPersonsMessage`
- `TestARefusalIsReadAsARefusalByTheReadersThatAlreadyExist`
- `TestTheClockEndsAConversation`
- `TestOnlyACeilingIsCounted`, `TestExpositionCarriesTheConversationCeilings`
- `TestTheDefaultCeilingsAreTheirStatedArithmetic` — pins the numbers *and* the claim that
  five minutes is two per-turn wall clocks, so a change to either makes somebody re-read the
  comment.

### 10d. Where the ticket was wrong

- **Its watermark cannot fire.** *"Do not enqueue an addressed agent whose context has not
  changed since its own last turn."*
  - Hermes' version fires in a *round*, where every member is re-polled whether or not
    anyone spoke to it.
  - Nothing here re-polls. Every turn is queued because a new message arrived for it — the
    person's, or a nudge — so "no new messages it has not seen" is never true when a turn is
    queued.
  - The ticket's own example, *"a user addresses the same agent twice in a row"*, is two
    user messages.

  What is real, and costs a model call for nothing, is an agent asked again what it was
  already asked in this conversation. That is what was built: `Verdict.Repeat`.
- **Half its *Do* has no caller before `T-N6`, and moved there.**
  - **The pass outcome** needs a nudged turn that can pass, and a way for the model to say
    so. A tool or a sentinel is a prompt change with an eval attached, for a turn that does
    not exist yet.
  - **The room notice** needs something to write it. Only an ask is ever refused, and asks
    are made in the worker by the tool `T-N6` adds. `Verdict.Notice` builds the sentence;
    `T-N6` writes it, and designs its marker beside the settle's, which is the distinction
    the ticket wanted built in rather than retrofitted.
  - **The gate drives "the always-nudging stub" through two workers.** There is nothing to
    drive. The ledger's half is proven with two processes (§10c); the workers' half is owed
    with `T-N6`.
- **It asks for per-company ceilings, and puts per-company overrides out of scope.**
  Decision 9 and the acceptance say per company; *Out of scope* says a deployment default
  until a tenant asks. The narrower was built. A settings row would be read where `Admit`
  takes its ceilings.
- **`CONVERSATION_WALL=5m` became `CONVERSATION_WALL_SECS=300`**, beside
  `AGENT_TURN_BUDGET_SECS`. The config has no duration parser, and seconds are the house
  idiom.
- **"`internal/metrics` already has the shape" — in a process nobody scrapes.** Only an ask is
  ever refused, asks run in the worker, and the worker has no exposition endpoint. That is
  `T-17`'s gap, the same one the grounding counters sit in. Until it closes, the series is
  declared on `cmd/api`'s `/metrics` and will only ever read zero there.
- **"Outlives the fan-out by exactly `WALL`" is right, and is not the whole bound.**
  - A turn admitted in the window's last second runs for up to 150s more, and its ask must
    find the ledger in order to be refused by the clock. `2 × Wall` covers that.
  - A queue backlog longer than `Wall` does not: that ask would open a fresh ledger.
  - Depth is what bounds that case, which is why it is kept out of the ledger.
- **"Incremented at enqueue"** — and a queue failure after `Open` leaves turns counted that
  never run. That over-counts, which is the safe direction for a loop guard to be wrong in.

### 10e. Proven failing

Eight mutations, run one at a time. Each file was restored, and its hash matched the
pre-run hash, before the gate.

| Mutation | Tests that failed |
| --- | --- |
| The depth check off | `TestDepthTwoRunsAndDepthThreeIsRefused`, the depth-binds room, `TestOnlyACeilingIsCounted` |
| The turns ceiling off, in `admitLua` | the fan-out test, the turns-binds room, `TestTwoProcessesShareOneCounter`, the readers test, `TestOnlyACeilingIsCounted` |
| The repeat check off | `TestAnAgentAskedTheSameQuestionTwiceIsQueuedOnce`, `TestOnlyACeilingIsCounted` |
| Every ask pushes the expiry back out | `TestTheLedgerExpiresAndIsNeverExtended` |
| The wall clock off | `TestTheClockEndsAConversation` |
| An ask admitted when Redis fails | `TestALedgerThatCannotBeReachedRefusesAnAskAndNeverAPerson` |
| The enqueuer never opens the ledger | `TestAMessagesFanOutIsOnTheLedgerBeforeAnyAsk`, `TestAFanOutWiderThanTheBudgetStillReachesEveryoneAddressed` |
| A ledger outage refuses the person's message | `TestALedgerOutageDoesNotRefuseAPersonsMessage` |

**Not proven by any test: that the ledger is opened before the first turn is queued rather
than after.** The fan-out tests' queue never asks, so it cannot see the order. What holds it
is the call's position — after the user message is appended, whose id it needs, and before
the enqueue loop — and the comment beside it. Moving the call below the loop would pass
every test here. It would fail the first always-nudging room.

The gate's output is in [`delivery-log.md`](delivery-log.md) Phase 3bc.

### 10f. What is owed, and what stays open

- ~~**`T-N6`'s part**, carried in its *Do*~~ — built 2026-09-14, §11: `Admit` is called, the
  notice written, the pass and its marker built, the depth on `PeerOrigin`, and the ledger
  wired into the worker.
- **The two Lua scripts on a real Redis** (live-gate §7e). Prediction: identical to
  `miniredis`.
- **Two workers and an always-nudging room** (§7e) — no longer waiting on code.
- **§2c's measurement**, before any of the three numbers stops being a placeholder (§7e).
- **Open: a queue backlog longer than `Wall`** can let an ask open a fresh ledger (§10d).
  Depth still bounds it. If a real backlog ever does this, the fix is a longer TTL, not a
  stored depth.
- **Open: the ceiling metric is unreadable** until the worker exposes metrics (`T-17`).

## 11. `T-N6`, and a colleague you can ask

**Built 2026-09-14, unit-gated.** Migration `086`, where the ticket said none. The paired
`make eval`, the live room, `086`'s round-trip and the screenshots are owed (live-gate §7f).

`T-N6` is the sending half of a peer turn. `T-N5` built the receiving end, and `T-N8` built
the budget. An agent in a room, asked something that needs a colleague's source, asks that
colleague one question:
- the question is posted into the room as the asker's message;
- the colleague answers in a turn of its own;
- the person reads both.

### 11a. What was built

| Piece | Where |
| --- | --- |
| `agents.can_nudge`, default false, no backfill | `migrations/control/086_agent_can_nudge.*`, `domain.Agent`, `postgres.AgentRepo`, `app.AgentInput` |
| `nudge_agent`, which parses two arguments and hands them over | `internal/tools/nudge_agent.go` |
| `tools.GatedByFlag`: the one tool the allowlist does not decide, kept out of the checkboxes | the same file; `app.NewAgentService` |
| `NudgeService`: the gate again, who may be asked, credits, the ledger, the question in the room, the queued turn | `internal/app/nudge_service.go` |
| `PeerOrigin.ParticipantID` and `PeerOrigin.Depth` | `internal/queue/peer.go` |
| `offerNudge` in the factory, fed `AgentSpec.Nudge` by the runner — `offerRoomTools` since `T-N7` (§13a) | `bootstrap/stack.go`, `app/chat_runner_room.go` |
| The seat check before a colleague's question runs, and the withdrawn line | `app/chat_runner_room.go`, called at the top of `ChatRunner.Run` |
| The pass, offered in a colleague's user turn and read back as a settle | `withPassOption` (`withPeerFraming` since `T-N7`), `isPass`, `settle` in the same file |
| `guardrails.WithPeerTurn`: the topic classifier stands aside | `internal/guardrails/guardrails.go` |
| `Conversation.ClaimNotice`: one limit line per agent per message | `internal/agentbudget/conversation.go` |
| Room lines kept out of hydrated history | `ChatRunner.hydrateMemory` |
| A catalog line and a guideline, both conditional on holding the tool | `bootstrap/system_prompt.go` |
| The room, the ledger and the service in the worker's stack | `bootstrap.New`, `Stack.NewChatRunner` |
| A `room_event` event; the form's checkbox; room lines drawn as lines | `app/event_bus.go`; `settings/agents-tab.tsx`; `chat/chat-page.tsx` |

**The order a nudge is decided in** is the order of the answers the asking model gets:

| Check | Refusal |
| --- | --- |
| The turn is on the context and scoped; its agent has `can_nudge`; the room holds more than one | `not_available` |
| A name and a question, under 500 characters | `missing_agent`, `missing_question`, `question_too_long` |
| The name is a participant — case-insensitive, a leading `@` ignored | `not_in_conversation` |
| Not the asker | `cannot_ask_yourself` |
| The participant's roster row is this company's and enabled | `not_in_conversation`, `agent_disabled` |
| The credit balance | `credits_exhausted`, `credits_unchecked` |
| The conversation budget (`T-N8`) | `already_asked` for a repeat; otherwise `budget_exhausted`, and a limit line in the room |
| The question written into the room, then the turn queued | `not_delivered` if either fails |

Every refusal before the ledger names who *can* be asked. Each is a result carrying `error`,
not a Go error: the key keeps the call out of the ones that succeeded, so a reply saying "I
asked Finance" after one is unevidenced (`T-Q13`).

**A colleague's turn is the asking turn's payload, re-aimed:**
- **Changed:** the agent id, the message, and `Peer` — asker, taint, seat, depth.
- **Kept:** the person, the channel and its reply refs, the company's name and currency
  convention, the request id.
- **Cleared:** what the asking turn was *for* — a directive, a report job, a scheduled run, a
  watcher event — and the trace.

**The room's own lines**, on `messages.metadata.room_event`:

| `room_event` | Written as | Reads |
| --- | --- | --- |
| `nudge` | the asker | `→ Finance: Was a goods-in posted for SKU 4471?`, as the asker's bubble |
| `unasked` | the asker | `T-N8`'s `Notice`, led "Limit reached" |
| `settle` | the colleague | `Finance had nothing to add to the question from Ops.` |
| `withdrawn` | nobody | `Finance left this conversation before answering the question from Ops: "…"`, led "Not asked" |

### 11b. Decisions worth the words

- **A column, not a tool checkbox.** `allowed_tools` is where "which tools" lives, and
  extending it was the house rule. It cannot hold this one: an empty allowlist means every
  tool, so the capability would be on for every unrestricted agent. Ticking it on such an
  agent would also narrow that agent to the one tool. The flag is its own column.
- **Withheld at a hop the ledger would refuse, not only in a room of one.** The runner
  reads the ledger's `MaxNudgeDepth`, and does not offer the tool to a turn whose next ask
  could only be refused. The ticket's argument for the room gate — a tool that always refuses
  wastes iterations — is the same argument at the last hop.
- **The question is written before the turn is queued.**
  - The other order can end with an answer in the room to a question nobody can see: decision
    6's side channel.
  - This order can end with a question nobody answers. The asking model is told `not_delivered`.
- **`room_event`, not `final`.** The dashboard keys a live bubble by job id and agent id. A
  `final` under the asker's id would close the asker's bubble while it is still streaming.
- **The pass is a sentinel in the colleague's user turn, not a tool.**
  - A pass tool would sit in the schema of every turn in every room, and change the asking
    turns' prompts too.
  - The sentinel survives a model's decoration (`**PASS**.`) and nothing looser: "I'll pass on
    that" is an answer.
- **One limit line per agent per person's message.**
  - Per refused question lets a looping agent fill the room.
  - Per message hides the second agent that was cut short.
  - On a Redis error the line is written anyway, bounded by the per-turn tool-call ceiling.
- **Room lines are never replayed into a model's history.** An agent that reads "→ Finance: …"
  as something it once wrote learns to type a hand-off instead of calling the tool. `T-N3`
  strips `@` from the model's input for the same reason.
- **Only the topic classifier stands aside for a colleague's question.** The skip is keyed on
  the payload. The injection and off-topic block rules still run, because a colleague's words
  are untrusted input (decision 5).

### 11c. The acceptance items, quoted back

- [x] *`nudge_agent` is not in the tool list for an agent with `can_nudge` false, nor in a
  thread with one participant.*
  - `TestNudgeIsOfferedOnlyWhereBothGatesHold`, which also shows an agent without the flag
    costs no room read.
  - `TestATurnIsOfferedNudgeOnlyInARoomOfMoreThanOne`, through `Run`.
  - `TestAnAgentThatMayNotNudgeGetsExactlyTheToolsItHadBefore`, at the factory.
- [x] *A nudge writes one visible assistant message from the asker and enqueues exactly one
  `chat:run` for the target.* `TestANudgeWritesOneVisibleQuestionAndQueuesOneTurn`.
- [ ] *The target's answer lands in the same thread, attributed to the target.* The queued
  payload carries the thread and the target's id, and `T-N1` attributes a turn's message to
  the agent it ran as; both are proven. The answer landing is the live arm (§7f).
- [x] *Nudging a non-participant, a disabled agent, another company's agent, or itself is
  refused with a sentence naming why, and enqueues nothing.*
  `TestANudgeThatCannotBeAskedIsRefusedAndNamesWhoCan`: eight cases, each asserting nothing
  queued, nothing written and no ledger key.
- [x] *The tool is absent from the schema in a room of one even when `can_nudge` is true, and
  the single-agent tool list is byte-identical to today's.*
  `TestTheNudgeToolAndItsGuidelineReachOnlyATurnOfferedThem` compares the composed prompt, for
  equality, with a registry that has no `nudge_agent`.
- [x] *A nudge whose pinned participant row is removed between enqueue and run does not run.*
  `TestAQuestionWhoseRecipientLeftIsNotRun` (removed; the seat now another agent's) and
  `TestAQuestionIsRunWhileItsSeatHolds` (the default speaker replaced). The model is never
  called.
- [x] *The asker's turn does not block on the target's turn — measured, not assumed.*
  `TestTheAskingTurnDoesNotWaitForTheAnswer` stands in a colleague whose turn runs for a
  second. **`nudge_agent` returned in 2.3 ms** under `-race -v`, with that turn still running.
  Measured at the service; the live measurement is §7f's arm.
- [x] *A nudge from a `KindDocument`-tainted turn produces a target turn that gates
  `propose_action` under `T-H9`.* `TestANudgeFromATurnThatReadADocumentGatesTheColleaguesActions`
  takes the payload this service queued through `receivePeer` and `ActionService.ProposeAction`
  itself.
- [ ] *`make eval` at or above baseline; both rates pasted.* **Owed** — no model key (§7f).
  **Prediction: identical.**
- [x] *The leak guard has a `nudge_agent` fixture.* `TestLeakShapes`, "a leaked nudge", with its
  negative.

**`T-N8`'s items, which moved here:**
- [x] *A stubbed agent that always nudges terminates, and the room says why.*
  `TestARoomThatAlwaysNudgesEndsAndSaysWhy` runs a room of three in which every turn asks both
  colleagues something new. **6 turns ran, 12 questions were asked, and 2 limit lines were
  written** — one each for People and Ops, the two agents cut short. The ledger is real; the
  runner and the queue are stand-ins.
- [x] *Exhaustion produces a visible message naming the unasked question.* The same test, and
  `TestARefusedQuestionIsToldToTheRoomOncePerAgent`.
- [x] *A pass ends a chain and renders as a settle, distinguishable in the transcript from a
  cap being hit.* `TestAColleagueWithNothingToAddSettles`. The two lines carry different
  `room_event` values and different lead words. **Not yet seen on screen** (§7f).
- [ ] *The two-worker gate.* Owed (§7e).

**Beyond the ticket:**
- `TestTheGatesAreCheckedAgainWhenTheToolRuns`, `TestATenantAtZeroReadsTheCreditRefusal`
- `TestTheSameQuestionToTheSameColleagueIsAskedOnce`, `TestAThirdHopIsRefusedOnDepth`
- `TestALedgerThatCannotBeReadRefusesAndStillTellsTheRoom`, `TestAQuestionThatCannotBeWrittenIsNotQueued`
- `TestTheDefaultSpeakerIsPinnedByTheEmptyMembership`, `TestTheToolCanReadTheTurnItRunsIn`
- `TestAPersonsTurnIsNeitherOfferedNorReadAsAPass`, `TestThePassSentinelSurvivesDecorationAndNothingElse`
- `TestARoomLineIsNeverReplayedIntoHistory`
- `TestAColleaguesQuestionIsNotTopicClassified`, `TestAColleaguesQuestionStillMeetsTheInjectionRules`
- `TestNudgingIsOffUnlessTheFormSaysSo`, `TestAnEditThatOmitsTheFlagLeavesIt`, `TestNudgeIsNeverACheckbox`
- three `ClaimNotice` tests, and the tool's six

### 11d. Where the ticket was wrong

- **"Migration: none."** Decision 8 names `agents.can_nudge`, and no ticket had added it. `086`
  does. It has no backfill, deliberately the opposite of `043` and `081`: an agent that cannot
  ask a colleague is the decision, not a gap.
- **"Registered in `registry.go` — so the API's scoping checkboxes get it for free."** Getting
  it for free would have made it an allowlist tool, on for every unrestricted agent — decision
  8 undone by the paragraph above it. The name is registered, as the prompt-line test needs,
  and dropped from the vocabulary. A submitted allowlist naming it is refused.
- **"The participant row id the nudge was planned against."** The default speaker has none
  (`T-N2`: it is `threads.agent_id`, listed without a row). Its seat is pinned by the empty id,
  and a question to it does not run once somebody else holds that seat.
- **"Repo: BE."** Three dashboard changes were needed:
  - **A checkbox for the flag** — without it no admin can turn nudging on.
  - **A handler for `room_event`**, because the question cannot be published as `final`.
  - **Room lines drawn as lines**, because a settle drawn as a bubble cannot be told from an
    answer.
- **"Another company's agent"** cannot be a participant: membership is company-scoped. What
  enforces it is the company-scoped roster read, and a test puts such an agent in the list
  anyway.
- **"Skip the input topic classifier"** is built as exactly that and no wider — see §11b.
- **Silent on grants, because roadmap 12 came after it.** A nudge does not ask whether the
  person may talk to the colleague. `T-Z10` already refuses a send into a conversation holding
  an agent the person may not read, so whoever sent the message could reach every participant.
  What is left is a grant revoked in the seconds between the message and the nudge, inside the
  five-minute window. Written down, not built.

### 11e. Proven failing

Sixteen mutations, applied one at a time. Each file was restored and hash-checked against its
pre-run hash before the gate, and no mutation was counted that only broke the build.

| Mutation | Tests that failed |
| --- | --- |
| The factory offers `nudge_agent` to every turn | the three `offerNudge` factory tests that withhold it |
| Dispatch ignores `can_nudge` | `TestTheGatesAreCheckedAgainWhenTheToolRuns`, the flag case |
| Dispatch ignores the room's size | the same test, the room-of-one case |
| The asked turn keeps the report job | `TestANudgeWritesOneVisibleQuestionAndQueuesOneTurn` |
| The asker's taint is not carried | `TestANudgeFromATurnThatReadADocumentGatesTheColleaguesActions` |
| The notice claim ignored | `TestARefusedQuestionIsToldToTheRoomOncePerAgent`, `TestARoomThatAlwaysNudgesEndsAndSaysWhy` |
| The credit check off | `TestATenantAtZeroReadsTheCreditRefusal` |
| The turn queued though the question was never written | `TestAQuestionThatCannotBeWrittenIsNotQueued` |
| The seat not checked | `TestAQuestionWhoseRecipientLeftIsNotRun` (both), `TestAQuestionIsRunWhileItsSeatHolds` (the replaced speaker) |
| Offered in a room of one | `TestNudgeIsOfferedOnlyWhereBothGatesHold`, `TestATurnIsOfferedNudgeOnlyInARoomOfMoreThanOne` |
| Offered at a hop that can only be refused | `TestNudgeIsOfferedOnlyWhereBothGatesHold` |
| The pass not detected | `TestAColleagueWithNothingToAddSettles` |
| Room lines replayed into history | `TestARoomLineIsNeverReplayedIntoHistory` |
| The topic classifier runs on a colleague's question | both `guardrails` peer tests |
| `nudge_agent` offered as a checkbox | `TestNudgeIsNeverACheckbox` |
| The notice claim not exclusive (`NX` dropped) | `TestARefusalIsAnnouncedOncePerAgentPerMessage` |

The gate's output is in [`delivery-log.md`](delivery-log.md) Phase 3bd.

### 11f. What is owed, and what stays open

- **The paired `make eval`.** Prediction: identical (§7f).
- **The live room, its negative arm and a colleague that passes** (§7f).
- ~~**`086` up, down, up**~~ — **run 2026-09-14 on a scratch stack, as predicted** (live-gate §7i).
- ~~**The room lines and the checkbox on screen**~~ — **run 2026-09-14 in light and grayscale, as
  predicted, plus one unpredicted observation** (§13g). Dark not run.
- **Two workers, and §2c's cost per message** (§7e), no longer waiting on code.
- **Deploy with `T-N11`, and with rooms.** Production runs `1.6.0`, which has neither. A nudge
  in a room without `T-N11` would hand the colleague's history over unfenced (§9d).
- **Open: two live bubbles under one key.** A colleague asked while its own addressed turn is
  still streaming shares a job id and agent id with it (§7f).
- **Open: a question written whose turn was not queued** stays unanswered in the room. The
  asking model is told; the room is not.
- **Open: a grant revoked between a message and its nudge** (§11d).

## 12. `T-N10`, and a public contract that assumed one answer per question

**Built 2026-09-14, unit-gated.** No migration, correctly this time; no prompt or tool changed,
so no `make eval` is owed. The live room, the quickstart run and the query on a real Postgres are
owed (live-gate §7g).

`T-N10` puts rooms on `/v1`. Picked over `T-N7` for three reasons:
- **§5's cut order drops `T-N7` second and `T-N10` last.** The status block said the reverse.
- **Its files are not `T-N6`'s.** `T-N6` is still uncommitted, and `T-N7` would have layered a
  second ticket into the same five files.
- **Whether a hand-off tool is needed at all** is the question `T-N6`'s live arm answers (§7f):
  does the model narrate a hand-off instead of calling the tool?

### 12a. What was built

| Piece | Where |
| --- | --- |
| `POST /v1/threads`: `user_ref`, `agent_id`, `participant_ids` | `handlers/v1_chat.go` `createThread`; `openapi/v1.yaml` `createThread` |
| Every agent checked before the row is written | `app.ChatEnqueuer.OpenAPIThread`, `ErrParticipantNotFound` |
| The ceiling checked against the whole room first | `ThreadParticipantService.Capacity`, read by `createThread` |
| `agent_id` naming a participant addresses it | `ChatEnqueuer.Enqueue`'s `/v1` arm, `roomHolds` |
| The agent each queued turn runs as | `EnqueueResult.AgentIDs` |
| `participants` on a thread read and on create | `threadResponse.Participants`, `participantResponse` |
| `agent_id`, `agent_name`, `room_event` on a message | `messageResponse`, `messageBody` |
| The agent on every frame | `withAgent` |
| Both send doors scoped to the caller's own turn — every frame marked `asked_by` skipped (§12g) | `turnRecord.own`; `forward`; `wait` |
| The answer lookup skips room lines, and a colleague's answer on a sent turn (§12g) | `MessageRepository.LatestAssistantSince(…, domain.AnswerScope)` |
| A colleague's turn marks its events and its answer (§12g) | `ChatEvent.AskedBy`, `metadata.asked_by`; `ChatRunner.publish`, `ThreadService.AppendAssistantMessage` |
| A widget conversation cannot become a room | `ThreadParticipantService.Add`, `ErrRoomNotOnWidget` (409 on the dashboard) |
| `threads.create` | `argentum-node/src/chat.ts`; `argentum-python` `client.py`, `aio.py` |
| Generated | `types.generated.ts`, `types.py`, the Postman collection (`make openapi`) |

**Who answers a `/v1` message in a room:**

| The call | Who answers |
| --- | --- |
| `thread_id`, no `agent_id` | The conversation's own agent, as before |
| `thread_id`, `agent_id` = the conversation's own agent | The same — agreement, as before |
| `thread_id`, `agent_id` = a participant | That participant |
| `thread_id`, `agent_id` = anyone else | `400 agent_mismatch`, as before |
| `thread_id`, `agent_id` off the key's list | `404 agent_not_found`, as before |
| Any of the above with `@Finance` in the text | Unchanged — the text is not read |

### 12b. Decisions worth the words

- **The whole room is checked before the conversation exists.** The dashboard's `POST
  /api/threads` writes the thread and then adds participants, and leaves the thread behind when
  one is refused. On `/v1` that thread would be listed by `GET /v1/threads` as a conversation the
  caller never had, and a retry would add another. So `OpenAPIThread` runs the same pick `POST
  /v1/chat` runs on every id first, and the handler checks the ceiling first. What remains is a
  race — an agent disabled between the check and the insert — and that answer names the
  conversation it left.
- **The conversation's own agent takes a seat whether named or not.** `T-N2`'s cap counts it
  only when the conversation is pinned (§12d). On `/v1` a room of `participant_ids` plus the
  workspace default is refused at the same size as one with `agent_id` named.
- **A repeat is dropped, not refused.** `participant_ids: [Finance, Finance, Ops]` with `agent_id`
  Ops names one agent who is not already there.
- **`Idempotency-Key` is honoured and not required.** The other write doors require one because a
  retry bills twice. A retry here opens an empty conversation that costs nothing.
- **A failed room read refuses an addressed message.** `resolveAddressing` degrades the same
  failure to the default speaker, because an unmatched `@` is text. An explicit `agent_id` is
  not, and answering it as another agent is the failure `T-S3` refused to ship.
- **The stream is filtered, not merged.** Forwarding the colleague's frames with `agent_id` on
  them would be the dashboard's model, one bubble per agent. It would also break every `/v1`
  client written against "`final` is terminal", and that sentence is in the spec. The colleague's
  answer is in the transcript, attributed.
- **The filter asks who asked for a turn, not which agent ran it.** The first cut compared agent
  ids, and §12g is why that was wrong. A frame with no `asked_by` is the caller's, whatever
  agent it names or whether it names one.
- **`room_event` is a string, not an enum.** The spec tells a caller to show an unknown value as a
  line. An enum would make a generated client reject the next kind of line `T-N7` adds.
- **Participants are on the single read and on create, not the list** — `T-N2`'s rule and its
  reason.

### 12c. The acceptance items, quoted back

- [x] *Every new field is in `v1.yaml` and both parity checks are green.* `TestEveryV1RouteIsSpecced`,
  `TestEverySpecEntryIsARoute` and `TestSpecScopeIsTheScopeTheRouterEnforces` pass with
  `POST /v1/threads`. The schema-parity test binds `ThreadParticipant` and `CreateThreadRequest`
  and passes on the changed `Thread`, `Message` and `PendingTurn`. `make openapi`: *"a valid
  OpenAPI 3.1 document (16 paths, 54 schemas)"*.
- [x] *Both SDKs regenerate with no hand edits.* `make openapi` rewrote `types.generated.ts` and
  `types.py`, and `tsc` compiled the node SDK. The two `threads.create` methods are in the
  hand-written clients, which is where every other method lives.
- [x] *A `/v1` caller that ignores participants sees no change.*
  - `TestAnAgentIDOutsideTheRoomIsStillAChangeOfAgent`: a room of one and no room wired both
    refuse as before.
  - `TestAThreadReadWithoutItsRoomIsStillTheThread`.
  - Every pre-existing `v1_chat_test.go` test passes unchanged.
  - The fields added to messages and frames are additive; the spec already told callers to
    ignore unknown ones.
- [x] *`POST /v1/chat` does not parse `@` from message text.* `TestAddressingIsDashboardOnly`
  (`T-N3`'s, the parser). `TestAnAgentIDNamingAParticipantAddressesIt` and
  `TestAnAPIMessageNamingNobodyGoesToTheConversationsOwnAgent` run it through `Enqueue`: the text
  keeps its `@` and routes nobody.
- [x] *A widget session cannot create or address a multi-agent thread.*
  - **Create:** the widget has no participant route, and `TestAWidgetConversationCannotBecomeARoom`
    closes the dashboard route that could.
  - **Address:** the widget arm has no `roomHolds`, so an `agent_id` that differs is
    `ErrAgentChange`, as before. That arm's code is unchanged.
- [ ] *The quickstart still runs unchanged.* `check-examples` passed (13 example files quoted
  exactly), and the quickstart never touches a room. Running it end to end needs the stack and a
  model key — owed (§7g).

### 12d. Where the ticket was wrong

- **"`POST /v1/threads` accepts `participant_ids`."** There was no `POST /v1/threads`. `/v1`
  opened conversations only inside `POST /v1/chat`. The route had to be built, with its scope,
  its idempotency rule, its refusals and an SDK method.
- **"`POST /v1/chat` accepts `agent_id` — explicitly."** It had since `T-S5`, and it meant the
  opposite: the conversation's agent, refused with `agent_mismatch` if it differed. The ticket
  read as if the field were new. In a room it now addresses a participant, and everywhere else
  it keeps its meaning.
- **"A caller reading only `final` still works and learns nothing."** True when the ticket was
  written; false once `T-N6` exists. A colleague asked mid-turn answers on the same channel, under
  the same job id, and a short turn finishes first. Unscoped, the stream would have closed on the
  colleague's `final`. The synchronous door would have returned the colleague's answer as the
  caller's. The transcript check could have returned `→ Finance: …`, written as the asking agent
  while its turn ran.
- **"The widget's UI gets the label" and "a room is not enabled for widget sessions".** Together,
  every bubble carries one name, and `T-N4`'s rule draws none then. Not built. **What was
  actually open:** `ThreadParticipantService.Add` checked only the company, so a member holding a
  widget conversation's id could add agents to it from the dashboard. And `T-N6` offers
  `nudge_agent` in any room of more than one — a website visitor's turn could have asked HR.
- **"The quickstart untouched."** True, and not the gate — `T-A4` wants it *run*, which needs a
  model key.
- **Silent on `room_event`.** `T-N6`'s settle, limit and withdrawn lines are assistant rows. A
  transcript without the field presents "Finance had nothing to add" as Finance's answer.
- **Found in `T-N2`, not changed:** its cap counts the default speaker only when
  `conversation_threads.agent_id` is set, so an unpinned dashboard room holds one more agent than
  `THREAD_MAX_PARTICIPANTS`. `/v1` counts the seat either way.

### 12e. Proven failing

Sixteen mutations, applied one at a time by a script. Each ran only its named tests. Every mutated
file was restored and matched its pre-run hash before the gate. None only broke the build.

| Mutation | Tests that failed |
| --- | --- |
| The stream forwards a colleague's frames | `TestARoomStreamIsTheCallersTurnAndNotAColleagues` |
| The synchronous door reads a colleague's `final` | `TestTheSyncDoorWaitsForTheCallersAgentInARoom` |
| The answer lookup is not bound to the agent | `TestARoomStreamIsTheCallersTurnAndNotAColleagues` |
| `send` does not scope the turn | the stream test, the synchronous test, `TestAPendingTurnInARoomNamesItsAgent` |
| Frames carry no agent | `TestARoomStreamIsTheCallersTurnAndNotAColleagues` |
| `room_event` not published | `TestATranscriptSaysWhoWroteEachMessageAndWhichAreRoomLines` |
| A message's author not published | the same |
| The ceiling not checked before opening | `TestARoomLargerThanTheCeilingIsRefusedBeforeAnythingOpens`, both cases |
| `participant_ids` not deduplicated | `TestCreatingAThreadOpensARoomAndReturnsIt` |
| A thread read carries no room | `TestAThreadReadCarriesItsRoom` |
| The enqueuer never finds a participant | `TestAnAgentIDNamingAParticipantAddressesIt` |
| The `/v1` address not merged into the fan-out | the same |
| Participants not checked before writing | `TestARefusedParticipantLeavesNoConversationBehind`, all four cases |
| A widget conversation may become a room | `TestAWidgetConversationCannotBecomeARoom` |
| The result does not report agents | `TestAnAgentIDNamingAParticipantAddressesIt`, `TestAnAPIMessageNamingNobodyGoesToTheConversationsOwnAgent` |
| The key's default rule skipped when opening | `TestAKeyLimitedToNamedAgentsMustNameOneToOpenAConversation` |

**The two room tests pause for 50 ms between the colleague's `final` and the caller's answer being
written.** Without the pause, a handler that forwarded the colleague's `final` could read the
caller's answer while handling it, and pass by luck. With it, M1 and M2 fail every time. **The SQL
half has no mutation** — its fake honours the bound, and the real query is §7g's arm.

The gate's output is in [`delivery-log.md`](delivery-log.md) Phase 3be.

### 12f. What is owed, and what stays open

- ~~**`LatestAssistantSince` on a real Postgres**~~ — **run 2026-09-14 on a scratch stack: clean, as
  predicted, and extended to a hand-off row** (§13g, live-gate §7i).
- **A room over `/v1`, live, and its stream while a colleague answers** (§7g). **Prediction: one
  `chat:run` per call, and no Finance frame in Ops' stream.**
- **The quickstart run end to end** (§7g). **Prediction: unchanged.**
- **Open: attaching to a room is not scoped.** `GET /v1/threads/{id}/events` delivers the newest
  answer from any agent, and waits on a room line (§7g; `api-chat.md` §6).
- **Open: `T-N2`'s unpinned cap** (§12d).
- **Open: no `/v1` route adds or removes a participant after creation.** The ticket asked only for
  creation. The dashboard's routes act on an API conversation, company-scoped.
- **Not built, by the ticket's own contradiction: the widget label** (§12d).

### 12g. Risk 2 — the stream was scoped by the wrong thing (found after the gate, fixed the same day)

**The first cut scoped a sent turn's stream by the agent it was sent to.** Filed as a risk in the
report, after `9f656b9` was pushed and before anything was deployed — production runs `1.6.0`, so no
caller met it. It was wrong in two ways.
- **A turn whose agent is deleted before it runs runs as the company default**
  (`ChatRunner.resolveAgent`, `T-S2`). Every frame and the saved answer carry the default's id,
  so nothing matched. Proven failing before the fix, by
  `TestATurnWhoseAgentWasDeletedStillEndsWithItsAnswer`:
  - streamed: *"the handler did not finish within 5s"*;
  - synchronous: *"status = 504, want 200"*, with `"agent_id":"ag-gone"` in flight, for a turn
    that had answered.
- **Found while fixing it: an agent can be asked back by the colleague it asked.** Ops asks
  Finance, and Finance asks Ops. The second Ops turn carries the caller's own agent id, so its
  `final` matched and could end the caller's stream with the wrong answer. `T-N8`'s depth of 2
  admits exactly this hop.

**What decides it now is who asked, not which agent.**
- **A colleague's turn marks itself.** `Run` installs the mark from `p.Peer` before anything
  publishes. `ChatRunner.publish` stamps it as `ChatEvent.AskedBy`.
  `ThreadService.AppendAssistantMessage` writes it as `metadata.asked_by`, onto a copy of the
  caller's map. Both are read off the context, as the agent is, so every row and event a turn
  writes agrees by construction.
- **`LatestAssistantSince` takes a `domain.AnswerScope`**, not an agent id. `OwnAnswer` also
  leaves out a row marked `asked_by`; `AnyAnswer` leaves out room lines only.
- **`turnRecord.own` marks a sent turn.** It is not stored in the idempotency record: send and its
  replay are the only producers of a sent turn, and both set it. Both send doors skip every
  frame marked `asked_by` and look up `OwnAnswer`. Attaching stays `AnyAnswer` and unscoped,
  as §12f records.
- **`in_flight.agent_id` now reads "the agent this turn was sent to"**, which it always was.

**Not published on `/v1`:** `asked_by` is on the internal event and the stored row, not on a
`/v1` message or frame. The send doors never forward such a frame. The transcript attributes
the colleague's answer by `agent_name`, beside the `nudge` line that asked for it.

**Generated:** `packages/api-types` (`events.ts` `asked_by`, `domain.ts` `AnswerScope`), the SDK
types and the Postman collection, from the reworded spec.

**Proven failing.** The deleted-agent test failed on the first cut, as quoted above. Then ten
mutations, one at a time by a script, after a pre-check that the new tests pass unmutated. Each
ran only its named tests. None only broke the build, and all three mutated files matched their
pre-run hashes afterwards.

| Mutation | Tests that failed |
| --- | --- |
| The stream forwards a colleague's turn | `TestARoomStreamIsTheCallersTurnAndNotAColleagues` |
| The synchronous door reads a colleague's turn | `TestTheSyncDoorWaitsForTheCallersAgentInARoom` |
| A send is not marked as the caller's own | both of the above |
| A replay is not scoped | `TestARetriedSendInARoomIsScopedAsTheOriginal` |
| A sent turn looks up any answer | `TestARoomStreamIsTheCallersTurnAndNotAColleagues` |
| **The first cut's agent comparison, restored** | both room tests, and `TestATurnWhoseAgentWasDeletedStillEndsWithItsAnswer`, streamed and synchronous |
| Events not stamped | `TestEveryEventOfAColleaguesTurnSaysWhoAsked` |
| `Run` does not mark a colleague's turn | `TestAColleaguesAnswerRecordsWhoAskedForIt` |
| The saved answer not marked | that test, and `TestEveryEventOfAColleaguesTurnSaysWhoAsked` |
| The caller's metadata map written into | `TestMarkingAColleaguesAnswerDoesNotWriteIntoTheCallersMap` |

The room tests now publish Finance asking Ops back, carrying Ops' own id. That is why restoring
the agent comparison fails them, and not only the deleted-agent test. **The SQL has no
mutation**: its fake honours both bounds, and the real query is §7g's arm. The gate's output is
in [`delivery-log.md`](delivery-log.md) Phase 3bf.

**Also owed:** §7g's query arm now seeds a row marked `asked_by`, and its stream arm asks Ops
back. A deleted agent is proven by unit only — deleting one between a send and its run is not a
live arm anybody can time.

## 13. `T-N7`, and a question that was not the asker's

**Built 2026-09-14, unit-gated.** No migration: `086`'s `can_nudge` gates it. A catalog line and a
guideline, so the paired `make eval` is owed, as are the live arms (live-gate §7h).

A nudge says *I am answering, and I need one fact from you*. A hand-off says *this is not mine, you
take it*. Ops, asked what was written off for SKU 4471 last quarter, hands the question to Finance,
whose ledger holds write-offs:
- the room shows Ops' reply: "Passed to Finance: Write-offs are booked in Finance's ledger.";
- Finance answers the person's own words, in a turn of its own;
- nothing Ops' model writes after handing the question over is published.

### 13a. What was built

| Piece | Where |
| --- | --- |
| `hand_off_to_agent(agent, reason)`, and the `HandOffer` it calls | `internal/tools/hand_off_agent.go` |
| `GatedByFlag` claims it too: never a checkbox, offered by `can_nudge` and the room | `internal/tools/nudge_agent.go` |
| `NudgeService.HandOff`, sharing `gate`, `colleague` and `credits` with `Nudge` | `internal/app/nudge_service.go` |
| `PeerOrigin.HandOff{Reason}` | `internal/queue/peer.go` |
| `AgentSpec.HandOff`, `offersHandOff`, `holdsPersonsQuestion`; `offerRoomTools` in the factory (was `offerNudge`) | `app/chat_runner.go`, `app/chat_runner_room.go`, `bootstrap/stack.go` |
| The turn ends on its hand-off: `handedOver`, and `finish` split so `deliver` can close a turn on a message already written | the same two `app` files |
| The handed-off turn's input: a framing sentence, the reason fenced, the person's words. No pass offered | `peerMessage`, `withPeerFraming` (was `withPassOption`) |
| `metadata.handed_off_to`, kept out of model history | `HandedOffToKey`, `hydrateMemory` |
| A catalog line, and a guideline that renders only for a turn holding the tool | `bootstrap/system_prompt.go` |
| The checkbox copy says what else the flag allows | `settings/agents-tab.tsx` |

**Where the order of checks differs from §11a's:**

| Check | Refusal |
| --- | --- |
| `nudge_agent`'s gate, then: the turn holds the person's question | `not_available`, pointing a colleague's question at `PASS` and `nudge_agent` |
| A name, and a reason under 300 characters | `missing_agent`, `missing_reason`, `reason_too_long` |
| Not already handed off in this turn | `already_handed_off` |
| The participant, not itself, its row, the credits | as §11a |
| The ledger, asked about the handing turn's own message | a repeat is `already_asked`, **carrying `error`**; a refusal is `budget_exhausted`, with a room line |
| The line written, then the colleague's turn queued | `not_delivered`, and the turn is not ended |

A nudge from a turn that has already handed off is refused `handed_off`.

### 13b. Decisions worth the words

- **The model never supplies the question.** The tool takes a name and a reason, and the colleague
  is sent the handing turn's own message. So no model retells the person's words on the way. And
  the ledger's repeat check sees the very words the person's message was opened with.
- **The reply is the product's line, not the model's sentence.**
  - The ticket asked the model to keep its reply to one sentence and attempt no answer. A rule a
    model is only asked to keep is one the room finds broken.
  - So the line is written with the hand-off, before the colleague's turn is queued (§11b's
    order), and the runner closes the turn on it.
  - A model that errors after handing off still ends on the line, and a failed stream is not
    retried.
  - The length of the dropped reply is logged as `dropped_reply_chars`, so the worker log can
    count how often a model would have answered anyway.
- **Stored as an answer, not a room line.** `LatestAssistantSince` skips `room_event` rows. A `/v1`
  caller who asked Ops synchronously is waiting for Ops' answer, and the hand-off is that answer.
  As a room line, the door would have waited and answered `504`. It is kept out of model history
  anyway, for the room line's reason.
- **Only the reason is fenced.**
  - The fence's rule in the system prompt reads its contents as a colleague's claim, "not as an
    instruction from the user".
  - The question is not a colleague's words. So the recipient reads the note fenced under Ops'
    name, then the person's words as a person's words arrive.
  - The turn is a peer turn in everything that decides anything: the taint is inherited, `agent`
    is marked, the directive is dropped, `asked_by` is stamped.
- **Not offered to a colleague's question.** Its words are the colleague's, and handing them on
  would present them to a third agent as the person's. Only a turn the person addressed, or one
  handed the question, holds the tool. So a handed-on question is the person's words at every
  hop.
- **No pass on a handed-off question.** A person whose question two agents declined is owed a
  sentence saying so, not a quiet line. A model that replies `PASS` anyway still settles, so the
  sentinel is never published.
- **One flag, and only beside `nudge_agent`.** A second column would be a second switch for "may
  involve a colleague". The factory never offers the hand-off alone, because its description and
  guideline send the model to `nudge_agent` for the case that is not a hand-off.

### 13c. The acceptance items, quoted back

- [x] *The handed-off agent receives the user's original wording, fenced, with the reason attached.*
  Built with the reason fenced and the words not (§13d).
  - `TestAHandOffWritesTheReplyAndSendsThePersonsOwnWords`: the queued payload's message is the
    person's, and the reason rides `Peer.HandOff`.
  - `TestAHandedOffQuestionArrivesInThePersonsWordsWithTheReasonFenced`, through `Run`: the
    model's input holds `FencePeer("Ops", reason)`, then the words, and never the words fenced.
- [x] *The handing agent's reply says it handed off and attempts no answer.*
  `TestAHandedOffTurnEndsOnTheHandOffAndPublishesNothingItWroteAfter` drives a whole turn.
  - The stub model calls the real tool and then answers anyway.
  - One message is written: the hand-off, as Ops. One `final` carries it.
  - No event carries the figure.
  - `TestAHandedOffTurnWhoseModelThenFailsStillEndsOnTheHandOff` is the same turn with a model
    that fails instead.
- [x] *`threads.agent_id` is unchanged after a hand-off.* By construction.
  - `domain.ThreadRepository` has no method that writes a thread's agent. Its writers are
    `Create`, `UpdateSummary`, `Touch`, `Archive` and `Delete`.
  - `NudgeService` holds no thread repository at all: a read-only roster, a read-only room, an
    append-only note writer, and the queue.
- [x] *A hand-off counts against the conversation budget.*
  `TestAHandOffCountsAgainstTheConversationBudget`: the ledger reads 2 turns, and the next ask
  from that fan-out is refused.
- [x] *A hand-off back to the original agent is refused by the depth counter, not by a special
  case.* Refused by the ledger's repeat check, one hop before the depth counter could refuse it
  (§13d).
  - `TestAHandOffBackIsRefusedByTheLedger`: `already_asked`, nothing queued, no second line.
  - `TestAThirdHopHandOffIsRefusedOnDepth`: a third hop is refused on depth, with a room line
    quoting the question.
- [ ] *`make eval` at or above baseline.* **Owed**, with no model key here (§7h). **Prediction:
  identical.**

**Beyond the ticket:**
- `TestAHandOffThatCannotBeMadeIsRefusedAndSendsNothing` (six cases)
- `TestTheHandOffsGatesAreCheckedWhenItRuns`, including a colleague's question
- `TestATurnHandsItsQuestionOnOnce`: a second hand-off, and a nudge after one
- `TestAHandOffThatWasNotQueuedLeavesTheQuestionWithTheTurn`, `TestATenantAtZeroCannotHandOff`
- `TestOnlyATurnHoldingThePersonsQuestionIsOfferedTheHandOff`, `TestAHandOffIsNeverReplayedIntoHistory`
- `TestTheHandOffIsOfferedOnlyBesideNudge`
- the tool's six, including `TestTheModelIsNeverAskedForTheQuestion`
- extended: the prompt-composition test, `TestNudgeIsNeverACheckbox`, the flag-gated tool list,
  the peer carrier's field list, and a leak-guard fixture with its negative

### 13d. Where the ticket was wrong

- **"A hand-off back to the original agent is refused by the depth counter."** At the default
  `CONVERSATION_MAX_NUDGE_DEPTH` of 2, Ops → Finance is hop 1 and Finance → Ops is hop 2, which
  the counter admits. The ledger refuses it anyway, as a repeat: `Open` recorded Ops as asked the
  person's cleaned message, and a hand-off sends exactly that message. Still `T-N8`'s ledger,
  still no special case.
  - The ledger cannot tell "Finance handed it here" from "the person addressed Finance too". So the
    refusal's sentence covers both.
  - It carries `error`, unlike a nudge's repeat. A repeated nudge is a question already on its way;
    a repeated hand-off handed nothing over.
- **"The asker's own reply is one sentence … It must not also attempt an answer"** was a request to
  the model. Built as a rule (§13b).
- **"Receives the user's original wording, fenced"** would have told the recipient to read the
  person's request as a colleague's claim. Only the reason is fenced (§13b).
- **Silent on a colleague's question**, and on where the "original wording" comes from at a later
  hop. Not offered there, which is what keeps the words the person's at every hop.
- **"Repo: BE"** — plus one line of dashboard copy. **"Migration: none"** was right this time.

### 13e. Proven failing

Seventeen mutations, applied one at a time by a script (`/tmp/tn7_mutate.py`), after a pre-check
that every named test passes unmutated. Each ran only its named tests. None only broke the build,
and every mutated file matched its pre-run hash afterwards. One mutation's target string first
matched twice — `Nudge` and `HandOff` log the same tail — so the script refused to apply it. It
was rerun alone with a string that matches once.

| Mutation | Tests that failed |
| --- | --- |
| The question sent is the model's reason, not the person's words | `TestAHandOffWritesTheReplyAndSendsThePersonsOwnWords`, `TestAHandOffBackIsRefusedByTheLedger` |
| A colleague's question may be handed on | `TestTheHandOffsGatesAreCheckedWhenItRuns`, `TestOnlyATurnHoldingThePersonsQuestionIsOfferedTheHandOff` (the colleague's-question cases) |
| The runner does not end a turn on its hand-off | `TestAHandedOffTurnEndsOnTheHandOffAndPublishesNothingItWroteAfter`, `TestAHandedOffTurnWhoseModelThenFailsStillEndsOnTheHandOff` |
| A model error after a hand-off is handled as a failed turn | `TestAHandedOffTurnWhoseModelThenFailsStillEndsOnTheHandOff` |
| A hand-off whose turn was not queued still ends the turn | `TestAHandOffThatWasNotQueuedLeavesTheQuestionWithTheTurn` |
| A second hand-off in one turn is not refused | `TestATurnHandsItsQuestionOnOnce` |
| A nudge after a hand-off is not refused | `TestATurnHandsItsQuestionOnOnce` |
| The hand-off is stored as a room line | `TestAHandOffWritesTheReplyAndSendsThePersonsOwnWords`, `TestAHandedOffTurnEndsOnTheHandOffAndPublishesNothingItWroteAfter` |
| The hand-off is replayed into history | `TestAHandOffIsNeverReplayedIntoHistory` |
| The person's words are fenced with the reason | `TestAHandedOffQuestionArrivesInThePersonsWordsWithTheReasonFenced` |
| A handed-off question is framed as a colleague's, with the pass | the same test |
| The factory offers the hand-off without `nudge_agent` | `TestTheHandOffIsOfferedOnlyBesideNudge` |
| The hand-off is an allowlist tool | `TestTheRoomToolsAreTheOnlyOnesTheAllowlistDoesNotDecide`, `TestNudgeIsNeverACheckbox` |
| The handing turn's taint is not carried | `TestAHandOffWritesTheReplyAndSendsThePersonsOwnWords` |
| A repeated hand-off is not refused as a repeat | `TestAHandOffBackIsRefusedByTheLedger` |
| The runner offers the hand-off whatever `nudge_agent`'s gates say | `TestOnlyATurnHoldingThePersonsQuestionIsOfferedTheHandOff` (the last-hop case) |
| The hand-off guideline renders for every turn | `TestTheNudgeToolAndItsGuidelineReachOnlyATurnOfferedThem` |

**Not mutated:** the wiring in `bootstrap.New` (§13f), and the depth refusal, which is `T-N8`'s
code and already mutated there. The gate's output is in [`delivery-log.md`](delivery-log.md)
Phase 3bg.

### 13f. What is owed, and what stays open

- **The paired `make eval`.** **Prediction: identical** (§7h).
- **A hand-off live, its negative, and a hand-back** (§7h). **Prediction:** `hand_off_to_agent`
  for the write-off question, nothing for the reorder question, and a nudge for the goods-in one.
- **A `/v1` caller whose agent hands off.** **Prediction: `200` on the hand-off line** (§7h).
- ~~**The hand-off bubble and the checkbox on screen**~~ — **run 2026-09-14, as predicted** (§13g).
- **Whether the tool is needed at all.** Phase 3be noted that `T-N6`'s live arm answers it, and
  that arm has not run. The tool is additive and behind the same flag. If the arm shows `nudge_agent`
  covers the case, cutting it is two registry lines and a prompt line.
- **Open: the line is English.** "Passed to Finance:" is the product's, like every room line
  (`settle`, `withdrawn`, `unasked`), whatever language the person wrote in. The reason is the
  model's, and the tool asks for it in the person's language.
- **Open: a line written and a turn not queued.** On a queue failure "Passed to Finance: …" stays in
  the room, and Ops is told and answers the question itself. This is §11f's open item, in hand-off
  form.
- **Open: tool calls after a hand-off.** The result tells the model to stop, and a nudge is refused.
  Data tools still run until the per-turn ceiling, and their output is dropped with the reply.
- **Not built: a `/v1` field for the hand-off.** A caller reads the sentence. `metadata.handed_off_to`
  is on the stored row, and not in `v1.yaml`'s message schema.
- **The wiring is not unit-proven.** `bootstrap.New` passes the service as `RegistryDeps.HandOffs`.
  Only the live arm shows the worker's tool reaching it.

### 13g. The free arms, run on a scratch stack (2026-09-14)

Everything in §7f–§7h that needs neither a model nor a worker. Run on an embedded Postgres and
miniredis, not production; the full record is live-gate §7i.

- **`086` up, down, up**, and the flag carried through an edit: **as predicted.**
- **`LatestAssistantSince` on a real Postgres**, both scopes: **as predicted.**
  - Extended for `T-N7` with a hand-off row. `OwnAnswer` returns the hand-off, and keeps returning
    it after Finance answers.
  - That is the database half of the `/v1` claim in §13b: a hand-off is found as the caller's
    answer.
- **The room on screen**, in light and grayscale: **as predicted.**
  - The three room lines are centred and unrated.
  - The limit and the withdrawn line lead with their words; the settle does not.
  - The hand-off is Ops' bubble, and the checkbox copy fits.

![A room's transcript: a nudge, a settle, a hand-off, a limit and a withdrawn question](assets/room-lines-and-hand-off.png)

**One thing nobody predicted: a nudge and a hand-off can be rated.** Both are stored as the
asking agent's assistant rows and drawn by `MessageBubble`. So both carry the copy and
thumbs-up/down controls of an answer.
- A thumbs-down on "→ Finance: Was a goods-in posted…" rates a question an agent asked, not an
  answer a person got. It lands in the same feedback as a real answer's rating.
- The hand-off's rating is at least a rating of Ops' reply.
- **Open, not fixed.** Withholding the controls on `room_event: nudge` would be a few lines in
  `MessageBubble`. Whether a hand-off should keep them is a product question.

**Kept for next time:** `internal/adapters/postgres/scratch_rooms_test.go`, behind the `scratch`
build tag, holds both database arms, with the command in its header.
