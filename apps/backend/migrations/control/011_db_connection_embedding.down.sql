-- Reversing 011 drops the table-picker embeddings and the per-source opt-in.
-- Re-applying it means re-embedding every table of every source that had the
-- feature on, which is a real cost in model calls rather than a schema change.
--
-- The vector extension is left installed, for the same reason 001 leaves
-- pgcrypto: it is database-level and another schema may be using it.
DROP TABLE IF EXISTS table_embeddings;
ALTER TABLE db_connections
    DROP COLUMN IF EXISTS enable_table_embedding,
    DROP COLUMN IF EXISTS embeddings_indexed_at;
