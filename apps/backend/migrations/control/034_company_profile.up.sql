-- T-B1: what business this workspace is, in the tenant's own words.
--
-- One row per company, created on demand. A company with no row behaves
-- exactly as it does today (locked decision 7), which is why this is a
-- separate table and not five nullable columns on companies: the absence has
-- to be as cheap to read as the presence, and companies is joined everywhere.
CREATE TABLE IF NOT EXISTS company_profiles (
    company_id   UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    industry     TEXT NOT NULL DEFAULT '',
    -- What the business does, in the tenant's words. The block's substance.
    description  TEXT NOT NULL DEFAULT '',
    -- Free-form: markets, seasonality, what "good" looks like. One field
    -- rather than six, because we do not yet know which six.
    context_notes TEXT NOT NULL DEFAULT '',
    fiscal_year_start_month SMALLINT NOT NULL DEFAULT 1
        CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
    -- 'human' | 'inferred' | 'inferred_edited'. Provenance, so the dashboard
    -- can say "we guessed this" and T-B2 can tell an untouched guess from a
    -- tenant's own words (locked decision 2).
    source       TEXT NOT NULL DEFAULT 'human',
    inferred_at  TIMESTAMPTZ,
    -- ON DELETE SET NULL on purpose: a departed admin must not take the
    -- company's profile with them.
    updated_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
