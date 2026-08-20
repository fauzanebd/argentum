-- Down for 065. Back to `061`'s tsv: what the document says, and nothing about
-- what it is called.
--
-- The order is `064`'s and for `064`'s reason — the index by name first, rather
-- than relying on the column drop to take it — and the same warning applies one
-- step harder here: after a down and an up, every chunk is still findable by
-- content, and nothing is findable by filename until this migration is applied
-- again. That is the capability being removed, so it is worth saying that a
-- deployment which downs this will see the exact failure T-P14 was written
-- from, and read it as a retrieval bug.

DROP INDEX IF EXISTS idx_document_chunks_tsv;

ALTER TABLE document_chunks DROP COLUMN IF EXISTS tsv;

ALTER TABLE document_chunks
    ADD COLUMN tsv tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(heading_path, '') || ' ' || coalesce(content, ''))
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_document_chunks_tsv ON document_chunks USING GIN (tsv);

ALTER TABLE document_chunks DROP COLUMN IF EXISTS source_name;
