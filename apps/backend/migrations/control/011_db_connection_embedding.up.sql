-- Per-source embedding-based table picker.
-- enable_table_embedding flips the feature on per db_connection.
-- table_embeddings stores one row per table per source, used at chat time
-- to pre-pick the top-K relevant tables for the user's message.
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS enable_table_embedding BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS embeddings_indexed_at  TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS table_embeddings (
    id          BIGSERIAL PRIMARY KEY,
    company_id  UUID         NOT NULL,
    source_id   UUID         NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
    table_name  TEXT         NOT NULL,
    doc_text    TEXT         NOT NULL,
    doc_hash    TEXT         NOT NULL,
    embedding   vector(1536) NOT NULL,
    model       TEXT         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (source_id, table_name)
);

CREATE INDEX IF NOT EXISTS idx_table_embeddings_source ON table_embeddings(source_id);
CREATE INDEX IF NOT EXISTS idx_table_embeddings_vec
    ON table_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
