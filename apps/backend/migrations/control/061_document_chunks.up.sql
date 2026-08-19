-- What a document *says*, chunked and indexed twice (T-P8).
--
-- The other half of this roadmap — 060 — is about what a document *contains*:
-- tables, typed, published into a warehouse and queried with SQL. This table is
-- the prose. A contract's payment terms, a policy's exception list, a report's
-- methodology note: the answer to a real class of question, and the half a
-- tenant usually means when they say "knowledge".
--
-- **It is retrieved by a tool, never injected into a prompt** (Decision 6). A
-- chunk that arrives in the context window as background text is invisible to
-- `CheckGrounding` — its evidence is what *tools* returned — so any figure
-- quoted out of it would be unfalsifiable rather than merely unchecked. T-P9's
-- `search_documents` puts these results where the instruments can see them.
--
-- What the schema encodes:
--
--   * page_from / page_to. Every chunk can name its pages, because a citation
--     is what separates a quotation from a claim with a friendly voice. A
--     chunk that cannot say where it came from has no business in an answer.
--   * context_prefix, generated once at ingest on the light model and stored.
--     One sentence situating the chunk in its document — the published
--     measurement is a 35% reduction in retrieval failure for embeddings alone
--     and 49% with a lexical index beside it. Generated at ingest so no turn
--     pays for it, stored so it can be inspected rather than guessed at.
--   * embedding NULL-able. A deployment with no embedding credentials still
--     ingests, and the lexical half still answers. The dense half is an
--     improvement, not a prerequisite — the same shape as the table picker,
--     which is off where embeddings are unconfigured.
--   * tsv, a generated tsvector with a GIN index. The lexical half of hybrid
--     retrieval: exact terms, product codes, clause numbers — everything a
--     dense vector is worst at and a contract is full of.
--   * ON DELETE CASCADE from source_documents. Deleting a document deletes what
--     was read out of it. T-P12 asserts all four removals; this is the one the
--     database can guarantee by itself.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS document_chunks (
    id             BIGSERIAL PRIMARY KEY,
    document_id    UUID         NOT NULL REFERENCES source_documents(id) ON DELETE CASCADE,
    company_id     UUID         NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Position in the document, so a re-ingest can replace a chunk rather than
    -- appending a second copy of it, and so an answer can quote them in order.
    ordinal        INTEGER      NOT NULL,
    page_from      INTEGER      NOT NULL,
    page_to        INTEGER      NOT NULL,
    -- The heading trail this chunk sits under: "Bab 3 › Ketentuan Pembayaran".
    -- Shown in a citation, and prepended to what is embedded, because a chunk
    -- read without its heading is a paragraph about nothing.
    heading_path   TEXT         NOT NULL DEFAULT '',
    content        TEXT         NOT NULL,
    context_prefix TEXT         NOT NULL DEFAULT '',
    embedding      vector(1536),
    model          TEXT         NOT NULL DEFAULT '',
    tsv            tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(heading_path, '') || ' ' || coalesce(content, ''))
    ) STORED,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT uq_document_chunks_ordinal UNIQUE (document_id, ordinal)
);

-- The lexical half. `simple` rather than `english` or a Indonesian
-- configuration on purpose: this deployment's documents are mostly Indonesian,
-- Postgres ships no Indonesian stemmer, and stemming an Indonesian document
-- with English rules is worse than not stemming it — it would turn "bulanan"
-- and "bulan" into two unrelated terms while claiming to relate them.
CREATE INDEX IF NOT EXISTS idx_document_chunks_tsv ON document_chunks USING GIN (tsv);

-- Every retrieval is scoped to one company, and usually to one document.
CREATE INDEX IF NOT EXISTS idx_document_chunks_company ON document_chunks(company_id, document_id);

-- No ivfflat index, deliberately, and the argument is 055's and 013's: an
-- approximate index over a few thousand rows is slower than the sequential scan
-- it replaces and returns approximate neighbours for the privilege. A tenant
-- with enough chunks for it to pay is a tenant worth measuring first — and the
-- measurement, not the index, is what would tell us the row count where it
-- starts to.
