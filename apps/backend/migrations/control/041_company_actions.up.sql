-- T-10: the action framework — write-capable agency, gated by human approval.
--
-- Every other tool the agent has only reads: run_sql is read-only by contract,
-- query_metric evaluates a defined number, generate_document renders one. This
-- is the first surface through which the agent changes something in the world,
-- so it is the first that cannot run on the agent's say-so alone. An action is
-- *proposed* by the agent and *executed* only after a human approves it — and
-- tenant SQL stays read-only permanently, so no action ever routes through it.
--
-- Two tables. company_actions is the per-tenant switchboard: which action kinds
-- this company has turned on, whether each still needs approval, and the sealed
-- configuration an action carries (an http_action's credentials, T-12b). Nothing
-- can be proposed for a kind that is not enabled here. action_invocations is the
-- ledger: one row per proposal, moving through the states below, holding the
-- redacted parameters, the decision, and the outcome.
--
-- Numbered 041 from schema_migrations at implementation time. The ticket header
-- read `024` until 2026-07-30 (024 has been api_keys since T-13); the tree is at
-- 040 (watchers) now — the seventh ticket in a row whose reserved migration
-- number was already spent. Take the next free number, update the ticket table.

-- The per-company switchboard. An action kind an admin has not enabled cannot be
-- proposed, and requires_approval defaults true: turning off the approval step
-- is a deliberate, admin-only opt-in, never the default a company drifts into.
CREATE TABLE IF NOT EXISTS company_actions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Which action this row configures: 'send_message' (T-12a), 'http_action'
    -- (T-12b). TEXT rather than an enum for the reason 023 gives — ALTER TYPE
    -- cannot run inside the transaction golang-migrate wraps a migration in, and
    -- every new action kind would need one.
    action_kind       TEXT NOT NULL,
    enabled           BOOLEAN NOT NULL DEFAULT false,
    -- When false, an admin has opted this kind out of the approval step: a
    -- proposal executes immediately instead of waiting for a decision. It still
    -- writes to agent_actions — the audit trail does not become optional because
    -- the approval did.
    requires_approval BOOLEAN NOT NULL DEFAULT true,
    -- The action's sealed configuration (crypto.DSNCipher, AES-256-GCM), NULL for
    -- an action that needs none. Per-company and set by an admin, so an
    -- invocation's own parameters never have to carry a credential — which is why
    -- storing those parameters redacted (below) loses nothing an action needs.
    config_encrypted  BYTEA,
    -- Roles permitted to approve this kind, e.g. ["admin"]. Empty means "any
    -- member". Enforced at the approval endpoint (T-11), stored here.
    allowed_roles     JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Who turned it on. Nullable and unreferenced, like watchers/metric_definitions:
    -- the configuration outlives the admin who wrote it.
    created_by        UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One configuration per kind per company: enabling send_message twice is a
    -- contradiction, not two rows.
    UNIQUE (company_id, action_kind)
);

CREATE INDEX IF NOT EXISTS idx_company_actions_company ON company_actions(company_id);

-- The invocation ledger. One row per proposal.
--
--   proposed ─approve─▶ approved ─execute─▶ executed
--      │                                └──▶ failed
--      ├─reject──▶ rejected
--      └─(24h)───▶ expired
--
-- The transition proposed→approved is the single point of serialization: it runs
-- under SELECT ... FOR UPDATE and only one caller can win it, which is what makes
-- "approving twice does not double-execute" true regardless of races. Execution
-- is idempotent on the row for the same reason.
CREATE TABLE IF NOT EXISTS action_invocations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- The thread the proposal was raised in, and the assistant message that
    -- proposed it. No foreign key, like agent_actions: DELETE /api/threads/:id
    -- exists, and the record of what the agent set in motion has to outlive the
    -- conversation it happened in.
    thread_id       UUID,
    message_id      UUID,
    action_kind     TEXT NOT NULL,
    -- The parameters, credential-shaped values stripped (tools.redactValue). An
    -- action's real secret lives in company_actions.config_encrypted, so a
    -- well-formed proposal's parameters carry nothing that redaction removes —
    -- which is why the executor can run off this column rather than a second,
    -- unredacted one that would put secrets in the ledger.
    params_redacted JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Dedup key for the proposal, unique per company. A second Propose with the
    -- same key returns the first invocation instead of raising a duplicate, so a
    -- retried tool call cannot create two approvable proposals for one intent.
    idempotency_key TEXT NOT NULL,
    -- proposed | approved | rejected | executed | failed | expired
    status          TEXT NOT NULL DEFAULT 'proposed',
    proposed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- When and by whom the proposal was approved or rejected. decided_by is
    -- nullable and unreferenced (the approver may later leave) and empty for the
    -- auto-approved path where no human decided.
    decided_at      TIMESTAMPTZ,
    decided_by      UUID,
    executed_at     TIMESTAMPTZ,
    -- The action's own return value, JSON, NULL until it runs.
    result          JSONB,
    -- Why it failed, NULL unless status is 'failed'.
    error_text      TEXT,
    UNIQUE (company_id, idempotency_key)
);

-- The pending-approvals read: a company's open proposals, and the state of any
-- one of them.
CREATE INDEX IF NOT EXISTS idx_action_invocations_company_status
    ON action_invocations(company_id, status);
-- The ledger read: a company's invocations newest first.
CREATE INDEX IF NOT EXISTS idx_action_invocations_company_proposed
    ON action_invocations(company_id, proposed_at DESC);
