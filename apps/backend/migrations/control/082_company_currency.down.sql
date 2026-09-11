-- Reversible, unlike 081, and for the opposite reason: these columns hold only
-- what an admin typed into two new fields. Dropping them loses that and nothing
-- else — no agent capability, no backfilled row, no tenant intent expressed
-- anywhere but here. A company reverted to this state quantises nothing, which
-- is what it did before the columns existed.
ALTER TABLE companies DROP COLUMN IF EXISTS currency_rounding;
ALTER TABLE companies DROP COLUMN IF EXISTS currency_minor_units;
