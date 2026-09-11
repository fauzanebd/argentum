# Multi-agent conversations — what is built, and what it found

The plan is
[`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)
(`T-N1`→`T-N10`, ~18.5d); the reference it was checked against is
[`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md).
**One ticket of ten is built.** This file records what landed, what it changed
that the ticket did not anticipate, and what is owed.

| Ticket | Status |
| --- | --- |
| `T-N1` Every assistant message says which agent wrote it | **built 2026-09-11, unit-gated. Migration `077` written, not applied — §4** |
| `T-N2` A conversation can hold more than one agent | **built 2026-09-11, unit-gated. Migration `078` written, not applied — §4** |
| `T-N3` Addressing — `@agent` decides who answers | **built 2026-09-11, unit-gated. One acceptance item struck as unachievable — §5** |
| `T-N4` The dashboard room | **built 2026-09-11. Visual gate run; three findings — §6** |
| `T-N5`→`T-N10` | Not built, not scheduled |

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
