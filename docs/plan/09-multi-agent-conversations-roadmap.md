# Multi-agent conversations roadmap — several agents in one room, and one of them can ask another

Written 2026-09-11 against `main` @ `1d5053c`. Ten tickets, **~18.5 days —
~14.0 backend, ~3.0 frontend, ~1.5 API/SDK** — across five tracks. Ticket ids
are `T-N1` → `T-N10`.

**Why `N`.** `grep -rhoE "T-[A-Z]" docs/` finds `A B D G H K M P Q R S U V`.
That leaves more than one free letter, and `C` and `E` are the tempting ones —
*conversation*, *ensemble*. Both are taken by something else in this
repository's vocabulary: findings are cited bare, as `C-1` and `C-2`
([`../coverage/environment-notes.md`](../coverage/environment-notes.md)) and
`E-5` (`T-16`'s), so `T-C1` and `C-1` would sit one character apart in the same
sentence. `N` collides with nothing.

The reference this plan was checked against is
[`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md):
Hermes Agent ships group rooms and bot-to-bot DMs today, and that document reads
its source at a pinned tag. §2d is what came back, and it changed four things
here — see the revision note below.

Every repository claim below was read at `1d5053c` and carries its file and
line. **No number in this document was measured by running a model**, and §2c
says so explicitly rather than leaving it to be discovered — the one figure this
plan would most like to have is what a room actually costs per user message, and
it is owed, not estimated.

> **Status, 2026-09-14, last: `T-N10` is built and unit-gated — `/v1` sees rooms.** No
> migration, correctly this time; no prompt change, so no eval. Record:
> [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §12.
>
> - **`POST /v1/threads`** opens a conversation holding named agents, all checked before
>   anything is written.
> - **`agent_id` on `POST /v1/chat` names who answers** in a room. An `@` in the text is text.
> - **`participants`** on a thread read. **`agent_id`, `agent_name` and `room_event`** on
>   messages, and the agent on every frame.
> - **The stream and the synchronous door are the asked agent's turn**, even when a colleague
>   answers first in the same thread.
> - **A widget conversation cannot become a room**, and both SDKs have `threads.create`.
>
> **Where the ticket was wrong** (§12d):
> - **"`POST /v1/threads` accepts `participant_ids`"** — there was no `POST /v1/threads`.
> - **"`POST /v1/chat` accepts `agent_id`"** — it had since `T-S5`, meaning the opposite: it
>   pins a conversation and refuses a different agent.
> - **"A caller reading only `final` still works"** stopped being true with `T-N6`. A
>   colleague's `final` arrives on the same channel, often first.
> - **"The widget gets the label"** contradicts "a room is not enabled for widget sessions".
>   Not built. What was open was a dashboard member turning a widget conversation into a room.
>
> **Also corrected:** the block below said the cut order drops `T-N9`/`T-N10` first. §5 drops
> `T-N9`, then `T-N7`, then `T-N10`. **Owed** (live-gate §7g): the query on a real Postgres, the
> live room, and the quickstart run. **Left on this track:** `T-N7`, and `T-N9`, which the
> cut order drops first.

> **Status, 2026-09-14, later still: `T-N6` is built and unit-gated — one agent in a room
> can ask another.** Migration `086`, where the ticket said none. Record:
> [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §11.
>
> - **`nudge_agent`** posts the question into the room as the asker's message, queues one
>   ordinary `chat:run` for the colleague, and returns at once — 2 ms, measured while the
>   colleague's turn was still running.
> - **It is offered only** to an agent with `can_nudge`, in a room of more than one, at a hop
>   the ledger could still admit, and it re-checks all three when it runs. Every other
>   turn's prompt is byte-identical.
> - **It refuses, and names who can be asked:** a non-participant, itself, a disabled agent,
>   another company's. The credit check runs before the ledger. A budget refusal is told to
>   the room once per agent.
> - **A colleague whose seat changed is not run**, and the room says so. **A colleague with
>   nothing to add replies `PASS`**, and the room shows a settle drawn differently from a
>   limit.
>
> **Where the ticket was wrong** (§11d):
> - **"Migration: none."** `agents.can_nudge` did not exist. `086` adds it, off, with no
>   backfill.
> - **"The API's scoping checkboxes get it for free"** would have made it an allowlist tool,
>   where empty means every tool. It is kept out of the vocabulary instead.
> - **The default speaker has no participant row to pin.** Its seat is pinned by the empty id.
> - **The visible question cannot be published as `final`**, which would close the asker's
>   own bubble. A `room_event` event and three dashboard changes were needed; the ticket said
>   BE only.
>
> **Owed** (live-gate §7f): the paired `make eval` (predicted identical), the live room arm
> and its negative, `086`'s round-trip, the two-worker arm, and a screenshot of the room
> lines. **Left on this track:** `T-N7`, whose dependency is now met, and `T-N9`/`T-N10`,
> which the cut order drops first.

> **Status, 2026-09-14, later: `T-N8`'s ledger is built and unit-gated — the
> conversation budget, with nothing to refuse until `T-N6`.** No migration, no prompt
> change. Record: [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §10.
>
> - **Every message's turns are counted** in Redis before the first is queued. A person's
>   own `@all` is never refused, even past the ceiling.
> - **An ask** — `T-N6`'s nudge, `T-N7`'s hand-off — is refused past 6 turns, at a third
>   hop, after 5 minutes, or when Redis cannot be read.
> - **The same agent asked the same question twice is queued once.**
> - **The three numbers are placeholders**, labelled so in the code. §2c's arm replaces them.
>
> **Where the ticket was wrong** (§10d):
> - Its watermark could never fire, and was built as that repeat check.
> - Its pass outcome, room notice and two-worker gate have no caller before `T-N6`, and
>   moved there.
> - It asked for per-company ceilings and put them out of scope. Deployment defaults were
>   built.
>
> **Next on this track: `T-N6`**, whose dependencies are now all met. It inherits `T-N8`'s
> room notice and pass — see its *Do*.

> **Status, 2026-09-14: `T-N11` is built and unit-gated.** The owner answered §8b's
> question: a colleague's earlier turns do not pass their document taint on. No migration.
> Record: [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §9.
>
> **What changed.** An agent in a room now reads a colleague's turn as a colleague's:
> - **It reads** the person's question, and the reply fenced under the colleague's name,
>   with the turn recording `agent`.
> - **It does not read** the colleague's tool calls, raw results or composed prompt.
> - **The prior-work block** leaves out the colleague's queries.
>
> A single agent's history, and an unscoped turn's, are byte-identical to before.
>
> **The ticket, written the day before, was short by two leaks.** The shared buffer also
> held every agent's tool results — rows from sources the reader may not reach — and every
> agent's composed prompt, source catalog included (§9a).
>
> **One condition on deploying it: ship `T-N11` no later than rooms.** Production runs
> `1.6.0`, which has no rooms, and the rule for unstamped history assumes that (§9d).
> **Owed:** the paired `make eval`, predicted identical, and §7d's before-and-after room
> arm. **Next on this track: `T-N8`**, which `T-N6` also waits on.

> **Status, 2026-09-13: `T-N5` is built and unit-gated — the trust boundary that has to
> exist before any agent talks to any agent.** No migration. Record:
> [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §8.
>
> When a payload says another agent wrote a turn's message, that turn:
> - inherits the author's taint, so a document the author read gates its actions under
>   `T-H9`;
> - records `agent` under the author's roster name;
> - reads the words fenced, in its user turn;
> - drops any directive.
>
> A payload stuffed with scope-shaped fields leaves the recipient's scope byte-identical.
> Nothing writes such a payload until `T-N6`. **Owed:** the paired `make eval` (no model
> key here) and the real-nudge arm, both in
> [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7d.
>
> **The ticket said the laundering path "exists the moment `T-N6` ships". Half of it
> already exists, in `v1.7.0`.** Every agent in a room shares one conversation memory. So
> `@Ops`, after Finance read a supplier PDF, puts Finance's reply in Ops' request as Ops'
> *own* unfenced `assistant` message, with no taint (§8b). Filed below as `T-N11`,
> **recommended before `T-N6`**. Its taint half needs an owner's decision.
>
> **Two corrections to `T-N6`.** Its `NudgeFromAgentID` could not carry a taint, so the
> carrier is now `ChatRunPayload.Peer`. And skipping the input classifier for peer text
> moved to `T-N6`, keyed on the payload rather than on the fence markers — a person can
> type those.

> **Status, 2026-09-11: nothing here is built, and none of it is scheduled.**
> This is a plan, not a track in flight. Committed work has been at 0.0 days
> since [`00-sprint-overview.md`](00-sprint-overview.md) §9e (2026-08-10). The
> open tickets on the board are `T-H4` step 2, `T-H6`, `T-H11`, `T-H12` and
> `T-H14` on the security roadmap, and `T-G7`→`T-G10` on the carousel roadmap
> are planned-not-scheduled. Whether this displaces any of them is the owner's
> call; §8 states the case against doing it now without making that call.
>
> **One thing here is not optional if any of this ships: `T-N5`.** An agent's
> words arriving in another agent's context is instruction authored by a model
> at runtime, and it can launder a document taint into what looks like a
> colleague's request. `T-K2` established the rule for skills and `T-H8`
> established it for everything else a turn reads; §3 decision 5 is the same
> argument, and it is a ticket ahead of the feature rather than a paragraph
> inside one.
>
> **Revised 2026-09-11, same day, after Hermes Agent's implementation was read
> at `v2026.9.7`** ([`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md)).
> Four of this document's locked decisions turn out to be Hermes' decisions,
> reached independently — which is the strongest evidence a plan gets, and §2d
> says where the evidence stops. Four things changed as a result: decision 8
> gained a second gate, decision 9 gained two dimensions and lost its
> compile-time literals, decision 3 gained `@all`, and `T-N5` gained an explicit
> rule that a nudge never widens the recipient's scope. **The ticket count and
> the 18.5-day total are unchanged.**
>
> **And one thing is cheap, useful alone, and a prerequisite for all of it:
> `T-N1`.** Today an assistant message does not record which agent wrote it —
> `agent_actions` and `usage_events` both gained an `agent_id` in
> `031_thread_agent.up.sql`, and `messages` did not. That is a gap worth closing
> whether or not the rest of this roadmap is ever built.

---

## 1. The request, and the four things it is not

> *"A user can talk with multiple agents like a group chat, and an agent can
> nudge or talk to another agent."*

Two capabilities, and they are separable — which matters, because the first is
8.0 days and the second is 6.5 on top of it:

1. **A room.** One conversation, several of the tenant's roster agents in it,
   the user addressing whichever one they mean. Ops and Finance on the same
   question, in the same pane, reading the same transcript.
2. **A nudge.** An agent in that room asking a named participant something, or
   handing the question over because it is not theirs.

Four things one word away from this, each already decided somewhere in the
repository and none of them delivered here:

| | What it is | Where it was decided | Why it is not this |
| --- | --- | --- | --- |
| **Planner + specialists** | An internal planner decomposing a question across specialist agents *we* write | [`backlog.md`](backlog.md) "Multi-agent architecture", and [`01-tickets.md`](01-tickets.md) §"Why this track exists" | Invisible to the tenant, gated on eval regressions. Its trigger — *eval cases failing because one agent does two incompatible jobs* — will never fire for a customer asking for a group chat. A planner would sit **inside** one participant of a room |
| **The roster** (`T-S1`→`T-S5`) | The customer creates named agents; the user picks one per conversation | `01-tickets.md` §"Sprint 2 — the agent roster" | Delivered, and it is what this track builds on. Its unit is **one thread, one agent**: `conversation_threads.agent_id` (`031_thread_agent.up.sql:17`), resolved once per turn at `chat_runner.go:1028`, and the picker deliberately disappears after the first message (`dashboard/src/features/chat/agent-picker.tsx:15`) |
| **Channel bindings** (`T-S4`) | An admin points a Discord channel or a Lark chat at one agent | `domain/agent_binding.go:41` | Address → **one** agent. A Slack room where three agents answer is a different cardinality, and it is `T-N9` |
| **Per-agent user grants** | Restrict which people may open which agent | [`backlog.md`](backlog.md) "Per-agent user grants", deferred 2026-07-29 | Still deferred. But a room is where its absence becomes **visible** rather than merely true — §7 |

**The gap this track closes is cardinality, in two places at once.** A
conversation holds one agent, and an assistant message records none. Everything
below follows from changing those two facts and nothing else.

---

## 2. What the codebase already gives this track, and the one thing it refuses

### 2a. Seven things that need no change at all

This is the reason the estimate is 18.5 days and not 40. A room is N ordinary
turns, and an ordinary turn is a solved problem here:

| Mechanism | Where | Why it survives a room unchanged |
| --- | --- | --- |
| **One turn, one agent** | `chat_runner.go:1028` `resolveAgent` | It reads `ChatRunPayload.AgentID` and falls back to the company default. A room sends N payloads with N different ids. The runner never learns what a room is |
| **Per-turn scope** | `agentscope.WithScope(ctx, scopeOf(agentRow))`, `chat_runner.go:783` | Sources, tools, MCP servers and skills are resolved per turn from the agent row. Two agents in one room get two scopes because they are two turns |
| **Audit attribution** | `agent_actions.agent_id`, `031_thread_agent.up.sql:24` | Already per-agent, already written by the tool decorator (`T-05`). A room needs no new audit surface |
| **Cost attribution** | `usage_events.agent_id` + `idx_usage_events_company_agent`, `031_thread_agent.up.sql:30-34` | *"What does the Finance agent cost us"* is already answerable. A room makes the question more interesting and adds no schema |
| **Credit enforcement** | `ChatEnqueuer.WithBudget`, `chat_enqueuer.go:76` | Checked **before** each enqueue. N addressed agents means N checks; a tenant at zero is refused on the second agent exactly as on the first |
| **Streaming** | `ChatEvent`, `app/event_bus.go:40-59`, keyed on `ThreadID` | Every event already carries `JobID` (the user message id). Two concurrent turns on one thread publish on one channel and are told apart by `JobID` — plus `T-N1`'s agent id |
| **The queue** | `queue.TypeChatRun`, `queue/tasks.go:36` | No new task type. A room is more `chat:run`s, which is the single most load-bearing decision in §3 |

### 2b. The three things that do have to change

1. **`messages` has no agent column.** The table is `002_threading.up.sql:26-37`
   and the struct is `domain/message.go:19`; neither has ever carried one. In a
   room of one this is invisible. In a room of three it breaks two things at
   once: the transcript a human reads, and the transcript the *next turn* reads
   — `hydrateMemory` (`chat_runner.go:2156`) replays prior messages into the
   model's context, and a replay in which three personas are flattened into one
   undifferentiated `assistant` voice is worse than no history. **`T-N1`.**

2. **A thread holds exactly one agent id.** `conversation_threads.agent_id`,
   `ON DELETE SET NULL`, added by `031`. It is the right column and it keeps its
   job; what it cannot express is *membership*. **`T-N2`.**

3. **There is no addressing.** Nothing in `ChatInput` (`chat_enqueuer.go:98-133`)
   or `ChatRunPayload` (`queue/tasks.go:184`) says who a message is for,
   because until now the thread said it. **`T-N3`.**

### 2c. The arithmetic, and the one number nobody has measured

`agentbudget.Default()` is 8 iterations and 12 tool calls
(`internal/agentbudget/budget.go:87-90`), enforced per turn. **Nothing bounds a
conversation.** With nudging enabled and no ceiling above the turn, one user
message can produce an unbounded number of turns: A nudges B, B nudges A, and
each of those is a fresh 8/12 budget that believes it is the only one running.

The arithmetic of the multiplication is not in doubt — three addressed agents is
three turns is up to 24 iterations and 36 tool calls for one sentence typed by a
human. **What is in doubt, and what this document does not claim to know, is
what that costs in money and wall clock on a real tenant.** No model was run to
write this plan. The arm that would settle it is one room, three agents, five
real questions, with `usage_events` read per agent afterwards — and it belongs
in [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2,
the bucket that costs model spend, **before `T-N8`'s ceilings are set to
numbers rather than to placeholders**. `T-N8` says so in its own *Notes*.

### 2d. The reference implementation, and the one decision it did not make

Hermes Agent (Nous Research, MIT) ships this feature: named agents, group rooms,
`@`-addressing, and bot-to-bot DMs. Its source was read at tag `v2026.9.7`
(commit `2237be35`) and written up in
[`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md).
The short version, because it is unusually useful:

| Our decision | Hermes | Evidence |
| --- | --- | --- |
| **3.** Deterministic `@name`, no router LLM | **Agrees, emphatically** | A 40-character regex against a frozen roster (`hosted_room_discussion.py:37`, `:291-308`), and a source comment reading *"never parallel, **no LLM router**"* (`group-rounds.ts:33-35`) |
| **4.** One addressed agent = one ordinary turn | **Agrees** | A member turn is an ordinary prompt submit (`tui_gateway/methods_prompt.py:195-203`); the orchestrator is a pure function returning at most one next task (`plan_next_task`, `:605-650`). No fan-out inside the runner anywhere in that codebase |
| **6.** A nudge is visible in the room | **Agrees for rooms, violates it for DMs** | A room reply is published to the shared log before its terminal event (`:713-722`); a DM lands in the recipient's *private* chat and the sender decides what to relay (`bot_mode_dm.py:245`, `bot_mode_probe.py:208-210`) |
| **9.** The conversation gets a budget | **Agrees, and got there first** | Four constants under the comment *"Every ceiling a single user send can spend"* (`group-chat.ts:1200-1207`) |
| **8.** Nudging off by default | **Disagrees on the default, agrees on the shape** | `bot_mode_protocol` defaults `True` (`config_defaults.py:155-156`), but the capability is gated by *session shape* — the tool exists only in a session titled `Bot Chat`, re-checked at dispatch (`bot_mode_dm.py:111-125`, `:185-190`) |
| **5.** A peer's words are untrusted, fenced, and taint is inherited | **No opinion — there is no trust boundary** | No fence, no taint, no sanitizer, no injection scan on any peer or room text; validation is type and byte-count only (`hosted_rooms_common.py:59-69`) |

**Two findings change this plan and one confirms its most expensive decision.**

1. **Our unaddressed-message rule is the cheaper one, and it is the one to
   keep.** Hermes answers a message that addresses nobody with **everybody**
   (`default_all=True`, `:305-307`). That single line is the largest cost
   multiplier in their design and the reason they need a pass protocol, a
   message cap *and* a continuation cap to keep a room from spinning. Decision 2
   — unaddressed routes to the default speaker — is one turn.
2. **Their loop guard has a hole exactly where ours would.** In a room they have
   four hard caps. On the **DM path there is nothing**: no depth cap, no counter,
   no cooldown, and the entire defence is one prompt sentence — *"never
   ping-pong acknowledgements"* (`bot_mode_probe.py:221-222`). The DM path is
   also the one place a **model** picks the recipient. `T-N8` is not cuttable,
   and that is why.
3. **Decision 5 is the one thing here that cannot be borrowed, and their code is
   the argument for it.** Hermes' mention parser runs over *model-authored* text
   (`_unaddressed_member_mentions:310-326`), so a document Agent A read can,
   through A's own summary, both name Agent B and supply what B acts on. §3
   decision 5 describes that laundering path as a risk; in their design it is a
   two-hop consequence of routing on model output. They get away with it because
   every bot in a Hermes society is the same human's bot on the same machine —
   the threat model `05-hermes-self-learning.md` §10 already identified. Ours is
   multi-tenant. **This is the decision we cannot copy**, and it is why `T-N5` is
   a ticket and not a paragraph.

**Cost: Hermes has not measured it either.** No figure is published anywhere in
their repo or docs. The derivable ceiling is 3 rounds × 6 members = 18 member
turns behind a 10-message cap — because a *pass* costs a full model call and
does not increment the message counter. So §2c's admission is not this project
being unusually scrupulous; it is the state of the art.

---

One prior finding is worth carrying forward here rather than rediscovering:
[`02-agent-quality-roadmap.md`](02-agent-quality-roadmap.md) records that
`MaxIterations` binds on roughly 5% of turns today and should stay where it is.
A room does not change that per-turn figure. It changes how many turns there
are.

---

## 3. Decisions (locked — do not re-litigate inside the tickets)

1. **A room is a thread with more than one participant, not a new object.**
   `conversation_threads` gains no meaning it does not already have. The new
   table is `thread_participants`. Every thread that exists today is a room of
   one, created by a backfill, and no turn that runs today changes.

2. **`threads.agent_id` keeps its job and gains a name: the default speaker.**
   It is who answers a message that addresses nobody. `ON DELETE SET NULL`
   stays, and so does the fallback at `chat_runner.go:1047` — a room whose
   default speaker was deleted falls back to the company default exactly as a
   thread does now. Do **not** migrate this column away into the participants
   table; the two answer different questions and the read path for the common
   case (one agent, no addressing) must stay a single row.

3. **Who speaks is decided by addressing, not by a model.** `@Finance` routes to
   Finance. Nothing addressed routes to the default speaker. **There is no
   router LLM in v1**, and this is the decision most likely to be re-argued, so:
   a model that picks the speaker is a new place a turn goes silently to the
   wrong persona with the wrong sources, it costs a light-tier call on *every*
   message, and this repository has already watched a classifier prompt edit not
   do what it said (`08-social-carousel-roadmap.md` §`T-G1`, the 2026-08-14
   edit). An `@` is unambiguous, free, and the user's own decision. **Hermes
   reached the same conclusion and put it in a comment** — *"never parallel, no
   LLM router"* (`group-rounds.ts:33-35`), §2d.

   **`@all` and `@everyone` are reserved handles**, added 2026-09-11 from
   Hermes' roster (`hosted_room_discussion.py:300-301`). They address every
   participant, they are the only way to do so, and an agent named `all` cannot
   be created. It is free, it is what a user of any group chat will type, and
   without it that message silently reaches one agent.

4. **One addressed agent = one turn = one `chat:run`.** No new task type, no
   fan-out inside `ChatRunner`. Three addressed agents is three
   `queue.ChatRunPayload`s, each identical in shape to one queued today. This is
   what keeps the budget guard, the audit decorator, the metering tap, the taint
   tracker and every guardrail working with no modification — see §2a.

5. **A peer agent's words are untrusted input, and they get their own taint
   kind.** `taint.KindAgent`, beside `KindDocument` and `KindData`
   (`internal/taint/taint.go:46-58`). Three reasons, and the third is the one
   that makes this a ticket of its own:
   - It is text authored by a model at runtime. `T-K2` and `T-H8` both decided
     that class is untrusted.
   - It is *persuasive* in a way a warehouse row is not: it arrives in the shape
     of a colleague's request, which is the shape a model is most inclined to
     comply with.
   - **It launders.** Agent A reads a supplier PDF (`KindDocument`, which gates
     actions under `T-H9`), summarises it, and nudges Agent B. Without
     inheritance, B receives the document's content as a peer message and is not
     gated. **Taint is inherited across a nudge**, and a nudge carrying a
     `document` taint gates on B's side too.

   **And a nudge never widens the recipient's scope.** Added 2026-09-11 from
   Hermes' one idea worth taking here: a target-issued execution policy, where
   the callee's toolsets, approval mode and iteration cap are resolved from the
   *callee's own* configuration and sealed with a digest the caller cannot alter
   (`hosted_room_execution_policy.py:71-90`). We already get this implicitly —
   `agentscope.WithScope(ctx, scopeOf(agentRow))` resolves per turn from the
   recipient's row (`chat_runner.go:783`) — but an emergent property is one
   refactor away from not being a property. `T-N5` states it as a rule and
   asserts it: **the recipient's sources, tools, MCP servers, skills and budget
   come from the recipient's row, and nothing in a nudge payload can influence
   any of them.**

   Peer text is delivered through `guardrails.Fence` (`fence.go:51`), labelled
   with the author's agent name, and **never** as a system-prompt addendum. A
   nudge cannot carry a `Directive` — that field is `T-A2b`'s, it is composed
   into the system prompt, and it exists precisely because a caller's
   instruction must not be laundered through the user's message. The same
   sentence applies one layer out.

6. **A nudge is a visible message in the room, never a side channel.** The user
   sees *Finance → Ops: can you confirm the stock figure for SKU 4471?* as a
   message, in order, with the answer under it. Two reasons: the audit trail
   people actually read is the transcript, and a hidden agent-to-agent channel is
   something a tenant would otherwise discover from an invoice.

7. **An agent may nudge only a current participant of the room, and the
   participant is pinned when the nudge is planned.** Not an arbitrary roster
   agent, not one from another company, not one that is disabled.
   `AgentForChannel`'s rule (`domain/agent_binding.go:80`) applies: a disabled
   agent is not found, because disabling is how an admin takes one out of
   service.

   **Pinned** is the part added 2026-09-11. Hermes projects an immutable
   validated roster per turn and embeds a member digest in every planned task
   (`hosted_room_discussion.py:87-94`, `:482-485`), so a roster edit mid-fan-out
   cannot silently redirect a queued turn. Our cheap equivalent: the nudge
   payload carries the **participant row id**, and the worker refuses a turn
   whose participant row is gone or now names a different agent. A room being
   edited while three turns are in flight is not exotic; it is a user clicking
   a chip.

8. **Nudging is off by default, per agent.** A new `agents.can_nudge` boolean,
   default `false`. This follows `MCPServerIDs`' rule — *empty means NONE*
   (`domain/agent.go:103`) — and deliberately not `AllowedTools`' rule, *empty
   means unrestricted*. The line between them is stated in `domain/agent.go`: a
   capability that only reaches the agent's own configuration defaults open, and
   one that reaches something the tenant had to bind defaults closed. A nudge
   spends another agent's budget against another agent's sources. It defaults
   closed.

   **Two gates, not one** — added 2026-09-11. Hermes defaults the *flag* open and
   then contains the capability by **session shape**: `message_agent` exists only
   in a session titled exactly `Bot Chat` on a managed install, re-checked at
   dispatch so a forged call fails (`bot_mode_dm.py:111-125`, `:185-190`). That
   axis is additive to ours and costs nothing. `can_nudge` answers *is this agent
   allowed to*; the conversation shape answers *is nudging reachable from here at
   all*. **`T-N6` requires both**: `agents.can_nudge` **and** more than one
   participant. An agent with the flag on, sitting in a room of one, must not
   find the tool in its schema — which also keeps the tool list byte-identical
   for every single-agent thread, and therefore keeps prompt caching intact.
   Hermes gives that same reason for making its own gate session-stable
   (`:128-131`).

9. **The conversation gets a budget, because the turn already has one and it is
   the wrong unit.** `T-N8`. Max agent turns per user message, max nudge depth,
   and a wall clock across the whole fan-out — with the in-band exhaustion
   message `T-16` established, delivered as a tool result the model reads rather
   than an error it never sees (`agentbudget/budget.go:18-20`).

   **Four amendments from Hermes' version, 2026-09-11** — theirs is four
   constants under the comment *"Every ceiling a single user send can spend"*
   (`group-chat.ts:1200-1207`), and it has been revised in production:
   - **Count turns enqueued, not messages appended.** Their 10-message cap does
     not bound model calls, because a *pass* costs a full call and does not
     increment the counter. A ceiling we can enforce before spending money is a
     ceiling on enqueues.
   - **The nudge-depth cap is load-bearing, not decorative.** Their
     `GROUP_CHAT_MAX_CONTINUATIONS = 2` exists because a review found that *"a
     pathological mention chain can't consume the room's whole budget on
     handoffs"* (`group-chat.ts:1210`). A single combined ceiling lets one chain
     eat the room.
   - **Settling is not exhaustion, and the two must read differently.** Their
     normal termination is a silent round, with the caps as the backstop
     (`hosted_room_discussion.py:646-647`), and they added the distinction after
     the fact: *"'settled' means quiet consensus … 'capped' means a
     round/message/continuation cap forced the exit — the activity feed must tell
     those apart"* (`group-rounds.ts:500-503`). Argentum has no pass concept
     today; `T-N8` adds one.
   - **The ceilings are per-company configuration from the first commit, not
     literals.** Hermes' are five `const`s with two open issues asking for
     configurability. A multi-tenant product cannot ship a spend ceiling a
     customer can neither see nor change.

   And one free optimisation: **the watermark.** Hermes skips a member without a
   model call when it has already seen everything (`:639-641`). In our terms, an
   addressed agent whose context has not changed since its own last turn should
   not be enqueued at all.

10. **Dashboard first; channels are their own track and change a unique key.**
    `agent_channel_bindings` is one row per (company, channel, external_id). A
    Slack group room with three agents in it is many. That is `T-N9`, it is the
    literal reading of *"group chat"*, and it is the first thing to cut.

11. **The room is not an access boundary, and a room is where that stops being
    theoretical.** `T-S1`'s locked decision 1 stands: company membership is the
    authorization boundary. §7 is what this track owes that decision.

---

## 4. The tickets

**Migration numbers are deliberately not pre-assigned.**
[`01-tickets.md`](01-tickets.md) records that the assignment was wrong twice,
both times the same way: a number reserved for an unlanded ticket that a later
ticket then took, producing a file golang-migrate can never apply. The last file
on disk is `076_query_example_archive` (`ls migrations/control/ | tail -3`), and
`01-tickets.md` records it as **written 2026-09-10, not yet applied**. **Read
`schema_migrations` and take the next free number when you land.** The slugs
below are stable; the numbers are not.

### Track A — The record and the room (5.5d) · do first

#### `T-N1` Every assistant message says which agent wrote it · **built 2026-09-11, unit-gated; migration round-trip owed**
**Repo:** BE + FE · **Size:** 1.5d · **Deps:** none · **Priority:** P0
**Migration:** `077_message_agent` — **written, not applied**

**Built, with two deviations from *Do* and one thing it found.**
*Do* asks for the agent to be passed into `completeWith` and on to
`AppendAssistantMessage`. No signature changed: `agentscope.Scope` already
carries the turn's agent id and name on the context and is already what the
audit decorator and the usage recorder read, so the service reads the same
value and the three rows agree by construction. And *Do* lists the five event
types to stamp; instead one decorator, `ChatRunner.publish`, stamps all twelve
sites — `T-05`'s argument, and `r.bus.Publish` now appears once in the file.
**What it found:** the small-talk short-circuit completed *before* the agent was
resolved, so a greeting would have recorded no author; the resolution moved
above it. Full write-up, including the three acceptance boxes that stay unticked
until the migration runs, in [`../coverage/multi-agent.md`](../coverage/multi-agent.md).

##### Why
`031_thread_agent.up.sql` gave `agent_actions` and `usage_events` an `agent_id`
and gave `messages` nothing (`:24`, `:31`). In a room of one that is harmless —
the thread's agent is the answer. In a room of three it breaks the two things
that matter most: the transcript a human reads, and the transcript
`hydrateMemory` (`chat_runner.go:2156`) replays into the *next* turn's context.
Three personas flattened into one undifferentiated `assistant` voice is a
history the model will reason over and get wrong.

**It is also worth landing alone.** A tenant with four agents cannot today
answer *"which agent said that?"* about their own conversation, and that is true
before any of this roadmap's other nine tickets.

##### Do
- Migration: `ALTER TABLE messages ADD COLUMN IF NOT EXISTS agent_id UUID;`
  **No foreign key**, for `031`'s stated reason on `agent_actions`: the row must
  outlive what it describes, and *"which agent wrote this"* is asked about
  deleted agents more often than live ones. No index — reads are by
  `(thread_id, created_at)`, which `002_threading.up.sql:39` already covers, and
  the agent id is a column those queries return rather than one they filter on.
- Backfill in the same migration:
  `UPDATE messages m SET agent_id = t.agent_id FROM conversation_threads t
   WHERE m.thread_id = t.id AND m.role = 'assistant' AND t.agent_id IS NOT NULL;`
  Assistant rows only. A `user` row has no agent and a `NULL` there is the
  truthful answer, not a missing one.
- `domain.Message` gains `AgentID string` and `AgentName string`
  (`internal/domain/message.go:19`). `AgentName` is **not persisted** — it rides
  along on reads so a transcript does not join the roster in the browser, which
  is exactly what `AgentChannelBinding.AgentName` already does
  (`domain/agent_binding.go:25-26`).
- `MessageRepository.Append` writes it; `ListByThread` and `ListPageByThread`
  join `agents` for the name. `LatestAssistantSince` and `LatestByThread` return
  it too — `/v1`'s resume path reads those.
- `ThreadService.AppendAssistantMessage` takes the agent id.
  `ChatRunner.completeWith` (`chat_runner.go:2026`) has it in
  `p.AgentID`; pass it. **Also pass it on the small-talk short-circuit**
  (`chat_runner.go:755`), which calls `completeWith` without ever resolving an
  agent row — a greeting in a room must still say who greeted.
- `ChatEvent` gains `AgentID` and `AgentName` (`app/event_bus.go:40`), set on
  `started`, `delta`, `tool_call`, `tool_result` and `final`. One field on the
  existing struct, no new event type — `T-Q10` established that shape for
  `next_steps` and the reason holds: every consumer that already reads these
  events keeps working and learns nothing.
- Dashboard: `MessageBubble` (`dashboard/src/features/chat/chat-page.tsx:821`)
  renders the agent's name on an assistant bubble **when the thread has more
  than one participant** — which is never, until `T-N2`. Ship it behind that
  condition so this ticket changes nothing on screen for a room of one.
- `make types` — `packages/api-types` regenerates from the Go structs.

##### Notes for the implementer
- `p.AgentID` is empty on a payload queued before `T-S2` and on one whose agent
  was deleted between enqueue and run — `resolveAgent` (`chat_runner.go:1028`)
  falls back to the company default and the payload is not rewritten. **Write
  the id the turn actually ran as**, not `p.AgentID`: have `Run` keep
  `agentRow.ID` and hand that to `completeWith`. The audit row and the message
  row must agree, and today only the audit row is right.
- Do not add `agent_id` to the `user` role's write path. A user message has no
  agent and inventing one — "the agent it was addressed to" — would make the
  column mean two things, which is how `T-N3`'s addressing later becomes
  unreadable.
- `tygo` regenerates on a doc-comment change too; expect `packages/api-types` to
  move more than you wrote.

##### Acceptance
- [ ] A dashboard turn writes an assistant row whose `agent_id` equals the
      `agent_actions.agent_id` of the tool calls in the same turn
- [ ] The backfill sets every historical assistant row of an agent-pinned thread
      and leaves every `user` row `NULL`
- [ ] A turn whose agent was deleted between enqueue and run records the
      **default agent's** id, not the deleted one, and not `NULL`
- [ ] A small-talk greeting records an agent id
- [ ] `final` carries `agent_id` and `agent_name`; a client that ignores both is
      unaffected
- [ ] Nothing on the dashboard changes for a single-agent thread — pin it with a
      screenshot diff or an explicit assertion
- [ ] `packages/api-types` is regenerated and committed

##### Gate
```bash
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
make check                      # vet + lint + test -race + build
node packages/api-types/scripts/generate.mjs --check
# migration round-trip, against a real Postgres
migrate -path apps/backend/migrations/control -database "$DATABASE_URL" up
migrate -path apps/backend/migrations/control -database "$DATABASE_URL" down 1
migrate -path apps/backend/migrations/control -database "$DATABASE_URL" up
```
Paste the `schema_migrations` version before and after, and one
`SELECT id, role, agent_id FROM messages WHERE thread_id = '…'` showing the
backfill.

##### Out of scope
- Anything about participants — that is `T-N2`.
- Rendering an avatar or a colour per agent. `T-N4` owns the room's visual
  identity; this ticket puts a name on a bubble and stops.

---

#### `T-N2` A conversation can hold more than one agent · **built 2026-09-11, unit-gated; migration round-trip owed**
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-N1` · **Priority:** P0
**Migration:** `078_thread_participants` — **written, not applied**

**Built, with one deliberate departure from *Do*, and it is the interesting
part of the ticket.** *Do* specifies a backfill giving every existing thread one
participant row. Writing it made `thread_participants` the only answer to *who
is in this conversation* — and threads are created by **six** paths, so all six
would have had to remember to write a row. That is `T-K1`'s defect exactly: a
binding table written by one path and read by another, invisible to every unit
test, wrong for five days. **The default speaker is now an implicit member with
no row**, `List` returns the union, and `078` writes no data at all. Two checks
that the unique index would otherwise have given free — adding the default
speaker, and the cap counting the implicit seat — are explicit and tested. Full
argument in [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §4.

**This makes one acceptance item below obsolete rather than owed**: *"the
backfill gives every agent-pinned thread exactly one participant"* describes a
backfill that no longer exists, and the property it checked is now structural.

##### Why
`conversation_threads.agent_id` says which agent a conversation *is*
(`031_thread_agent.up.sql:17`). A room needs to say which agents are *in* it,
which is a different question with a different cardinality, and there is nowhere
to put it. This ticket is the table and the read model; nothing routes to it
until `T-N3`.

##### Do
- Migration:
  ```sql
  CREATE TABLE IF NOT EXISTS thread_participants (
      id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      thread_id  UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,
      agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
      added_by   UUID REFERENCES users(id) ON DELETE SET NULL,
      added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      UNIQUE (thread_id, agent_id)
  );
  CREATE INDEX IF NOT EXISTS idx_thread_participants_thread
      ON thread_participants(thread_id);
  ```
  `ON DELETE CASCADE` on `agent_id` and **not** `SET NULL`: a participant row
  naming no agent is not a fact about anything. This is the opposite of
  `threads.agent_id`'s choice and the difference is the point — a deleted agent
  must not strand a *conversation*, but it should certainly leave the *room*.
  `added_by` is `SET NULL` because a departed employee must not delete a room.
- Backfill: one row per thread that has an `agent_id`. Every existing thread
  becomes a room of one and behaves identically.
- `domain.ThreadParticipant` + `ThreadParticipantRepository` in
  `internal/domain/thread_participant.go`. **Every method takes `companyID`**,
  including the ones that already have a primary key — `AgentRepository`'s rule
  (`domain/agent.go:114`) and for the same reason.
- `ConversationThread` gains `Participants []ThreadParticipant` on reads, and a
  helper `HasParticipant(agentID string) bool`. One method, one place the
  membership rule is written down — `AllowsTool`'s pattern
  (`domain/agent.go:79`).
- `ThreadService`: `AddParticipant`, `RemoveParticipant`, `ListParticipants`.
  Rules, and each gets a negative test:
  - An agent must belong to the caller's company. A cross-company id is
    `ErrNotFound`, never `ErrForbidden` — `RosterReader.GetByID`'s comment
    (`chat_enqueuer.go:50-55`) explains why a distinguishable error is an
    existence oracle.
  - A **disabled** agent cannot be added, for `AgentForChannel`'s reason.
  - The default speaker (`threads.agent_id`) cannot be removed while it is the
    default speaker. Change the default first, or remove it last.
  - A cap: `THREAD_MAX_PARTICIPANTS`, default **4**. Not a guess dressed as a
    constant — it is the number of jobs `domain/agent.go`'s own opening comment
    names (Marketing, Ops, HR, Finance), and §2c's unmeasured cost is why it is
    a config key rather than a literal. For calibration: Hermes caps a room at
    **6** members (`group-chat.ts:1208-1214`), as a compile-time constant with an
    open issue asking for configurability — §2d's third "do not copy".
- Routes on the existing `ChatHandler` (`handlers/chat.go:25-31`):
  `GET/POST /api/threads/:id/participants`, `DELETE
  /api/threads/:id/participants/:agentID`. `POST /api/threads` accepts
  `participant_ids` beside today's `agent_id` (`handlers/chat.go:44-49`).
- `GET /api/threads/:id` returns participants. `GET /api/threads` does **not** —
  a listing that joins a second table per row to render a sidebar is a cost paid
  on every page load for a label; `recent-chats.tsx` needs a title, not a roster.

##### Notes for the implementer
- **Do not remove or repurpose `conversation_threads.agent_id`.** Decision 2. It
  becomes the default speaker, the common path stays one row, and
  `resolveAgent`'s fallback chain (`chat_runner.go:1047`) keeps working
  untouched.
- The backfill must be idempotent — `ON CONFLICT (thread_id, agent_id) DO
  NOTHING`. `T-K1`'s per-agent binding shipped a table that was written and read
  by nobody for five days (`07-agentic-skills-roadmap.md`, the 08-27 note); the
  cheapest insurance against the same shape here is that this ticket's
  acceptance includes a read path, which it does.
- `002_threading.up.sql:39`'s `idx_messages_thread` covers the transcript read.
  Do not add an index for participants on `messages`; there is no such column.

##### Acceptance
- [ ] Migration round-trips; the backfill gives every agent-pinned thread
      exactly one participant, and threads with `agent_id IS NULL` none
- [ ] `GET /api/threads/:id` returns participants with names; `GET /api/threads`
      does not and issues no extra query (check the SQL log)
- [ ] Adding another company's agent returns **404**, not 403
- [ ] Adding a disabled agent is refused
- [ ] Removing the default speaker is refused with a message naming the fix
- [ ] The cap is enforced and the refusal names the limit
- [ ] Deleting an agent removes its participant rows and leaves the threads
      intact and openable
- [ ] **No turn behaves differently** — the chat pipeline is untouched by this
      ticket, and one before/after transcript of an ordinary question proves it

##### Gate
`make check`, plus the migration round-trip above, plus a `curl` transcript of
add / list / remove / the three refusals.

##### Out of scope
- Routing a message to a participant. `T-N3`.
- The dashboard control for adding one. `T-N4`.
- Channel rooms. `T-N9`, and note that `agent_channel_bindings`' unique key is
  untouched here.

---

#### `T-N3` Addressing — `@agent` decides who answers · **built 2026-09-11, unit-gated**
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-N2` · **Priority:** P0
**Migration:** none

**Built. One acceptance item was struck as unachievable rather than ticked or
quietly dropped** — the per-payload budget refusal below; `CheckBudget` is a
cached balance read with no reservation, so N calls return one verdict N times.
Three dependencies were narrowed to consumer-declared interfaces along the way
(`ChatRunEnqueuer`, `CompanyReader`, `RoomReader`), which is what made the
central claim — *one user message, N turns, one `UserMsgID`* — testable at all:
it previously needed a live Redis, which is why nothing checked it.
[`../coverage/multi-agent.md`](../coverage/multi-agent.md) §5.

##### Why
With participants stored and nothing reading them, a room is a settings page.
This is the ticket that makes one user message become N turns — and decision 4
is the whole design: it becomes N `chat:run` payloads, each identical to one
queued today, so nothing downstream of the queue learns what a room is.

##### Do
- `internal/app/addressing.go`: `ParseAddressing(text string, participants
  []domain.ThreadParticipant) (addressed []string, cleaned string)`. The grammar
  is Appendix A. Match on the agent's `Name`, case-insensitive, longest name
  first so *"@Finance Ops"* does not match a shorter agent whose name is a
  prefix of a longer one.
  - **`@all` and `@everyone` are reserved** (decision 3) and address every
    participant. `AgentService` refuses to create or rename an agent to either,
    case-insensitively — a reserved handle that a tenant can shadow is a
    reserved handle that stops working for one tenant only.
  - **It is a pure function** of `(text, participants)` — no repository, no
    context, no clock. Hermes' room orchestrator is a pure function over a
    durable log for the same reason (`plan_next_task`,
    `hosted_room_discussion.py:605-650`): a routing decision that can be
    replayed is a routing decision that can be tested and, after a crash,
    reconstructed.
- **Strip the `@` tokens from what the agent sees**, exactly as
  `lark.StripMentions` does (`internal/lark/mention.go:28-33`) and for the same
  reason its comment gives: the addressing is routing metadata, and leaving it
  in the prompt teaches the model that `@` is something it should produce.
  Persist the **original** text on the user message — the transcript is what the
  person typed.
- `ChatInput` gains `AddressedAgentIDs []string` (`chat_enqueuer.go:98-133`).
  `ChatEnqueuer.HandleInput` resolves the list:
  - Non-empty → one payload per id, each with `AgentID` set.
  - Empty → one payload for `threads.agent_id`, i.e. today's exact behaviour.
    **Not every participant.** Hermes answers an unaddressed message with all of
    them (`default_all=True`, `hosted_room_discussion.py:305-307`) and §2d
    records what that costs them.
  - An `@` naming a roster agent that is **not** a participant → refuse the
    whole message with a typed error naming the agent and saying it is not in
    this conversation. **Do not silently add it**: a message that quietly
    enlarges the room is a message that quietly spends money.
  - An `@` matching nothing → not addressing. It is someone's email address or a
    handle, and it goes to the default speaker as ordinary text.
- **One user message row, N turns.** The user message is appended once; every
  payload carries the same `UserMsgID`. This matters more than it looks:
  `ChatEvent.JobID` is the user message id (`event_bus.go:41`), so N concurrent
  turns publish under one job id and are told apart by `T-N1`'s `agent_id`.
  Write that down in the `ChatEvent` doc comment.
- **The budget check runs per payload**, not once for the message
  (`chat_enqueuer.go:76`). Three addressed agents is three `CheckBudget` calls,
  and a tenant who can afford one turn and not three gets one answer and one
  refusal rather than three half-turns. The refusal names which agents did not
  run.
- Ordering: enqueue in the order the agents were addressed. asynq gives no
  ordering guarantee across workers and this ticket does not add one — the
  transcript is ordered by `messages.created_at`, which is Postgres's clock, and
  that is the order a reader sees. Say so in the code comment; do not build a
  sequencer.

##### Notes for the implementer
- **Resist making this a tool call or a classifier.** Decision 3. A reviewer
  will suggest "let the model decide who should answer" and the answer is in §3.
- `trivialReply` (`chat_runner.go:2660`) short-circuits greetings. *"@Finance
  hi"* strips to *"hi"* and short-circuits — which is correct and cheap, and
  `T-N1` makes it say who said it.
- Guardrails are unchanged. The input classifier judges the user's own words,
  and after stripping, the user's own words are what it gets. Do **not** run the
  addressing prefix through it.
- The `api`, `widget` and channel surfaces do not gain addressing here. `/v1`
  and the widget are `T-N10`; the channels are `T-N9`. This ticket is dashboard
  only, and `ChatInput` is shared — make the field optional and unset
  everywhere else, which is `Directive`'s exact pattern.

##### Acceptance
- [ ] *"@Ops @Finance what happened to margin last week?"* in a three-agent room
      enqueues exactly two `chat:run` tasks, with two different `AgentID`s and
      one shared `UserMsgID`
- [ ] A message with no `@` enqueues exactly one task, for the default speaker —
      byte-identical payload to what the same message produces today
- [ ] `@Finance` where Finance is on the roster but not in the room is refused,
      the room is unchanged, and **nothing is enqueued**
- [ ] `@notanagent` is treated as text and produces one ordinary turn
- [ ] The persisted user message contains the original `@` tokens; the message
      handed to the model does not
- [ ] ~~A tenant with credit for one turn who addresses three gets one answer and
      a refusal naming the other two~~ — **struck 2026-09-11 as unachievable as
      written.** `UsageService.CheckBudget` (`credits.go:122`) is a *cached
      balance read*; the decrement happens in the worker after a turn runs, and
      nothing reserves credit at enqueue. Calling it three times returns the
      same verdict three times. Making this true needs a reservation at enqueue
      — a credits ticket, not an addressing one. The exposure is bounded by
      `THREAD_MAX_PARTICIPANTS`, and today's single-turn path already has the
      same overshoot of one. See [`../coverage/multi-agent.md`](../coverage/multi-agent.md) §5
- [ ] Two concurrent turns on one thread both stream, and every event carries
      the agent that produced it

##### Gate
`make check`, plus a worker-log transcript of the two-agent case showing two
`chat:run` ids and two agent ids, plus the three refusal transcripts.

##### Out of scope
- An agent addressing another agent. That is `T-N6`, and it must not be
  reachable from this ticket's code path.
- Addressing on any surface but the dashboard.

---

### Track B — The room on screen (2.5d)

#### `T-N4` The dashboard room — participants, `@` autocomplete, and who said what · **built 2026-09-11, visual gate run**
**Repo:** FE · **Size:** 2.5d · **Deps:** `T-N3` · **Priority:** P0
**Migration:** none

**Built. The state change was the work**, not the chrome: `liveAssistant` was
one `LiveTurn | null`, and a room has several streaming concurrently under one
job id — so `final` and `error` had to stop meaning "the turn ended" and start
meaning "one agent finished". Three screenshot scenes were added and **looking
at them found three defects**, one of them in the harness itself: its Tailwind
globs excluded `harness/`, so a class used only in a scene was silently absent
and the grayscale scene came out in full colour. Details in
[`../coverage/multi-agent.md`](../coverage/multi-agent.md) §6.

**One acceptance item is owed rather than met**: *"A single-agent thread is
pixel-identical to today — proven by a harness screenshot"*. The harness mounts
components, not the routed `ChatPage`, so there is no before/after pair of the
whole screen. What is proven instead is structural and stated as such — the bar
returns null, `showAuthors` is false, and `PendingBubble` takes no author when a
thread has fewer than two participants.

##### Why
Three backend tickets in and the feature is reachable only by `curl`. This is
the surface, and it also has to resolve a rule the roster deliberately made:
`AgentPicker` renders only before the first message, because *"the history in
the model's context was produced under a different persona… and reinterpreting
it under new ones is a decision, not a widget"*
(`dashboard/src/features/chat/agent-picker.tsx:13-19`). **That rule was right
and a room does not break it** — adding a participant does not reinterpret
history under a new persona, it adds a reader who can see it. The UI must make
the difference obvious, because the distinction is the whole reason the old rule
is not being deleted.

##### Do
- **Participant bar** above the composer: one chip per participant with the
  agent's name and colour, a `+` that opens the roster (disabled agents greyed
  with the reason), and a remove affordance per chip. The default speaker is
  marked, and the mark has a tooltip: *"answers when you don't @ anyone"*.
- **`AgentPicker` keeps its job and its rule.** It still sets the *default
  speaker* on an empty thread and still disappears after the first message. Add
  one line to its doc comment pointing at the participant bar so the next reader
  does not "fix" the inconsistency.
- **`@` autocomplete in the composer.** Typing `@` opens a participant list;
  selection inserts `@Name `. It offers **participants only** — never the whole
  roster — because `T-N3` refuses a non-participant and an autocomplete that
  suggests a refusal is a bug with a menu.
- **`MessageBubble` (`chat-page.tsx:821`) grows an author.** Name + colour on
  assistant bubbles **only when `participants.length > 1`**, so a room of one
  looks exactly as it does today. Colour comes from `packages/design-tokens` —
  a categorical ramp, **not** a hash of the agent id: see the note below.
- **Concurrent streaming.** `use-thread-stream.ts` holds one streaming assistant
  turn (`chat-page.tsx:43`). It has to hold a map keyed by agent id: two agents
  answering means two live bubbles, each with its own thinking trace
  (`thinking-trace.tsx`) and its own tool cards (`tool-call-card.tsx`). Events
  are routed by `ChatEvent.AgentID` from `T-N1`.
- **Next-step chips** (`next-steps.tsx`) render under the newest assistant
  message *per agent*, and clicking one prefills the composer with `@ThatAgent `
  already in it — `T-U13`'s rule holds, a click never sends.
- Empty-room and single-agent states unchanged. A company with one agent sees no
  participant bar at all, for `AgentPicker`'s `agents.length < 2` reason
  (`agent-picker.tsx:36`): a control that cannot control anything is furniture.

##### Notes for the implementer
- **Do not hash the agent id to a colour.** Two agents colliding on one hue in a
  room of four is a coin flip roughly one time in three, and the failure is
  silent and permanent for that tenant. Assign from an ordered categorical ramp
  by the agent's position in the company roster, and persist nothing.
- Accessibility: colour is not the attribution. The name is, and the colour is
  redundant. A reader with a colour-vision deficiency must lose nothing — the
  same constraint `T-R3`'s palette gate imposed on the report charts.
- The transcript can now interleave two agents' messages by timestamp. Do not
  group by agent; a room read out of order is not a room.

##### Acceptance
- [ ] A single-agent thread is pixel-identical to today — proven by a harness
      screenshot, not by inspection
- [ ] A three-agent room renders three chips, and removing one updates without a
      reload
- [ ] `@` opens participants only; a roster agent not in the room is not offered
- [ ] Two agents addressed in one message stream **simultaneously**, in two
      bubbles, each with its own thinking trace and tool cards
- [ ] Every assistant bubble in a multi-agent room names its author
- [ ] Attribution survives with colour removed (grayscale screenshot)
- [ ] A next-step chip prefills `@Agent ` and does not send

##### Gate
```bash
pnpm --filter dashboard test
pnpm --filter dashboard build
pnpm --filter dashboard shots       # apps/dashboard/harness → docs/coverage/assets/
```
Add room scenes to `apps/dashboard/harness/main.tsx` and rows to `shoot.mjs`.
**Drive the state through the product's own controls, not fixtures** — a
screenshot built from a fixture documents the harness. Paste the single-agent
before/after pair and the grayscale room still.

##### Out of scope
- The widget's UI. `T-N10` covers `/v1` and the widget's data shape; the
  widget's own Preact UI (`apps/widget/`) is a separate surface and this track
  does not redesign it.
- Reordering or renaming agents. Settings → Agents already owns that.

---

### Track C — The trust boundary (1.5d) · before any agent talks to any agent

#### `T-N5` A peer agent's words are untrusted input · **built 2026-09-13, unit-gated; `make eval` owed — `coverage/multi-agent.md` §8**
**Repo:** BE · **Size:** 1.5d · **Deps:** `T-N1` · **Priority:** P0 — **not cuttable**
**Migration:** none — correct, for once

> **What moved against this ticket ([`../coverage/multi-agent.md`](../coverage/multi-agent.md) §8c):**
> - **The carrier is `ChatRunPayload.Peer`** (`queue.PeerOrigin{AgentID, Taint}`), because
>   `T-N6`'s `NudgeFromAgentID` could not carry a taint.
> - **The author's name is looked up from the roster**, never carried on the payload.
> - **The input-classifier exemption moved to `T-N6`.** The only safe signal is the payload;
>   a skip keyed on the fence markers is a guardrail bypass anybody can type.
> - **The names went on the completion line as `peer_agents`**, because `agent_actions`
>   has no sources column.
>
> **And the *Why* below is half right about timing.** The laundering path does not wait for
> `T-N6`: a room's shared conversation memory already opens it (§8b, `T-N11`).

##### Why
This is `T-K2` one layer out, and the argument is the same one `T-H8` made for
everything a turn reads: content that arrives at runtime and was not authored by
the tenant is untrusted. A peer agent's message is worse than a warehouse row on
two counts. It is authored by a model, so it can be *steered* by anything that
model read. And it arrives wearing the shape of a colleague's request, which is
the shape compliance is cheapest.

**The laundering path is the one to hold in mind.** `taint.KindDocument` exists
because an uploaded PDF is the most untrusted thing this product reads, and
`T-H9` makes a turn that read one require human approval before anything reaches
the outside world (`internal/taint/taint.go:13-20, 46-49`). Agent A reads the
supplier's PDF, summarises it in good faith, and nudges Agent B. Without
inheritance, B has read no document, gates nothing, and is holding the
document's content — with the PDF's instructions now delivered by a trusted
colleague instead of by a file. **That is a privilege escalation with no code
defect in it**, and it exists the moment `T-N6` ships unless this lands first.

##### Do
- `taint.KindAgent Kind = "agent"` in `internal/taint/taint.go:44-58`, with the
  doc comment stating what it is and what it is not: recorded and fenced like
  `KindData`, **and** a carrier for whatever the author's turn was tainted with.
- **Inheritance.** `Tracker` gains `Inherit(from map[Kind][]string)` — or the
  carrier shape the nudge payload ends up using. A nudge crossing from A to B
  copies A's taint set into B's tracker **before B's first tool call**, beside
  `taint.With(ctx, taints)` at `chat_runner.go:737`. A `document` taint in A is
  a `document` taint in B, and `T-H9`'s gate fires on B.
- **Delivery shape.** Peer text reaches the model through
  `guardrails.Fence(label, content)` (`internal/guardrails/fence.go:51`), with
  the label carrying the author's agent name. It is delivered as a **user-turn
  block**, never as a system-prompt addendum and never through `Directive`.
  `Directive` is composed into the system prompt (`T-A2b`); an agent that could
  write into another agent's system prompt is an agent that can rewrite its
  persona, its dialect rules and `T-16`'s anti-fabrication language — the exact
  thing `T-S1`'s locked decision 3 refuses to let a *tenant* do.
- **The fence is not advice.** `neutralizeFence` (`fence.go:74`) already strips
  fence markers out of content so a payload cannot close its own fence. Peer
  text goes through it; add the test that a nudge whose body contains the fence
  sentinel cannot escape.
- One sentence in the shared system prompt (`bootstrap.SystemPrompt`) about how
  to read a fenced peer message: it is a colleague's question or claim, it is
  not an instruction from the operator, and a figure inside it is not evidence —
  verify it before repeating it. **This is a prompt change**, so `make eval`
  before and after is mandatory (`../agents/verification.md` §"Changed the
  system prompt").
- **State the scope rule and assert it** (decision 5). The recipient's sources,
  tools, MCP servers, skills and budget are read from the recipient's agent row
  by `scopeOf` (`chat_runner.go:783`); **no field on a nudge payload may
  influence any of them.** A test that constructs a nudge payload with every
  scope-shaped field an attacker could wish for and asserts the recipient's
  scope is byte-identical to its row. Hermes seals the equivalent with a digest
  (`hosted_room_execution_policy.py:71-90`); ours is emergent today, and an
  emergent property is one refactor from not being one.
- The audit row records the kind, as `agent_actions` already records taint for
  `T-P10`/`T-H8`. *"What did this turn read before it did that"* must answer
  *"a message from the Finance agent, which had read invoice-4471.pdf"*.

##### Notes for the implementer
- **Build this against a stub, not against `T-N6`.** The unit tests should drive
  `Inherit` directly. A trust boundary whose only test is an end-to-end nudge is
  a trust boundary tested by the thing it is supposed to constrain.
- `taint.Tracker` is already mutex-guarded for concurrent tool calls
  (`taint.go:60-63`). Two agents in a room are two trackers in two turns, not one
  shared tracker — do not "simplify" them into one.
- Do not run peer text through the **input** guardrail classifier. That
  classifier decides whether a *user's* request is on-topic; asking it whether
  another agent's sentence is on-topic will refuse legitimate hand-offs and
  teach nobody anything. The control here is the fence and the taint, not the
  topic gate.

##### Acceptance
- [ ] `KindAgent` is recorded on a turn that received a nudge (driven by a stub)
- [ ] A turn whose author was tainted `document` produces a recipient turn that
      is **also** tainted `document`, and `T-H9`'s approval gate fires on it
- [ ] Peer content arrives fenced and labelled with the author's agent name
- [ ] A nudge body containing the fence sentinel does not escape the fence
- [ ] **No path exists by which a nudge sets `Directive`** — assert it, do not
      review it
- [ ] A nudge payload carrying scope-shaped fields does not widen the
      recipient's scope by one source, tool, server or skill
- [ ] `make eval` pass rate is at or above
      [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md); both
      numbers pasted

##### Gate
`go test ./internal/taint/... ./internal/guardrails/... -race -v`, then
`make check`, then `make eval` before and after the prompt edit with both pass
rates pasted. The eval run costs model spend — file it under
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2 if it
is not run in the same sitting, and **do not tick the box until it is**.

##### Out of scope
- The nudge tool itself. `T-N6`.
- Gating on `KindAgent` the way `T-H9` gates on `KindDocument`. Decided against:
  every hand-off would need an approval, which is an off switch rather than a
  control — `taint.go:13-20`'s own argument about why `KindData` does not gate.

---

### Added 2026-09-13, after `T-N5`

#### `T-N11` A room's history is a peer's words too · **built 2026-09-14, unit-gated; `make eval` and the room arm owed — `coverage/multi-agent.md` §9**
**Repo:** BE · **Size:** ~1.5d · **Deps:** `T-N5` · **Priority:** P0 — **recommended before `T-N6`**
**Migration:** none — correct
**Decision first:** whether a document read in a peer's *earlier* turn is inherited — see *Do*.
**Decided 2026-09-14 by the owner: not inherited**, as recommended. The gap between turns is
filed against `T-H9`.

> **Where this ticket was wrong** ([`../coverage/multi-agent.md`](../coverage/multi-agent.md) §9d).
> It was written from a grep, not from reading the SDK.
> - **The shared buffer held two more leaks than it names:** every agent's tool calls and
>   raw results — rows from sources the reader may not reach — and every agent's composed
>   prompt, source catalog included. A colleague's tool plumbing is now dropped, and its
>   prompt is replaced by the person's words.
> - **Re-roling an assistant message would have broken requests.** Most assistant messages
>   in the buffer are empty tool-call carriers, and re-roling them would have orphaned tool
>   results.
> - **The unstamped-history rule was not built.** It needs a participant lookup inside
>   memory, and production has never run a room — which holds **only if this ships no later
>   than rooms**.

##### Why
[`../coverage/multi-agent.md`](../coverage/multi-agent.md) §8b.
- **One buffer per thread.** The SDK's conversation memory is keyed by company and
  thread, and every agent in a room writes to and reads from that one buffer.
- **So a colleague's reply reads as the agent's own.** An agent addressed after another
  sees its colleague's replies as its own `assistant` messages — unfenced, unlabelled,
  untainted. `hydrateMemory`, the cold path, does the same.
- **It is already live.** This is `T-N5`'s laundering path with no nudge in it, and it
  shipped with `T-N3` in `v1.7.0`.
- **It is also the flattening `T-N1`'s *Why* named,** left open: `T-N1` fixed the record
  and the transcript, not the replay.

##### Do
- **Stamp and re-role the warm path.** Put a memory decorator around `buildMemory`'s
  result (`bootstrap/stack.go`).
  - On `AddMessage`, stamp `Metadata["agent_id"]` from `agentscope.AgentID(ctx)`.
  - On `GetMessages`, turn an `assistant` message stamped with *another* agent into a
    user-role message, `guardrails.FencePeer(name, content)`, and mark
    `taint.KindAgent` on the turn's tracker.
  - **Check first that `RedisMemory` round-trips `Metadata`.** The warm path is the one
    that matters, and a stamp that does not survive Redis is no stamp.
- **The same rule for the cold path.** `hydrateMemory` reads `messages.agent_id` (`T-N1`)
  rather than a stamp.
- **Messages from before the decorator carry no stamp.** Replay them as today on a thread
  with one participant, and as unattributed peer text in a room.
- **The decision.**
  - *Inherit* a document taint from a peer's earlier turn, and every room turn after
    anyone read a document is gated — `T-H9`'s off switch.
  - *Do not inherit*, and the room keeps the cross-turn gap a single agent already has,
    since `T-H9` gates per turn.
  - **Recommendation: do not inherit, record `agent`, and file the cross-turn gap against
    `T-H9`**, where it belongs for a single agent too.

##### Notes for the implementer
- **Consecutive user-role messages will occur:** the person's question, then a fenced
  peer reply. OpenAI-compatible endpoints accept that. Check the Anthropic interface
  before assuming it does.
- **A person's own replies stay `assistant`.** Fence everything and the fence stops
  meaning anything; `T-H8`'s `trustedResults` comment makes the same argument for tools.

##### Acceptance
- [ ] In a room, B's provider request carries A's reply fenced under A's name in a
      user-role message, and B's own earlier replies as `assistant`
- [ ] A single-agent thread's provider request is byte-identical to today's
- [ ] B's turn records `agent` with A's name
- [ ] Proven on both paths: the shared buffer (warm) and `hydrateMemory` (cold)
- [ ] `make eval` before and after, both rates pasted
- [ ] [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7d's third
      arm re-run as the *after*

---

### Track D — An agent talks to an agent (5.0d)

#### `T-N6` `nudge_agent` — one participant asks another · **built 2026-09-14, unit-gated; `make eval`, the live arms and `086`'s round-trip owed — `coverage/multi-agent.md` §11**
**Repo:** BE, and three dashboard changes it did not name · **Size:** 2.5d · **Deps:** `T-N5`, `T-N8` · **Priority:** P0
**Migration:** ~~none~~ **`086_agent_can_nudge`** — `agents.can_nudge` did not exist

> **What moved against this ticket ([`../coverage/multi-agent.md`](../coverage/multi-agent.md) §11d):**
> - **"Migration: none" was wrong.** Decision 8 names `agents.can_nudge`, and nothing had
>   added it. `086` does, default false, with no backfill.
> - **"The API's scoping checkboxes get it for free" would have broken decision 8.** A
>   checkbox is an `allowed_tools` entry, and an empty allowlist means every tool. The tool is
>   dropped from the vocabulary (`tools.GatedByFlag`), and the factory offers it from the flag
>   and the room alone.
> - **The default speaker has no participant row** (`T-N2`), so "the participant row id" does
>   not exist for it. Its seat is pinned by the empty id against `threads.agent_id`.
> - **The visible question cannot be published as `final`** — that would close the asker's
>   own bubble mid-stream. It is a `room_event` event, and the dashboard needed a handler for
>   it, a drawing for the room's own lines, and a checkbox for the flag. The ticket said BE.
> - **The classifier skip is narrower than "the input topic classifier" could be read.** Only
>   `require` rules stand aside; injection and off-topic block rules still run on peer text.
> - **The pass is a sentinel, not a tool.** A tool would change every room turn's schema. The
>   sentence offering `PASS` rides a colleague's question only.
> - **The notice is written once per agent per person's message** — the decision `T-N8` left
>   here.
> - **Not asked for, and built:** a colleague whose seat changed writes a room line rather than
>   vanishing (decision 6), and a room's own lines are never replayed into model history.

##### Why
The second half of the request. Ops is asked about a stock discrepancy, the
number it needs lives behind Finance's source, and today the only path is the
human reading Ops' answer, opening a second conversation, asking Finance, and
carrying the figure back by hand. That is the failure the roster was supposed to
remove and instead relocated.

##### Do
- New tool in `internal/tools/nudge_agent.go`, registered in
  `internal/tools/registry.go` — **one construction site**, per that file's
  opening comment, so the API's scoping checkboxes get it for free. Follow
  [`../agents/playbooks/add-agent-tool.md`](../agents/playbooks/add-agent-tool.md).
- Parameters: `agent` (the participant's name, not a uuid — the model has names,
  not ids) and `question` (capped; a nudge is a question, not a transcript).
- **Provenance is structural, never a text prefix.** The payload carries
  `NudgeFromAgentID` and the **participant row id** the nudge was planned
  against (decision 7); the worker refuses a turn whose participant row is gone
  or now names a different agent. Hermes' provenance is a prefix the sender
  writes into the message body and nobody ever parses
  (`bot_mode_dm.py:212`) — forgeable in both directions, and the research doc
  says so.
- Refusals, each returning a plain sentence the model can act on, never an
  error — `load_skill.go:196`'s `refusal` helper is the prior art, including its
  habit of listing what *is* available:
  - The tool is not offered at all unless `agents.can_nudge` is true for the
    calling agent **and** the thread has more than one participant — **both
    gates**, decision 8. A tool that is present and always refuses is a tool the
    model wastes iterations on, and a tool list that varies by thread shape is a
    prompt prefix that stops caching. Re-check the gate **at dispatch** as well
    as at registration, which is what Hermes does and why
    (`bot_mode_dm.py:185-190`): a model that has seen the tool in one session
    will try to call it in another.
  - Not a participant → refuse, and name the participants that exist.
  - Itself → refuse. It reads as a joke until a model does it in a loop.
  - Budget exhausted → `T-N8`'s in-band message.
- **The mechanism is an enqueue, not a call.** The tool writes an assistant
  message to the thread (role `assistant`, `agent_id` = the asker, metadata
  marking it a nudge and naming the target) and enqueues one ordinary
  `chat:run` for the target, with the nudge as the message and
  `NudgeFrom`/`NudgeDepth` on the payload. It returns immediately: *"Asked
  Finance; their answer will appear in this conversation."* **A tool call that
  blocks a turn while another whole turn runs would spend the asker's wall clock
  on the answerer's work** — the same reasoning `T-V3` used to make video
  rendering asynchronous (`01-tickets.md`, the `generate_document` mp4 note).
- ~~`ChatRunPayload` gains `NudgeFromAgentID` and `NudgeDepth int`
  (`queue/tasks.go:184`), both `omitempty`. `ChatRunner.Run` reads them to
  install the inherited taint (`T-N5`) and to fence the incoming question.~~
  **Revised 2026-09-13 by `T-N5`, which built the receiving half.** The author
  and its taint already ride `ChatRunPayload.Peer` (`queue.PeerOrigin{AgentID,
  Taint}`). When `Peer` is set, `ChatRunner.Run` already inherits the taint,
  marks `agent`, fences the message under the author's roster name and drops any
  directive. This ticket's part:
  - **Set `Peer`** from the asker's context: `agentscope.AgentID(ctx)` and
    `taint.FromContext(ctx).Carry()`, read at the moment the tool runs, so
    everything the asker had read by then crosses.
  - **Add the participant row id and the depth** to `PeerOrigin`. Extend
    `TestThePeerCarrierHoldsNothingThatDecidesATurn`'s list, with the reason
    beside each.
  - **Skip the input topic classifier on a peer turn**, keyed on `p.Peer != nil`
    and **never** on finding a fence marker in the text — a person can type one
    (`multi-agent.md` §8c).
- **Revised 2026-09-14 by `T-N8`, which built the ledger and left its callers here**
  (`multi-agent.md` §10d):
  - **Ask the ledger before enqueueing.** Call `agentbudget.Conversation.Admit` with the
    asker's `UserMsgID`, the target, the question, and the asker's depth plus one. Wire
    the ledger into the worker's stack — today only `cmd/api` and `cmd/discord` hold one.
  - **What each verdict does:**
    - **Admitted:** enqueue.
    - **A repeat:** return `Verdict.ToolResult`, and enqueue nothing.
    - **Refused, or `ErrLedgerUnavailable`:** return `Verdict.ToolResult`, and write
      `Verdict.Notice` into the room.
  - **Decide how often the notice is written:** once per message, or once per refused
    question. A model that keeps asking after a refusal would otherwise fill the room.
  - **The credit check runs before `Admit`**, so a tenant at zero reads the credit refusal.
  - **The pass, and its transcript marker, are this ticket's now.** A nudged agent with
    nothing to add ends the chain without an answer, and the room shows a settle that reads
    differently from `Notice`'s cap. Design the two markers together.
  - **`T-N8`'s open acceptance items move here:**
    - *a stubbed agent that always nudges terminates, and the room says why*;
    - *exhaustion produces a visible message naming the unasked question*;
    - *a pass renders as a settle*;
    - the two-worker gate.
- The asker's turn ends normally. The answer arrives as a later message in the
  room, attributed to the answerer by `T-N1`, and the human sees both.
- **The asker is not automatically resumed.** v1 does not re-run A when B
  answers. Two reasons: a resume is another turn nobody addressed, and a room
  where the human can read both answers rarely needs the synthesis. If it turns
  out to be needed, it is a ticket, not a flag.
- `propose_action`'s taint gate is unchanged and must stay unchanged: a nudged
  turn that inherited `KindDocument` gates exactly as `T-H9` says.

##### Notes for the implementer
- **Do not add a queue task type.** Decision 4. `chat:run` already carries a
  thread, an agent, a message and retry semantics; a nudge is one more of those.
- **Do not let a nudge set `Directive`.** `T-N5` asserts this; the assertion
  lives there so that this ticket cannot quietly reintroduce it.
- Dedup: asynq's `Unique` is `md5(payload)`, and `BusinessInferPayload`'s
  comment (`queue/tasks.go:77-82`) records what that costs — a per-call field
  silently retires the window. **Do not reach for `Unique` here.** `T-N8`'s
  depth and turn counters are the loop control; a hash window is not.
- Two agents nudging each other in the same room write to one thread
  concurrently. `messages` is append-only and ordered by Postgres's clock
  (`002_threading.up.sql:39`); there is nothing to lock and nothing to serialise.
- **A new tool is a new thing the primary model must call correctly.** The
  2026-09-11 finding behind `T-Q17` was an OpenRouter endpoint returning Kimi's
  native tool-call syntax as plain text, which made the agent silently stop
  answering (`../coverage/provider-routing.md`). `guardrails.CheckToolCallLeak`
  catches the reply; a *nudge* that leaks would look like an agent narrating a
  hand-off that never happened. Add `nudge_agent` to the leak guard's fixtures.

##### Acceptance
- [ ] `nudge_agent` is not in the tool list for an agent with `can_nudge` false,
      nor in a thread with one participant
- [ ] A nudge writes one visible assistant message from the asker and enqueues
      exactly one `chat:run` for the target
- [ ] The target's answer lands in the same thread, attributed to the target
- [ ] Nudging a non-participant, a disabled agent, another company's agent, or
      itself is refused with a sentence naming why, and enqueues nothing
- [ ] The tool is absent from the schema in a room of one **even when
      `can_nudge` is true**, and the single-agent tool list is byte-identical to
      today's
- [ ] A nudge whose pinned participant row is removed between enqueue and run
      does not run
- [ ] The asker's turn does not block on the target's turn — measured, not
      assumed
- [ ] A nudge from a `KindDocument`-tainted turn produces a target turn that
      gates `propose_action` under `T-H9`
- [ ] `make eval` at or above baseline; both rates pasted
- [ ] The leak guard has a `nudge_agent` fixture

##### Gate
`go test ./internal/tools/... -race -v`, then `make check`, then the live arm
from [`../agents/verification.md`](../agents/verification.md) §"Added or changed
an agent tool": a real room, a real question that needs the other agent's
source, and the worker log showing the tool called, the second `chat:run`, and
both answers. **Also the negative arm from that checklist** — an unrelated
question in the same room that must *not* nudge. Costs model spend; if it is not
run in the same sitting it goes to
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2 and
the boxes stay unticked.

##### Out of scope
- Handing the *conversation* over rather than asking a question — `T-N7`.
- Nudging an agent that is not in the room. Deliberate: see decision 7.
- Resuming the asker with the answer.

---

#### `T-N7` `hand_off_to_agent` — "this one isn't mine"
**Repo:** BE · **Size:** 1.0d · **Deps:** `T-N6` · **Priority:** P1
**Migration:** none

##### Why
A nudge and a hand-off look the same and mean opposite things. A nudge says *I
am answering, and I need one fact from you*; the asker keeps the question. A
hand-off says *this is not mine, you take it*; the asker is done. Collapsing
them into one tool produces the failure both ways round — an agent that answers
half a question it should have passed on, and an agent that passes on a question
it should have answered with one lookup.

##### Do
- A sibling tool, deliberately not a boolean parameter on `nudge_agent`. The
  description is where the distinction has to land, because the description is
  the only place the model reads it: two short paragraphs, one example each.
- Gated by the same `can_nudge` flag, the same participant rule, and the same
  `T-N8` budget. It counts against the same conversation ceiling.
- The difference in behaviour: the handed-off turn receives **the user's
  original question**, fenced with the handing agent's note about why it was
  passed on — not the handing agent's paraphrase of the question. A game of
  telephone between two models is one retelling too many.
- The asker's own reply is one sentence saying it passed the question on and to
  whom. It must not also attempt an answer.

##### Notes for the implementer
- Resist the boolean. `nudge_agent(handoff: true)` is one parameter and two
  behaviours, and the model will get it wrong in the direction that costs a turn.
- A hand-off does **not** change `threads.agent_id`. The default speaker is the
  tenant's setting, not a thing a model may edit — decision 2, and the same
  reasoning as `T-S1`'s "scoping is enforced in the tools, not in the prompt".
- Two hand-offs of the same question is a cycle. `T-N8`'s depth counter is what
  stops it; do not add a second mechanism here.

##### Acceptance
- [ ] The handed-off agent receives the user's original wording, fenced, with
      the reason attached
- [ ] The handing agent's reply says it handed off and attempts no answer
- [ ] `threads.agent_id` is unchanged after a hand-off
- [ ] A hand-off counts against the conversation budget
- [ ] A hand-off back to the original agent is refused by the depth counter, not
      by a special case
- [ ] `make eval` at or above baseline

##### Gate
As `T-N6`, plus one live transcript of each: a question that should be handed
off and a question that should not.

##### Out of scope
- Round-robin, voting, or any scheme where more than one agent is asked to
  decide. That is a planner, and it is `backlog.md`'s item.

---

#### `T-N8` The conversation budget and the loop guard · **ledger built 2026-09-14, unit-gated; pass, room notice and two-worker arm moved to `T-N6` — `coverage/multi-agent.md` §10**
**Repo:** BE · **Size:** 1.5d · **Deps:** `T-N3` · **Priority:** P0 — **not cuttable**
**Migration:** none — correct

> **What moved against this ticket ([`../coverage/multi-agent.md`](../coverage/multi-agent.md) §10d):**
> - **The watermark below cannot fire.** Every turn here is queued because a new message
>   arrived for it, so an agent's context has always changed when it is queued. Built
>   instead: the same agent asked the same question twice in one conversation is queued
>   once.
> - **The pass, the room notice and the two-worker gate have no caller until `T-N6`**, and
>   moved there with their acceptance items. `Verdict.Notice` builds the sentence;
>   `agentbudget.Conversation.Admit` is what `T-N6` calls.
> - **A person's own fan-out is counted and never refused**, which the ticket did not say.
>   A loop guard is for turns nobody addressed.
> - **Per deployment, not per company.** *Do* and the acceptance say per company, and *Out
>   of scope* excludes it; the narrower was built.
> - **`CONVERSATION_WALL` is `CONVERSATION_WALL_SECS`**, the house idiom.
> - **The metric is counted where asks run — the worker, which has no exposition endpoint
>   yet** (`T-17`).

##### Why
`agentbudget` bounds one turn: 8 iterations, 12 tool calls, a token ceiling and
a wall clock (`internal/agentbudget/budget.go:87-90`). **Nothing bounds a
conversation**, and until `T-N6` that was fine, because one user message was one
turn. With nudging, A→B→A is three turns from one sentence, each starting a
fresh budget believing it is alone. The ceiling that matters moves up a level.

This is also where §2c's honesty lives: the numbers below are **placeholders
chosen from arithmetic, not from measurement**, and this ticket's job includes
saying so in the config comments.

##### Do
- `agentbudget.Conversation`, keyed on the originating user message id — which
  every turn in the fan-out already carries as `ChatRunPayload.UserMsgID`
  (`queue/tasks.go:216`), because `T-N3` appends the user message once. **Redis,
  not memory**: turns run in `cmd/worker`, which is horizontally scaled, and an
  in-process counter is a limit that stops working the day a second replica
  starts.
- Three dimensions, three keys, **per-company configuration with a deployment
  default from the first commit** — not literals (decision 9, and §2d's third
  "do not copy": Hermes' equivalents are five `const`s with two open issues
  asking for configurability). Each carries the comment §2c's measurement is
  owed against:
  - `CONVERSATION_MAX_AGENT_TURNS`, default **6**. Four participants addressed
    at once is four, leaving two nudges. Arithmetic, not measurement.
  - `CONVERSATION_MAX_NUDGE_DEPTH`, default **2** — the same number Hermes
    arrived at (`GROUP_CHAT_MAX_CONTINUATIONS`, `group-chat.ts:1210`), and it is
    **separate from the turn ceiling on purpose**: their comment records a
    review finding that a pathological mention chain must not be able to consume
    the room's whole budget on handoffs. A asks B, B asks C, C answers; a third
    hop is where a room stops being legible to the person reading it.
  - `CONVERSATION_WALL`, default **5m**, from the first turn of the fan-out.
- **The unit is turns enqueued, not messages appended** (decision 9). A message
  counter does not bound model calls — Hermes' 10-message cap sits over a
  possible 18 member turns because a pass is free of it. The counter is
  incremented **at enqueue**, not at run: a refusal that arrives after the work
  was queued has refused nothing.
- **A settle is not an exhaustion, and the room must say which.** Add a `pass`
  outcome: a nudged agent with nothing to add ends the chain without publishing
  an answer, and the room shows a quiet marker rather than a limit notice.
  Hermes' normal termination is a silent round with the caps as backstop
  (`hosted_room_discussion.py:646-647`), and they added the *settled* vs
  *capped* distinction after the fact with the reason left in the code
  (`group-rounds.ts:500-503`). Building it in is cheaper than retrofitting it.
- **The watermark, which is free.** Do not enqueue an addressed agent whose
  context has not changed since its own last turn in this thread — same thread,
  no new messages it has not seen. Hermes skips such a member without a model
  call (`hosted_room_discussion.py:639-641`). In a room this fires whenever a
  user addresses the same agent twice in a row with nothing in between.
- Exhaustion is **in-band and visible**: the refusing tool call returns the
  sentence, and a message goes into the room saying the conversation reached its
  limit and which question went unasked. `agentbudget`'s package comment
  (`budget.go:18-20`) is explicit that the model must be told *as a tool result,
  which it reads*, rather than an error, *which it never sees* — and here the
  human needs telling too.
- `T-16`'s per-turn budget is untouched. Two ceilings, two levels, and the
  per-turn one stays where
  [`02-agent-quality-roadmap.md`](02-agent-quality-roadmap.md) left it.
- Metrics: a counter per dimension, so *"how often does a room hit the ceiling"*
  is answerable without adding a log line to read by hand. `internal/metrics`
  already has the shape.

##### Notes for the implementer
- **The defaults are placeholders and must be labelled as such in
  `config.go`.** §2c names the arm that would replace them with measured
  numbers: one room, three agents, five real questions, `usage_events` read per
  agent. Until that runs, a comment saying *"chosen by arithmetic; see
  09-multi-agent-conversations-roadmap.md §2c"* is the honest state, and it is
  the difference between a default somebody can revise and a magic number
  nobody dares touch.
- The credit check is **not** this. `ChatEnqueuer.WithBudget`
  (`chat_enqueuer.go:76`) still runs per enqueue and still refuses a tenant at
  zero. This ceiling is about runaway, not about money, and the two failures
  read differently to a user — do not merge the messages.
- Key expiry: the conversation key outlives the fan-out by exactly `WALL`. A key
  that never expires is a leak; one that expires early is a limit that resets
  mid-loop.
- **Test the limit with a stub that always nudges.** A loop guard whose only
  test is a well-behaved model is a loop guard tested by the absence of the
  problem.

##### Acceptance
- [ ] A stubbed agent that always nudges terminates, and the room says why
- [ ] Depth 3 is refused; depth 2 runs
- [ ] The turn counter is shared across two worker replicas — proven with two
      processes, not one
- [ ] Exhaustion produces a visible message naming the unasked question
- [ ] The per-turn budget is unchanged: an ordinary single-agent turn's iteration
      and tool-call counts are identical before and after this ticket
- [ ] A tenant at zero credit still gets the credit refusal, not this one
- [ ] The conversation key expires
- [ ] A pass ends a chain and renders as a settle, distinguishable in the
      transcript from a cap being hit
- [ ] An agent addressed twice with nothing in between is enqueued once
- [ ] The ceilings are readable and overridable per company, not compiled in

##### Gate
`go test ./internal/agentbudget/... -race -v`, `make check`, and a two-replica
run: start two workers against one Redis, drive the always-nudging stub, show
the counter shared and the ceiling honoured.

##### Out of scope
- Per-tenant overrides of these ceilings. When a tenant asks, it is a settings
  row; today it is a deployment default.

---

### Track E — The other surfaces (4.0d)

#### `T-N9` Group rooms on Slack, Discord and Lark
**Repo:** BE · **Size:** 2.5d · **Deps:** `T-N3` · **Priority:** P1
**Migration:** `*_channel_binding_many`

##### Why
This is the literal reading of *"group chat"*, and it is where a tenant already
has one: a `#ops` Slack channel with people in it. `T-S4` pointed an address at
**one** agent because *"an inbound message carries a user and a room and no place
to put a picker"* (`domain/agent_binding.go:10-14`). In a real group chat there
is a place to put a picker, and it is the same one humans use — the `@`.

It is `P1` and first in the cut order because the dashboard room is the surface
this track has to prove itself on, and this one multiplies the number of places
a bug can appear by three providers.

##### Do
- Migration: drop the unique constraint on `(company_id, channel, external_id)`
  and re-add it as `(company_id, channel, external_id, agent_id)`. **This is a
  constraint change on a live table** — write the `down` first and check it
  against a table that has duplicate addresses in it, because that is the state
  the `down` has to refuse to enter.
- `AgentForChannel` (`domain/agent_binding.go:80`) keeps its signature and its
  meaning — *the default speaker for this address* — and gains a sibling,
  `AgentsForChannel`, returning the participants. One address, one default, many
  members. The existing method's callers do not change, which is what keeps
  every unbound address on today's path.
- Inbound: each provider already strips its own mention syntax. Lark has
  `StripMentions` (`internal/lark/mention.go:28-33`) and `MentionsBot`
  (`:12`); Slack requires `app_mention` in channels
  (`handlers/slack_webhook.go:23`); Discord likewise. **Extend
  `MentionsBot`-shaped logic to answer "which of our bots"**, not "was it one of
  ours". The name matching is `T-N3`'s `ParseAddressing`, given the bound agents
  rather than the thread participants.
- A thread opened from a channel room gets participant rows for the bound agents
  at creation. `LatestForSlackThread` and friends are unchanged.
- **Outbound is where this gets expensive, and it is provider-specific.** One
  Argentum app posting as three agents means either three bot identities or one
  identity that prefixes the agent's name. Slack has `username`/`icon_url`
  overrides on `chat.postMessage`; Discord has webhooks with per-message
  identity; Lark has neither in the same form. **Do not build a per-provider
  identity abstraction.** Prefix the agent's name in the message text as the
  floor, use the provider's override where it exists, and write down per
  provider which one it does — one table in `../coverage/`, not an interface.
- `agent-bindings` settings tab (`handlers/agent_bindings.go`, and its dashboard
  tab) becomes many-per-address with a default marked.

##### Notes for the implementer
- **Read [`../agents/playbooks/add-channel.md`](../agents/playbooks/add-channel.md)
  first.** This is not a new channel, but it touches the parts that playbook
  exists to keep consistent.
- Nudging in a channel room is the same code and a different social contract: a
  Slack channel is full of people who did not ask. Ship it with
  `can_nudge` still defaulting false (decision 8) and let a tenant opt in per
  agent.
- A channel room's budget is `T-N8`'s, unchanged. Three bots in `#ops` answering
  every message is the runaway this feature makes easiest to reach.

##### Acceptance
- [ ] One Slack channel bound to three agents: `@Ops what's stock?` is answered
      by Ops only
- [ ] An unaddressed message in that channel is answered by the default binding
      once — **not three times**
- [ ] An address with exactly one binding behaves byte-identically to today
- [ ] Each reply identifies its agent on all three providers
- [ ] The migration `down` is safe against a table holding several agents per
      address, or refuses with a message saying what to remove first
- [ ] Removing the default binding while others remain is refused

##### Gate
`make check`, the migration round-trip, and one transcript per provider against
a real workspace. Provider transcripts need credentials — whatever is not run
goes to [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md)
§3, the bucket for arms owed on a credential.

##### Out of scope
- WhatsApp. It is 1:1 in this product's model and a group there is a different
  product decision, not a binding change.
- The widget and `/v1`. `T-N10`.

---

#### `T-N10` `/v1`, the widget, the spec and the SDKs · **built 2026-09-14, unit-gated; the live room, the quickstart run and the query on a real Postgres owed — `coverage/multi-agent.md` §12**
**Repo:** BE + PKG · **Size:** 1.5d · **Deps:** `T-N3` · **Priority:** P1
**Migration:** none — correct

> **What moved against this ticket ([`../coverage/multi-agent.md`](../coverage/multi-agent.md) §12d):**
> - **"`POST /v1/threads` accepts `participant_ids`" — there was no `POST /v1/threads`.** `/v1`
>   opened conversations only as a side effect of `POST /v1/chat`. The route was built. Every
>   agent is checked before the row is written, where the dashboard's `POST /api/threads`
>   leaves a thread behind.
> - **"`POST /v1/chat` accepts `agent_id`" — it already did (`T-S5`), meaning the opposite.**
>   With a `thread_id` it had to match the conversation's agent or answer `agent_mismatch`. In
>   a room, a participant's id now names who answers. Anything else is still refused, so a
>   room of one behaves as before.
> - **"A caller reading only `final` still works" stopped being true with `T-N6`.** A colleague
>   asked mid-turn answers on the same channel, under the same job id, often first. The stream
>   would have ended on the colleague's `final`, and the answer lookup could return a room line.
>   Both doors and the lookup are now scoped to the agent asked. The ticket asked for `agent_id`
>   on every frame, which this needed as well.
> - **"The widget gets the label" contradicts "a room is not enabled for widget sessions".**
>   With no rooms, every bubble would carry the same name, and `T-N4`'s rule draws no name then.
>   Not built. What *was* open was a dashboard member adding an agent to a widget conversation
>   by id — company-scoped, so allowed. `ThreadParticipantService.Add` now refuses it.
> - **Not asked for, and built:** `room_event` on a message, because `T-N6`'s settle and limit
>   lines are assistant rows a caller could not otherwise tell from answers. `threads.create` in
>   both SDKs — the hand-written clients, not the generated types.
> - **Found in `T-N2`:** its cap counts the default speaker's seat only when the conversation is
>   pinned. An unpinned dashboard room can hold one more than the ceiling. `/v1` counts the seat
>   either way. The dashboard is not changed here.

##### Why
`T-A4` made a route without an OpenAPI entry a red build **in both directions**,
and `T-S5` put agent selection on `/v1`. A thread that can hold several agents
and a transcript whose messages name their author are both changes to a
published contract; leaving them out does not keep the contract stable, it makes
it wrong.

##### Do
- `GET /v1/threads/:id` returns `participants`. `POST /v1/threads` accepts
  `participant_ids`. `POST /v1/chat` accepts `agent_id` — **explicitly, not by
  parsing `@` out of an API caller's text.** A machine caller has a field; `@`
  is a human affordance and `T-N3` is dashboard-only for exactly this reason.
- Transcript reads (`ListPageByThread`) return `agent_id` and `agent_name` per
  message, from `T-N1`.
- SSE: `agent_id` and `agent_name` on every event, as `T-N1` put them on
  `ChatEvent`. A caller reading only `final` still works and learns nothing —
  `T-Q10`'s property.
- The widget (`apps/widget/`) reads the same events. Attribution is a label on a
  bubble; the widget's Preact UI gets the label and nothing else. **A room is
  not enabled for widget sessions in v1** — a tenant's end customer addressing
  the HR agent is §7's problem arriving through the one surface where the person
  on the other end is not staff.
- `openapi/v1.yaml`, both parity checks, both SDKs regenerated
  (`make openapi`), and the quickstart untouched — it is single-agent and should
  stay the simplest path.

##### Notes for the implementer
- `ThreadFilter`'s isolation rules are unchanged and must stay unchanged
  (`domain/thread.go:89`): `/v1` always sets `Channel: ChannelAPI`, and
  `APIUserRef`/`EmbedUserRef` still separate one end user from another.
  Participants are agents, not people; they touch none of this.
- CI fails on an unregenerated SDK. Run `make openapi` before pushing.

##### Acceptance
- [ ] Every new field is in `v1.yaml` and both parity checks are green
- [ ] Both SDKs regenerate with no hand edits
- [ ] A `/v1` caller that ignores participants sees no change
- [ ] `POST /v1/chat` does **not** parse `@` from message text
- [ ] A widget session cannot create or address a multi-agent thread
- [ ] The quickstart still runs unchanged

##### Gate
`make check && make openapi`, both parity checks pasted, and the quickstart
executed end to end as `T-A4` requires.

##### Out of scope
- MCP (`internal/mcpserver`). Exposing a room to an external agent is a
  different threat model and wants its own ticket.

---

## 5. Cut order

| # | Cut | Saves | What is lost |
| - | --- | ----- | ------------ |
| 1 | `T-N9` | 2.5d | Group rooms on Slack/Discord/Lark. The dashboard room still works, and `agent_channel_bindings` keeps its unique key |
| 2 | `T-N7` | 1.0d | Hand-off. `nudge_agent` covers the common case; an agent that should pass a question on will ask instead, which is worse but not broken |
| 3 | `T-N10` | 1.5d | `/v1` and the widget do not see rooms. They keep working exactly as today, because `T-N1`'s fields are additive |
| — | **Floor** | **13.5d** | The dashboard room, addressing, the trust boundary, nudging, and the conversation budget |

**Two things are never cut: `T-N5` and `T-N8`.** One is the trust boundary and
the other is the loop guard. Shipping `T-N6` without either is shipping a
laundering path and an unbounded spend, and both would be found by a customer
rather than by us.

**And there is a second, smaller product inside this one.** `T-N1` + `T-N2` +
`T-N3` + `T-N4` is **8.0 days** and delivers the whole first half of the
request: several agents in one room, the user addressing whichever they mean,
every answer attributed. No agent talks to any agent, so `T-N5`, `T-N6`, `T-N7`
and `T-N8` are all unnecessary. If the ask turns out to be *"I want Ops and
Finance in one conversation"* rather than *"I want them to consult each
other"*, that is the whole build, and it carries none of this track's risk.
**That is the version to ship first**, and §8's recommendation says so.

---

## 6. What needs no ticket

Worth writing down, because each one looks like a ticket until you check:

- **Per-agent cost in a room.** `usage_events.agent_id` and
  `idx_usage_events_company_agent` already exist
  (`031_thread_agent.up.sql:30-34`). *"What did the Finance agent cost in this
  room"* is a `WHERE` clause, not a feature.
- **Per-agent audit in a room.** `agent_actions.agent_id`, same migration
  (`:24`), written by the decorator `T-05` put over the whole registry. Every
  tool call in a room is already attributed.
- **Credit enforcement across N turns.** `ChatEnqueuer.WithBudget`
  (`chat_enqueuer.go:76`) runs per enqueue. `T-N3` inherits it by enqueueing N
  times. The only rule is that `T-N8` must not bypass it — stated in that
  ticket's *Notes*.
- **Per-agent scoping in a room.** `agentscope.Scope` is installed per turn from
  the agent row (`chat_runner.go:783`) and enforced in `tools.ResolveSource`.
  Two agents in one room reach two different sets of sources because they are
  two turns. Nothing to add; this is `T-S1`'s locked decision 4 paying off.
- **Thread summaries and titles.** `UpdateSummary` is per thread and a room is
  one thread. A summary of a multi-agent conversation is the same summary.
- **Retention.** `retention:purge` (`queue/tasks.go:58-62`) walks messages by
  thread. Participants cascade on thread delete by construction.

---

## 7. What this makes urgent that was already deferred

**Per-agent user grants** — [`backlog.md`](backlog.md), "Platform depth",
deferred 2026-07-29, estimated 2d.

`T-S1`'s locked decision 1 is that an agent is **not** an access boundary: the
Finance agent physically cannot query the HR source, but any company member can
open it and ask what it can reach. That decision came with an obligation, stated
in the same paragraph and again in `domain/agent.go:14-22`: **the dashboard must
say so out loud**, rather than let a customer infer otherwise from an agent
named "HR".

A room does not change that fact and it changes how the fact reads. Four agents
named Marketing, Ops, HR and Finance, in one pane, answering the same person, is
a picture of an org chart. A person looking at it will conclude the boundary is
real, and the conclusion will be wrong.

**This is not a blocker for `T-N1`→`T-N4`**, and this document does not turn a
deferred backlog item into a dependency by assertion. What it does record:

- `backlog.md`'s trigger for grants is *"the first tenant who puts genuinely
  sensitive data behind an agent"*. A room makes that tenant more likely, not
  less, because a room is the reason to create the HR agent in the first place.
- `T-N4`'s copy has to carry the same sentence the roster's does, in the
  participant bar rather than in a settings page nobody reopens.
- `T-N10` refuses multi-agent threads for widget sessions, and the reason is
  exactly this: a widget session's user is the tenant's *customer*, not their
  staff, and that is the one surface where the missing boundary is not merely
  visible but reachable by someone outside the company.

If grants land first, nothing here changes except that the sentence gets
shorter. If they do not, this section is what a reviewer is owed.

---

## 8. The case against doing this now, stated fairly

**What is ahead of it.** Five open security tickets — `T-H4` step 2, `T-H6`,
`T-H11`, `T-H12`, `T-H14` — and `T-G7`→`T-G10` planned-not-scheduled on the
carousel track. [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md)
holds arms owed on three separate things (a credential, a permission, model
spend), and `00-sprint-overview.md` §9e's closing observation was that the
cheapest thing in this project is running a gate you already wrote. **This
roadmap adds 18.5 days of build and at least three more owed live arms.**

**The demand is not evidenced in this repository.** The roster track could point
at a stated customer need — *"the customer has four jobs and one agent"*
(`domain/agent.go:10-13`) — and this one points at a feature request. No usage
data was gathered for this document: nobody has measured how many tenants have
more than one agent, how often a user opens two conversations about one
question, or whether the roster is used at all beyond its backfilled default.
**That measurement is cheap** — it is a query against `agents` and
`conversation_threads` on the production control plane — and it would settle
whether §5's 8.0-day version is worth building before any of this is.

**It multiplies the thing tenants notice.** Credit enforcement exists because
spend is what customers complain about. This track's core mechanic is *one
message, N model turns*, and §2c admits the per-room cost has not been measured.

**It sharpens a gap that is still open.** §7.

**One thing has gotten stronger since this was first written.** The design is no
longer only argued from first principles: Hermes Agent ships this feature and
independently reached four of the six locked decisions, with production-revised
constants for the fifth (§2d,
[`../research/06-hermes-multi-agent.md`](../research/06-hermes-multi-agent.md)).
That lowers the design risk materially. It does not lower the cost, it does not
supply the demand evidence, and Hermes has not measured the per-room cost
either — so none of the three arguments above is answered by it.

**The case for, stated just as plainly.** The roster shipped a product surface
whose unit is one thread, one agent. A user who wants Ops and Finance on the
same question today opens two conversations and carries numbers between them by
hand — which is a structural claim about the product, readable from
`031_thread_agent.up.sql:17` and `agent-picker.tsx:15`, and not a measured one.
That is the failure the roster was built to remove, relocated rather than fixed.
And `T-N1` — *which agent said this* — is a gap in the record that exists today,
in a product that already attributes tool calls and money per agent and does not
attribute words.

**The recommendation.** Run the two cheap measurements first — the roster-usage
query, and §2c's three-agent room — then build **`T-N1` on its own** (1.5d,
useful regardless, closes a real gap in the record), then decide between §5's
8.0-day room and the full 18.5-day track on what those numbers say. Do not start
at `T-N6`.

---

## Appendix A — The addressing grammar

`T-N3` implements exactly this and nothing more:

| Input | Resolves to | Why |
| --- | --- | --- |
| `what happened to margin?` | the default speaker (`threads.agent_id`) | Today's behaviour, byte-identical payload |
| `@Finance what happened to margin?` | Finance, one turn | The ordinary case |
| `@Finance @Ops what happened?` | two turns, one user message, one `UserMsgID` | Order of addressing is enqueue order; display order is `created_at` |
| `@finance …` | Finance | Case-insensitive |
| `@Finance Team …` where two agents are `Finance` and `Finance Team` | `Finance Team` | Longest name first |
| `@all what do you each make of this?` | every participant, one turn each | Reserved handle (decision 3). `@everyone` is its synonym |
| `@Legal …` where Legal is on the roster but not in the room | **refused**, nothing enqueued | Decision: a message must not silently enlarge the room |
| `@notanagent …` | the default speaker, `@notanagent` left in the text | It is somebody's handle, not addressing |
| `email me at @finance.example.com` | the default speaker | No participant named `finance.example.com`; falls through as text |
| `@Finance` alone | Finance, and `trivialReply` does not fire — there is no message | Refuse an empty message, as today |

Two rules the table encodes and the implementation must keep:

1. **The `@` tokens are stripped from what the model sees and kept in what the
   database stores.** `lark.StripMentions` (`internal/lark/mention.go:28-33`) is
   the prior art and its comment is the reason.
2. **An unrecognised `@` is not an error.** It is text. The only refusal is a
   name that matches a roster agent who is not in this room, and that one is
   loud because it is the one a user can act on.

---

## Appendix B — What a room's transcript looks like

The shape `T-N1` and `T-N6` produce together, as stored:

| role | agent_id | content |
| --- | --- | --- |
| `user` | — | `@Ops we're short on SKU 4471, what happened?` |
| `assistant` | Ops | *"Stock shows 0 since Tuesday. The receiving side is clean, so this is either a sales spike or a goods-in that never posted. The purchase ledger is Finance's source — asking them."* |
| `assistant` | Ops | *(nudge)* `→ Finance: was there a goods-in posted for SKU 4471 after Monday?` |
| `assistant` | Finance | *"One GRN on Tuesday, 200 units, still unposted — it's sitting in the approval queue since 14:02."* |
| `user` | — | `@Ops @Finance anything else?` |
| `assistant` | Ops | *(pass — nothing to add; rendered as a settle, not as a limit)* |
| `user` | — | `@Finance who has to approve it?` |
| `assistant` | Finance | *"…"* |

Four properties to hold on to, each of which is a decision from §3:

- **Every assistant row names its author** (`T-N1`). Without it this table is
  unreadable, and so is the version of it that `hydrateMemory` replays into the
  next turn's context.
- **The nudge is a row.** Decision 6. It is not a hidden call, and the person
  reading the room can see why Finance spoke.
- **Finance's turn is an ordinary turn.** Decision 4. Its own scope, its own
  sources, its own budget, its own audit rows — a `chat:run` identical in shape
  to one queued today.
- **Ops is not resumed.** `T-N6`'s *Do*. The human reads both answers; nothing
  runs a third turn to synthesise them for free.
