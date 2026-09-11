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

> **Status: nothing here is built.** This ticket-ifies a backlog item that has
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
- [ ] An entry naming a route that does not exist fails the test
- [ ] An entry naming a param the route does not declare fails the test
- [ ] A new route with `:id` on a restrictable resource fails the test until it is
      classified or exempted — asserted by adding one in the test
- [ ] An exemption without a comment fails the test
- [ ] The middleware order is asserted: `Auth` → `RequireRole` →
      `RequireCapability` → `RequireResource` → rate limiter, keeping `T-04`'s
      rule that a request a member may not make does not spend their tokens

---

### Track B — The resources (4.0d)

#### `T-Z4` Agents: who may talk to which
**Repo:** BE + FE · **Size:** 2.0d · **Deps:** `T-Z2` · **Migration:** none

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
- [ ] A member with no grant cannot open a restricted agent by id, by default
      resolution, by thread rehydration, by `@`-addressing, or by fork — **five
      tests, one per seam**, and the fifth is the one a route check would miss
- [ ] An existing thread whose agent later became restricted refuses on the next
      turn and says why; the transcript stays readable
- [ ] The picker omits what the caller may not reach, and the count in the UI
      matches
- [ ] A room refuses to add a participant the caller may not talk to
- [ ] Every existing single-agent deployment behaves identically — no agent is
      restricted after `084`

---

#### `T-Z5` Dashboards: who may open which
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-Z3` · **Migration:** none

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
- [ ] A restricted dashboard 403s on read and on `/data` without a grant
- [ ] The list omits it
- [ ] Minting a share on a restricted dashboard is refused, and the error names
      the reason
- [ ] Restricting a dashboard that already has a live share **revokes the share**
      and says so in the confirmation — the ordering nobody would test for
- [ ] `created_by` confers nothing (`056:50` — provenance, not ownership)

---

#### `T-Z6` The "etc": data sources and documents
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-Z3` · **Migration:** none

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
- [ ] A restricted connection disappears from the source list and 403s by id
- [ ] An agent's ability to query that source is **unchanged** — the assertion
      that proves the two concepts did not get conflated
- [ ] `search_documents` returns no passage from a document the caller may not
      read, and the turn does not reveal that it exists
- [ ] A turn on a channel or `/v1`, where there is no caller, is unaffected —
      decision 7, asserted rather than assumed

---

### Track C — The surfaces, and the doors that have no user (3.0d)

#### `T-Z7` Settings → Team: the access matrix
**Repo:** FE · **Size:** 1.5d · **Deps:** `T-Z1`, `T-Z2` · **Migration:** none

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
- [ ] A key with an allowlist cannot reach an agent outside it; an empty
      allowlist reaches every agent, exactly as today
- [ ] Binding a restricted agent to a channel without the acknowledgement is
      refused; with it, the audit row names the admin
- [ ] Binding a restricted agent to an embed key is refused under every path
      that can create one
- [ ] A watcher whose creator lost their grant is disabled at the next fire, not
      silently skipped, and the notice says why
- [ ] A watcher whose creator was **deleted** is disabled too — the case the
      previous line does not cover

---

### Track D — Proving it (1.5d)

#### `T-Z9` The negative suite, and the row that says a refusal happened
**Repo:** BE · **Size:** 1.5d · **Deps:** all · **Migration:** none

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
- [ ] The cross product runs and every cell has an expected value written down
- [ ] Adding a fifth resource kind requires exactly one new row in the table
- [ ] A refusal increments its counter and writes its audit row
- [ ] A grant change writes an audit row naming both users
- [ ] Filed in the live-gate backlog: two users, one browser, both restricted
      resources — and it needs the stack and nothing else, which is the bucket
      that has paid sixteen out of sixteen

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
