# The tenant agent roster — T-S1 → T-S5 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Sprint 2 — The agent
roster*. Five tickets, 9.5d, filed 2026-07-29.

This file is the track's record. `T-S1` is written up below; `T-S2`'s gate
names this file too, and each later ticket appends its own section.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-S1` | `agents` + `agent_sources`, CRUD, Settings tab | 2.5d | **code complete 2026-07-29, live gate outstanding** |
| `T-S2` | Turn composition and enforcement | 2.5d | not started |
| `T-S3` | Agent picker in the dashboard chat | 1.0d | not started |
| `T-S4` | Discord / Lark / WhatsApp channel bindings | 2.0d | not started |
| `T-S5` | `agent_id` on `/v1`, plus `GET /v1/agents` | 1.5d | not started |

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

**The gate is outstanding and the acceptance boxes stay unticked.** It wants:
three agents created through the dashboard, the `GET /api/agents` body, the 403
a member receives on `POST`, the `agents` table dumped showing the backfilled
default for the demo company, and one chat turn proving the reply is unchanged
from before the migration. `make infra` provides the postgres for it.

**Migration `030_agents` has never been applied.** It is written and unrun.

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
