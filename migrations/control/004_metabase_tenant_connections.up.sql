-- Per-tenant Metabase warehouse linkage (postgres only). Argentum mirrors each
-- registered postgres DSN to a distinct Metabase "Database" over the REST API.
ALTER TABLE db_connections ADD COLUMN IF NOT EXISTS metabase_database_id INTEGER;

-- One Metabase connection id maps to one control-plane row within an instance.
CREATE UNIQUE INDEX IF NOT EXISTS idx_db_connections_metabase_database_id_unique
    ON db_connections(metabase_database_id)
    WHERE metabase_database_id IS NOT NULL;
