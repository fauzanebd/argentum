-- RIZ-89: a new workspace's free credit waits for an operator's approval.
--
-- Until now every sign-up was credited $0.50 at once, and a company with no
-- grant was credited lazily at its first budget check. Sign-up has no email
-- verification, so free credit could be farmed by scripting sign-ups. Now
-- sign-up files one of these rows instead of writing a balance, and the only
-- thing that writes free credit is an operator approving it.
--
-- **This row is the audit record, not a ledger entry**, for the reason 090's
-- top-ups are not: approval is a guarded UPDATE of this row from `pending` in
-- the same transaction as the balance write, so a second approve — a double
-- click, a second operator — updates no row and adds nothing.
--
-- **One per company, ever** (`company_id` UNIQUE). A rejection is final in v1,
-- and a company that already filed cannot file again by signing in from
-- another pod or being checked twice: InsertIfAbsent is ON CONFLICT DO NOTHING.
--
-- **The amount is frozen when the request is filed**, from
-- CREDITS_DEFAULT_GRANT_USD at that moment, so the operator approves the
-- number the user was shown.
--
-- **No backfill.** Companies that already hold a grant keep it in
-- company_credits and never get a row here. A company that never received a
-- grant gets its row lazily, at its next budget check, with no signup_ip.

CREATE TABLE IF NOT EXISTS credit_grant_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id       UUID NOT NULL UNIQUE REFERENCES companies(id) ON DELETE CASCADE,
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected')),
    amount_micro_usd BIGINT NOT NULL CHECK (amount_micro_usd > 0),
    -- SET NULL: the request outlives the admin who signed up, and a lazy
    -- request filed by a budget check has nobody to name at all.
    requested_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    -- The client address sign-up came from, for the operator's "N other
    -- sign-ups from this IP in 7 days" signal. Null for a lazy request. There
    -- is no retention rule for it yet (a non-goal of RIZ-89, recorded in
    -- docs/coverage/credit-enforcement.md).
    signup_ip        INET,
    reviewed_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The operator's queue (pending, oldest first) and the Reviewed table.
CREATE INDEX IF NOT EXISTS idx_credit_grant_requests_status_created
    ON credit_grant_requests (status, created_at);
-- The same-IP signal: other sign-ups from one address within a week.
CREATE INDEX IF NOT EXISTS idx_credit_grant_requests_signup_ip
    ON credit_grant_requests (signup_ip, created_at) WHERE signup_ip IS NOT NULL;
