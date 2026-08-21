-- Reverses 067. The erasure records go with it, which is the honest cost of a
-- down migration on an evidence table: there is nowhere else to put them.
DROP INDEX IF EXISTS idx_data_erasures_company_requested;
DROP TABLE IF EXISTS data_erasures;
ALTER TABLE companies DROP COLUMN IF EXISTS message_retention_days;
