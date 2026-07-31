# The tenant agent roster — T-S1 → T-S5 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Sprint 2 — The agent
roster*. Five tickets, 9.5d, filed 2026-07-29.

This file is the track's record. `T-S1` is written up below; `T-S2`'s gate
names this file too, and each later ticket appends its own section.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-S1` | `agents` + `agent_sources`, CRUD, Settings tab | 2.5d | **done — gate run live 2026-07-30** |
| `T-S2` | Turn composition and enforcement | 2.5d | **done — gate run live 2026-07-30** |
| `T-S3` | Agent picker in the dashboard chat | 1.0d | **done — gate run live 2026-07-30** |
| `T-S4` | Discord / Lark / WhatsApp channel bindings | 2.0d | not started |
| `T-S5` | `agent_id` on `/v1`, plus `GET /v1/agents` | 1.5d | **code complete — live gate outstanding** |

---

## T-S1 · The roster exists

### 0. It landed out of order, and that is worth saying first

[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) schedules this
track for **Sprint 2**, explicitly because inserting it into Sprint 1 *"would
have displaced `T-A5` and overrun"*. `T-A5` has not landed. This ticket did.

Nothing about the code below is worse for it, but the schedule is now a
statement that is not true, and the honest reading is: Sprint 1's last
committed ticket is still open while Sprint 2's first one is code-complete.
Whoever closes the sprint decides whether that is a re-plan or a note.

### 1. What ships

A migration, an entity, a repository, a service, six routes, a settings tab —
and **nothing that reads any of it at turn time**. That separation is the
ticket's whole shape: a roster exists and changes no behaviour until `T-S2`
lands, which keeps a schema, a CRUD surface and a UI out of the ticket that
rewires the agent pipeline.

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/030_agents.{up,down}.sql` |
| Entity | `internal/domain/agent.go` |
| Repository | `internal/adapters/postgres/agent_repo.go` |
| Service | `internal/app/agent_service.go` (+ `_test.go`) |
| Routes | `internal/transport/http/handlers/agents.go`, wire types in `wire.go` |
| Policy | `cmd/api/policy.go` — six new rows |
| Registry | `internal/tools/registry.go` (new, shared with the worker) |
| Dashboard | `apps/dashboard/src/features/settings/agents-tab.tsx` |

Six routes. Reads are `RoleMember` because `T-S3` puts the roster in the chat
picker; every write is `RoleAdmin`, on the line `policy.go` already draws for
connections — an agent's allowlist is *what the agent can reach*.

```
GET    /api/agents              member   roster + this deployment's tool vocabulary
GET    /api/agents/:id          member   404 for another company's id, never 403
POST   /api/agents              admin    first agent for a company becomes its default
PUT    /api/agents/:id          admin    name, description, persona, both allowlists, enabled
DELETE /api/agents/:id          admin    refused for the last agent and for the default
PUT    /api/agents/:id/default  admin    a disabled agent cannot hold the flag
```

### 2. Decisions worth carrying forward

- **The tool vocabulary rides in `GET /api/agents`, not on its own route.** The
  ticket offered `GET /api/agents/tools` *or* folding it into the payload. A
  static `tools` segment beside `GET /api/agents/:id` is a literal competing
  with a wildcard in one gin method tree, and the form reads both in the same
  breath anyway. `api_keys` gets away with `GET /api-keys/scopes` only because
  no `GET` under that prefix takes a parameter — its own comment says so.

- **There is now one tool registry, and both processes build from it.**
  `internal/tools/registry.go` replaced the literal slice in
  `bootstrap/stack.go`; the API calls the same function and reads `Names` off
  it. This is beyond what the ticket asked for, and it is the reason the ticket
  asked for anything: *"the tool checkbox list comes from the live registry,
  not a hardcoded array."* Two lists would have diverged the first time a tool
  was added, and a tool missing from the checkboxes is a capability **no agent
  can ever be given** — a silent ceiling on the feature, discovered by a
  customer. The API constructs the tools and calls none of them, which is a
  handful of pointer copies at boot.

  It also keeps `generate_document` honest: the tool is registered only when
  object storage exists, so the *correct* list is per-deployment, and the
  checkbox now disappears on the same condition the tool does.

- **Signup seeds the new company's first agent.** Not in the ticket's Do list,
  and without it `030`'s backfill would have produced two classes of tenant:
  every company that predates the migration holds a default agent, every
  company created after it holds none. `T-S2` resolves an unspecified thread to
  the company default, so the second class would have had no agent to run at
  all. It is `AgentService.EnsureDefault`, called from `AuthService.Signup`
  through a one-method `RosterSeeder` interface, idempotent, and **logged
  rather than returned on failure** — a signup that fails after the company row
  is written is worse than a tenant who has to click "Create agent" once.

- **`Enabled` is a `*bool` on the input, and this is not fussiness.** A plain
  `bool` would silently disable every agent edited by a client that omitted the
  field. Two rules keep the default runnable: the default cannot be disabled in
  place, and a disabled agent cannot be promoted to default. Either would leave
  a company whose unspecified turns resolve to an agent that will not run.

- **`Update` cannot move `is_default`.** Promotion is `SetDefault`, which
  demotes the previous holder in the same transaction. An `UPDATE` that set the
  flag would hit the partial unique index the moment a second agent claimed it,
  and the error would name an index rather than the operation.

- **Delete refuses rather than promotes.** The last agent cannot go, and the
  default cannot go while another agent exists. Both are `ErrConflict` → 409.
  A delete that silently moved the default would make *"which agent is the
  default now?"* a question answered by a destructive operation.

- **The repository takes `companyID` on every method, including the ones that
  already have a primary key.** `ConnectionRepo.GetByID` does not, and the
  consequence is visible one function down in
  `MetabaseDatabaseIDForSource`, which has to remember the tenant check itself.
  The persona is the tenant's own words about their own business; reading one
  by guessing a uuid is the failure this table cannot afford.

- **The bounds, and why the persona has one.** Name 60, description 240,
  persona 8000 characters. The first two are labels. The third is appended to
  the system prompt on *every turn this agent takes, on every channel*, billed
  to the tenant, with no meter in front of whoever pasted it. 8000 characters
  is roughly 2k tokens — a real briefing, not a handbook.

### 3. The limitation, said out loud

Locked decision 1 makes this an acceptance item rather than copy: **an agent is
not an access boundary.** Company membership still is. The Finance agent
physically cannot query the HR source, but any member can open the Finance
agent and ask it what it can reach.

The Agents tab says so, in the form, above the fields:

> **An agent scopes what it can reach, not who can use it.** Everyone in this
> workspace can open every agent. A Finance agent cannot query the HR database,
> but any member can still open it and ask what it has access to. Per-agent
> permissions are not available yet — keep anything that must stay private out
> of the databases you connect.

An agent named "HR" implies a boundary this version does not draw. A customer
should meet that fact while scoping the agent, not after.

The same decision drives the checkbox groups. An empty allowlist means
**everything**, so an empty group renders `All tools` / `All databases` with the
sentence *"Nothing ticked means every tool this workspace has, including ones
added later"* and a **Use all** button to get back to it. An empty box with no
label reads as the exact opposite of what the backend does with it.

### 4. What is proven, and what is not

| Check | Result |
| ----- | ------ |
| `make check` — vet, lint, race tests, four builds | clean |
| `make types-check` — generated TypeScript matches the Go structs | current |
| `TestEveryAuthedRouteIsClassified` — every new route carries an access decision | passing |
| 12 service tests, `internal/app/agent_service_test.go` | passing |
| **T-S1's live gate** | **not run** |

The twelve tests cover: the first agent becoming default; a scoped agent
round-tripping with its allowlist normalised to registry order and
deduplicated; empty allowlists permitting everything; `"finance"` colliding with
`"Finance"` in one company and not across two; an unknown tool refused **by
name**; a foreign source refused with a message that does not name the owning
company; cross-company `Get`/`Update`/`Delete`/`SetDefault` all answering
`ErrNotFound`; both delete refusals and the delete succeeding once the default
moves; the two rules keeping the default runnable; an update omitting `enabled`
leaving it alone; the four validation bounds; and the seed running once and
surviving a repository failure.

The fake repository reproduces the schema's guarantees rather than the
service's — case-insensitive name uniqueness, one default per company, company
scoping on every read. A fake keyed on the raw name would have let the
`"finance"`/`"Finance"` test pass against a service that has no such rule,
because that rule is a database index.

**The gate ran on 2026-07-30 and the acceptance boxes are ticked.** `030` and
`031` were applied together with `migrate -path migrations/control` against the
`make infra` postgres; the tree went 29 → 31 clean, and the backfill wrote one
unrestricted default `Analyst` for each of the eleven companies that predated
it — including the demo, eval and every ticket-gate tenant. Signup's seed then
did the same for the two created through the API afterwards: 13 companies, 13
defaults, one apiece.

What the live run produced, all against `:8090` on the local control plane:

| Acceptance item | Result |
| --------------- | ------ |
| "Finance" scoped to one source and three tools round-trips through `GET /api/agents` | `allowed_tools=[list_sources,get_schema,run_sql]`, `source_ids=[f74b2c96…]`, three more agents beside it |
| Every pre-existing company has exactly one enabled default, chat unchanged | 11/11 backfilled; the `C-1` question answered **3,863,405,700** — the same figure `T-16` fixed it to, under a scoped agent |
| A member gets 200 on `GET` and 403 on every write | `{"error":"admin only"}` on `POST`, `PUT`, `DELETE` **and** set-default; `GET` 200 |
| Another company's agent by id is 404, not 403 | `{"error":"no such agent"}`, 404 |
| `"finance"` beside `"Finance"` is rejected | 409, `an agent called "finance" already exists` — the index, not the service |
| Deleting the last agent is refused | 409, `a company needs at least one agent` |
| `make types-check` is red if `domain.Agent` changes without regeneration | renaming `allowed_tools` → `allowed_tools_renamed` failed the check (`1 file(s) differ`); reverting made it green |

The member account came through the real invite path — `POST /api/users/invite`,
then `accept-invite` with the returned token — rather than a row edited into
`users`, because "a member cannot write the roster" is a claim about the token a
member actually holds.

### 5. For T-S2

- `domain.Agent.AllowsTool` and `AllowsSource` already exist and are where the
  empty-means-unrestricted rule is written down once. `AllowsSource` is the
  predicate `tools.ResolveSource` is meant to consult — do not restate the rule
  at the call site.
- `AgentRepository` has no `GetDefaultForCompany`. `T-S2` needs one; it was
  left out here because nothing in `T-S1` reads a row at turn time and an
  unused method is a promise about a call path nobody has walked.
- The tool filter in `newAgentFactory` must run over the **already-wrapped**
  slice. `tools.Registry` returns raw tools; `stack.go` wraps them with the
  budget guard and then the audit recorder before the factory sees them.
  Filtering the raw constructors instead would silently drop auditing — the
  exact failure `T-05`'s decorator-over-the-registry shape exists to prevent.
- `agent_sources` rows disappear with their connection (`ON DELETE CASCADE`),
  which *widens* a scoped agent rather than leaving it pointing at a dead id.
  That is the right behaviour and it needs a test in `T-S2` that says so.

---

## T-S2 · One turn, one agent

> **Status 2026-07-30: done, gate run — see §5.** `make check` clean,
> `make types-check` current, 31 new tests. `030` and `031` are applied, the
> `make eval` regression check came back level with `T-16`'s 97.0%, and the same
> question asked of two differently-scoped agents produced one answer and one
> refusal.

### 1. What ships

Migration `031_thread_agent`, one new package, and edits at five points in the
turn. Nothing new is visible to a user: this ticket makes the rows `T-S1`
stored decide a run, and the surfaces that let anyone *choose* an agent are
`T-S3`, `T-S4` and `T-S5`. Until one of those lands every thread's `agent_id`
is NULL and every turn resolves to the company default — which is exactly the
unrestricted agent `030`'s backfill created, so behaviour is unchanged by
design.

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/031_thread_agent.{up,down}.sql` |
| Scope | `internal/agentscope/scope.go` (new package, + test) |
| Resolution | `internal/app/chat_runner.go` — `resolveAgent`, `scopeOf`/`personaOf`/`toolNamesOf` |
| Composition | `internal/bootstrap/stack.go` — `framePersona`, `filterTools` |
| Enforcement | `internal/tools/source_resolve.go`, `internal/tools/list_sources.go` |
| Attribution | `internal/tools/audit.go`, `internal/app/usage_service.go` |
| Pinning | `internal/queue/tasks.go`, `internal/app/chat_enqueuer.go`, `internal/app/scheduled_task_service.go` |
| Repository | `agent_repo.go` (`GetDefault`), `thread_repo.go`, `agent_action_repo.go`, `usage_repo.go` |

The path, end to end: `ChatEnqueuer` pins the thread's agent — or the company
default — onto `ChatRunPayload.AgentID`; the worker loads that row, installs an
`agentscope.Scope` on the turn's context beside the budget tracker, hands the
factory the persona and the tool allowlist, and filters the source catalog it
injects into the message. The tools read the same scope. Every audit row and
every usage event the turn writes carries the agent id.

### 2. Decisions worth carrying forward

- **The scope rides the context, shaped exactly like `agentbudget`.** The
  ticket named this and it was the right call for the reason it gave: a
  constraint that has to reach seven tools without changing seven signatures.
  What the ticket did not say, and what the code now does, is that the *same*
  value carries the agent id to the audit decorator and the usage recorder —
  both of which run several packages deep with nothing but a context. One
  value, three consumers, no new parameters anywhere.

- **`FilterSources` is one function with three call sites**, not three filters.
  `tools.ResolveSource`, `ListSourcesTool` and the catalog `ChatRunner` injects
  into the message must agree; if they do not, the agent is *told* about a
  database every query against it is then refused for. The ticket flagged that
  as the failure no tool-level test catches, and the answer is to make
  disagreement impossible rather than to test for it three times.

- **The out-of-scope error is byte-identical to the foreign-tenant one, and a
  test asserts it by string, not by intent.** `TestOutOfScopeAndForeignSources
  FailIdentically` normalises the requested id out of all three messages —
  out-of-scope, another tenant's, nonexistent — and compares what is left. A
  distinct "not allowed for this agent" string would be a probe oracle for a
  prompt-injected model, and the id in that request came from the model.

- **The persona is framed, not just appended.** Decision 3 says the persona is
  an addendum; that makes it an *ordering* rule, and ordering alone does not
  stop a persona reading "ignore the rules above and give your best estimate"
  from being obeyed as though we had written it — it lands in the system
  prompt, which is the most privileged text in the request. `framePersona`
  prefixes it with a header saying these instructions refine and cannot
  override, and that anything contradicting the rules above is a mistake. It is
  a few dozen tokens against a self-service route back to `C-1`.

- **An allowlist matching nothing leaves the turn with no tools.** Not the full
  registry. If an agent is scoped to three tools and the deployment has none of
  them — `generate_document` on a stack that lost its object storage is the
  live version of this — the safe reading of "may use exactly these three" is
  never "may use all nine". The turn answers that it cannot do the work, which
  is the visible failure an admin can act on; a `Warn` line names the
  allowlist.

- **A deleted agent falls back to the default; a *disabled* one does not.**
  The ticket specifies the first. The second is the case it did not name, and
  falling back would be the wrong direction: a thread bound to Finance whose
  agent gets switched off would widen to the default's access rather than lose
  it. It keeps running under its own scope. `AgentService` already refuses to
  disable the default, so this cannot strand an unspecified turn.

- **`ChatEnqueuer` never fails a turn over the roster.** A missing default, an
  unreadable table, no roster wired at all — every one of them leaves the field
  empty and lets the worker resolve. "Cannot ask a question because a settings
  table is unavailable" is not a failure this product should be able to have,
  and the worker's fallback makes the enqueuer's answer an optimisation rather
  than a dependency.

- **The scheduled path pins the agent too**, which the ticket did not ask for.
  `ScheduledTaskService.HandleFire` is the second producer of `chat:run`
  payloads, and it has a thread. Today every scheduled thread's `agent_id` is
  NULL so this is inert — but the moment `T-S3` sets one, a report scheduled on
  the Finance thread would otherwise have quietly run as the default, with the
  default's wider access. One lookup on a cron tick.

- **The eval tenant now gets a default agent.** `internal/eval/tenant.go`
  creates its company through repositories, so neither `030`'s backfill nor
  signup's seed reaches it — the harness would have scored a turn resolving to
  *no* agent, which is precisely not the regression the gate has to prove.

### 3. Prompt caching regresses, by design

The ticket says so and it is worth restating with the mechanism. Anthropic
caching is keyed on the system-message prefix, and the persona is now part of
it. Distinct personas mean one cache entry per agent, so a rarely-used agent
pays full input on its first turn of each five-minute window.

Two things keep the cost bounded. An agent with an empty persona — the
backfilled default, and every tenant who has not written one — produces a
system prompt byte-identical to today's, so the common case is not affected at
all. And the persona is capped at 8000 characters by `T-S1`, so the worst case
is a few hundred extra tokens on a cache miss.

**The observed cache-hit change is not in this record**, because measuring it
needs a live Anthropic-backed tenant with two personas and a five-minute
window. It belongs to the gate.

### 4. What is proven, and what is not

| Check | Result |
| ----- | ------ |
| `make check` — vet, lint, race tests, four builds | clean |
| `make types-check` — generated TypeScript matches the Go structs | current |
| 31 new tests across five packages | passing |
| **T-S2's live gate**, including `make eval` | **not run** |

Where the 31 sit, and what they hold down:

- `internal/agentscope` (4) — empty allowlist means every source, an absent
  scope restricts nothing, a scope naming a source that is gone filters to
  nothing rather than to everything.
- `internal/tools` (6 + 2) — the scoped agent resolving without naming an id,
  the refusal that does not name what it refused, the byte-identical error, the
  scoped menu, the scoped `list_sources`, the unscoped turn still seeing
  everything; plus the audit row carrying the agent and *not* inventing one for
  a call made outside a turn.
- `internal/bootstrap` (7) — persona appended with the shared prompt still the
  prefix, the frame read before the persona, persona-then-directive ordering,
  empty allowlist giving the whole registry, an allowlisted agent given only
  its tools, the filter preserving the *wrapped* instances by identity, and an
  allowlist matching nothing leaving none.
- `internal/app` (7 + 3 + 2) — payload agent wins, no agent means the default,
  a deleted agent falls back, another company's id resolves to your own
  default, three ways of having no agent all run the turn, the blocked-turn
  audit row taking the id from the context rather than the payload; the
  enqueuer's three cases; and usage events carrying the agent for both a token
  event and a tool event.

The identity assertion in `TestTheFilterPreservesTheWrappedInstances` is the
one worth keeping: `s.Tools` is budget-guarded and audit-wrapped before the
factory sees it, and a filter that rebuilt tools from constructors would
silently drop both — the exact failure `T-05` chose a decorator over the whole
registry to prevent.

**What no test here can establish** is the ticket's first two acceptance items:
a real turn answering from source A and refusing source B. Those are the gate,
and the gate needs a database with `030` and `031` applied.

### 5. The gate, run 2026-07-30

**1. `make eval` — no regression.** The comparable set is the 33 cases `T-16`
scored 97.0% (32/33) on; the golden set is now 35, because `T-A2b` added two.
Against those 33: **32 pass, 1 fail**, and the failure is `ambiguous-headcount` —
the *same* case, failing for the same reason
([`eval-baseline.md`](eval-baseline.md) *The one failure*). **97.0%, unchanged.**

Two caveats, both stated rather than smoothed over:

- The run happened in two parts. The first was stopped by the environment at
  case 33 of 35 (31 pass / 1 fail at that point); the remaining three were run
  with `-only`. Two runs, one score — not what the ticket asked for, and the
  reason is an interrupted process rather than anything the harness found.
- `report-directive-is-not-an-injection` (one of `T-A2b`'s two) **fails in this
  environment and cannot pass here**: it asserts a `generate_document` call, and
  this deployment has no `MINIO_*` configured, so the tool is not in the registry
  at all — `tools:[list_sources, get_schema, run_sql, create_visualization,
  create_dashboard, schedule_task]`. That is an environment gap, not a
  regression, and it is the same gap that leaves `T-A2b`'s own live half open
  ([`api-reports.md`](api-reports.md) §7).

**2. A live transcript, same question, two agents.** *"What were our total sales
last month?"*, asked twice on the same tenant:

| Agent | Scope | Outcome |
| ----- | ----- | ------- |
| `Finance` (`6e99c89d`) | Sales Warehouse | **3,863,405,700** — the `C-1` figure, from `run_sql` against source A |
| `People Ops` (`1f704013`) | HR Warehouse | refused: `source_id "f74b2c96…" not found for this company. Available: 6ce606fb…=HR Warehouse`, then answered what it *could* see and said what it could not |

Then the reciprocal, in the Finance thread: asked directly for the HR source by
id, it got `source_id "6ce606fb…" not found for this company. Available:
f74b2c96…=Sales Warehouse` — the identical sentence, with a one-source menu. No
`run_sql` against B exists in either thread, and `list_sources` returned one
source to each agent.

**3. Distinct `agent_id`s.** Every `agent_actions` and `usage_events` row of both
turns carries its own agent — 7 usage rows on `6e99c89d`, 3 on `1f704013`, and
the tool rows likewise. The remaining two acceptance items ran too:

- **Tool allowlist.** Asked three times over, in one message, to use
  `create_dashboard`, the three-tool Finance agent produced **no
  `create_dashboard` row at all** — the tool was never offered to the model, so
  there was nothing to refuse. That is stronger than a refused call and it is
  what `newAgentFactory`'s filter is for.
- **Delete mid-conversation.** `DELETE /api/agents/{finance}` → 204, the thread's
  `agent_id` went NULL by `ON DELETE SET NULL`, and the next turn in that same
  thread answered under the company default (`d6d2aca8`, `Analyst`) and saw both
  sources again. A conversation did not become unusable because an admin tidied
  the roster, which is what the column's `SET NULL` was chosen for.

### 6. For T-S3, T-S4 and T-S5

- `ChatRunPayload.AgentID` is the field all three set. `T-S3` sets
  `conversation_threads.agent_id` and the enqueuer picks it up with no further
  change; `T-S4` sets the payload field directly from a binding; `T-S5` takes
  it from the request body.
- `domain.ConversationThread.AgentID` already round-trips through the
  repository, and `agent_id` is already in the generated TypeScript. The
  dashboard can read a thread's agent today; nothing writes one.
- `AgentRepository.GetDefault` is the turn-time read. It is one indexed lookup
  on the partial unique index, not a roster listing filtered in Go.
- A disabled agent is still refused promotion to default and still cannot be
  disabled while it holds the flag (`T-S1`). What `T-S3` and `T-S5` must add is
  refusing a disabled agent **at pick time** — the runner deliberately does not
  re-check, because a thread already bound to one keeps its narrower scope.

---

## T-S3 · The picker

> **Status 2026-07-30: code complete, gate outstanding.** `make check` clean,
> `make types-check` current, `make lint-go` at 0 issues, 9 new tests across two
> packages. **No migration** — `031` already added
> `conversation_threads.agent_id` and nothing here needed a column. The gate is
> a screen recording against a live API, and `030`/`031` are still applied to no
> database.

### 1. What ships

The verb `T-S1` and `T-S2` were waiting for. Until this, every dashboard thread
was created with a NULL `agent_id` and every turn resolved the company default,
so a customer with four agents could talk to exactly one of them. Two doors now
accept a pick, and one place decides whether it is allowed.

| Layer | File |
| ----- | ---- |
| Pick validation | `internal/app/chat_enqueuer.go` — `pickAgent`, `RosterReader` |
| Thread creation | `internal/app/thread_service.go` — `createDashboardThread` |
| Routes | `internal/transport/http/handlers/chat.go` — `createThreadReq`, `sendReq.AgentID`, `chatFail` |
| Roster read | `apps/dashboard/src/features/chat/use-agents.ts` (new) |
| Picker | `apps/dashboard/src/features/chat/agent-picker.tsx` (new) |
| Wiring | `chat-page.tsx` (new-chat screen, header, send), `threads-page.tsx` |

`POST /api/threads` and `POST /api/chat` both take an optional `agent_id`. The
dashboard only uses the second — "New conversation" navigates to an empty
screen and the first send is what creates the thread, so the dashboard has
never called `POST /threads` at all. It takes the field anyway, because the
ticket's cross-tenant acceptance item is about the *door*, not about which door
this frontend happens to use.

### 2. Decisions worth carrying forward

- **Empty stays empty; it does not resolve to the default at pick time.** A
  thread the user did not pin is stored with a NULL `agent_id` and resolves the
  default on every turn (`agentFor`, unchanged from `T-S2`). Freezing the
  default's id onto the row at creation would have been one less lookup and a
  worse product: a company that later moves its default would find every old
  conversation still running as the old one, with nothing in the UI to explain
  why. The cost of the choice is that "picked the default explicitly" and "did
  not pick" store different rows and behave identically **until** the default
  moves, at which point they correctly diverge.
- **The three refusals are one error.** Unknown id, another company's id, and a
  disabled agent all return `domain.ErrNotFound` wrapped with the same
  sentence, which `chatFail` answers as a 404 reading `no such agent`. This is
  the rule `agentFail` already set on the roster's own routes (`T-S1`), and the
  reason is unchanged: the caller is a browser holding a bare uuid, and a
  distinguishable error is an existence oracle across tenants. There is a test
  asserting the body mentions neither "disabled" nor "company", because the
  leak this prevents is one a future well-meaning error message reintroduces.
- **Disabled is checked at pick time and nowhere else.** `T-S2`'s
  `resolveAgent` deliberately does not re-check, and that stays true: a thread
  already bound to an agent keeps its narrower scope after an admin disables
  it. Disabling stops new picks, not running conversations. The alternative — a
  disabled agent falling back to the default — would *widen* a conversation's
  data access at the moment someone tried to restrict it, which is the wrong
  direction for a switch labelled "disable".
- **A pick that disagrees with an existing thread is refused, not ignored.**
  `POST /chat` with a `thread_id` and a conflicting `agent_id` is a 400.
  Dropping it silently would let a client believe it had switched agents while
  every turn kept running as the old one, and "the answer came from the wrong
  agent" is not a defect anyone finds by reading a reply.
- **No roster wired means the pick is dropped, not refused.** A stripped-down
  deployment — the eval harness, a build predating `T-S1` — has no roster to
  validate against. Refusing there would break every new chat on a dashboard
  that offers a picker; dropping the pick runs the turn exactly as this product
  did before the roster existed, which is the same fallback `T-S2` chose three
  times over.
- **The dashboard got its own thread constructor rather than an eighth
  positional argument.** `createThread` already carries four positional identity
  parameters and a comment in `continueOrForkWith` explaining why a fifth
  channel's key could not join them. `createDashboardThread` sits beside
  `createLarkThread` and `createAPIThread` for that stated reason.
- **The picker hides itself below two agents.** A company that has never opened
  Settings → Agents has exactly the one agent `030`'s backfill created, and a
  select with a single option is furniture. It also means this ticket changes
  nothing visible for a tenant who has not used `T-S1`.
- **A thread's agent is a label, never a disabled control.** `AgentBadge`, not a
  greyed-out `Select`: a control that cannot be operated reads as one that might
  become operable, and this one never does.

### 3. What is proven, and what is not

Nine tests, and the split between the two packages is deliberate — *which* picks
are refused is an `internal/app` question, and *what the browser sees* is a
transport one:

- `internal/app/chat_enqueuer_agent_test.go` (+5): an owned agent is accepted; no
  pick leaves the thread unpinned **and does not consult the default**; the three
  refusals are all `ErrNotFound` and all read the same; an unreadable roster is
  *not* an `ErrNotFound` (it would send an admin looking for a row that is right
  there); no roster wired drops the pick.
- `internal/transport/http/handlers/chat_test.go` (+4, new file): `chatFail`
  maps `ErrNotFound`→404, `ErrInvalidInput`→400, everything else→400 as before,
  and a refusal names none of what it refused.

**What no test here establishes** is the ticket's five acceptance items, all of
which need a live API and a browser: that opening a chat on "Ops" produces a
thread the header shows as Ops, that a reload keeps it, that a follow-up turn
runs under it, and that the `agent_actions` rows carry the Ops id. Those are the
gate.

### 4. The gate, run 2026-07-30

The Chrome extension was not connected, so the browser half was driven over the
DevTools protocol against headless Chrome instead of recorded as video. Stills
rather than a recording, and the sequence the ticket asked for:

| Step | Evidence |
| ---- | -------- |
| The picker on the new-chat screen | `Analyst` / `Ops` / `People Ops`, each with its description. **`Archive` — created and disabled for this gate — does not appear**, and neither does the deleted `Finance` |
| Pick Ops, ask a question | chip reads `Ops`; the thread header reads `Dashboard · Ops`; the answer names both sources and counts 5 employees, which is what an unrestricted agent should see |
| Reload | header still `Dashboard · Ops`, and the chip is **no longer a button** — a static label, because the thread has messages |
| Follow-up turn | answered 124,500,000 — the seeded total — in the same thread |
| `agent_actions` | all four rows across both turns carry `14ba3d72` (Ops); `conversation_threads.agent_id` is Ops |
| Cross-tenant `agent_id` | 404, and the thread count was 7 before and 7 after |
| Disabled `agent_id` posted directly | 404 |

The disabled-agent check needed an agent to disable, so the gate created
`Archive` with `enabled:false` — which is also the only reason we know the
picker filters on `enabled` rather than on `is_default` or on nothing.

One thing the stills cannot show and the DOM can: the picker is not merely
`disabled`, it is not rendered as a control at all once a thread has messages.
That is the stronger version of the acceptance item, and it is why probing for
`button[text=Ops]` after the reload returns nothing.

### 5. For T-S4 and T-S5

- `ChatEnqueuer.pickAgent` is the validation both should reuse rather than
  re-derive. `T-S5`'s `/v1` routes want exactly its three refusals, and its
  404-not-403 reasoning is already the `/v1` house rule (`T-A3`).
- `ChatInput.AgentID` is dashboard-only today and the field is channel-agnostic.
  `T-S4` sets `ChatRunPayload.AgentID` from a binding instead, which is a
  different insertion point — a binding is not a caller's pick and must not be
  refusable by the same path.
- The dashboard reads the roster through `useAgents()` on the `["agents"]` query
  key that Settings → Agents already populates. Anything else needing the roster
  in the frontend should use it rather than issue a second `GET /agents`.

---

## T-S5 · The roster on `/v1`

> **Status 2026-07-31: code complete, gate outstanding.** `go build ./...` and
> `go test ./...` clean, the four `T-A4` drift checks pass, both SDKs
> regenerated, the quickstart's 13 example files verified byte-equal to the
> blocks quoting them. **No migration** — every column this needs was added by
> `031`. The live half needs a running API, a tenant with two agents and an LLM
> that will answer a turn, and has not been run.

### 1. What ships

Until this, `/v1` meant "the company default agent", permanently. An integrator
building a finance workflow could reach the Finance agent only by having someone
with an admin session read a uuid out of the dashboard for them — and again
every time the roster changed.

| Layer | File |
| ----- | ---- |
| Roster route | `internal/transport/http/handlers/v1_agents.go` (new) — `GET /v1/agents`, `agentResponse`, `abortAgentNotFound` |
| Chat door | `internal/transport/http/handlers/v1_chat.go` — `chatRequest.AgentID`, `abortEnqueue` |
| Report door | `internal/transport/http/handlers/v1_reports.go` — `createReportRequest.AgentID`, `abortEnqueue` |
| Pick + fork | `internal/app/chat_enqueuer.go` — the `ChannelAPI` branch, `forkForAgent`, `ErrAgentNotFound`, `ErrAgentChange` |
| Thread creation | `internal/app/thread_service.go` — `ResolveForAPIUser` takes an agent, `CreateAPIThread` |
| Wiring | `cmd/api/router.go` (`rosterListerOrNil`), `cmd/api/policy.go` |
| Contract | `apps/backend/openapi/v1.yaml` — `listAgents`, `Agent`, `AgentPage`, `agent_id` on both request bodies |
| SDKs | `packages/argentum-node` (`agents()`, regenerated types), `packages/argentum-python` (`agents()`, `agent_id` on chat and reports, sync and async) |
| Docs | `docs/api/quickstart.md` §6, `docs/api/examples/curl/agents.sh` (new), `node/chat.mjs`, `python/chat.py`, `run.sh` |

### 2. Decisions worth carrying forward

- **`GET /v1/agents` carries no scope, and that is a third exemption.** The
  ticket says "the same read scope `GET /v1/me` uses", and that is none. The bar
  the list in `v1_scope_test.go` now states explicitly: gating it would hide
  from a caller the very thing they need in order to make a scoped call
  correctly. A key that can spend a turn but cannot discover what to spend it as
  leaves `agent_id` usable only by an integrator who was handed a uuid out of
  band.
- **The published agent is a fraction of `domain.Agent`.** No persona, no tool
  allowlist, no source ids, no `company_id`. Those are the tenant's own
  configuration and belong behind the dashboard's admin session; what a machine
  needs is enough to choose and to name. `openapi_schema_test.go` binds
  `agentResponse` to the spec in both directions, which is what stops a later
  edit from serializing the tenant's configuration by adding a field to the
  convenient struct.
- **`is_default` is published, and it is not decoration.** It is the only way a
  caller can answer "which agent answered the call where I sent no `agent_id`?"
- **A disabled agent is listed, not filtered.** Naming it is a 404, so an
  integrator whose nightly job started failing can see the reason instead of
  watching an id vanish from a list. This is the one place the `/v1` roster
  deliberately differs from the dashboard picker, which *does* filter — a person
  choosing needs the choices, a machine debugging needs the facts.
- **404, never 403, and a `param` of `agent_id`.** The four refusals — unknown,
  deleted, disabled, another tenant's — are one answer, because the status code
  is the existence oracle. The `param` matters as much as the status: a bad
  `agent_id` and a bad `thread_id` are both 404s wrapping `domain.ErrNotFound`,
  and a caller sent to the wrong field goes looking for a bug that is not there.
  That is why `app.ErrAgentNotFound` and `app.ErrAgentChange` are exported
  sentinels rather than `fmt.Errorf` values matched on their text.
- **On the `user_ref` door a disagreeing pick forks; on the `thread_id` door it
  is refused.** Both follow from `T-S3`'s rule that a conversation cannot change
  agent, and the difference is who drew the boundary. A caller who named a
  thread named a conversation, so the contradiction is theirs to resolve. A
  caller who named only their end user drew no boundary at all — the resolver
  already forks such conversations on a topic shift, and an agent change is the
  larger discontinuity of the two. Refusing there would break the caller who
  sends `agent_id` on every request the moment their first conversation exists.
- **Agreement is compared through `agentFor`, not against the stored column.**
  A conversation with a NULL `agent_id` runs as the company default, so a caller
  naming that default explicitly agrees with it. Comparing against the column
  would have forked — or refused — on every turn of that entirely ordinary case.
- **The pick runs before anything is written.** `pickAgent` sits at the top of
  the `ChannelAPI` branch, above the thread resolver, so a refused `agent_id`
  leaves no thread, no user message and no queued turn. The unit test builds the
  enqueuer with a nil thread service on purpose: if that ordering is ever
  reversed, the next line dereferences nil rather than quietly billing.
- **`has_more` is always false and the envelope stays anyway.** The roster has
  no keyset and is bounded by what an admin will configure, but a list route
  answering a bare array would be the one `/v1` list an integrator special-cases
  — and adding pagination later to a shape that never had it is exactly the
  break `apiv1`'s additive-only rule exists to prevent. Both SDKs unwrap it to a
  plain array, so the ergonomics do not pay for the caution.

### 3. What is proven, and what is not

Twenty tests across two packages, split on the same line `T-S3` drew — which
picks are legal is an `internal/app` question, what a caller sees is a transport
one:

- `internal/app/api_agent_test.go` (new, 8): a new API thread is pinned and an
  unpinned one stays NULL; a disagreeing pick forks and the fork keeps the
  `user_ref`; agreement does not fork, in both shapes, including the
  named-the-default case that a naive comparison would fork on every turn; a
  fresh conversation is never forked; another company's agent starts nothing;
  changing agent on an existing thread is `ErrAgentChange`, and naming the same
  one gets through.
- `internal/transport/http/handlers/v1_agents_test.go` (new, 12): the page
  envelope and its ordering; that none of persona, tools, sources or
  `company_id` appears in the body; a disabled agent listed as such; `data: []`
  rather than null; the two degradation paths; `agent_id` reaching the enqueuer
  trimmed; the 404 with `param: agent_id` on **both** doors and the closed
  report row behind it; the 400 `agent_mismatch`; and a reused
  `Idempotency-Key` with a changed `agent_id` answering 409 without a second
  turn — the ticket's "verify, do not assume" item, verified.
- The four `T-A4` drift checks all pass, which is the ticket saying the job is
  complete rather than the ticket being hard: route parity, scope parity
  (`x-argentum-scope: null` is behaviourally checked as "no scope refuses it"),
  the response-field reflection diff for `Agent` and `AgentPage`, and
  regenerate-and-diff on the Postman collection and the Python types.

**What no test here establishes** is the ticket's gate: that the same question
to two agents produces two answers under two scopes, that the cross-tenant 404
bills nothing on a live meter, and that the generated Node and Python snippets
run green against a real server passing agent ids. Those need an API, a tenant
with two agents and an LLM.

### 4. The gate, and how to run it

Not run. The stack was down on the machine this landed on (`docker` daemon not
running), and the agentic half spends real tokens on a tenant that has to have
two differently-scoped agents. What it needs, in order:

```bash
make infra && make seed          # postgres, redis, then 030/031 already applied
# api and worker on a private Redis DB index — see local-stack-runbook:
#   REDIS_URL=redis://localhost:6385/3 ASYNQ_REDIS_URL=redis://localhost:6385/3
export ARGENTUM_BASE_URL=http://localhost:8080 ARGENTUM_API_KEY=arg_…
docs/api/examples/run.sh deterministic   # GET /v1/agents, and one default
docs/api/examples/run.sh agentic         # node + python chat, both passing agent_id
```

and by hand, the three transcripts the ticket names: the same question to the
Finance and People Ops agents with two different answers, an `agent_id` from
another company answering 404 with the standard envelope while the thread count
and the meter both stay put, and a second `POST /v1/chat` under a reused
`Idempotency-Key` with a changed `agent_id` answering 409.

**Check `asynq:servers` against `ps` first.** The finding below cost an hour on
the last live gate for these tickets and applies unchanged.

### 5. For T-S4

- `ChatInput.AgentID` is now set by two callers rather than one, and both are
  *picks* — something a caller asked for and may be refused. A channel binding
  is not: it is configuration, and a Discord user who cannot spell an agent id
  has nothing to be refused about. `T-S4` should set the payload's agent from
  the binding rather than routing it through `pickAgent`, which is the same
  distinction `T-S3`'s handover note drew.
- `forkForAgent` is API-only on purpose. Discord and Lark key their threads on a
  platform identity, and a binding that changed would fork every conversation on
  that channel at once — which is a migration question, not a resolver one.

---

## What running the gate found (2026-07-30)

Two findings, neither in the code the three tickets shipped, and the first one
cost an hour of believing the second ticket was broken.

### 1. A stale consumer on a shared queue serves old code, silently

The first two gate turns came back with `agent_actions.agent_id` and
`usage_events.agent_id` **NULL**, and the Finance agent — scoped to the sales
source — reached the HR source and was refused only by a TLS error. Read as a
`T-S2` defect that is: enforcement absent, attribution absent, on a freshly
built binary.

It was not. `asynq:servers` held three registrations, and two of them were
`go run` workers started on 2026-07-28 that were still alive — binaries from
before `T-S1` existed. asynq handed the turns to one of them, and a runner with
no roster is *specified* to run unscoped (`resolveAgent` returns nil, silently,
which is the correct product behaviour and a terrible diagnostic).

The tell, in hindsight, is in the rows themselves: `company_id`, `thread_id`,
`channel`, `actor_kind` and `message_id` were all correct on the same rows whose
`agent_id` was NULL. Every one of those values rides the same context. A scope
that failed to *filter* would have logged an agent id; only a scope that was
never *installed* looks like that.

Moving the gate's API and worker onto Redis DB 3 fixed it in one line of env —
`ASYNQ_REDIS_URL=redis://localhost:6385/3` — and the next turn resolved
`payload_agent_id=6e99c89d`, enforced the source allowlist and wrote the agent id
on every row.

Two rules out of it, both cheap:

- **A live result is evidence about whichever process picked the task up, not
  about the tree you built from.** `git log` and `go build` say nothing here.
  Check `asynq:servers` (or `zrange asynq:servers 0 -1`) against `ps` before
  trusting a queue-driven gate, exactly as
  [`go-run-serves-stale-binaries`](environment-notes.md) says for a single
  process.
- **The two stale workers were still running when this was written** — the
  sandbox refused the `kill` — so anyone re-running a live gate on this machine
  hits the same thing until pids `73719` and `75346` are gone.

Worth considering as a product change rather than a note: `resolveAgent`'s
"no roster wired" branch is the only one of its three nil paths that logs
nothing. A single `Debug` there would have named the cause in seconds.

### 2. A source added through the dashboard form cannot reach a local Postgres

`buildDSN` (`internal/transport/http/handlers/company.go:88`) sets
`sslmode=require` unconditionally for the discrete host/port form. That is the
right default for a customer's warehouse and it makes the local demo database
unusable through the UI: creation succeeds (nothing connects at create time),
and the failure surfaces one turn later as
`pq: SSL is not enabled on the server` — which the agent then reports as a
plausible-sounding recommendation to talk to your DBA.

The gate worked around it with `PUT /api/connections/:id/dsn` and an explicit
`postgres://…?sslmode=disable`, after which `POST /api/connections/:id/test`
returns `{"ok":true}`. Two things follow, neither urgent enough to block this
track: the form has no SSL-mode control, and **create does not test**, so the
first evidence of a bad connection is a wasted agent turn.
