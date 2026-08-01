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
| `T-10` | Action framework: schema, `Action` interface + registry, propose/approve/reject/execute state machine, `propose_action` tool | 2.5d | **code complete + unit-tested — live gate (migration apply + repo round-trip) outstanding** |

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

### 6. Outstanding — the live gate

No Docker/Postgres is available in the implementation environment, so two items
are code-complete but not exercised against a running database, the same state
`T-06`→`T-09` are in:

- **Migration round-trip.** `041_company_actions.{up,down}.sql` has not been
  applied to a live control DB (`migrate up` → `down 1` → `up`, then boot the API
  and watch the `control DB migrated to version 41` log line). Both files follow
  the 040 conventions (`gen_random_uuid()` PKs, `BYTEA` for `*_encrypted`, `TEXT`
  status, unique `(company_id, action_kind)` and `(company_id, idempotency_key)`).
- **Repository round-trip test, including the real `FOR UPDATE` race and a
  cross-tenant read.** The state machine is unit-tested against an in-memory repo
  that implements the exact documented contract; the SQL that implements the same
  contract under a row lock (`action_repo.go`) needs a Postgres-backed test — the
  playbook's Step 6. The service's cross-tenant refusal is covered
  (`TestApprove_CrossTenantIsNotFound`); the repository's is not yet.

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

- **Live gate (propose→approve→executed screenshot) not run** — needs a running
  stack. Structurally complete and unit/type-checked; the recording is owed.
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
- **Live delivery gate not run** — needs a real WhatsApp number on the allowlist.
