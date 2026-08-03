-- Reversing 004 forgets which Metabase database each connection was registered
-- as. The Metabase-side databases are untouched and will be re-registered on
-- the next sync, which is why this is safe to reverse: the mapping is a cache
-- of something Metabase itself holds.
DROP INDEX IF EXISTS idx_db_connections_metabase_database_id_unique;
ALTER TABLE db_connections DROP COLUMN IF EXISTS metabase_database_id;
