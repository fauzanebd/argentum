-- Reversing this loses every tenant's freshness configuration — the expression
-- they wrote and the two thresholds they chose. Nothing else depends on the
-- columns, so the down is safe in the sense that the product keeps working:
-- every source reverts to `unknown`, which is what it reported before 079.
ALTER TABLE db_connections DROP COLUMN IF EXISTS freshness_stale_after_mins;
ALTER TABLE db_connections DROP COLUMN IF EXISTS freshness_warn_after_mins;
ALTER TABLE db_connections DROP COLUMN IF EXISTS freshness_sql;
