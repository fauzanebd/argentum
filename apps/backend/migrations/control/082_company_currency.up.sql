-- T-W2: how much precision a money figure has, and which way it rounds.
--
-- The currency CODE already lives on this table as `default_currency` and has
-- since before the agent could compute anything. A second currency column on
-- company_profiles would be two rows expressing one idea, which is the rot this
-- repository has avoided everywhere else — so the precision and the policy go
-- beside the code rather than opposite it.
--
-- Both are nullable/empty by default, and both read as "not stated". A company
-- that never touches them behaves exactly as it does today: nothing is
-- quantised that was not quantised before, because before this migration
-- nothing was quantised at all.

-- How many decimal places to write this currency with, when the currency's own
-- precision is not what this tenant uses. NULL is the ordinary case and means
-- "use the currency's own" — domain.CurrencyMinorUnits, where IDR is 0 and USD
-- is 2. It exists for the ledger that genuinely prices in half-cents, not as a
-- place to put a guess, and the CHECK is what stops a fat-fingered 20 from
-- turning every figure into a twenty-place decimal.
ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS currency_minor_units SMALLINT
        CHECK (currency_minor_units IS NULL OR currency_minor_units BETWEEN 0 AND 4);

-- What a money figure does at exactly half a minor unit: '' (unstated),
-- 'half_up' or 'half_even'.
--
-- Empty resolves to half-up, which is what every answer this product has ever
-- given already did — so the default changes nothing. It is a column rather
-- than a constant because half-even is an accounting requirement in several
-- standards, and which one a tenant is held to is not something this product
-- can know.
ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS currency_rounding TEXT NOT NULL DEFAULT ''
        CHECK (currency_rounding IN ('', 'half_up', 'half_even'));
