-- Down for 068. Every restriction a tenant configured goes with it, and the
-- sources become readable in full again — which is the honest cost of reversing
-- a permission column and is worth stating plainly rather than discovering.
ALTER TABLE db_connections DROP COLUMN IF EXISTS allowlist;
