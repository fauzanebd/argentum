-- Reversing 003 drops the metering ledger and the credit balances. The balances
-- are not reconstructible from anywhere else — usage_events is the only record
-- of what was spent — so this is a destructive down in the ordinary sense, not
-- a structural one.
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS company_credits;
