# Who may talk to which agent, open which dashboard, and press which button — `T-Z1` → `T-Z9`

Written 2026-09-11 against `main` @ `7b1a55b`, from the owner's request:

> *"An admin of a company can give specific access — like what agents they can
> talk to, or this voice feature, or dashboards, etc."*

Nine tickets, **~13.0 days — ~10.5 backend, ~2.5 frontend** — across four
tracks. Every repository claim carries its file and line, read at `7b1a55b`.

**Why `Z`.** `grep -rhoE "T-[A-Z]" docs/` finds `A B C D F G H K M N P Q R S U V
W X Y`, and `grep -rhoE "\b[A-Z]-[0-9]+\b"` finds bare findings under `B C E O P
Q S T`. The mnemonics are gone — `A`ccess, `P`ermissions and `G`rants are taken,
and `E`ntitlements collides with the finding `E-5`. `L` and `I` are free and are
rejected anyway: this repo has numeric tickets `T-01`→`T-23`, so `T-L1` and
`T-I1` sit one glyph from `T-11`. `Z` collides with nothing and cannot be
misread.

> **Status, 2026-09-13, after `T-Z9`: `T-Z13` and `T-Z14` are built, `make check` green, unit-gated — §13d's two owner
> decisions, made and closed.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §19 and §20.
>
> - **A public link** to a document made in a conversation with a restricted agent does not open
>   and cannot be minted while that agent is restricted, whoever minted it. Nothing is revoked:
>   re-opening the agent opens the link again, and the share list marks it *paused* meanwhile.
> - **A pending action** raised in a conversation somebody may not read is gone from their
>   approvals list, is not found by id, and cannot be approved or rejected by them — admins
>   included.
>
> **Where §13d's own proposal was not taken:** it suggested revoking links on restrict, `T-Z5`'s
> shape. The owner chose refusing at open, which undoes with the flip that caused it (§19b).
>
> **Still reachable, written down:** a presigned download URL handed out before the restriction
> works until it expires (§19e). The admin's company-wide action ledger still lists every
> proposal (§20d).
>
> **Live, 2026-09-13, on a scratch stack — production runs `1.6.0` without this code:**
> `T-Z14`'s API arm ran exactly as predicted. `T-Z13`'s did not run, because it needs object
> storage the scratch stack lacked. **Owed:** `T-Z13`'s arm, and `T-Z14`'s browser badge and
> waiting case (§7c). The negative suite's table grew by one kind and three
> surfaces, to 41 × 8.

> **Status, 2026-09-13, last: `T-Z9` is built, `make check` green, unit-gated — every ticket in this roadmap is
> built.** No migration. Record: [`../coverage/access-grants.md`](../coverage/access-grants.md)
> §18. One table in `internal/authz/authztest` says what each of 38 surfaces does for an admin and
> a member, granted or not, open or restricted, and probes in four packages hold the product to
> all 304 cells. Every refusal of one object is counted on `argentum_access_refusals_total{kind,
> reason}` and written as an `access.refused` row. Every grant, revoke and flip is written too:
> undone if an opening cannot be recorded, left standing if a closing cannot.
>
> **Where it was wrong:** a door is not an axis — the widget refuses a restricted agent and quotes
> a restricted document — so expectations are per surface (§18b). And "count refusals" had to say
> that a list leaving something out is not one (§18c).
>
> **One finding outside the ticket (§18f):** the worker's and the Discord bot's refusals are
> counted in processes with no `/metrics`, so the series shows the API's refusals and none of the
> tools' or jobs'. It is `T-17`'s standing gap.
>
> **Live, 2026-09-13, on a scratch stack:** refusal arm (1) and change arm (2) ran exactly as
> predicted (§18j). **Owed:** the widget arm and the worker half (§7c). §13d's two owner decisions and
> §17c's open surfaces are now the whole of what stands between the feature row and ✅, beside
> the live arms.

> **Status, 2026-09-13, later: `T-Z6` is built, `make check` green, unit-gated — every kind has a
> switch, and `resourcePending` is empty.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §17. A restricted source leaves a
> member's source list and refuses its three reading routes to anyone not granted it, and no
> agent's reach changes. A restricted document leaves Knowledge, refuses its pages and its tables
> — checked through their document, since a table is served under its own id — and
> `search_documents` quotes nothing from it for a person, answering a named one as if it did not
> exist.
>
> **Two of the ticket's three source surfaces do not exist** (no schema browser, no freshness
> panel), and an admin's source list has to stay whole, or the agent form would untie a restricted
> source from every agent it saves (§17b).
>
> **Still open, written down (§17c):** metrics on a restricted source are listed to members; tables
> published from a restricted document stay queryable by agents with that source.
>
> **Owed:** the stack arms and one model turn (§7c).
>
> **Next on the floor, recommended: `T-Z9`** (the negative suite, and the row that says a refusal
> happened). Every dependency is now built, it is never cut, and its cross-product has three more
> kinds and three more doors to cover than when it was written. §13d's two owner decisions still
> stand.

> **Status, 2026-09-13, after `T-Z12`: `T-Z8` is built, `make check` green, unit-gated — the other three doors are
> decided.** Migration `085`. Record: [`../coverage/access-grants.md`](../coverage/access-grants.md) §16.
> An API key reaches only the agents it lists; the website widget never reaches a restricted
> agent; a channel answers as one only where an admin acknowledged it; a watcher or schedule whose
> creator lost the agent switches itself off at its next fire and says why. Settings states each
> rule beside the switch, which retires cut order row 3's caveat.
>
> **Two findings bigger than the ticket.** Slack has answered no message since 2026-08-08 —
> `Enqueue` never had its arm — and its feature row said ✅; fixed and moved to 🟡 until its gate
> runs. And the widget half could not be built as written: an embed key names no agent (§16c).
>
> **Owed:** `085` and every arm, most without a model key (§7c).
>
> **Next on the floor, recommended: `T-Z6`** (sources and documents) — the last unbuilt
> dependency of `T-Z9`, which is never cut. If the owner takes cut #1 instead, `T-Z9` is next.
> §13d's two owner decisions still stand; `T-W7` claims `086`.

> **Status, 2026-09-12, after `T-Z5`: `T-Z12` is built, `make check` green, unit-gated — an agent
> asked to change a dashboard checks the person's grant.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §15.
> `update_dashboard`'s *"which one?"* list omits a restricted dashboard. One reached
> by id, or as the conversation's own, is refused by name before any edit is read,
> and never swapped for another. A turn with no person asks nothing. Two of
> §14d's claims did not hold, both under `T-Z12` below.
>
> **Owed:** the track's first live arm that needs the worker and a model key: what
> the model does with the refusal ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7c).
>
> **Next on the floor, recommended: `T-Z8`** (the three doors with no person). It
> is now the only way a restricted agent *or* dashboard is still reachable, and
> the card carries its caveat for both. `T-Z6` is cut #1 and adds kinds rather
> than closing doors. `T-Z8` claims `085` at build time — `T-W7` wanted a number
> too. §13d's two owner decisions still stand.

> **Status, 2026-09-12, earlier: `T-Z5` is built, `make check` green, unit-gated — a dashboard can be
> restricted, and a route asks for a grant for the first time.** No migration.
> Record: [`../coverage/access-grants.md`](../coverage/access-grants.md) §14. A
> restricted dashboard `403`s on open, data and links, leaves the dashboards list
> for anyone not granted it, cannot be shared, and takes its live links with it;
> Settings → Team has its switch. Three corrections are under `T-Z5` below.
>
> **One edge found and not closed, recommended next as a small ticket (~0.5d):
> `update_dashboard`** lists and edits restricted dashboards for anyone who asks an
> agent (§14d). The card says so meanwhile.
>
> **Next on the floor:** that fix, then `T-Z6` (sources and documents, cut #1) or
> `T-Z8` (the three doors with no person). §13d's two owner decisions still stand.

> **Status, 2026-09-12, later still: `T-Z11` is built, `make check` green, unit-gated — what a
> restricted agent's conversation produced goes with it.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §13. Written under
> Track D's additions and built on the owner's go-ahead: a generated document is
> omitted from the documents list and not found by its slides, its caption or its
> link routes for anyone who may not read its conversation. Revoking a link is never
> gated.
>
> **Two surfaces found still readable, and both want the owner rather than a ticket
> (§13d):** a share link minted before a restriction still plays, and pending
> actions proposed in a hidden conversation are listed to every member — the second
> is also a question about who may *approve* one.
>
> **Next on the floor is `T-Z5`** (dashboards), which also carries `T-Z3`'s live arm.
> `T-Z9`'s cross-product now needs a conversation row and a document row.

> **Status, 2026-09-12, late: `T-Z7` is built, `make check` green, unit-gated — the mechanism
> has a control.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §12. Settings → Team
> restricts an agent behind a warning that counts and names who loses access, grants
> it from the agent's side or the person's — one read, two renders — and shows an
> admin the agents they themselves are refused.
>
> **Two things it got wrong, and three it hands on.** The ticket said *Repo: FE*,
> but no read answered "one query, two renders": an agent carries no
> `access_mode`, so the matrix needed `GET /api/access/:kind` (§12b). And *"a
> member who lacks a capability sees the control, disabled"* has no control to
> disable — no route asks for a capability (§12d). Handed on: **`T-Z5` and `T-Z6`
> each add their kind to `ENFORCED_KINDS`** in `apps/dashboard/src/features/settings/access.ts`
> in the commit that enforces it, because only agents get a switch until then
> (§12c); **`T-W7` rewrites `voice`'s "does nothing today"** in `CAPABILITY_COPY`
> beside its policy entry, and owns the member's disabled control.
>
> **Next, and not yet a ticket: generated documents.** §11e of the record called
> them the largest remaining gap before there was a switch; now an admin can
> restrict HR with one click and reasonably believe its reports went with it.
> `T-Z5` follows it on the floor's order.

> **Status, 2026-09-12, night: `T-Z10` is built, `make check` green, unit-gated — the hole `T-Z4`
> found is closed.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §11. The owner
> chose §10d's first option: **a conversation is hidden from anyone not granted
> every agent that is or was in it** — its own, its room's, and every agent that
> wrote in it, a third source §10d's own wording missed. Every dashboard route that
> lists, opens, streams or writes into a conversation asks, and answers a hidden
> one as missing. `T-Z10` is written up under Track D below.
>
> **Still company-readable: generated documents** — a report an HR conversation
> produced is listed to every member (§11e). That is the next piece of this rule
> to build, and it is not ticketed yet.
>
> **Next on the floor is `T-Z7`** (Settings → Team, frontend): the whole mechanism
> still has no control an admin can press.

> **Status, 2026-09-12, evening: `T-Z4` is built, `make check` green, unit-gated — the first
> boundary that refuses something.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §10. Nobody
> without a grant — admins included — can talk to a restricted agent from the
> dashboard: not by pick, default, an existing conversation, `@`, or adding it to
> a room. A member is not offered it.
>
> **A hole this roadmap does not close, and it wants the owner before `T-Z7`
> ships a switch: every member can list and read every conversation in the
> company.** `GET /api/threads` is `ListByCompany`, and the thread and message
> reads check only the company — so restricting HR stops a member asking it about
> payroll and does not stop them reading the answer a granted colleague got.
> Three options, and a recommendation (a conversation inherits its agents'
> restriction on read, ~1d), are in §10d. The dashboard's copy says so meanwhile,
> and the feature row stays ❌.
>
> **`forkForAgent` was never a dashboard seam** — only `/v1` and the widget reach
> it, and neither carries a user — so its test asserts the opposite of the other
> four. `T-Z4` decided the six `/api/agents/:id` rows as exemptions and added no
> `resourcePolicy` entry, so `T-Z3`'s live arm moves to `T-Z5`.
>
> **Next on the floor is `T-Z7`** (Settings → Team, frontend), without which
> none of this has a control — once §10d is decided.

> **Status, 2026-09-12, latest: `T-Z3` is built, `make check` green, unit-gated — Track A, the
> mechanism, is complete.** No migration. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §9. **`T-Z4` is
> next** — it is the ticket the backlog's trigger is about, and §9's sequencing
> names it; `T-Z5` and `T-Z6` are unblocked beside it.
>
> **Still nothing enforces a resource grant, and now it is a list.**
> `RequireResource` is on the chain and `resourcePolicy` is empty. A third table,
> `resourcePending`, holds the 27 routes that carry a restrictable id and whose
> decision is `T-Z4`'s, `T-Z5`'s or `T-Z6`'s; the classification test refuses a
> row keyed to a ticket that has shipped, so the list cannot outlive its owners.
> The sentence below saying no seam asks *"until `T-Z3` and `T-Z4`"* was wrong
> about `T-Z3`: it makes no route ask.
>
> **Two things the next tickets must pick up.** `T-Z4`'s *Do* list omits the six
> `/api/agents/:id` routes. And `GET /api/knowledge/tables/:tableId` serves a
> document's extracted table under the table's own id, which no route entry can
> see — **`T-Z6` must resolve table → document, or a restricted document's
> contents stay one URL away.** `T-Z7` still must not ship ahead of `T-Z4`.

> **Status, 2026-09-12, later: `T-Z2` is built too, `make check` green,
> unit-gated.** Migration `084`. Record:
> [`../coverage/access-grants.md`](../coverage/access-grants.md) §8. **`T-Z3` is
> next**; `T-Z4` is unblocked beside it.
>
> **Nothing enforces a resource grant yet.** `internal/authz` decides, and no
> seam asks it until `T-Z3` and `T-Z4`. The admin routes to restrict and grant
> exist, so until then a restricted agent is restricted on paper.
>
> **`T-Z7`'s dependency list is not enough.** It names `T-Z1` and `T-Z2`, both
> now met — so as written it could ship a working open/restricted switch that
> restricts nothing. It should follow `T-Z4`, or render the switch only for the
> kinds whose enforcement has landed.

> **Status, 2026-09-12: `T-Z1` is built, `make check` green, unit-gated.**
> Migration `083`. Record: [`../coverage/access-grants.md`](../coverage/access-grants.md).
> **`T-Z2` is next** — no deps, and `T-Z3`, `T-Z4` and `T-Z7` all wait on it.
>
> **`capabilityPolicy` ships empty, and that is the acceptance, not a gap.** The
> ticket's day-one vocabulary names two things routes already do
> (`export_data`, `approve_actions`), its migration grants nobody anything, and
> its acceptance says every existing route behaves identically. All three hold
> only if nothing is gated yet; gating either route without a backfill would lock
> out everyone who does it today. Whoever adds the first entry owns that
> backfill, and a test pins the three routes so it cannot happen by accident.
>
> **Owed:** `083`'s round-trip, the repository's three statements against a real
> Postgres, and a two-user revoke — none needs money
> ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7c).
> The middleware's live arm cannot run until a route asks for a capability, so
> it is owed by roadmap 11's `T-W7` rather than by this ticket.

> **Status, 2026-09-11: nothing here is built.** This ticket-ifies a backlog item that has
> been filed since 2026-07-29 — *Per-agent user grants*,
> [`backlog.md`](backlog.md) — widened from agents to every resource an admin
> would want to gate, and merged with the capability half that
> [`11-voice-and-exact-computation-roadmap.md`](11-voice-and-exact-computation-roadmap.md)
> `T-W6` was going to build for voice alone.
>
> **`T-W6` is superseded by `T-Z1` and should not be built separately.** Two
> tables expressing the same idea, filled in by two different tickets a week
> apart, is the rot this repository has avoided everywhere else. If Track C of
> roadmap 11 ships first, it depends on `T-Z1`; if this ships first, voice is
> one row in a vocabulary that already exists.

---

## 1. What is true today, and it is less than the dashboard implies

**Company membership is the entire boundary.** The backlog said so in July and
nothing has changed it:

> `T-S1`'s v1 makes company membership the whole boundary: the Finance agent
> cannot query the HR source, but **any member can talk to the Finance agent**.
> […] Until it ships, the dashboard must say plainly that an agent is not an
> access boundary. ([`backlog.md`](backlog.md), *Per-agent user grants*)

The code says the same thing in one line. Every dashboard turn resolves its
agent through one function, and its signature is the finding:

```go
// internal/app/chat_enqueuer.go:397
func (s *ChatEnqueuer) pickAgent(ctx context.Context, companyID, agentID string) (string, error)
```

`companyID` and `agentID`. **There is no user in it.** The only checks are that
the agent exists in the company and is enabled (`:408`, `:418`).

Dashboards are the same shape one level up. `GET /api/dashboards/:id` is
`RoleMember` (`cmd/api/policy.go:306`), and `dashboards.created_by` is
documented as **not** an ownership column:

```sql
-- migrations/control/056_dashboards.up.sql:50
-- Provenance, not ownership: which conversation produced this dashboard, if
-- one did.
```

So today an admin has exactly two settings for everything: *everyone in the
company*, or *nobody* (delete it). The request is for the middle.

### 1a. Two different things are being asked for, and they need two mechanisms

| | Example | Has an object? |
| --- | --- | --- |
| **Capability** | voice, approving an action, exporting data | No. It is a power, not a thing |
| **Resource grant** | the HR agent, the payroll dashboard | Yes, and there are many of them |

Folding these into one table makes `resource_id` nullable, makes every query
ambiguous about what a NULL means, and makes the UI render two unrelated
concepts in one list. They are built as two mechanisms that share one decision
function.

### 1b. The existing vocabulary to copy, not to reinvent

`domain.Scope` (`internal/domain/api_key.go:10`) is already *"one capability an
API key may carry"*, with a **closed** vocabulary, a `Valid()`, a
`NormalizeScopes`, and eleven members. `domain.Capability` is that, for a human
instead of a machine. Copy the shape; do not merge the two, because a key
outlives the person who minted it (decision 9).

---

## 2. The four doors, and only one of them has a user

This is the part that decides the scope, and getting it wrong would ship a
feature that is a boundary on one path and a decoration on three.

```go
// internal/app/chat_enqueuer.go:170
UserID           string // dashboard only
```

That comment is the whole problem in five words.

| Door | Who the caller is | Is there an Argentum user? |
| --- | --- | --- |
| **Dashboard** (`POST /api/chat`) | A member, authenticated by JWT | **Yes** — `ChatInput.UserID` |
| **`/v1`** (`POST /v1/chat`) | An API key, minted by an admin | No. `APIKeyAuth` sets no `user_id`, by design |
| **Channels** (Slack, Discord, Lark, WhatsApp) | A platform identity — `SlackUserID` (`:180`), `DiscordUserID` (`:172`) | No. There is no mapping from a Slack id to a user row |
| **Widget** (embed session) | The tenant's *customer*, on the tenant's website | **No, and deliberately** |

The widget's middleware states its reasoning already, and it generalises to this
whole track:

> **`user_id` and `role` are deliberately absent.** […] an embed session belongs
> to somebody who has no account with us at all. If this middleware set a role,
> `RequireRole` would start admitting visitors of a tenant's website to routes
> the policy table believes are staff-only; if it set a user id, every handler
> that reads one would attribute a stranger's turn to whichever real user that
> id happened to name. (`middleware/embedauth.go:15`)

**So a grant is enforceable on the dashboard and nowhere else without a
decision.** `T-Z8` is that ticket, and decisions 8–11 are those decisions. None
of the three other doors is left implicit, because a door nobody decided about
is a door that is open.

---

## 3. Decisions (locked — do not re-litigate inside the tickets)

**1. Two mechanisms, one decision function.** A capability has no object; a
grant names one. `authz.Decide` is the single place that answers *may this user
do this to this*, and every caller goes through it.

**2. Grant, never deny.** No denylist. A denylist means adding a person to the
company opens everything to them and closes it again one row at a time, and the
failure mode is silent.

**3. A resource is open until it is restricted.** Each restrictable object
carries an `access_mode`: `open` — which is today's behaviour and the migration's
default, so nothing changes for anybody on the day this ships — or `restricted`,
where only granted users may reach it. Without this, making one agent private
means granting forty people to thirty-nine other agents, and nobody does that
twice.

**4. An admin manages grants and does not bypass them.** A rank is not a grant.
An admin can grant themselves, so **this is a boundary against accident and
casual browsing, not against a determined admin** — and the audit row is what
makes that acceptable rather than dishonest. The dashboard says exactly this
sentence; it does not say "secure".

**5. Unlisted stays denied, and the classification test grows an arm.** `T-04`'s
property — *"a route added without a decision fails closed on its first request
rather than shipping open"* (`coverage/rbac.md`) — is the reason this can be
added without a sieve. `TestEveryAuthedRouteIsClassified` gains: **a route that
serves a restrictable resource kind must declare which kind and where the id
is**, or the test fails. A new dashboard route cannot forget.

**6. The check goes at the seam, not at the route.** For agents the seam is
`pickAgent` and its four siblings — `defaultAgent` (`:948`), `agentFor`
(`:934`), `resolveAddressing` (`:692`) and `forkForAgent` (`:790`). `T-N3` added
three paths that select an agent with no route naming it; a route-level check
would have missed all three, and a room is exactly where a misrouted HR answer
would land.

**7. A grant is a property of an Argentum user.** Only the dashboard has one
(§2). Every other door gets an explicit decision below rather than an inherited
one.

**8. On a channel, the channel is the grant — and the admin is told so.** A
binding maps the HR agent to a Discord channel; who may read that channel is the
tenant's Discord ACL, which this product does not have and must not pretend to.
Binding a **restricted** agent to a channel is therefore allowed, requires an
explicit acknowledgement on the form that says *anyone who can post here can use
this agent*, and is recorded on the audit log. Refusing outright would break the
legitimate case — a private `#hr` channel is a real boundary — and allowing it
silently would be a hole.

**9. An API key is a machine, not a person.** It gets an optional agent
allowlist of its own, beside `T-13`'s scopes. It does not borrow a user's
grants, because a key outlives whoever minted it and an inherited grant would
expand when that person's did.

**10. The widget never reaches a restricted resource, and this is the one door
where refusing is right.** An embed session belongs to the tenant's customer
(§2). A restricted agent is not bindable to an embed key — refused at save time,
with the reason — because the person on the other end is not staff and no
acknowledgement from an admin changes that.

**11. A watcher or a scheduled task runs as its creator, re-checked at fire
time.** It stores the user it was created by; when it fires, the grant is
re-evaluated. A revoked grant disables the task and says why. A cron that keeps
working after its author lost access is a hole with a schedule attached.

**12. Every refusal is counted and every grant change is audited.** A grant
nobody can review is a grant nobody can trust, and a refusal nobody counts is a
misconfiguration nobody finds. `T-05`'s audit log is the destination.

**13. The copy changes the day the mechanism ships.** The backlog carries a
standing obligation — *"the dashboard must say plainly that an agent is not an
access boundary"* — and `T-Z4` **replaces** that sentence rather than deleting
it, with decision 4's sentence in its place.

---

## 4. What already exists, and is the reason this is 13 days

| Mechanism | Where | Why this track needs no new version of it |
| --- | --- | --- |
| Access as data, diffable against the router in both directions | `cmd/api/policy.go`, `TestEveryAuthedRouteIsClassified` (`policy_test.go:114`) | The resource declaration is a second table with the same test property |
| A closed capability vocabulary with `Valid()` and a normaliser | `domain.Scope` (`domain/api_key.go:10-113`) | `domain.Capability` is the same file, rewritten for a human |
| One choke point for agent selection | `ChatEnqueuer.pickAgent` (`chat_enqueuer.go:397`) | Decision 6's seam already exists and is already the only way in |
| A caller identity on the turn | `ChatInput.UserID` (`chat_enqueuer.go:170`) | The dashboard path already carries who is asking |
| A middleware that refuses to invent an identity | `EmbedAuth` (`middleware/embedauth.go:15`) | Decision 10 is already half-enforced by a comment and a missing key |
| An audit log with attribution and redaction | `agent_actions` (`T-05`) | Grant changes and refusals are rows, not a new store |
| Per-object sharing with its own table and admin-only writes | `dashboard_shares` (`071`), `policy.go:317` | The precedent that a dashboard already has a per-object access concept |
| A disabled control with a sentence, not a hidden one | The 2026-08-04 decision, [`../coverage/watchers-ui.md`](../coverage/watchers-ui.md) | What a member sees when they lack a grant is already decided |
| Agents shaped for a grants table | `agents`, `agent_sources` (`030`) | The backlog's own note: *"an `agent_grants` table adds no column to either"* |

---

## 5. The tickets

### Track A — The mechanism (4.5d) · do first

#### `T-Z1` A capability is a named power, and a user is granted it
**Repo:** BE + FE · **Size:** 1.5d · **Deps:** none · **Migration:** `083`

> **Supersedes `T-W6`** in [`11-voice-and-exact-computation-roadmap.md`](11-voice-and-exact-computation-roadmap.md).
> That ticket built this table for voice alone; this one builds it once.

> **Built 2026-09-12, as written, with five places the ticket was wrong or
> silent** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §5):
>
> 1. **The vocabulary contradicts "behaves identically".** `approve_actions` and
>    `export_data` name existing routes, and nobody holds anything on day one, so
>    neither route can be gated without a backfill. `capabilityPolicy` is empty.
> 2. **"A short cache" contradicts "the next request"** unless a write clears the
>    cache. It does, with a generation counter against the race; across replicas
>    the bound is the ten-second TTL, and there is one replica today.
> 3. **No cross-company acceptance line.** A foreign key to `users(id)` does not
>    prove the user is in `company_id`; every statement now starts from the
>    company-scoped `users` row.
> 4. **`RequireCapability(capabilityPolicy)` needs a checker** as well — a grant
>    is data, not a claim on the token.
> 5. **`Repo: BE + FE` with no FE work in *Do*.** The frontend half is the
>    generated `Capability` and `CapabilityGrant` types; the surface is `T-Z7`'s.
>
> `Migration: 083` was right.

##### Do
- `083_user_capabilities`: `(company_id, user_id, capability, granted_by,
  granted_at)`, unique on the first three. Additive, no backfill — nobody has
  anything on day one, and decision 3's `open` default is what makes that safe.
- `domain.Capability` as a **closed** set with `Valid()` and a normaliser, the
  shape `domain.Scope` already has. Day-one members: `voice` (roadmap 11),
  `approve_actions`, `export_data`. A typo in a policy entry is a compile error.
- `middleware.RequireCapability(capabilityPolicy)`, composed **after**
  `RequireRole`. A capability only ever *adds* a requirement; it can never admit
  a caller the role table refused.
- Grant and revoke are admin routes; a user reads their own.
- No JWT claim. Capabilities are read per request behind a short cache, because
  a revoke that waits for a token to expire is not a revoke.

##### Acceptance
- [ ] A member without the capability gets 403 on a capability-gated route
- [ ] An **admin** without the capability also gets 403 — decision 4, and this is
      the assertion that proves it was implemented rather than described
- [ ] A revoke takes effect on the next request, with no re-login
- [ ] Granting twice is idempotent, not an error
- [ ] An unknown capability string is refused at the API boundary, not stored
- [ ] Every route that exists today behaves identically — the whole role table
      re-asserted unchanged

---

#### `T-Z2` A resource can be restricted, and a grant is what opens it
**Repo:** BE · **Size:** 2.0d · **Deps:** none · **Migration:** `084`

> **Built 2026-09-12, with the table reshaped and five silences decided**
> ([`../coverage/access-grants.md`](../coverage/access-grants.md) §8d):
>
> 1. **`(resource_kind, resource_id)` cannot carry the foreign key the next
>    bullet demands.** A polymorphic id cascades from nothing. `084` has one
>    typed, cascading column per kind, CHECKs binding them to `resource_kind`,
>    and a partial unique index per kind.
> 2. **No routes were specified, and `T-Z7` is frontend-only.** Five admin
>    routes under `/api/access` and `/api/users/:id/grants`.
> 3. **`Decide` needs an error beside its answer.** A load can fail, and a
>    caller must be able to refuse on "could not check" rather than read it as
>    "refused" or "allowed".
> 4. **"document" named two tables.** It is `source_documents`.
> 5. **Nothing enforces a grant until `T-Z3`/`T-Z4`**, and `T-Z7`'s dependency
>    list does not say so — see the status block.
>
> `Migration: 084` was right.

##### Do
- `084_resource_grants`: `(company_id, user_id, resource_kind, resource_id,
  granted_by, granted_at)`, unique on the first four. Plus `access_mode` on the
  restrictable tables, `NOT NULL DEFAULT 'open'` (decision 3).
- `domain.ResourceKind`, closed: `agent`, `dashboard`, `connection`, `document`.
- `internal/authz`: `Decide(ctx, user, kind, id) (Allowed, Reason)` — one
  function, pure given its loader, exhaustively table-testable. `Reason` is a
  value so a refusal can say *why* without a caller composing a string.
- A batch form, `Visible(ctx, user, kind, ids)`, because the agent picker and
  the dashboard list each ask about N objects and N round trips per page render
  is how this feature becomes the reason the dashboard is slow.
- A grant is dropped when its user or its resource is, by foreign key. A grant
  row that outlives its subject is a permission nobody can see.

##### Acceptance
- [ ] An `open` resource is allowed for every member, with no grant row
- [ ] A `restricted` resource is refused without a grant and allowed with one
- [ ] An admin without a grant is refused on a `restricted` resource (decision 4)
- [ ] Flipping `open` → `restricted` with no grants makes it reachable by nobody,
      including its creator — and the UI warns before the flip, because this is
      the one transition that can lock a company out of its own dashboard
- [ ] Flipping back to `open` restores access without touching grant rows
- [ ] A grant cannot name a resource in another company — asserted, and it is
      the tenant-isolation arm
- [ ] Deleting a user removes their grants; deleting a resource removes its
- [ ] `Visible` over 50 ids issues one query

---

#### `T-Z3` A route that serves a restricted resource has to say so
**Repo:** BE · **Size:** 1.0d · **Deps:** `T-Z2` · **Migration:** none

> **Built 2026-09-12, with a third table and four gaps in the text closed**
> ([`../coverage/access-grants.md`](../coverage/access-grants.md) §9e):
>
> 1. **Two places cannot classify 29 routes honestly.** 27 belong to `T-Z4`–`T-Z6`;
>    exempting them empties the word "exemption", and gating them makes those
>    tickets' decisions and pre-empts the cut order. `resourcePending` keys each
>    to its owner, and the test refuses a ticket that has shipped.
> 2. **A Go comment cannot fail a test.** The exemption's reason is the map's
>    value, and fewer than five words is refused.
> 3. **gin exposes no chain to assert an order against.** The chain became
>    `authedChain(d)`, a slice, and the test reads its links' names.
> 4. **Silent on not-found.** It passes through to the handler, whose own 404
>    stands — no second, differently shaped answer for a cross-tenant probe.
>
> The detector cannot see `GET /api/knowledge/tables/:tableId` — `T-Z6`'s to
> resolve. `Migration: none` was right.

##### Why
This is the ticket that makes the difference between an access model and a
sieve. `T-04` chose a table over per-route middleware for exactly one reason:
*"it cannot be verified […] 'Did we remember to gate the new route?' would be
answerable only by reading a dozen files carefully, forever"*
([`../coverage/rbac.md`](../coverage/rbac.md)). A resource check added by hand
in each handler reintroduces that problem one layer down.

##### Do
- `resourcePolicy`: route pattern → `(ResourceKind, param name)`, beside
  `apiPolicy` in `cmd/api/policy.go`, with its reasoning in the same file.
- `middleware.RequireResource(resourcePolicy, authz)` after `RequireCapability`.
- `TestEveryAuthedRouteIsClassified` gains two arms: every `resourcePolicy` entry
  names a route that exists **and** a param that route actually declares; and
  every route whose path contains a restrictable resource's id param is either in
  `resourcePolicy` or in a short, **commented** exemption list — so an exemption
  is a decision somebody wrote down rather than an omission.

##### Acceptance
- [x] An entry naming a route that does not exist fails the test
- [x] An entry naming a param the route does not declare fails the test
- [x] A new route with `:id` on a restrictable resource fails the test until it is
      classified or exempted — asserted by adding one in the test
- [x] An exemption without a comment fails the test
- [x] The middleware order is asserted: `Auth` → `RequireRole` →
      `RequireCapability` → `RequireResource` → rate limiter, keeping `T-04`'s
      rule that a request a member may not make does not spend their tokens

---

### Track B — The resources (4.0d)

#### `T-Z4` Agents: who may talk to which
**Repo:** BE + FE · **Size:** 2.0d · **Deps:** `T-Z2` · **Migration:** none

> **Built 2026-09-12** ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §10c), with these corrections:
>
> 1. **`forkForAgent` is only reached on `/v1` and the widget**, which have no
>    user. Its test is inverted: a restricted agent is forked to there, and the
>    grant store is never read. `T-Z8`'s.
> 2. **The fall-through must pin**, and an *existing* unpinned conversation whose
>    default became restricted refuses rather than falling through — no agent
>    switches mid-conversation.
> 3. **`GET /api/agents` is Settings → Agents too.** A member is sent what they
>    may use; an admin the whole roster plus `reachable_agent_ids`. Talking is
>    still refused to the admin. The six `/api/agents/:id` routes are exempt on
>    that line: a grant gates talking and being offered; configuring the roster is
>    the role table's.
> 4. **The copy change needed three more clauses** than decision 4's sentence:
>    the dashboard only, no control yet, and conversations stay readable.
> 5. **Not in the ticket and not in the roadmap: conversation reads are
>    company-wide** (§10d). Needs a decision.
>
> `Migration: none` was right.

##### Why
The backlog's trigger, stated in July: *"the first tenant who puts genuinely
sensitive data behind an agent — payroll, personnel, unreleased financials.
Expect it early, because 'HR agent' is one of the four use cases that motivated
the track."*

##### Do
- `authz.Decide` at **five** seams, not at the route (decision 6): `pickAgent`
  (`:397`), `defaultAgent` (`:948`), `agentFor` (`:934`), `resolveAddressing`
  (`:692`) and `forkForAgent` (`:790`).
- `GET /api/agents` returns only what the caller may reach, via `Visible`. An
  agent nobody may talk to is not in their picker — this is the one place
  hiding beats disabling, because a picker is a list of things you can do.
- A **restricted agent's default** is not a default. If the company default is
  restricted and the caller has no grant, resolution falls through to the first
  agent they may use, and to a plain refusal if there is none.
- `T-N3`'s room: a participant the caller may not talk to cannot be added, and a
  message addressing one is refused with the reason rather than silently
  dropped — a silent drop in a room reads as the agent choosing not to answer.
- **Decision 13's copy change**, in the same commit.

##### Acceptance
- [x] A member with no grant cannot open a restricted agent by id, by default
      resolution, by thread rehydration, by `@`-addressing, or by fork — **five
      tests, one per seam**, and the fifth is the one a route check would miss
      *(the fork test is inverted — correction 1 above)*
- [x] An existing thread whose agent later became restricted refuses on the next
      turn and says why; the transcript stays readable
- [ ] The picker omits what the caller may not reach, and the count in the UI
      matches *(the payload and the hook are tested; no browser has seen it)*
- [x] A room refuses to add a participant the caller may not talk to
- [x] Every existing single-agent deployment behaves identically — no agent is
      restricted after `084`

---

#### `T-Z5` Dashboards: who may open which
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-Z3` · **Migration:** none

> **Built 2026-09-12** ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §14c), with these corrections:
>
> 1. **The list is narrowed for admins too, so it cannot name what an admin
>    manages.** A dashboard the admin restricted and is not granted would be a uuid
>    on Settings → Team. `ResourceAccessView` gained `name`, read with the mode.
> 2. **A check at mint and a revoke at restrict race.** Both are transactions on
>    the dashboard's row — the flip first, the mint under `FOR SHARE`.
> 3. **"Says so in the confirmation" needed a count and a confirmation.** There is
>    no dashboard share UI; the count is in Settings → Team's restrict warning, and
>    the mode route answers `200` with `revoked_shares` where it answered `204`.
> 4. **Silent: a link minted by the previous binary mid-deploy.** It does not open
>    a restricted dashboard.
> 5. **"403s" is kept against `T-Z4`'s hide rule** — a dashboard is reached by a
>    link already on somebody's screen (§14b); the list hides.
> 6. **Found, not closed: `update_dashboard`** lists and edits restricted
>    dashboards for anyone who asks an agent (§14d). ~0.5d; the card says so.
>    **Closed by `T-Z12`**, below (§15).
>
> `Migration: none` was right.

##### Do
- `agent`'s sibling for `dashboard`, through `resourcePolicy` rather than by
  hand — `GET /api/dashboards/:id` and `/:id/data` are routes with an id, which
  is exactly what `T-Z3` was built for.
- The list route filters through `Visible`.
- **A share link is not a grant and does not consult one.** `T-D13`'s public
  share is a deliberate, admin-minted, unauthenticated door; a restricted
  dashboard therefore **cannot be shared**, refused at mint time with the reason.
  That is the same argument as decision 10, and `2026-09-03`'s P1 — a share link
  serving a tenant's panel SQL ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1s)
  — is why it is refused rather than warned about.

##### Acceptance
- [x] A restricted dashboard 403s on read and on `/data` without a grant
- [x] The list omits it
- [x] Minting a share on a restricted dashboard is refused, and the error names
      the reason *(the row lock that closes the race has not run against Postgres)*
- [x] Restricting a dashboard that already has a live share **revokes the share**
      and says so in the confirmation — the ordering nobody would test for
      *(the count and the revoke's statement are tested; the transaction is owed)*
- [x] `created_by` confers nothing (`056:50` — provenance, not ownership)

---

#### `T-Z6` The "etc": data sources and documents
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-Z3` · **Migration:** none

> **Built 2026-09-13** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §17b),
> with these corrections:
>
> 1. **Two of the three source surfaces do not exist** — no schema browser, no freshness
>    panel. The member surface is `GET /api/connections`.
> 2. **"403s by id" has no member route**; every connection route with an id is admin. The
>    three that read what is in a source are gated, admins included; nine are exempt.
> 3. **Silent: an admin's source list stays whole.** The agent form saves the full set of
>    ticked sources, so narrowing it would untie a restricted source from every agent saved.
> 4. **A document's tables are served under their own id**, which no route entry can name;
>    the handler resolves them to their document.
> 5. **"Filters at the tool's seam" was silent on how**: a named document is answered as a
>    missing one; hidden passages are replaced from a deeper search only when something was
>    hidden, so a result with nothing restricted is byte-identical.
> 6. **Still open, written down (§17c):** metrics on a restricted source are listed to
>    members; tables published from a restricted document stay queryable by agents with
>    that source.
>
> `Migration: none` was right. `resourcePending` is empty.

##### Do
- `connection` and `document` as restrictable kinds, through the same two
  mechanisms and no new code paths.
- For a **connection** the grant gates the surfaces a member can reach today —
  the source list, the schema browser, `T-F1`'s freshness panel. It does **not**
  gate what an agent may query: that is `agent_sources` and `T-H12`'s allowlist,
  which are a different question with a different answer, and conflating them
  would make a user grant look like a data boundary it is not.
- For a **document**, `T-P9`'s `search_documents` filters to what the *caller*
  may read, at the tool's seam, because a passage quoted into an answer is a
  read.

##### Acceptance
- [x] A restricted connection disappears from the source list and 403s by id
      *(a member's list; no member route carries an id — the three reading routes
      refuse an admin, §17d)*
- [x] An agent's ability to query that source is **unchanged** — the assertion
      that proves the two concepts did not get conflated *(a pin: nothing here
      touches resolution)*
- [x] `search_documents` returns no passage from a document the caller may not
      read, and the turn does not reveal that it exists
- [x] A turn on a channel or `/v1`, where there is no caller, is unaffected —
      decision 7, asserted rather than assumed

*Unit-gated; the statements and every live arm are owed — [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7c.*

---

### Track C — The surfaces, and the doors that have no user (3.0d)

#### `T-Z7` Settings → Team: the access matrix
**Repo:** FE · **Size:** 1.5d · **Deps:** `T-Z1`, `T-Z2` · **Migration:** none

> **Built 2026-09-12, after `T-Z4` and `T-Z10`** ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §12d), with these corrections:
>
> 1. **`Repo: FE` could not meet "one query, two renders".** Agents carry no
>    `access_mode`; the reads that existed were one per agent and one per person.
>    `GET /api/access/:kind` is one statement for a whole kind, admin, exempt.
> 2. **The member's disabled control has nothing to disable.** `capabilityPolicy`
>    is empty and Team is admin-only; the rule is written in `person-access-panel.tsx`,
>    and the control and its screenshot are `T-W7`'s.
> 3. **`Deps` omitted `T-Z4`** — built after it, and a switch is rendered only for
>    agents, the one enforced kind.
> 4. **Silent on who "loses access"**: people who can sign in and hold no grant,
>    admins and the person pressing it included; not pending invitations, not the
>    removed. And "nobody is granted" is worded apart from "0 people".
> 5. **Silent on what a grant changes off this screen**: the admin's own chat
>    picker and the conversation list (`T-Z10`), both refetched.
>
> `Migration: none` was right.

##### Do
- A per-user panel: their role, their capabilities as toggles, and their grants
  grouped by resource kind.
- The mirror view on each resource — *who can reach this agent* — because an
  admin asks the question both ways and only ever gets given one.
- The `open`/`restricted` switch on the resource, with decision 4's sentence
  beside it verbatim and `T-Z2`'s lock-out warning on the flip.
- A member who lacks a capability sees **the control, disabled, with a sentence
  saying who to ask** — the 2026-08-04 decision. A *resource* they lack is
  hidden, not disabled (`T-Z4`'s rule), and the difference is written down in
  the component rather than left to the next person to infer.

##### Acceptance
- [ ] Granting and revoking round-trip without a reload
- [ ] The resource-side view and the user-side view cannot disagree — one query,
      two renders
- [ ] The flip to `restricted` warns, names how many users lose access, and
      requires a confirm
- [ ] An admin can see that they themselves lack a grant — decision 4 is
      invisible unless the UI shows it
- [ ] `pnpm --filter dashboard lint` and `build` clean, plus a harness screenshot
      of the matrix and of a member's disabled control

---

#### `T-Z8` The other three doors, decided rather than inherited
**Repo:** BE + FE · **Size:** 1.5d · **Deps:** `T-Z4` · **Migration:** `085`

> **Built 2026-09-13** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §16c),
> with these corrections:
>
> 1. **"Refused at save time" had nothing to refuse.** An embed key names no agent; the
>    visitor's browser picks. Decision 10 is enforced at the pick, the conversation, the
>    default and the picker instead.
> 2. **An acknowledgement on the form covered only bindings made after a restriction.**
>    Bind while open, then restrict, and nobody was asked. It is stored on the binding
>    (`085`) and checked every turn; restricting silences unacknowledged bindings, the
>    warning counts them, and `PUT /api/agent-bindings/:id/acknowledgement` clears one.
>    Silent: an unbound channel whose default is restricted — refused, not swapped.
> 3. **"Writes an audit row" is the first access change audited at all**; grants are
>    still `T-Z9`'s.
> 4. **Watchers and schedules already stored their creator**, and have no agent column —
>    they run as the default. "Deleted" is usually deactivated, with grant rows surviving,
>    so membership is its own read. Only a restricted agent is checked (decision 3), which
>    is where the acceptance's deleted-creator line applies. There was no notification
>    system: the notice is `disabled_reason` (`085`) on the row, a failed run, an event.
> 5. **`/v1` was silent** on a call naming no agent when the default is not listed (refused
>    before a thread is opened) and on `GET /v1/agents` (narrowed).
> 6. **Found: Slack refused as an unknown channel since 2026-08-08** — a missing case in
>    `Enqueue`. Fixed.
>
> `Migration: 085` was right.

##### Why
§2's table. Three of four doors carry no Argentum user, and a feature that is a
boundary on one path and a decoration on three is worse than no feature — it is
a claim an admin will believe.

##### Do
- **`/v1`** (decision 9): an optional agent allowlist on an API key, beside
  `T-13`'s scopes. `085` adds the column. Empty means every agent, which is
  today's behaviour.
- **Channels** (decision 8): binding a restricted agent requires an explicit
  acknowledgement on the form, worded as the decision words it, and writes an
  audit row naming the admin who acknowledged.
- **Widget** (decision 10): binding a restricted agent to an embed key is
  refused at save time, with the reason.
- **Watchers and scheduled tasks** (decision 11): store the creating user,
  re-check at fire time, disable with a notice on a revoked grant.
- The Settings copy for each says which rule applies, because an admin cannot
  reason about a boundary whose edges are in a roadmap.

##### Acceptance
- [x] A key with an allowlist cannot reach an agent outside it; an empty
      allowlist reaches every agent, exactly as today
- [x] Binding a restricted agent to a channel without the acknowledgement is
      refused; with it, the audit row names the admin
- [x] Binding a restricted agent to an embed key is refused under every path
      that can create one *(no path binds one — every path a widget turn reaches
      an agent by is refused instead, §16e)*
- [x] A watcher whose creator lost their grant is disabled at the next fire, not
      silently skipped, and the notice says why
- [x] A watcher whose creator was **deleted** is disabled too — the case the
      previous line does not cover *(on a restricted agent, §16c item 4)*

*Unit-gated; the SQL and every live arm are owed — [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7c.*

---

### Track D — Proving it (1.5d)

#### `T-Z9` The negative suite, and the row that says a refusal happened
**Repo:** BE · **Size:** 1.5d · **Deps:** all · **Migration:** none

> **Built 2026-09-13** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §18e),
> with these corrections:
>
> 1. **A door is not an axis.** The same door does different things to different kinds, so
>    each of 38 surfaces carries its own typed outcome, with conversation and generated-document
>    rows (§18b).
> 2. **Silent on what a refusal is.** One named object turned away; a list omission, a missing
>    object and a failed check are not counted (§18c).
> 3. **"Kind" needed `conversation`**, and a generated document's refusal is its conversation's.
> 4. **The acceptance audits every refusal, which decision 12 does not ask**; built as written,
>    one insert per refused request (§18i).
> 5. **Silent on capabilities** (`T-Z1` §5 left them here), **on an unwritable row** — an opening
>    is undone, a closing stands — **and on a change that changes nothing** — no row (§18d).
> 6. **"Exactly one new row"** holds for the expectation; the probes are code, in the four
>    packages that own the checks, which is why the table is its own package.
> 7. **Found: refusals in the worker and the Discord bot are counted where nothing scrapes**
>    (§18f).
>
> `Migration: none` was right.

##### Why
An authorisation feature is defined by what it *refuses*. This repository's
record is that the live half finds something in sixteen sittings out of sixteen;
for this track, most of what it would find is reachable by a test, and the ones
that are not are named below.

##### Do
- One table-driven suite over the cross product: `{admin, member} ×
  {granted, not} × {open, restricted} × {agent, dashboard, connection,
  document} × {every door in §2}`. It is a loop, not four hundred hand-written
  tests, and the point is that a new resource kind joins by adding a row.
- A refusal counter per `(kind, reason)` on `/metrics`, so a misconfiguration
  looks like a spike rather than like a quiet week.
- Grant, revoke and `access_mode` changes as audit rows (decision 12).
- **The live arms**, filed rather than claimed: two real users in one company,
  one restricted agent, one restricted dashboard, and a browser — the class of
  defect where the data is right and the *rendering* misstates it has no other
  way to be caught ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1e).

##### Acceptance
- [x] The cross product runs and every cell has an expected value written down
      *(per surface, not per door — correction 1)*
- [x] Adding a fifth resource kind requires exactly one new row in the table
      *(one row of expectation; its probes are code — correction 6)*
- [x] A refusal increments its counter and writes its audit row
- [x] A grant change writes an audit row naming both users *(by id)*
- [x] Filed in the live-gate backlog: two users, one browser, both restricted
      resources — and it needs the stack and nothing else, which is the bucket
      that has paid sixteen out of sixteen

*Unit-gated; the counter and the rows against the stack are owed — [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7c.*

---

### Added 2026-09-12, after `T-Z4`

#### `T-Z10` A conversation is as restricted as the agents in it
**Repo:** BE + FE (copy) · **Size:** 1.0d · **Deps:** `T-Z4` · **Migration:** none

> Not in the roadmap as written. `T-Z4`'s build found that every member can list
> and read every conversation in the company, so restricting an agent stopped
> people *talking* to it and not *reading* what it told somebody who was granted
> it ([`../coverage/access-grants.md`](../coverage/access-grants.md) §10d). Of the
> three options there, the owner chose this one on 2026-09-12. It is cut-proof in
> the same sense `T-Z3` is: without it, `T-Z4` is a boundary with the answers
> left on the other side of it.

##### Why
`GET /api/threads` is `ListByCompany`, and the thread, message, participant and
stream routes check only the company. A payroll answer given to the two people
granted HR is one click away for the other forty.

##### Do
- **The rule:** a person may read a conversation when they may talk to every
  agent that is or was in it — the thread's own agent, its room, and every agent
  that wrote a message in it. A conversation with none of those runs as the
  company default and is judged by it. An agent that no longer exists restricts
  nothing.
- One statement per page for "which agents are in these conversations", and one
  grant read over the union — not a round trip per conversation.
- Every dashboard route that lists or opens a conversation asks: the list, the
  detail, the transcript, delete, the three room routes, the live stream, and the
  two per-conversation usage routes plus the usage list (whose rows carry each
  conversation's title — its first question).
- **Hidden, not refused**: a conversation the person may not read is omitted from
  lists and answered as not found by id — `T-Z4`'s picker rule.
- A send into a conversation the person may not read is refused as not found —
  or its restricted answers are replayed into the next turn's memory.
- Decision 4 holds: an admin without the grant is hidden from too.
- The copy's "conversations stay visible" clause is replaced, not deleted.

##### Acceptance
> **Built 2026-09-12** ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §11). Every box below is unit-gated and was proven failing; none has run
> against Postgres, and the statement behind all of them is owed (§11h).

- [x] A conversation whose own agent, room or history includes a restricted agent
      is absent from the list and 404s on every route by id, for a member and an
      admin without the grant
- [x] A grant makes it reappear; another person's grant does not
- [x] With nothing restricted, every conversation is listed and readable exactly
      as before
- [x] Sending into a hidden conversation is refused before anything is written,
      including when the turn would run as an open agent
- [x] The live stream refuses before the upgrade
- [x] A page of conversations costs a fixed number of loads, asserted
- [x] Out of scope, and written down with a reason: generated documents and
      dashboards a restricted agent produced, scheduled-task run history, and the
      admin-only company-wide reviews (audit log, export, feedback list)

---

### Added 2026-09-12, after `T-Z7`

#### `T-Z11` A generated document is as restricted as the conversation that made it
**Repo:** BE + FE (copy) · **Size:** 0.5d · **Deps:** `T-Z10` · **Migration:** none

> **Built 2026-09-12** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §13).
> One acceptance line was corrected by the build: listing a hidden document's
> links answers the empty list a document nobody shared gets, not a `404`. The
> route never looked a document up, so a `404` would have been the one answer
> that confirmed the id (§13c). `Migration: none` was right.

> Not in the roadmap as written. `T-Z10`'s record named generated documents *"the
> largest remaining gap"* ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §11e), and `T-Z7` made that gap reachable by a click: an admin who restricts HR
> from Settings → Team reasonably believes its reports went with it. Written and
> built on the owner's go-ahead the same day.

##### Why
`GET /api/documents` is `ListByCompany`. A payroll report HR's conversation
produced — presigned download link included — is listed to every member, and a
carousel's slides and caption open by id to anyone holding it. Hiding the
conversation hid the question and left the answer on the documents page.

##### Do
- **The rule is `T-Z10`'s, through the document's `thread_id`:** a person may see a
  generated document when they may read the conversation that produced it. A
  document with no conversation (`POST /v1/reports/render`, 027) restricts
  nothing. `documents.thread_id` cascades on delete (007), so no document outlives
  its conversation to be judged by a missing one.
- One `ConversationAccess.Readable` over a page's conversation ids — not one check
  per document.
- Every dashboard route that lists or opens a generated document asks: the list, a
  carousel's pages, its caption, and listing or minting its share links. **Hidden,
  not refused** — omitted from the list, not found by id.
- **Revoking a share is never gated**, for `resourceExempt`'s reason on the
  dashboard revoke: it can only close a door.
- The copy that says a restricted agent's conversations are hidden says its
  documents are too.

##### Acceptance
- [x] A document from a conversation the person may not read is absent from the
      list and 404s on its pages, its caption, and minting its shares — for a
      member and for an admin without the grant *(listing its shares answers the
      empty list — the correction above)*
- [x] A document with no conversation, and every document when nothing is
      restricted, reads exactly as before
- [x] Revoking a share on a hidden document still works
- [x] A page of documents costs one readability check, asserted
- [x] A check that fails serves nothing: `503`, never the unfiltered list
- [x] Out of scope, written down with a reason: a share link minted before the
      restriction, `/v1/documents`, and pending actions proposed in a hidden
      conversation

---

### Added 2026-09-12, after `T-Z5`

#### `T-Z12` An agent asked to change a dashboard checks the person's grant
**Repo:** BE + FE (copy) · **Size:** 0.5d · **Deps:** `T-Z5` · **Migration:** none

> **Built 2026-09-12** ([`../coverage/access-grants.md`](../coverage/access-grants.md)
> §15), correcting the note it was written from:
>
> 1. **"Its result carries the saved spec, panel SQL included" was wrong.** It
>    never did. What leaked was panel titles and filter names through a bad edit's
>    errors, plus the write — hence the check before the edit is read.
> 2. **"Answer a refused id as not found" was not kept.** Named, for §14b's reason,
>    and because "not found" sends a model to rebuild the dashboard.
> 3. **Silent: a refusal without an `error` key counts as an edit** to `T-Q13`'s
>    evidence check; and **a scheduled task carries its creator**, so it asks as
>    them.
>
> `Migration: none` was right. No prompt or description changed, so no `make eval`.

> Not in the roadmap as written. `T-Z5` found it and did not close it
> ([`../coverage/access-grants.md`](../coverage/access-grants.md) §14d), and
> recommended it next. Written and built by `/continue-building` the same day.

##### Why
`T-Z5` closed a restricted dashboard on every dashboard route and left
`update_dashboard` open. The tool names a company's five most recent dashboards
to anyone who asks without an id, and edits any of them by id. So a person
refused *Payroll* on the dashboards page can ask an agent *"which dashboards are
there?"*, then *"change Payroll"* — and Settings → Team says so in a sentence,
which is a boundary with a caveat printed on it.

##### Do
- The worker has the person on the turn (`tenantctx.UserID`); the tool asks
  `internal/authz` for them, through the same grant store the routes read.
- **The ask list hides**, as the dashboards page does: one `Visible` over the
  company's dashboards, and a restricted one takes none of the five places.
- **A dashboard reached by id, or as the conversation's own, is refused by
  name**, as its routes are (§14b) — a result the model reads, never a Go error,
  and never swapped for an older dashboard from the same conversation.
- The check comes before the edit is read, so a refused dashboard's panel titles
  and filter names cannot come back in an error.
- A turn with no person asks nothing: decision 7, and `T-Z8`'s doors.
- The Settings card's caveat says where the check stops, not that it is missing.

##### Acceptance
- [x] By id and by default, a person not granted a restricted dashboard edits
      nothing, and the answer carries no id, title, panel or filter of it
- [x] The refusal counts as a failed call, so `T-Q13`'s evidence check cannot
      read it as an edit
- [x] The ask list omits what the person may not open, in one load; with nothing
      openable it is the empty workspace's answer, byte for byte
- [x] A granted person edits it; a turn with no person is unchanged and asks
      nothing
- [x] A check that fails edits and lists nothing, and does not hand the storage
      error to the model *(what the model then does with a refusal is owed live —
      §7c)*

---

### Added 2026-09-13, after `T-Z9` — §13d's two owner decisions

Written and built the same day, after the owner answered the two questions
[`../coverage/access-grants.md`](../coverage/access-grants.md) §13d had left open since `T-Z11`.
Neither changes a decision above; each applies decision 10's refusal (a door with no person) or
`T-Z10`'s hiding (a person who may not read the conversation) to a surface that had neither.

#### `T-Z13` A public link does not open what a restricted agent's conversation produced
**Repo:** BE + FE (copy) · **Size:** 0.5d · **Deps:** `T-Z11` · **Migration:** none

> **Built 2026-09-13** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §19). §13d
> proposed revoking every link on restrict; **the owner chose refusing at open and at mint**, which
> is reversible and needs no bulk revoke. `Migration: none` was right.

##### Why
A link minted on HR's payroll carousel before HR was restricted still played at `/share/:token`
for anyone holding it, and a person granted HR could mint a new one: a bearer door out of the
boundary `T-Z11` drew around the conversation's documents.

##### Do
- "Is every agent that is or was in this conversation open?" — asked as nobody, because a link has
  no person. `T-Z10`'s rule otherwise: the default judges an unattributed conversation, a deleted
  agent restricts nothing.
- **Mint:** refused `409` with the reason while the answer is no, whoever asks.
- **Open:** answered exactly as a revoked link, the view not counted or audited.
- **The share list** marks a live link on such a document as paused, so an admin is not shown as
  working a link a visitor is told is gone.
- The restrict copy says links to those documents stop opening.

##### Acceptance
- [x] A link minted before a restriction does not open while the agent is restricted, and opens
      again when it is re-opened — nothing is revoked
- [x] Minting a link on such a document is refused with the reason, for a person granted the agent too
- [x] A document with no conversation, and every document when nothing is restricted, shares as before
- [x] A check that fails mints nothing (`503`), opens nothing, and lists nothing

#### `T-Z14` A pending action is as hidden as the conversation that raised it
**Repo:** BE + FE (copy) · **Size:** 0.5d · **Deps:** `T-Z10` · **Migration:** none

> **Built 2026-09-13** ([`../coverage/access-grants.md`](../coverage/access-grants.md) §20), as the
> owner chose it: hidden, and decided only by people who may read the conversation.
> `Migration: none` was right.

##### Why
`GET /api/actions/pending` listed every proposal in the company — an email body, a caption —
including those raised in a conversation `T-Z10` hid, and `allowed_roles` alone decided who could
approve one. Approving an action without being able to read why it was proposed is deciding blind.

##### Do
- The pending list keeps the proposals whose conversation the person may read, with one
  readability check for the page.
- `GET /api/actions/:id`, `approve` and `reject` answer a hidden proposal with the not-found an
  unknown id gets, **before the role check**, and decide nothing.
- A proposal raised outside any conversation is listed and decided as before.
- Decision 4 holds: an admin not granted the agent is hidden from too, so an admin-only kind raised
  in a conversation no admin is granted waits until one grants themselves — which is audited.
- The negative suite gains a `pending_action` row.

##### Acceptance
- [x] A proposal from a conversation the person may not read is absent from the list and 404s by id,
      on approve and on reject, for a member and an admin — byte-identical to an unknown id
- [x] A readable proposal and one with no conversation are listed and decided as before
- [x] A page of proposals costs one readability check
- [x] A check that fails serves and decides nothing

---

## 6. Cut order

| # | Cut | Saves | What is lost |
| - | --- | ----- | ------------ |
| 1 | `T-Z6` | 1.0d | Sources and documents stay company-wide. Agents and dashboards, which is what was asked for, still work |
| 2 | `T-Z5` | 1.0d | Dashboards stay company-wide |
| 3 | `T-Z8` | 1.5d | **Only if the copy changes with it.** Grants bind the dashboard and nothing else, and Settings must say so in those words |
| — | **Floor** | **9.5d** | `T-Z1`→`T-Z4`, `T-Z7`, `T-Z9`: capabilities, agent grants, the matrix, and the suite that proves the refusals |

**`T-Z3` is never cut.** Without it this is a hand-checked authorisation model,
which is the thing `T-04` chose a table to avoid.

**`T-Z9` is never cut.** An access feature with no negative tests is a claim.

**`T-Z8` may be cut only with its copy change**, and that is the one cut here
with a wrong version: cutting the tickets and keeping the Settings page's
implication is how an admin puts payroll behind an agent that a Slack channel
still answers from.

## 7. What is deliberately not here

- **Row-level data policy** — *a regional manager sees only their region*.
  That is [`backlog.md`](backlog.md)'s own entry, 6 days, and it needs a policy
  model injected into every generated query. **It is a different axis**: this
  track gates *which objects a person can reach*, that one gates *which rows come
  back*. Ordering is not accidental — the backlog notes it is far easier once
  the metric registry is the primary query path, and `T-F4` is the ticket that
  measures whether it is.
- **Groups or teams.** Grants are per user in v1. A tenant with forty people and
  eight agents will ask for groups; a tenant with six will not, and the join
  table above is shaped so a `group_id` alternative to `user_id` is additive.
- **Per-agent model, temperature and budget** ([`backlog.md`](backlog.md)). It
  reads like an access setting and is a cost setting, and it needs an eval run
  per configuration rather than a settings field.
- **SSO group mapping.** Grants driven by an IdP's groups is the enterprise
  shape of this, and it is downstream of SSO, which is itself trigger-gated.
- **A deny rule** (decision 2), in any form, including "temporarily revoke".
  Revoking is deleting a grant.

## 8. What needs no ticket

- **`/v1` scope checks.** `T-13`'s scopes already gate the API surface; `T-Z8`
  adds an agent allowlist and touches nothing else.
- **MCP.** The MCP server exposes tools, not agents, and a tenant's MCP
  credential is a machine credential under decision 9.
- **Capabilities on the report and carousel surfaces.** They are reached through
  routes the role table already gates; a capability there is one row in
  `capabilityPolicy` the day somebody wants it.

## 9. Sequencing, against what is already on the board

`T-Z1` is a dependency of roadmap 11's Track C and supersedes its `T-W6`, so
**if voice is going to be built, this track's first ticket is built either way**
— the question is only whether it is built once or twice.

The honest order across both roadmaps: `T-Z1` → `T-Z2` → `T-Z3` is the
mechanism (4.5 days) and everything else in both documents that touches access
is a caller of it. `T-Z4` is the one the backlog's trigger is actually about,
and it is the two days that turn *"an agent is not an access boundary"* into a
sentence that no longer has to be printed on the page.
