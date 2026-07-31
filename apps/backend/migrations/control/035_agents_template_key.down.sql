-- Reversing T-B3 loses which template each agent came from. The agents
-- themselves are untouched: nothing at turn time reads this column, so an agent
-- whose provenance is dropped still runs exactly as it did.
ALTER TABLE agents DROP COLUMN IF EXISTS template_key;
