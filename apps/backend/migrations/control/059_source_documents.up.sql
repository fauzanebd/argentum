-- A PDF a tenant handed us, before anything has been read out of it (T-P1).
--
-- This is the first file this product accepts from a tenant that is not a
-- branding logo, and the first that becomes *data* rather than decoration. The
-- roadmap it opens is `docs/plan/06-pdf-knowledge-roadmap.md`.
--
-- **Why this is not `documents`.** That table (031) holds what the agent
-- *generated* — a PDF this product wrote, addressed by thread, with a format
-- enum and a presigned link. This one holds what a tenant *supplied*, which has
-- the opposite lifecycle: it arrives once, it is parsed repeatedly as the
-- pipeline improves, and its interesting state is how far through that pipeline
-- it has got. Sharing a table would have meant a nullable half on every row and
-- a `source` column deciding which half is real.
--
-- What the schema encodes:
--
--   * content_sha256, unique per company. Re-uploading the same file is an
--     idempotent no-op rather than a second parse of identical content. The
--     number that saves is the OCR bill on a monthly report somebody sends
--     twice, and the confusion it saves is two rows nobody can tell apart.
--     Per company rather than globally: two tenants uploading the same public
--     filing are two documents, and deduplicating across the boundary would let
--     one tenant learn that another holds a file.
--   * status, with the terminal states written only by the worker. The handler
--     writes 'uploaded' and never anything else, so "what is happening to my
--     document" has exactly one writer per state and a stuck row is a worker
--     question rather than an ambiguity.
--   * status_detail in the same row as the status. A parse that fails for a
--     readable reason ("no parser configured", "page 4 is not a page") is a
--     sentence somebody can act on; a parse that fails into a log line is a
--     support ticket.
--   * page_count starts at 0 and is written by the parse. It is on the document
--     rather than derived from page rows because a caller listing documents
--     wants it and should not join to get it — and because the cap that refuses
--     an 800-page scan needs it before any page row exists.
--   * uploaded_by ON DELETE SET NULL. Who uploaded it is worth keeping; a person
--     leaving the company is not a reason to lose the document.

CREATE TABLE IF NOT EXISTS source_documents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     UUID        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- As the tenant named it. Shown in the review surface, and the only human
    -- handle on a row whose other identifier is a hash.
    filename       TEXT        NOT NULL,
    content_sha256 TEXT        NOT NULL,
    byte_size      BIGINT      NOT NULL,
    page_count     INTEGER     NOT NULL DEFAULT 0,
    -- The object key, not a URL. A stored URL embeds the endpoint, and an
    -- endpoint changes when a deployment moves buckets or puts a CDN in front.
    storage_key    TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'uploaded'
        CHECK (status IN ('uploaded', 'parsing', 'parsed', 'failed')),
    status_detail  TEXT        NOT NULL DEFAULT '',
    uploaded_by    UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_source_documents_sha UNIQUE (company_id, content_sha256)
);

-- The list every read of this table starts from: one tenant's documents, newest
-- first. The unique constraint above already serves the dedupe lookup.
CREATE INDEX IF NOT EXISTS idx_source_documents_company_recent
    ON source_documents(company_id, created_at DESC);
