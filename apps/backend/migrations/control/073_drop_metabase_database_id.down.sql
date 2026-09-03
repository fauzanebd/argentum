-- The schema round trips; the data does not.
--
-- Re-adding the column nullable restores the shape `004` created, which is what
-- a down migration is for here: it lets the migration sequence be replayed in
-- both directions against a real database. The Metabase database ids themselves
-- are gone, and nothing exists to repopulate them from — Metabase was
-- decommissioned in T-D15. A rollback past this point gets an empty column, and
-- the only code that ever read it was deleted a release earlier.
ALTER TABLE db_connections ADD COLUMN IF NOT EXISTS metabase_database_id INTEGER;

CREATE UNIQUE INDEX IF NOT EXISTS idx_db_connections_metabase_database_id_unique
    ON db_connections(metabase_database_id)
    WHERE metabase_database_id IS NOT NULL;
