-- Drops the index with the table; named here anyway so a reader of the down
-- file can see everything the up file created.
DROP INDEX IF EXISTS idx_source_profiles_company;
DROP TABLE IF EXISTS source_profiles;
