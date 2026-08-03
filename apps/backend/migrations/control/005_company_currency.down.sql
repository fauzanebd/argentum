-- Reversing 005 loses each company's currency and the agent formats every
-- monetary value as the deployment default again.
ALTER TABLE companies DROP COLUMN IF EXISTS default_currency;
