# Action framework — T-10 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 4 — It does things*.
Argentum's first write-capable surface. Every other tool the agent has only
reads — `run_sql` is read-only by contract, `query_metric` evaluates a defined
number, `generate_document` renders one. `propose_action` is the first tool
through which the agent changes something outside the product, so it is the first
that cannot run on the agent's word alone: the agent **proposes**, a human
**approves**, and only then is the action **executed**, exactly once.

**Tenant SQL stays read-only, permanently.** No action routes through `run_sql`;
the two paths do not meet.

No concrete actions ship in T-10. `send_message` is T-12a and `http_action` is
T-12b. Until one registers, the action registry is empty and a proposal is
refused with "no action named X is available" — the framework is in place, the
actions are not yet. That is the correct behaviour for this ticket, not a gap.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-10` | Action framework: schema, `Action` interface + registry, propose/approve/reject/execute state machine, `propose_action` tool | 2.5d | **gated live 2026-08-02** — a Postgres-backed `FOR UPDATE` race test is still owed |

`T-11` (approval UI + WS `action_proposed` event + `GET /api/actions/pending`,
approve/reject endpoints) and `T-12a`/`T-12b` (the first two actions) are
separate tickets. The service methods `Approve`/`Reject`/`ListPending`/`Get`/
`List` and the `ActionRepo` on the stack are built here for T-11 to wire to HTTP.

---

## T-10 · Action framework

### 1. What ships

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/041_company_actions.{up,down}.sql` — `company_actions` + `action_invocations` |
| Entity | `internal/domain/action.go` — `CompanyAction`, `ActionInvocation`, `InvocationStatus`, `ActionRepository`; `internal/domain/errors.go` — `ErrActionExpired` |
| Action contract | `internal/actions/action.go` — `Action` interface (`Kind`/`Describe`/`Validate`/`Execute`) + `Registry` |
| Service | `internal/app/action_service.go` — `ProposeAction`, `Approve`, `Reject`, `execute`, decision audit, `ListPending`/`List`/`Get` |
| Repository | `internal/adapters/postgres/action_repo.go` — the state machine's atomicity (`FOR UPDATE`), idempotent create, guarded mark-executed/failed |
| Agent tool | `internal/tools/propose_action.go` — the one tool the agent may call but not perform; `internal/tools/registry.go` (`Actions` dep, unconditional registration) |
| Redaction reuse | `internal/tools/audit.go` — `RedactJSON` exported so `params_redacted` uses the same strip as the audit log |
| Stack wiring | `internal/bootstrap/stack.go` — `ActionRepo`, `Actions` (empty registry), passed into `RegistryDeps.Actions` |

### 2. The state machine

```
proposed ─approve─▶ approved ─execute─▶ executed
   │                                └──▶ failed
   ├─reject──▶ rejected
   └─(24h)───▶ expired
```

The exactly-once guarantee is a **database** one, not the service's cleverness.
`ActionRepo.Approve` moves a row from `proposed` to `approved` inside a
`SELECT ... FOR UPDATE` transaction and returns `transitioned bool` — true only
for the caller that actually made the move. Of two requests racing to approve one
proposal, exactly one is told it may execute; every other caller (a concurrent
approve, a re-approve of an already-approved or executed row) gets
`transitioned=false` and runs nothing. `MarkExecuted`/`MarkFailed` carry
`WHERE status = 'approved'` as a last guard.

`params_redacted` is what the executor runs on. An action's real secret lives in
`company_actions.config_encrypted` (per-company, admin-set), so a well-formed
proposal's own parameters carry nothing redaction removes — which is why storing
them redacted loses nothing and keeps the ledger free of credentials.

Expiry is lazy: `Approve` refuses a proposal older than 24h and marks it
`expired` in the same transaction, rather than needing a sweeper.

### 3. Audit (T-05)

Every proposal and decision lands in `agent_actions`:

- **The proposal** is audited by the existing tool decorator — `propose_action`
  is a registered tool, so `WithAuditAll` records it with no code in this ticket.
  Verified by `TestProposeActionTool_ProposesAndIsAudited`.
- **The decisions** (`action:approve`, `action:reject`) and the **execution**
  (`action:execute`) are written by `ActionService.auditDecision`, under the
  approving human's authority (`actor_kind=user`, `actor_ref=decided_by`) and
  tied to the thread the proposal was raised in. The auto-execute path
  (`requires_approval=false`) audits `action:execute` under the agent's turn
  actor.

`requires_approval=false` is an explicit admin opt-in per company per kind. It
lets a proposal execute immediately — but it does **not** turn off the audit
trail, and it is never the default (the column defaults `true`).

### 4. Acceptance — how each is met

| Acceptance | Where |
| ---------- | ----- |
| Agent can propose but never execute | `propose_action` has no execute path; `TestPropose_RequiresApproval_DoesNotExecute` asserts `execCount==0` after a proposal |
| Approving executes exactly once; approving twice does not double-execute | `TestApprove_ExecutesOnce_DoubleApproveNoDoubleExecute` — `execCount==1` after two approves, via `Approve`'s `transitioned` flag |
| Rejecting leaves no side effect | `TestReject_NoSideEffect` — no execution, and approve-after-reject is `ErrConflict` |
| A proposal older than 24h cannot be approved | `TestApprove_ExpiredCannotBeApproved` — clock advanced past `actionProposalTTL`, `Approve` returns `ErrActionExpired`, status `expired`, no execution |
| Every proposal and decision appears in `agent_actions` | `TestDecisionsAreAudited` (approve/reject/execute rows) + `TestProposeActionTool_ProposesAndIsAudited` (the proposal) |

Extra coverage: `TestPropose_NotEnabled_Refused`, `TestPropose_UnknownKind_Refused`,
`TestPropose_AutoExecuteWhenApprovalNotRequired`,
`TestApprove_ExecutionFailureIsRecorded`, `TestApprove_CrossTenantIsNotFound`,
`TestProposeActionTool_NotConfigured`.

### 5. Gate output

The ticket's gate is *"unit tests for the state machine including double-approve
and expiry. Paste output."*

```
$ go test ./internal/app/ -run 'TestPropose|TestApprove|TestReject|TestDecisions' -v
--- PASS: TestPropose_RequiresApproval_DoesNotExecute (0.00s)
--- PASS: TestPropose_NotEnabled_Refused (0.00s)
--- PASS: TestPropose_UnknownKind_Refused (0.00s)
--- PASS: TestApprove_ExecutesOnce_DoubleApproveNoDoubleExecute (0.00s)
--- PASS: TestReject_NoSideEffect (0.00s)
--- PASS: TestApprove_ExpiredCannotBeApproved (0.00s)
--- PASS: TestDecisionsAreAudited (0.00s)
--- PASS: TestPropose_AutoExecuteWhenApprovalNotRequired (0.00s)
--- PASS: TestApprove_ExecutionFailureIsRecorded (0.00s)
--- PASS: TestApprove_CrossTenantIsNotFound (0.00s)
ok  	github.com/fauzanebd/argentum/internal/app

$ go test ./internal/tools/ -run 'ProposeAction' -v
--- PASS: TestProposeActionTool_ProposesAndIsAudited (0.00s)
--- PASS: TestProposeActionTool_NotConfigured (0.00s)
ok  	github.com/fauzanebd/argentum/internal/tools
```

Whole backend: `go build ./...` clean, `go vet` clean on the touched packages,
`go test ./...` — 65 packages ok, 0 failures.

### 6. The live gate — run 2026-08-02

`041` applied to a live control DB on the API's boot (`control DB migrated to
version 42`, covering `038`→`042`). The state machine was then driven end to end
against real rows, with `http_action` as the executing kind (`T-12b`) and a
local sink as its destination.

| acceptance item | what happened |
| --------------- | ------------- |
| Agent proposes, never executes | `propose_action` returned `proposed`; the sink received nothing until a human approved |
| Approving executes exactly once | `POST /actions/:id/approve` → `status: executed`; the sink logged **one** request |
| Approving twice does not double-execute | second approve returned the same executed row; sink still one request |
| Rejecting leaves no side effect | `status: rejected`, `executed_at` null, sink unchanged |
| A proposal older than 24h cannot be approved | `proposed_at` aged 25h → `[409] this proposal has expired; ask the agent to propose it again`, row moved to `expired` |
| Every proposal and decision in `agent_actions` | `propose_action`, `action:approve`, `action:execute`, `action:reject` — four rows, `actor_kind=user` |
| Non-permitted role | member `GET /actions/:id` 200, `POST …/approve` `[403] your role is not permitted to decide this action` |

A second decision path was exercised by accident and is worth recording: a turn
whose message the `semantic_prompt_injection` guardrail refused wrote its own
`agent_actions` row (`guardrail | blocked`) with no invocation, which is `T-05`'s
blocked-turn integration behaving as designed.

**Still owed:** a Postgres-backed repository test for the `FOR UPDATE` race and
the repository's own cross-tenant refusal. The gate proved the *contract* under
concurrency-free conditions (double-approve serially); two racing approvals
against one row is still only covered by the in-memory repo.

#### What the gate found: the agent cannot find the actions it is allowed to propose

Four turns tried to get an `http_action` proposed. One succeeded, and only
because the message dictated the exact JSON. The other three:

- proposed **`send_message`** with `channel: "ops_ticket"`, refused by
  `Validate` with `channel "ops_ticket" is not supported for send_message; use
  one of [whatsapp]`;
- answered *"the `propose_action` tool … currently only supports `send_message`,
  not HTTP actions"* and called nothing;
- asked the user to confirm the kind was enabled before it would call anything.

The model is reading the tool honestly. `ProposeActionTool.Description()` names
`send_message` as *the* example and its `params` spec spells out that action's
shape ("for send_message: channel, target_ref, body"); nothing tells the model
which kinds this company has enabled, that `http_action` exists, or what
endpoints are registered under it. `ChatRunner` injects a source catalog
(`withSourcesContext`) and a metric catalog (`withMetricsContext`) into every
turn — there is no equivalent for actions, though `company_actions` is exactly
such a list and `http_endpoints` is another.

The consequence is that `T-12b` ships an action a tenant can enable, register an
endpoint for, and never reach: the capability is real and the discovery path is
missing. It is not the same limit as *"the agent does not yet discover endpoint
names automatically"* recorded under `T-12b` — that one assumed the agent gets
as far as the kind. Injecting the enabled kinds (and, per kind, the names it may
name) is the fix, in the place the other two catalogs already live.

### 7. Notes for the next ticket

- **T-11** wires `ActionService.ListPending`/`Approve`/`Reject`/`Get` to
  `GET /api/actions/pending`, `POST /api/actions/:id/approve`,
  `POST /api/actions/:id/reject`, adds the `action_proposed` WS event carrying the
  invocation id and `Describe()` text, and enforces `company_actions.allowed_roles`
  (stored here, not yet enforced) at the endpoint. The API process must construct
  its own `ActionService` in `cmd/api` (the stack instance lives in the worker).
- **T-12a** registers `send_message` in the stack's `actions.NewRegistry(...)`
  call in `bootstrap/stack.go`, and is the first real exercise of `Execute`. Note
  `Action.Execute(ctx, params)` takes only the context and params — the tenant is
  on the context; per-company config (`config_encrypted`) plumbing is added with
  the first action that needs it (`http_action`, T-12b).

---

## T-11 · Approval UI + events — 2026-08-02

### What shipped

- **Endpoints** (`internal/transport/http/handlers/actions.go`, member in
  `cmd/api/policy.go`): `GET /api/actions/pending`, `GET /api/actions/:id`,
  `POST /api/actions/:id/approve`, `POST /api/actions/:id/reject`. `actionFail`
  maps the state machine's sentinels — `ErrActionExpired`→409, double-decide
  `ErrConflict`→409, `ErrNotFound`→404.
- **`action_proposed` WS event** (`chat_runner.go`, `AgentEventToolResult`): when
  `propose_action` returns `status:"proposed"`, an event of `type:"action_proposed"`
  is published on the thread's Redis channel carrying the tool result
  (`invocation_id`, `action_kind`, `status`, `description`, `requires_approval`) in
  `Metadata`. Only a proposal awaiting a decision gets one — an admin-opt-out kind
  already carries its outcome on the `tool_result`.
- **Per-kind role gate** (`ActionService.PermittedToDecide`): the coarse policy
  table cannot express `company_actions.allowed_roles` (per company, per kind), so
  the handler calls this before every decision. Empty allowed_roles → any member;
  a missing config row → admin only.
- **Dashboard**: `features/actions/use-actions.ts` (pending query + decide
  mutation, invalidated live by the `action_proposed` event), `approval-card.tsx`
  (inline Approve/Reject card + `PendingApprovals` strip above the composer), and
  `components/layout/approvals-nav.tsx` (the app-shell pending-count badge, hidden
  at zero).

### Verified

- `go build ./...`, `go test ./internal/actions/... ./internal/app/... ./cmd/api/...`
  green (state-machine tests unchanged; policy diff test accepts the four new
  routes). `make types` regenerated. `make lint-web` green (0 errors).

### Known limits / outstanding

- **The endpoints were gated live on 2026-08-02; the UI was not.** Against a
  running API: `GET /api/actions/pending` returned the proposal with its
  `describe` payload, `approve` executed it, a second `approve` returned the same
  row without re-executing, `reject` was terminal (409 on a later approve), an
  aged proposal answered 409 `expired`, and a member got
  `[403] your role is not permitted to decide this action` while still reading
  the proposal. What is still owed is the half this ticket is named for: the
  `action_proposed` event arriving in the chat stream **without a refresh**, the
  card reflecting the outcome, and the pending badge — none of which an HTTP
  transcript can show. That is a browser session, not a stack.
- **Read-only-for-non-permitted-role is enforced server-side (403), not yet
  rendered as a disabled card.** The pending payload does not carry the caller's
  decidability, so the card shows buttons to everyone and surfaces the 403 inline.
  Surfacing `allowed_roles`/`can_decide` on the pending item is the follow-up.

## T-12a · Action `send_message` — 2026-08-02

### What shipped

- `internal/actions/send_message.go`: the `send_message` action — `channel`,
  `target_ref`, `body`, optional `attach_document_id` (accepted, not yet
  delivered). The allowlist check in `Execute` runs **before** delivery and is the
  whole guardrail: an approved proposal to an un-allowlisted target still does not
  send.
- `internal/app/action_messenger.go`: `ActionMessenger` satisfies
  `actions.Messenger`, reusing the phone allowlist (`FindCompanyByPhone`, scoped to
  the calling company) and the WhatsApp provider. Wired in both processes — the API
  in `cmd/api/bootstrap.go`, the worker in `bootstrap/stack.go` with the provider
  set in `NewChatRunner` where it arrives.
- Tests (`send_message_test.go`): validate (channel/target/body), describe,
  **un-allowlisted target refused with nothing sent**, allowlisted target
  delivered.

### Known limits / outstanding

- **WhatsApp only.** Discord and Lark allowlist *inbound users*, while delivery on
  those channels addresses a *channel*/*chat* — a different identifier space, so
  "send only to an allowlisted ref" has no safe meaning there without a
  channel-level allowlist that does not exist. Adding them is additive against
  `actions.Messenger`; scoped out here rather than closed with an unsafe guess.
- **`attach_document_id` is accepted but not delivered** (forward-compat for the
  backlog's scheduled-report delivery). `Describe` says so on the card.
- **Live delivery gate not run, and deliberately deferred on 2026-08-02.** The
  gate is *propose → approve → the message arrives*, and on this deployment
  `.env` carries live Twilio credentials, so closing it sends a real WhatsApp
  message to a real handset. The repo owner's instruction on the gate run was to
  skip delivery rather than pick a number for it. What is therefore still owed is
  **both** halves the ticket asks for: the delivery, and the un-allowlisted-target
  refusal — the latter is only reachable by approving a proposal, because
  `Execute` is where the allowlist is consulted, so it cannot be demonstrated
  without driving the same path. Unit coverage for both directions exists
  (`send_message_test.go`); neither has been through a running stack.
  Cheapest way to close it later without a handset: give the gate tenant a
  Discord or Lark credential and a channel-level allowlist, which the first
  limit above already names as the missing piece.

## T-12b · Action `http_action` — 2026-08-02

The second shipped action, and the one that closes phase 4. A generic
authenticated outbound call so a company can wire Argentum into whatever they
already run — a ticket queue, an ERP, an internal service. The safety property
the whole ticket rests on is that **the agent never types a URL**: it names a
*registered* endpoint, and the method, the host and the credentials were fixed by
an admin, not by the model at turn time.

### What shipped

| Layer | File |
| ----- | ---- |
| Schema | `migrations/control/042_http_endpoints.{up,down}.sql` — `http_endpoints`: name, method, `url_template`, `header_encrypted` (BYTEA), `body_template`, unique `(company_id, name)` |
| Entity | `internal/domain/http_endpoint.go` — `HTTPEndpoint`, `HTTPEndpointRepository` |
| Repository | `internal/adapters/postgres/http_endpoint_repo.go` — company-scoped CRUD, header stored and returned sealed (`HasHeader` derived on read) |
| Action | `internal/actions/http_action.go` — `HTTPAction`, and the `EndpointStore` + `Egress` interfaces it consumes |
| Turn-time deps | `internal/app/http_action_deps.go` — `httpEndpointResolver` (decrypts the header) and `guardEgress` (the guarded call) |
| Admin CRUD | `internal/app/http_endpoint_service.go` + `internal/transport/http/handlers/http_endpoints.go` — `GET/POST/DELETE /api/http-endpoints`, admin-only in `cmd/api/policy.go` |
| Egress guard | `internal/adapters/mcp/egress.go` — `StrictClient()` added: the address-pinned transport, redirects refused outright |
| Wiring | `cmd/api/bootstrap.go` + `internal/bootstrap/stack.go` — `http_action` registered in both processes' action registries |

### The decisions worth carrying forward

- **The endpoint registry is a separate table, not `company_actions.config_encrypted`.**
  `action_service.go`'s `ActionConfigInput` already said this would be so: an
  admin PUTs enable/approval/roles as JSON, and "the encrypted-credential plumbing
  an http_action needs travels a separate path that holds the DSN cipher, never a
  JSON body an admin PUTs." http_action needs *many* named endpoints per company,
  each with a sealed credential — a DSN-class object — so it gets a table and an
  admin CRUD of its own, like MCP servers, rather than a blob on the switchboard row.

- **The host is un-forgeable because the authority is literal.** A `url_template`
  may carry `{{.placeholders}}` in its path and query, never in its scheme or host —
  registration refuses a `{{` before the first `/` after the scheme. At execute
  time the rendered URL is parsed and its scheme+host must equal the template's, so
  a value like `rest = "@169.254.169.254/…"` that tries to smuggle a new authority
  is refused *before* egress. That is what makes "the agent picks a name, never a
  URL" true even when the name carries free-form values. The SSRF address guard is
  the second line, not the first.

- **The SSRF guard is `mcp.Guard`, reused, not re-implemented.** "Reach a tenant's
  own system" and "reach a tenant's MCP server" are one threat model — a URL a
  tenant supplied, fetched from our network position — so http_action dials with
  the same `checkIP` allowlist, address-pinned `Control` dialer, and private-range
  refusal. `StrictClient()` was added to the guard (an additive refactor extracting
  the shared `safeTransport`) so the two share one `checkIP` with one test table,
  which is the thing `egress.go` exists to avoid. The one difference is redirects:
  the MCP transport follows up to five with a re-check; http_action refuses them
  outright, because a registered endpoint has one fixed host and a 3xx is a call
  trying to leave it. The egress flags (`MCP_ALLOW_PRIVATE_EGRESS`,
  `MCP_ALLOW_INSECURE_HTTP`) are shared; the timeout is the ticket's fixed **10s**.

- **`Validate`/`Describe` are params-only, and that is the interface, not a
  shortcut.** The `Action` contract's inspection methods take no context, so they
  cannot do a company-scoped endpoint lookup at propose time — the tenant is only
  on the context `Execute` gets. So a proposal for an unknown endpoint is recorded
  and fails at execute, after approval, with a plain "no endpoint named X"; the
  approval card names the endpoint (which the approver registered) and the values
  the agent supplied. Everything that decides safety — endpoint existence, host
  match, method, the SSRF verdict — is enforced in `Execute` where the company is
  known.

- **Registration validates what execute will check, at admin time.** A private
  host, a non-https URL, a bad method, a templated host, a broken `{{`, a header
  that is not a JSON object — each is a rejected save with the reason attached,
  rather than a proposal that fails after a human approved it. The URL passes the
  same egress guard the turn-time dial will, so an unreachable endpoint cannot be
  stored.

- **A non-2xx is a recorded outcome, not an execution failure.** The far end
  answering `404` means the call was made; the invocation stores the status and the
  (capped) body so the agent and the approver can see it. A *failure* is the guard
  refusing the address or the network dropping — the call never happening.

### Note on redacted params

`ActionService.execute` runs `Action.Execute(ctx, inv.ParamsRedacted)` — the
executor deliberately runs off the redacted parameters (T-10), because a
well-formed proposal's own params carry no secret. That holds here: http_action's
credentials live in the endpoint's sealed `header_encrypted`, never in the values
the agent fills, so redaction of a path or query value (an id, a date) is
harmless. A credential does not belong in an http_action param, and the header is
where it goes.

### Acceptance — how each is met

| Acceptance | Where |
| ---------- | ----- |
| Per-company registered endpoints only; agent picks a name, never a raw URL | `httpActionParams` carries `endpoint` + `params`; `Execute` resolves the name through `EndpointStore`, and there is no field for a URL |
| Credentials encrypted with the DSN cipher | `header_encrypted` sealed by `crypto.DSNCipher` in `HTTPEndpointService.Register`, decrypted only by `httpEndpointResolver` at call time; never on a list |
| Host allowlist | literal authority enforced at registration + `sameAuthority` at execute (`TestHTTPActionExecuteRefusesTemplatedHost`, `TestHTTPActionExecuteRefusesHostChange`) |
| 10s timeout | `httpActionEgressTimeout`/`StrictClient` `Client.Timeout` |
| No redirects | `StrictClient` `CheckRedirect` refuses every hop (`TestGuardEgressRefusesRedirect`) |
| Block private/link-local (SSRF) | reuses `mcp.Guard.checkIP` (`TestGuardEgressBlocksMetadataEndpoint`, `TestGuardEgressBlocksLoopbackWhenPrivateDisallowed`) |

### Gate output

```
$ go test ./internal/actions/ -run 'HTTPAction' -v
--- PASS: TestHTTPActionValidate
--- PASS: TestHTTPActionDescribeNamesEndpointAndParams
--- PASS: TestHTTPActionExecuteRendersAndCalls
--- PASS: TestHTTPActionExecuteUnknownEndpoint
--- PASS: TestHTTPActionExecuteMissingPlaceholderRefused
--- PASS: TestHTTPActionExecuteRefusesTemplatedHost
--- PASS: TestHTTPActionExecuteRefusesHostChange
--- PASS: TestHTTPActionExecutePropagatesEgressRefusal
--- PASS: TestHTTPActionExecuteNon2xxIsNotAnError
ok  	github.com/fauzanebd/argentum/internal/actions

$ go test ./internal/app/ -run 'GuardEgress|RegisterEndpoint|DeleteEndpoint' -v
--- PASS: TestGuardEgressReachesAllowedHostWithHeaders
--- PASS: TestGuardEgressBlocksMetadataEndpoint
--- PASS: TestGuardEgressBlocksLoopbackWhenPrivateDisallowed
--- PASS: TestGuardEgressRefusesRedirect
--- PASS: TestGuardEgressCapsResponseBody
--- PASS: TestRegisterEndpointValidAndSealed
--- PASS: TestRegisterEndpointRejections (7 subtests)
--- PASS: TestRegisterEndpointCaseInsensitiveCollision
--- PASS: TestDeleteEndpoint
ok  	github.com/fauzanebd/argentum/internal/app
```

Whole backend: `go build ./...` clean, `go vet ./...` clean, `go test ./...`
green across all packages, `gofmt` clean, `make types-check` current (the new
`domain.HTTPEndpoint` regenerated into `packages/api-types/src/domain.ts`).

### The live gate — run 2026-08-02

`042` applied on boot. A Python sink on `127.0.0.1:8123` logged every request it
received, so the gate shows the request the approved action *made* rather than
inferring it from a 200.

**Register → propose → approve → observe.** The endpoint:
`POST http://127.0.0.1:8123/tickets`, header template
`{"Authorization":"Bearer gate-secret-token","Content-Type":"application/json"}`,
body template `{"title":"{{.title}}","severity":"{{.severity}}"}`. After the
approval the sink logged exactly one line:

```json
{"method": "POST", "path": "/tickets", "authorization": "Bearer gate-secret-token",
 "content_type": "application/json",
 "body": "{\"title\":\"December revenue spike\",\"severity\":\"low\"}"}
```

and the invocation recorded what came back:

```
status = executed
result = {"status": 200, "endpoint": "ops_ticket",
          "response_body": "{\"ok\": true, \"ticket\": \"GATE-1\"}"}
```

The admin's credential reached the far end and never passed through the model:
the proposal's `params_redacted` is `{"endpoint":"ops_ticket","params":{"title":…,
"severity":"low"}}` — a name and two values, no URL, no header.

**The metadata endpoint, refused before it was stored.** `POST
/api/http-endpoints` with `http://169.254.169.254/latest/meta-data/iam/security-credentials/`:

```
[400] {"error":"invalid input: egress blocked: 169.254.169.254 is a link-local address"}
```

Refused identically with `MCP_ALLOW_PRIVATE_EGRESS=true`, which is the rule the
guard states — link-local is the one range the development escape hatch does not
open. A plaintext public URL under production settings answered
`egress blocked: an MCP server URL must be https on this deployment`. That is
earlier than the ticket asked (it wanted the refusal observed through a
proposal); nothing was stored, so there was no proposal to make.

#### What the gate found: registration asked the weaker egress question

`https://localtest.me/tickets` registered **201** under `ENV=production` with no
escape hatch set. `localtest.me` is a public name that answers `127.0.0.1`.

Approving an invocation against it then failed:

```
status = failed
error  = call rebind2: Get "https://localtest.me/tickets":
         dial tcp [::1]:443: egress blocked: ::1 is a loopback address
```

So the defence held — `Guard.HTTPClient`'s `Control` hook runs after the resolver
and before the connect, and it refused. This is not an exploitable SSRF. What it
is: an endpoint that can never work, stored as if it could, whose reason only
appears **after a human approved an invocation against it** — the exact outcome
`Guard.CheckResolvedURL`'s doc comment says that method exists to prevent, and
whose comment names `localtest.me` by name. `mcp_servers` calls it at save time
(via `mcp.Client.CheckURL`); `HTTPEndpointService` was wired to plain
`CheckURL`, which decides literal IPs and lets hostnames through to the dialer.

**Fixed the same day**: `HTTPEndpointURLChecker` now declares `CheckResolvedURL`,
so a save resolves the name and refuses every answer that is ours. The
development escape hatch is unaffected (`CheckResolvedURL` returns early under
`AllowPrivate`), so the loopback sink above still registers in a dev stack. A
`host resolving to loopback` case joins `TestRegisterEndpointRejections`; it
fails against the old wiring. The existing endpoint tests moved off fictional
hostnames (`api.acme.com`) onto `example.com`, because a save that resolves means
a test host must resolve too.

### Known limits / outstanding

- **Backend-only, no dashboard UI.** The ticket is `Repo: BE`; endpoints are
  registered through `POST /api/http-endpoints` (admin). A Settings tab is additive
  and is not in scope here — the same shape `T-12a` shipped in.
- **No update, by design.** An endpoint is a credential plus an egress
  destination; editing one already named by in-flight proposals changes what they
  point at, so a change is delete-then-register. This mirrors `T-13`'s "scopes are
  fixed at creation, so the repository has no `Update`."
- **The agent does not yet discover endpoint names automatically** — and the live
  gate showed the problem starts one level up: it does not discover the *kind*
  either, so `http_action` is effectively unreachable without the caller
  dictating the tool arguments. One proposal in four attempts. See §6's finding;
  this limit is the second half of it, not a separate one.
