# Task Template

Copy this when writing a new ticket. A ticket that cannot be filled in completely
is not ready to hand to an agent — the missing section is exactly where the agent
will guess wrong.

```markdown
## T-NN · <imperative title>
**Repo:** BE | FE | LP (or several) · **Size:** Nd · **Deps:** T-XX, T-YY · **Priority:** P0|P1|P2
**Migration:** NNN_name (omit if none)

### Why
One or two sentences. If this fixes a finding, cite it: "Finding S-1." If it
enables another ticket, say which. An agent that understands the why makes better
micro-decisions in the 90% of the work the ticket doesn't specify.

### Do
Concrete steps. Name real files and real symbols — `internal/app/foo_service.go`,
not "the service layer". Include the schema if there is one. Include the exact
config keys and their defaults.

### Notes for the implementer
Non-obvious constraints, gotchas, prior art in the repo to copy. This is where you
pre-empt the wrong-but-reasonable approach — e.g. "reuse the existing
PeriodicTaskManager, do not build a second scheduler."

### Acceptance
- [ ] Observable, checkable statements
- [ ] Include at least one negative case (what must NOT happen)
- [ ] No item that can only be confirmed by reading code

### Gate
The exact command(s) to run and what output proves success. See
[`verification.md`](verification.md) for the gate matching this change type.

### Out of scope
What an agent might reasonably add but should not. Prevents scope creep and
prevents a second agent redoing week-5 work in week 2.
```

---

## Worked example

```markdown
## T-05 · Agent action audit log
**Repo:** BE · **Size:** 1.5d · **Deps:** T-02 · **Priority:** P0
**Migration:** 021_agent_actions

### Why
Finding S-5: `usage_events` records what the agent cost, not what it did. Before
the agent can take actions (T-10), there must be an immutable record of every tool
invocation — who triggered it, against which source, with what result. Also the
prerequisite for any future compliance conversation.

### Do
- Table `agent_actions` with columns: … (full DDL)
- `domain.AgentAction` + `AgentActionRepository` in `internal/domain/agent_action.go`
- `adapters/postgres/agent_action_repo.go`
- `tools.WithAudit(tool, repo)` decorator; wrap every tool in the `agentTools`
  slice in `cmd/worker/main.go:156`
- `GET /api/audit/actions?from&to&thread_id&tool&limit&offset`, admin-only

### Notes for the implementer
- Decorate at the registration point, not inside each tool — one integration point
  beats seven duplicated call sites, and a new tool then gets auditing for free.
- `args_redacted` keeps full SQL text (that is the point) but must strip anything
  DSN-shaped. Reuse the redaction helper from `internal/crypto` if one fits.
- Repository must expose no Update or Delete. Append-only is the property that
  makes the log worth having.

### Acceptance
- [ ] Every tool call produces exactly one row, on success and on failure
- [ ] A guardrail-blocked turn records `result_status=blocked`
- [ ] No row contains a decrypted DSN or an API key
- [ ] Audit endpoint returns 403 for a member and is company-scoped
- [ ] A member cannot read another company's rows even with a valid id

### Gate
Run one demo chat that calls `get_schema` + `run_sql` + `create_visualization`.
Paste the three resulting rows. Then dump the table and grep for the demo DSN
password — show zero matches.

### Out of scope
- UI for the audit log (Sprint 2)
- Retention policy / archival (Sprint 2)
- Auditing HTTP requests generally — this is agent actions only
```

---

## Sizing guide

Calibrated against the observed velocity in
[`../coverage/delivery-log.md`](../coverage/delivery-log.md): roughly 1.5
substantial features per working day during active weeks, solo, backend and
frontend together.

| Size | Means | Shape |
| ---- | ----- | ----- |
| 0.5d | One file, or a config/copy change with a test | `T-07b` guardrail narrowing |
| 1d   | One service or handler + tests | `T-03` credit enforcement |
| 1.5d | New domain entity + migration + repo + wiring | `T-05` audit log |
| 2d   | Full vertical slice, one repo | `T-13` API keys |
| 2.5d | Vertical slice touching the agent pipeline | `T-10` action framework |
| 3d   | New subsystem | `T-08` watchers |
| >3d  | **Split it.** | — |

If an estimate exceeds 3 days, the ticket is hiding a design decision. Find the
decision, make it explicitly, then split.

## Priority definitions

| P | Means |
| - | ----- |
| P0 | Sprint fails without it. Security, billing, or a milestone's core capability. |
| P1 | Sprint is materially weaker without it, but still ships. |
| P2 | Nice to have. First to cut. |

Every P2 must appear in the cut order in
[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §6. A P2 with no
cut position is really a P1 in disguise.
