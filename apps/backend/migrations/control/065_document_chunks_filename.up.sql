-- The one handle a person is certain of, and the index could not match it
-- (T-P14).
--
-- `061` built the lexical half over `heading_path || content`, which is what a
-- document *says*. Thirty seconds after an upload, the person who did it asked
-- for "the uploaded invoice 09-scan-invoice.pdf" — the filename, the only
-- string they knew for certain — and the turn answered that no such document
-- existed. It did exist, it had parsed, it had chunked, and it answered
-- perfectly about its contents. The filename was the one term the index did
-- not hold.
--
-- **Why a stored column rather than a join.** `tsv` is `GENERATED ALWAYS AS`,
-- and a generated expression may read only its own row — it cannot reach
-- `source_documents.filename` across the join `document_chunks` already has.
-- So the filename's search terms are denormalised onto the chunk. The ticket
-- said "Migration: none" on the assumption the tsv could see the join; it
-- cannot, and this is the cheapest correct shape. A re-ingest rewrites the
-- chunk rows anyway (`ReplaceForDocument` is delete-then-insert), so the copy
-- cannot drift from a rename that goes through the upload path.
--
-- **Why the terms rather than the filename.** Postgres's parser reads
-- `09-scan-invoice.pdf` as a single `host` token — one lexeme, matched only by
-- somebody who types the whole name. `domain.FilenameSearchTerms` stores the
-- name *and* its stem split on `-`, `_` and `.`, so `09-scan-invoice.pdf`,
-- `scan invoice` and `invoice` all reach the row. The UPDATE below is that
-- function in SQL, for rows that exist already.
--
-- **Why weights.** A document *about* invoices must outrank one merely *named*
-- invoice. `setweight` puts the prose at 'A' and the filename at 'B', and
-- `ts_rank`'s default weight array (0.1, 0.2, 0.4, 1.0) makes the ordering the
-- database's rather than a constant this repo would have to keep true.

ALTER TABLE document_chunks
    ADD COLUMN IF NOT EXISTS source_name TEXT NOT NULL DEFAULT '';

-- Backfill before the generated column is redefined, so the new tsv is
-- computed once, off populated rows, rather than over an empty string that a
-- re-ingest would have to correct.
UPDATE document_chunks c
   SET source_name = btrim(
           d.filename || ' ' ||
           regexp_replace(
               translate(regexp_replace(d.filename, '\.[^.]+$', ''), '-_.', '   '),
               '\s+', ' ', 'g')
       )
  FROM source_documents d
 WHERE d.id = c.document_id
   AND c.source_name = '';

-- The expression itself has to be replaced, and Postgres 15 has no
-- `ALTER COLUMN ... SET EXPRESSION` (17 added it). Drop and re-add, which
-- recomputes every row — the same cost as the backfill above and the reason
-- both are in one migration rather than two.
DROP INDEX IF EXISTS idx_document_chunks_tsv;

ALTER TABLE document_chunks DROP COLUMN IF EXISTS tsv;

ALTER TABLE document_chunks
    ADD COLUMN tsv tsvector GENERATED ALWAYS AS (
        setweight(
            to_tsvector('simple', coalesce(heading_path, '') || ' ' || coalesce(content, '')),
            'A')
        ||
        setweight(to_tsvector('simple', coalesce(source_name, '')), 'B')
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_document_chunks_tsv ON document_chunks USING GIN (tsv);
