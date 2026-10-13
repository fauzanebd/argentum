-- RIZ-83: a workspace tops up its credit by bank transfer, with proof of
-- payment an operator reviews.
--
-- **This row is the audit record for a top-up, not a ledger entry.** There is
-- no general credit ledger in this product (non-goal), and a top-up does not
-- need one to be credited exactly once: approval is a guarded UPDATE of this
-- row from `in_review` to `approved` in the same transaction as the balance
-- write, so a second approve — a double click, a second operator — updates no
-- row and adds nothing.
--
-- **Money is integers only.** Rupiah as BIGINT rupiah, credit as BIGINT
-- micro-USD (the unit every balance in the control plane is in), and exchange
-- rates as BIGINT rupiah-per-dollar × 10 000, so 16 523,40 is 165234000. The
-- rate the credit was computed from is frozen here when the request is made:
-- the customer is credited exactly what the page showed them, and the operator
-- can see why.
--
-- **The 3-digit code is the matching key.** expected_amount_idr = amount_idr +
-- unique_code is what the operator looks for on the bank statement, so no two
-- open requests may share it — whatever their base amount (Rp 150.347 from a
-- 150.000 request and from a 150.300 one would be the same line on a
-- statement). Hence the partial unique index over open rows, and amount_idr
-- in whole thousands so the code is always the last three digits.

CREATE TABLE IF NOT EXISTS credit_topups (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id                  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- SET NULL: a top-up outlives the admin who asked for it. The money moved.
    requested_by                UUID REFERENCES users(id) ON DELETE SET NULL,
    amount_idr                  BIGINT NOT NULL CHECK (amount_idr > 0 AND amount_idr % 1000 = 0),
    unique_code                 INTEGER NOT NULL CHECK (unique_code BETWEEN 1 AND 999),
    expected_amount_idr         BIGINT NOT NULL CHECK (expected_amount_idr = amount_idr + unique_code),
    -- 'fallback' means no market rate younger than TOPUP_FX_MAX_AGE_HOURS was
    -- available, and the configured TOPUP_IDR_PER_USD was the effective rate,
    -- with no spread on top of it.
    fx_source                   TEXT NOT NULL CHECK (fx_source IN ('market', 'fallback')),
    fx_market_idr_per_usd_e4    BIGINT,
    -- The ECB reference date the market rate is for, which is not the day it
    -- was fetched: ECB publishes on working days only.
    fx_rate_date                DATE,
    fx_spread_bp                INTEGER,
    fx_effective_idr_per_usd_e4 BIGINT NOT NULL CHECK (fx_effective_idr_per_usd_e4 > 0),
    credit_micro_usd            BIGINT NOT NULL CHECK (credit_micro_usd > 0),
    status                      TEXT NOT NULL DEFAULT 'awaiting_payment'
        CHECK (status IN ('awaiting_payment', 'in_review', 'approved', 'rejected', 'expired', 'cancelled')),
    -- Which configured payee the customer says they paid. Text, not a foreign
    -- key: payees are deploy config (TOPUP_PAYEES), not rows.
    payee_id                    TEXT,
    -- The latest proof. A replaced proof's object is kept in the bucket, and
    -- only the newest is referenced here.
    proof_key                   TEXT,
    proof_content_type          TEXT,
    proof_sha256                TEXT,
    proof_uploaded_at           TIMESTAMPTZ,
    reviewed_by                 UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at                 TIMESTAMPTZ,
    reject_reason               TEXT,
    expires_at                  TIMESTAMPTZ NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open request per workspace: it keeps the operator's matching unambiguous
-- and stops request spam without a rate limiter.
CREATE UNIQUE INDEX IF NOT EXISTS idx_credit_topups_one_open_per_company
    ON credit_topups (company_id) WHERE status IN ('awaiting_payment', 'in_review');
-- No two open requests share the amount the operator matches on.
CREATE UNIQUE INDEX IF NOT EXISTS idx_credit_topups_open_expected_amount
    ON credit_topups (expected_amount_idr) WHERE status IN ('awaiting_payment', 'in_review');
-- The operator's queue: in_review, oldest proof first.
CREATE INDEX IF NOT EXISTS idx_credit_topups_status_proof
    ON credit_topups (status, proof_uploaded_at);
-- A workspace's own history, newest first.
CREATE INDEX IF NOT EXISTS idx_credit_topups_company_created
    ON credit_topups (company_id, created_at DESC);
-- The duplicate-screenshot flag.
CREATE INDEX IF NOT EXISTS idx_credit_topups_proof_sha256
    ON credit_topups (proof_sha256);

-- When the balance was last topped up. Nullable and additive: the Usage card
-- captions the bar "at your last top-up" once it is set, and a workspace that
-- never topped up reads exactly as it did before.
ALTER TABLE company_credits ADD COLUMN IF NOT EXISTS last_topup_at TIMESTAMPTZ;
