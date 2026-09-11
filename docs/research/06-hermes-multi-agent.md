# Hermes Agent's group chat — who speaks, what stops it, and the guard that isn't there

Written 2026-09-11 against `NousResearch/hermes-agent` at tag `v2026.9.7`
(= release "Hermes Agent v0.21.1", commit `2237be35`, MIT) — the same pin as
[`05-hermes-self-learning.md`](05-hermes-self-learning.md). Every repository
claim carries its file and line; every external claim carries its source URL in
Appendix A, with the unverified ones flagged there.

> **The request.** Argentum is planning multi-agent conversations
> ([`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)):
> several tenant-created agents in one room, addressed by `@name`, able to nudge
> each other. Hermes ships that today — "Bot Mode", a roster of named agents,
> group rooms, and `hermes peer` bot-to-bot DMs. Find out how it actually works,
> in code, and above all **what stops A→B→A forever**.
>
> **The one-sentence finding.** Hermes reaches every one of our locked decisions
> independently — deterministic `@name` routing with **no router model**, one
> addressed agent = one ordinary turn, visible-in-the-room nudges, hard
> conversation-level caps — and then stops one decision short: **a peer agent's
> words are not fenced, not labelled untrusted, and not sanitized anywhere**, so
> the only thing standing between a poisoned document and a colleague's context
> is a bullet point in a prompt asking the model to behave.

---

## 1. A Bot is a profile; a "society" is a directory of them

There is no multi-agent primitive in Hermes. A Bot **is** a profile, and the docs
say so in a tip box: "isolated config, memory, skills, credentials, and chat
history under `~/.hermes/profiles/<name>/`. Bot Mode is a UI over that primitive"
(`website/docs/user-guide/bot-mode.md:12-14`). The CLI parity table at
`:262-269` is the proof: `hermes -p <bot> chat` opens the same agent the roster
row opens.

**What a profile isolates** is set by one environment variable. `HERMES_HOME`
points at `~/.hermes/profiles/<name>/`, and "since 119+ files in the codebase
resolve paths via `get_hermes_home()`, Hermes state automatically scopes to the
profile's directory — config, sessions, memory, skills, state database, gateway
PID, logs, and cron jobs" (`website/docs/user-guide/profiles.md:300`). This is
the same isolation unit §5 of the self-learning study found for skills and
memory: a directory and a process, not a row and a `WHERE` clause.

**What is shared** is narrower and more interesting than the marketing:

- **OAuth credentials, deliberately.** `--clone-all` drops OAuth rows from a
  clone because single-use refresh tokens mean "a copy of one is not a second
  credential, it is the same credential with two owners"; every profile reads the
  login from the root `~/.hermes/auth.json` (`profiles.md:67-68`).
- **Code and bundled skills.** `hermes update` pulls once and syncs bundled
  skills to all profiles (`profiles.md:232`).
- **The room log itself.** This one crosses the isolation boundary on purpose.
  `default_db_path()` walks *up* out of the profile —
  `(home.parent.parent if home.parent.name == "profiles" else home) / "state.db"`
  (`gateway/hosted_rooms.py:398-402`) — so every room's event log lives in the
  install-root database, shared by all profile gateways. The shared-store
  connector says as much: "Multiple profile gateways share this database"
  (`gateway/hosted_rooms_common.py:105-113`).

The docs are blunt that profiles are the isolation mechanism and that violating
it corrupts state: "Never point two agent processes at the same profile … Both
write memory automatically, and each loads the other's writes into its system
prompt at session start" (`profiles.md:14`).

A "society" is therefore configured by three things and no schema: a directory
of profiles; per-room membership stored in each Bot's profile metadata
(`bot-mode.md:89`); and, for cross-machine peers, a `bot_peers` map in
`config.yaml` with the key in `~/.hermes/.env` as `HERMES_PEER_<NAME>_KEY`
(`bot-mode.md:169`; `hermes_cli/subcommands/peer.py:32-50`). `bot_peers` has no
entry in `config_defaults.py`, and `_load_peers` returns `{}` when it is absent
(`peer.py:36-42`) — **peering is off until a human runs `hermes peer add`.**

---

## 2. Who speaks is decided by a regex, and the code says so out loud

There are two implementations of the round loop, sharing one behavioural model.
The Desktop plugin drives local rooms
(`apps/desktop/src/plugins/hermes-bots/group-rounds.ts`); the gateway drives
hosted rooms with a pure, replayable policy
(`gateway/hosted_room_discussion.py`). The TypeScript file states the design in a
comment worth quoting whole (`group-rounds.ts:29-41`):

```
// Behavioral model (clean-room): a group conversation is ONE ordered room log
// owned by the plugin. A user send triggers at most GROUP_CHAT_MAX_ROUNDS
// serial round-robin rounds over the member roster — never parallel, no LLM
// router. Who speaks each round is a deterministic @mention parse since the
// last user message (mentioned members only, else everyone); whether a member
// actually speaks is its own turn's choice — replying with exactly "(pass)"
// (or nothing, or failing) is silence.
```

**The parse is a regex against a frozen roster.** `_MENTION_RE` is
`@([A-Za-z0-9][A-Za-z0-9._:-]*)` (`hosted_room_discussion.py:37`);
`resolve_mentions` casefolds each capture and looks it up in a dict keyed by
member handle (`:291-308`). `@all` and `@everyone` are the only special tokens
(`:300-301`). An unknown handle — an email address, a stray `@` — matches
nothing and is ignored. There is no model call anywhere on this path.

**A message that addresses nobody is answered by everybody.** `resolve_mentions`
takes `default_all: bool = True`, and with no recognised mention returns the full
roster (`:305-307`). The docs confirm the user-visible behaviour: "@-mentioned
Bots respond (everyone responds when nobody is mentioned)"
(`bot-mode.md:97`). **This is where Hermes differs from Argentum's decision 2**,
which routes an unaddressed message to a single default speaker.

**Later rounds are opt-in and narrower.** The round loop is thirty lines and
carries its own explanation (`hosted_room_discussion.py:625-649`):

```python
for round_index in range(MAX_DISCUSSION_ROUNDS):
    # The user's message selects the first round, with no mention meaning
    # everyone. Later rounds are opt-in: only a peer explicitly cited by a
    # Bot and not heard from afterward gets another turn.
    responders = (
        resolve_mentions((str(discussion.payload["text"]),), room.members) if round_index == 0
        else _unaddressed_member_mentions(discussion_messages, room))
```

`_unaddressed_member_mentions` (`:310-326`) walks the room log, records who each
member cited and when each member last posted, and returns only members whose
citation is newer than their last post — with `default_all=False`, so a reply
citing nobody pulls nobody in, and with an explicit `member.member_id !=
speaker_id` check so **a bot cannot re-elect itself by saying its own name**
(`:324`).

Responder order rotates by round (`_rotate`, `:487-490`) so the same member does
not always speak first.

**Speaking is optional.** `is_pass_text` treats empty text, `pass`, `(pass)` and
`(pass).` as silence (`:285-288`), and a passing turn publishes **no room
message** at all (`_settled_effects`, `:713-720`). In the Desktop path a *failed*
turn is also a pass: `reply = null // a failed turn is a pass, never a room
error` (`group-rounds.ts:665`).

---

## 3. `message_agent` and `hermes peer` — the DM path

Group rooms are one of two channels. The other is a direct message, and its
shape is different in every respect that matters.

**The tool.** `message_agent(target, message)` is injected, never registered:
`ensure_message_agent_tool` appends the schema to the agent's tool list at
prompt-build time (`tools/bot_mode_dm.py:128-151`) only when
`message_agent_authorized` passes (`:111-125`), which requires (a)
`agent.bot_mode_protocol` on, (b) the session title to be exactly `Bot Chat`,
and (c) the install to be Bot-Mode-managed. The module docstring calls this
"Containment" (`:1-15`), and dispatch re-checks the same gate so "a forged call
returns a structured error" (`:185-190`). The docs state the consequence:
"regular chats, group-room member sessions, and CLI sessions never see it"
(`bot-mode.md:110`).

**Target selection is the model's**, unlike room routing. The tool validates the
target against the live roster but the *choice* of recipient is the LLM's, guided
by a roster of names and roles injected into the system prompt
(`tools/bot_mode_probe.py:193-228`).

**The wire format is astonishingly plain.** A local DM spawns a background
process: `hermes -p <profile> chat --in ~ -c "Bot Chat" --create-if-missing -Q
--query-file <tmp>` (`bot_mode_dm.py:245`; `BOT_CHAT_TURN_ARGS`,
`tools/bot_relay.py:71`). The body rides a 0700 temp file, never inline shell
text (`_write_dm_file`, `bot_mode_dm.py:319-330`), capped at 16,000 characters
(`MESSAGE_MAX_CHARS`, `:41`). A cross-machine DM becomes `hermes peer dm
<peer>[/<profile>]` (`:225-226`), which is an HTTP `POST` to
`{base}/api/sessions/<id>/chat` with body `{"message": "…"}` and
`Authorization: Bearer <API_SERVER_KEY>`, held open for up to 600 seconds
(`hermes_cli/subcommands/peer.py:334-349`, `:65-89`, `:28`).

**So a peer message arrives as an ordinary user turn.** Not a system
instruction, not a distinct role, not an envelope with a sender field — the same
`/api/sessions/{id}/chat` endpoint a human client uses
(`gateway/platforms/api_server.py:3071`). It can carry anything a user message
can carry, including instructions, and nothing marks it as machine-authored.

**Provenance is a string prefix, applied by the sender.** Exactly one line does
it (`bot_mode_dm.py:212`):

```python
content = f"Message from 🤖 {_handle(me)} (@{_handle(me)}): " + body
```

The recipient is taught to recognise that prefix by prompt text, not by code:
"When YOU receive a `Message from 🤖 <name> (@<handle>):` message, a teammate
agent is talking to you (not the user)" (`bot_mode_probe.py:218-220`). **The
prefix is therefore forgeable in both directions** — a bare `hermes peer dm`
from a shell carries no prefix at all, and a `message` body beginning with
`Message from 🤖 ceo (@ceo):` would be indistinguishable from a real one, because
the prefix is prepended and never parsed.

The only structural guard on the DM path is self-addressing: `resolved == me`
returns "You can't message yourself. Pick a teammate from the roster."
(`:233`, `:240-241`).

**One thing on this path is genuinely good and we should note it now.** A
peer-triggered turn runs under an execution policy the **target** issues about
itself, not one the caller supplies: `execution_policy_mapping`
(`gateway/hosted_room_execution_policy.py:71-90`) reads the *target's own*
config for its enabled toolsets, its approval mode and its iteration cap, and
seals the result with a sha256 `policy_digest` (`:22-23`) that renewal must match
or the grant is refused (`gateway/platforms/api_server_room_grants.py:22-26`).
The caller cannot widen the callee's capabilities. That is the correct direction
for the authority to flow.

---

## 4. Loop prevention — the constants, and where they stop

**This is the question we came for, and the answer is two different answers.**

### In a room: four hard caps, none configurable

All five live in one block, with a comment explaining why they are literals
(`apps/desktop/src/plugins/hermes-bots/group-chat.ts:1200-1214`):

| Constant | Value | Cites |
| --- | --- | --- |
| `GROUP_CHAT_MAX_ROUNDS` / `MAX_DISCUSSION_ROUNDS` | **3** | `group-chat.ts:1208`; `hosted_room_discussion.py:26` |
| `GROUP_CHAT_MAX_MESSAGES` / `MAX_DISCUSSION_MESSAGES` | **10** | `group-chat.ts:1211`; `hosted_room_discussion.py:27` |
| `GROUP_CHAT_MAX_CONTINUATIONS` | **2** | `group-chat.ts:1212` |
| `GROUP_CHAT_HISTORY_LIMIT` / `MAX_DISCUSSION_DELTA_LINES` | **24** | `group-chat.ts:1213`; `hosted_room_discussion.py:28` |
| `GROUP_CHAT_MAX_MEMBERS` / `MAX_DISCUSSION_MEMBERS` | **6** (min 2) | `group-chat.ts:1214`; `hosted_room_discussion.py:24-25` |

The comment above them is the honest part (`group-chat.ts:1200-1207`): "Every
ceiling a single user send can spend, in one block on purpose: making them
configurable (per room, or model-aware from config.yaml) is live contributor work
— #92213 (per-room limits) and #96842 (config + token budget) — and both need
exactly one seam to hook." **Today they are compile-time constants with no
override.**

Three further mechanisms do real work beyond the counters:

1. **The settle rule.** A round in which every selected member passed ends the
   drive: `return decide("settled", "silent_round")` when no member message
   carries this round's index (`hosted_room_discussion.py:646-647`). The room
   stops because nobody had anything to say, not because a budget ran out.
2. **The watermark.** Each member holds a per-`(thread, member)` sequence
   watermark (`_effective_watermarks`, `:594-603`). A member with no new messages
   since its last turn is **skipped without a model call**: `if not any(watermark
   < event.seq <= seen_through_seq …): continue` (`:639-641`). This is the dedup,
   and it is what makes "cited later still gets the full delta" work.
3. **The terminal set.** `(round_index, member_id)` pairs that already produced a
   terminal event are skipped (`:635-637`), so **one member gets at most one turn
   per round** — restart-safe, because it is derived from the durable log rather
   than from memory.

The independence of the continuation cap is itself a review finding, recorded in
the source: "#94478 review: continuation rounds are bounded independently of the
message cap so a pathological mention chain can't consume the room's whole budget
on handoffs" (`group-chat.ts:1210`).

### On the DM path: nothing. Prompt text only.

`hermes peer dm` and `message_agent` have **no depth cap, no hop counter, no
turn budget, no cooldown and no dedup.** Grepping the whole DM path
(`tools/bot_mode_dm.py`, `tools/bot_relay.py`, `tools/bot_mode_probe.py`) for
loop, recursion, depth, hop or ping-pong returns two comments about a "runaway
paste" and one prompt sentence. That sentence is the entire mechanism
(`bot_mode_probe.py:218-222`):

> When YOU receive a "Message from 🤖 <name> (@<handle>):" message, a teammate
> agent is talking to you (not the user): address them, reply concisely via
> `message_agent` to their handle, and **if it is a pure FYI with nothing to add,
> staying silent is fine — never ping-pong acknowledgements.**

The same section asks the model not to fan out: "Message ONE clearly relevant
teammate; don't fan out to several unless the user explicitly asked"
(`:215-217`), repeated verbatim in the tool description (`bot_mode_dm.py:75-77`).
Both are requests, not constraints.

The only things that in practice bound a DM cascade are incidental: delivery is
fire-and-forget so nothing blocks (`bot_mode_dm.py:1-15`), a busy target refuses
with `target_busy` derived from `SESSION_NOT_OWNED` (`bot-mode.md:125-128`), and
a failed delivery turn "is retried at most once, and only when a retry can
actually help" (`bot-mode.md:130`). None of those is a loop guard; two agents
that keep replying to each other will keep replying to each other.

---

## 5. Shared context: one room log, per-member private sessions, bounded deltas

The room is one ordered event log (`gateway/hosted_rooms.py`, capped at
`MAX_EVENTS_PER_ROOM = 50_000`, `:39`). **No member ever sees it whole.**

Each member runs its turn in **its own persistent session**, titled `Group:
<room_id>` with `source = 'bot_room'`
(`gateway/platforms/api_server_room_dispatch.py:18-44`;
`tui_gateway/hosted_room_driver.py:906`) — "Each member keeps its own persistent
`Group: <name>` session, so room context survives like any other conversation"
(`bot-mode.md:100`). The turn is submitted through the ordinary
`prompt.submit` path (`tui_gateway/methods_prompt.py:195-203`), so the room's
text lands as a **user message** in that private session.

What that user message contains is the **delta since this member's watermark**,
last 24 lines (`_build_prompt`, `hosted_room_discussion.py:507-538`), each line
attributed:

```
[Discussion: "<name>"] You are @<handle>, one participant with @a, @b and the user.

New messages in this thread since your last turn (oldest first):
  User (user): …
  @finance: …

Rules for this Discussion:
- Reply with one conversational message only when you have something new worth adding.
- If you have nothing new to add, reply with exactly "(pass)".
- Mention a teammate by handle to pull them into the next round; do not repeat points already made.
- Never reveal content from private conversations. Your reply is published verbatim.
```

Attribution exists at two levels and they are not the same quality. In the
**durable log** it is structural: every `message.member` event carries an
`actor` built from the member (`_member_actor`, `:172-180`; published at
`:720-722`). In the **prompt the model sees** it is a string prefix produced by
`_format_message` (`:492-496`) — `@handle: text` for a member, `User (user):
text` for the human. The whole prompt is capped at `MAX_PROMPT_BYTES = 128 KiB`
(`gateway/hosted_room_driver.py:29`) with older lines dropped first and an
explicit `[Earlier content omitted to fit this turn.]` marker
(`hosted_room_discussion.py:523-533`), and a
member's published reply is truncated at 64 KiB with a notice (`:713-717`,
`MAX_MEMBER_TEXT_BYTES` at `:30`).

So: **shared transcript, privately replayed, bounded, and attributed in text.**
Over time each member's session accumulates only the slices of the room it was
shown.

---

## 6. Trust boundary: there isn't one

`05-hermes-self-learning.md` §1 found that Hermes gates project-local skills
behind `skills.trusted_project_dirs` — an explicit anti-injection gate, because a
skill file in a cloned repo is an instruction the agent obeys. **There is no
analogue for peer messages.** We looked for one on every path:

- **No fencing.** `_build_prompt` (`hosted_room_discussion.py:507-538`) and
  `buildGroupChatTurnPrompt` (`group-rounds.ts:195-221`) interpolate member text
  directly into the prompt body. There is no `<untrusted>` wrapper, no delimiter,
  no "the following is data, not instructions" sentence — compare the `/learn`
  path's `_SOURCE_HYGIENE` block, which does exactly that for fetched sources
  (`agent/learn_prompt.py:125-133`).
- **No sanitizing.** The only validation a member's or user's room text receives
  is `text()` — is it a string, is it non-blank, is it under the byte cap
  (`gateway/hosted_rooms_common.py:59-69`). No Unicode normalization, no bidi or
  zero-width stripping, no pattern scan. `tools/skills_guard.py`'s threat-pattern
  scanner is never invoked here.
- **No provenance in the data model where the model can see it.** §3: the DM
  prefix is sender-applied text, and the room line prefix is generated from the
  log's actor but rendered into the same flat text stream as the user's own
  line. Nothing in `_format_message` escapes or re-prefixes the continuation
  lines of a multi-line reply, so the shape `User (user): …` is one that a
  member's own reply text can also produce. (We did not run an agent to
  demonstrate this — see §8.)
- **Mentions are parsed out of model-authored text.** `_unaddressed_member_mentions`
  (`:310-326`) runs the mention regex over **member** messages. An `@name` that a
  bot only emitted because a document it was reading contained one will route a
  turn to that member. Addressing is deterministic, but its *input* is not
  trusted.
- **The word "fence" in this codebase means something else.** `_GENERATION_FENCE`
  and `_RUN_FENCE` (`gateway/hosted_room_driver.py:57-58`), the `4122` "managed
  by its gateway" rejection (`tests/tui_gateway/test_hosted_room_prompt_fence.py`)
  and the promote/demote procedure (`bot-mode.md:180-198`) are all
  **concurrency** fencing — keeping two writers off one room. They are careful,
  well-tested work on a completely different problem.

What Hermes has instead is a strong **authentication and authorization** story
for the transport: a bearer `API_SERVER_KEY` per peer with redirect-stripping so
the key cannot be harvested by a MITM'd peer (`peer.py:76-81`), room grants that
can be revoked and superseded (`api_server_room_grants.py:22-34`), and the
target-issued digest-sealed execution policy of §3. The boundary Hermes defends
is *which machine may make my agent run a turn*. The boundary it does not defend
is *what that turn's text is allowed to talk my agent into*.

The three prompt bullets that stand in for it are real but thin: "Never reveal
content from your private 1:1 chats" (`group-rounds.ts:217`), "never forward the
user's words verbatim, and never reveal private 1:1 chat content"
(`bot_mode_probe.py:211-213`), and the pass rule. They protect the *user's*
privacy from the room. Nothing protects the *agent* from the room.

---

## 7. Cost: nobody has measured it, and the caps do not bound model calls

**Hermes publishes no figures.** `bot-mode.md` mentions tokens exactly once, and
it is about OAuth pools (`:51`). There is no benchmark, no telemetry field and no
issue with numbers in it.

What the constants bound is derivable. For one human message in an N-bot room:

- **Round 0** selects the mentioned members, or all N if nobody was mentioned
  (`hosted_room_discussion.py:631-633`). N ≤ 6.
- **Rounds 1–2** select only cited-and-unanswered members (`:633`).
- A member gets at most one turn per round (`:635-637`) and is skipped entirely
  if it has no new delta (`:639-641`).

So the ceiling on **member turns** is 3 × 6 = **18**, and the ceiling on
**published messages** is 10. Those are not the same number, and the difference
is the trap: **a pass costs a full model call and does not increment the message
counter.** In the Desktop path `posted += 1` sits inside `if (reply !== null &&
!isGroupPassText(reply))` (`group-rounds.ts:711-733`); in the gateway path a
passing turn returns no `EventPlan` (`:718-719`). A room of six where everyone
passes politely burns eighteen turns and publishes nothing.

And each of those turns is itself unbounded by default. The room turn's
iteration cap comes from the target's own `agent.max_turns`
(`hosted_room_execution_policy.py:89`), whose default is `None`, which
`resolve_turn_limit` maps to `TURN_LIMIT_UNLIMITED = sys.maxsize`
(`hermes_cli/config.py:1836`, `:1843-1847`). The config comment states the
reasoning: "Turn cap. null = unlimited (default; caps caused silent mid-task
truncation)" (`hermes_cli/config_defaults.py:49-52`).

**That is the mirror image of Argentum.** Hermes bounds the conversation and
leaves the turn unbounded; we bound the turn (8 iterations / 12 tool calls,
`internal/agentbudget/budget.go:87-90`) and leave the conversation unbounded.
Neither half is sufficient alone, and Hermes reached the conclusion our decision
9 reached, from the opposite direction.

---

## 8. What we could not verify

- **Any cost figure at all.** No tokens-per-room number is published, measured
  or inferable from the repo (§7). Our plan's §2c owes exactly this measurement
  and Hermes cannot supply it; the ceilings above are arithmetic, not
  observation.
- **Whether the `User (user):` line prefix is actually spoofable in practice.**
  The rendering code applies no escaping (`_format_message`,
  `hosted_room_discussion.py:492-496`) and the text validator applies none either
  (`hosted_rooms_common.py:59-69`), which is what we assert. We did not run an
  agent to produce a reply that forges the prefix, so the *exploitability* is
  inferred from the code, not demonstrated.
- **Whether the DM cascade actually loops in the wild.** We established there is
  no code-level guard (§4). We did not run two agents to see how often the prompt
  guidance holds. Nous publishes nothing on this.
- **Issue numbers cited in source comments** (#92213, #96842, #94478, #93129,
  #91583, #93091) are referenced in the code but we read them only as they appear
  there, not on the tracker.
- **The needs-you / `@user` escalation path.** We confirmed the prompt rule
  (`group-rounds.ts:216`) and the store (`$groupNeedsYou`, `:977-980`) but did
  not trace how a bot's `@user` sets the badge; `@user` is not a member handle,
  so `resolve_mentions` ignores it on the gateway path.
- **We did not review** the ACP adapter, the Honcho memory plugin's cross-agent
  "peers" concept, or the hosted-room replication/promotion machinery beyond
  establishing that it is concurrency control rather than trust control.
- **We did not read the SEO domains** (hermes-agent.org / .io / .ai,
  hermesagent.agency, hermesagents.net). Nothing in this document comes from
  them.

---

## 9. What this means for Argentum

### The headline: our locked decisions are mostly Hermes' decisions

Four of the six read like independent rediscovery, which is the strongest
evidence a plan can get.

**Decision 3 — deterministic `@name`, no router LLM. Hermes agrees, emphatically.**
`group-rounds.ts:33-35` says "never parallel, **no LLM router**" in a comment,
and the implementation is a regex against a frozen roster
(`hosted_room_discussion.py:37`, `:291-308`). A shipped product with 244k stars
runs group rooms on a 40-character regex. The one place a model *does* pick a
target is the DM tool, where the recipient is the LLM's choice
(`bot_mode_dm.py:174-245`) — and that is precisely the path with no loop guard,
which is not a coincidence worth ignoring.

**Where Hermes is better than our plan here**: two details we should take.
`@all`/`@everyone` as reserved handles (`:300-301`) is free and obvious and our
plan does not mention it. And a **frozen roster per turn** — `DiscussionRoom` is
an immutable validated projection (`:87-94`) and every task identity embeds a
`_member_digest` of the member it was planned for (`:482-485`), so a roster
change mid-fan-out cannot silently redirect a queued turn. Our decision 7 says an
agent may nudge only a current participant; Hermes additionally pins *which*
participant, at plan time, cryptographically. `T-N4` should do the same with the
participant row id.

**Where our plan is better**: an unaddressed message. Hermes answers it with
**everyone** (`default_all=True`, `:305-307`; `bot-mode.md:97`), which is the
single largest cost multiplier in their design and the reason their pass rule
has to exist. Our decision 2 — unaddressed routes to the default speaker — is
strictly cheaper and strictly more predictable. Keep it.

**Decision 4 — one addressed agent = one turn = one ordinary queue task. Hermes
agrees.** A member turn is an ordinary `prompt.submit` into an ordinary session
(`tui_gateway/methods_prompt.py:195-203`) and the room orchestrator is a **pure
function** that returns *at most one* next task from a replay of the durable log
(`plan_next_task`, `hosted_room_discussion.py:605-650`). There is no fan-out
inside the agent runner anywhere in this codebase. Our §2a argument — that a room
is N ordinary turns and therefore every guardrail keeps working unmodified — is
the same argument, and Hermes' version is stronger in one respect worth copying:
because the planner is pure and the log is durable, a restart **reconstructs** the
in-flight task and verifies it byte-for-byte (`reconstruct_task_plan`,
`:653-692`). Our `chat:run` payloads are already durable in the queue; the
cheap version of this for `T-N4` is that the next-speaker decision be a pure
function of `(thread_participants, messages)` with no state in the runner.

**Decision 6 — a nudge is a visible message in the room. Hermes agrees for
rooms, and violates it for DMs.** In a room, a member's reply is published as a
`message.member` event in the shared log before its terminal event
(`:713-722`, and the comment at `:580-583` about the crash gap). In a DM, the
message is delivered into the *recipient's private* Bot Chat
(`bot_mode_dm.py:245`) and the user of the sending conversation sees only what
the sender chooses to relay: "the reply arrives later as a background-process
completion notification that wakes you; **relay it to the user then**"
(`bot_mode_probe.py:208-210`). That is precisely the hidden side channel our
decision 6 forbids, and the docs' own framing of the cost — a cross-machine DM
"works while a Desktop that knows both connections is running"
(`bot-mode.md:142`) — shows how much machinery a side channel needs. **Hold
decision 6.** Hermes shows what the alternative looks like and it is worse.

**Decision 9 — the conversation gets a budget. Hermes agrees, and got there
first.** Their room budget is four constants in one block (§4) with a comment
naming it "Every ceiling a single user send can spend"
(`group-chat.ts:1200-1207`). Three specific lessons for `T-N8`:

1. **Count turns, not messages.** Their 10-message cap does not bound model
   calls because a pass is free of it (§7). Our ceiling must be on *agent turns
   enqueued*, which is a thing we can count before spending money, not on
   messages appended.
2. **Bound the handoff chain separately from the message budget.** Their
   `GROUP_CHAT_MAX_CONTINUATIONS = 2` exists because a review found that "a
   pathological mention chain can't consume the room's whole budget on handoffs"
   (`group-chat.ts:1210`). Our decision 9 lists "max agent turns per user
   message, max nudge depth, and a wall clock" — the middle one is their
   continuation cap and it is load-bearing, not decorative.
3. **Settle on silence, not only on exhaustion.** `silent_round`
   (`hosted_room_discussion.py:646-647`) is the *normal* termination; the caps are
   the backstop. Argentum has no "pass" concept. A nudged agent that has nothing
   to add should be able to end the chain without publishing, and `T-N8`'s
   in-band exhaustion message should be distinguishable from a settle — Hermes
   added exactly that distinction after the fact and left the reason in the code
   ("'settled' means quiet consensus … 'capped' means a round/message/
   continuation cap forced the exit — the activity feed must tell those apart",
   `group-rounds.ts:500-503`).

Also worth stealing outright: **the watermark**. Each member is skipped without a
model call when it has seen everything already (`:639-641`). In our terms, an
addressed agent whose context has not changed since its last turn should not be
enqueued. That is free money and it is not in the plan.

**Decision 8 — nudging off by default per agent. Hermes disagrees on the
default and agrees on the shape of the containment.** `bot_mode_protocol`
defaults to `True` (`hermes_cli/config_defaults.py:155-156`), so on a
Bot-Mode-managed install every bot can DM every other bot out of the box. But the
capability is scoped by **session**, not by agent: `message_agent` exists only in
a session titled exactly `Bot Chat` on a managed install
(`bot_mode_dm.py:111-125`), re-checked at dispatch so a forged call fails
(`:185-190`), and explicitly absent from group-room member sessions and CLI
sessions (`bot-mode.md:110`). Peering, separately, is off until a human registers
one (`peer.py:36-42`).

**That session axis is additive to ours and we should take both.** A per-agent
`can_nudge` boolean answers "is this agent allowed to nudge"; a conversation-shape
gate answers "is nudging reachable from here at all". `T-N6` should require
**both**: `agents.can_nudge = true` *and* the thread having more than one
participant. An agent with `can_nudge` on, sitting in a room of one, should not
find the tool in its schema — which also keeps the tool list byte-identical for
every single-agent thread and therefore keeps our prompt caching intact, the same
reason Hermes gives for making its gate session-stable (`:128-131`).

### Decision 5 is where Hermes has nothing to teach us, and that is the finding

**Hermes has no opinion on trust, because it has no trust boundary here.** §6:
no fence, no taint, no sanitize, no scan, no data/instruction separation. Peer
text is interpolated straight into the prompt body, and a DM arrives as an
ordinary user turn through the same endpoint a human uses (§3).

It gets away with this for the reason `05-hermes-self-learning.md` §10 already
identified: a single-user agent's threat model is "the user's own machine, the
user's own risk". Every bot in a Hermes society is the same human's bot, on the
same machine, with the same credentials, reading the same files. There is no
tenant to leak across and no untrusted warehouse row arriving every turn.

Our threat model is not that, so **decision 5 stands unmodified and is now better
evidenced**:

- `taint.KindAgent` beside `KindDocument` and `KindData`
  (`internal/taint/taint.go:46-58`) — Hermes has no equivalent because it has no
  taint at all.
- **Delivery through `guardrails.Fence`** (`fence.go:51`), labelled with the
  author's agent name. Hermes' attribution is a text prefix generated into the
  same flat stream as the user's own line (`_format_message`,
  `hosted_room_discussion.py:492-496`), with no escaping — the exact shape our
  fence exists to prevent.
- **Taint inheritance across a nudge** is the decision Hermes most conspicuously
  lacks, and their code shows why it matters. Their mention parser runs over
  **model-authored** text (`_unaddressed_member_mentions:310-326`): a document
  Agent A read can, through A's summary, both name Agent B and supply the content
  B acts on. The laundering path our decision 5 describes is not hypothetical in
  their design; it is a two-hop consequence of routing on model output.
- **A nudge cannot carry a `Directive`.** Hermes' DM *can* carry anything,
  because it is a user turn (§3). Hold the line.

One Hermes idea belongs in `T-N5` even so: the **target-issued execution policy**
(`hosted_room_execution_policy.py:71-90`). The callee's toolsets, approval mode
and iteration cap are resolved from the *callee's own* configuration and sealed
with a digest the caller cannot alter. We already do the equivalent implicitly —
`agentscope.WithScope(ctx, scopeOf(agentRow))` resolves per turn from the agent
row (`chat_runner.go:783`, §2a) — but we should state it as a rule in `T-N5`
rather than leaving it as an emergent property: **a nudge never widens the
recipient's scope, and the recipient's sources, tools and budget are read from
the recipient's row.**

### What not to copy

- **"Unaddressed means everybody."** (`:305-307`) It is the most expensive line
  in their design and the reason they need a pass protocol, a message cap and a
  continuation cap to keep rooms from spinning. Our default speaker is one turn.
- **Prompt text as a loop guard.** "never ping-pong acknowledgements"
  (`bot_mode_probe.py:221-222`) is the entire DM-path defence (§4). We have
  watched a prompt edit fail to do what it said in this repository before
  (`08-social-carousel-roadmap.md` §`T-G1`, the 2026-08-14 edit). Every one of
  `T-N8`'s ceilings must be enforced in code, on the enqueue path, before a token
  is spent.
- **Compile-time caps with no override.** Hermes' room budget is five `const`s
  with two open issues asking for configurability (`group-chat.ts:1200-1207`). A
  multi-tenant product cannot ship a spend ceiling a customer cannot see or
  change; `T-N8`'s ceilings should be per-company configuration with a default,
  from the first commit, not literals we promise to parameterize later.
- **A DM path that bypasses the room.** §3, and decision 6's reasoning.
- **`HERMES_HOME` as the isolation unit.** The same conclusion as
  `05-hermes-self-learning.md` §10, with one new wrinkle: their room log
  deliberately escapes the profile boundary into the install-root `state.db`
  (`gateway/hosted_rooms.py:398-402`). Every shared-state feature eventually needs
  a scope wider than one agent — we already have one (`company_id`), and that is
  the only reason `thread_participants` is a table rather than an architecture.

The short version: **Hermes is a strong independent confirmation of decisions 3,
4, 6, 8 and 9, and supplies concrete numbers and failure modes for all of them.**
On decision 5 it is a worked example of the opposite choice, made by people who
were careful about authentication, careful about concurrency, careful about
credentials — and who then handed a peer agent's prose to a model with a bullet
point for a guardrail. We are multi-tenant; that is the one decision we cannot
borrow.

---

## Appendix A — external facts, with sources

Gathered 2026-09-11 via the GitHub API and a shallow clone at the pinned tag.
Repository claims in the body are cited by file and line and not repeated here.
Official sources unless marked.

- `NousResearch/hermes-agent` — MIT, "The agent that grows with you", **244,301
  stars**, Python. https://github.com/NousResearch/hermes-agent
- Release read here: **Hermes Agent v0.21.1**, tag `v2026.9.7`, published
  2026-09-07T22:17:01Z, commit
  `2237be355906fbe6065ce1815711eee52b2d646e`, target `main`.
  https://api.github.com/repos/NousResearch/hermes-agent/releases/tags/v2026.9.7
  This is the same pin as [`05-hermes-self-learning.md`](05-hermes-self-learning.md),
  deliberately, so the two documents describe one artifact.
- Official docs site: https://hermes-agent.nousresearch.com/docs/ — the pages
  cited (`user-guide/bot-mode`, `user-guide/profiles`) are the in-repo sources
  under `website/docs/`, which is what we read. Bot Mode:
  https://hermes-agent.nousresearch.com/docs/user-guide/bot-mode ; Profiles:
  https://hermes-agent.nousresearch.com/docs/user-guide/profiles
- The tag list at the pinned commit runs `v2026.7.30` → `v2026.9.7`; we checked
  out the newest. `git tag --list | sort -V | tail`.
- Issue numbers appearing in source comments (#92213 per-room limits, #96842
  config + token budget, #94478 continuation bound, #93129 member holds, #93091
  typed failure reasons, #91583 hidden Bot Chat, #93127 stale-turn commit) are
  quoted **as they appear in the code**; we did not open them on the tracker.
  *(unverified against the issue tracker)*
- The SEO/marketing domains (hermes-agent.org, hermes-agent.io, hermes-agent.ai,
  hermesagent.agency, hermesagents.net) were **not consulted**. No claim in this
  document derives from them.
- Argentum cross-references — [`../plan/09-multi-agent-conversations-roadmap.md`](../plan/09-multi-agent-conversations-roadmap.md)
  §3 (decisions) and §2a–2c (what survives a room, and the unmeasured number),
  read at `1d5053c`; [`05-hermes-self-learning.md`](05-hermes-self-learning.md)
  §1, §5 and §10 for the skills trust gate and the `HERMES_HOME` isolation
  finding this document extends.
