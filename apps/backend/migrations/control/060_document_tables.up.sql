-- A table somebody found inside a PDF, and the decision to trust it (T-P4/T-P6).
--
-- **This table is the draft, not the data.** The rows extracted from a document
-- live in the document warehouse — a separate database, reached through a
-- `db_connections` row like any other source — and this is the record of what
-- was extracted, what it was typed as, whether it added up, and who decided it
-- was good enough to publish. The roadmap's Decision 3 is the reason the two are
-- apart: an extraction is a draft until a human applies it, and a draft that
-- lived in the warehouse would be a fabrication with a UI.
--
-- What the schema encodes, and why:
--
--   * columns JSONB. The typed columns, their multipliers, their decimal
--     separators and their PII class, exactly as `internal/doctable` produced
--     them and as a reviewer edited them. JSONB rather than a child table
--     because nothing queries *across* columns — every read is "give me this
--     table's columns" — and because the shape is owned by a Go package that
--     will grow fields as the parser improves.
--   * verify_status / verify_detail, written by T-P5. `quarantined` is a state
--     the publish path refuses, not a warning a surface displays. A table whose
--     stated total disagrees with the sum of its own rows has a parse error in
--     it, and the one thing that must not happen is publishing it and letting
--     the agent answer from it.
--   * status: draft → applied, or quarantined. Only 'applied' exists in the
--     warehouse. Re-applying replaces the warehouse table rather than appending
--     to it, so a document re-parsed after a reviewer fixed a column type does
--     not double its rows.
--   * applied_by / applied_at. Decision 3 again: the human is the gate, so the
--     human is on the row. ON DELETE SET NULL for the reason source_documents
--     uses it — somebody leaving is not a reason to lose the record that the
--     table was reviewed.
--   * table_name is the identifier in the warehouse schema, slugified from the
--     document's own words. UNIQUE per company because that is what CREATE
--     TABLE will enforce anyway, and a constraint violation at publish time is
--     a worse place to discover a collision than at insert time.

CREATE TABLE IF NOT EXISTS document_tables (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id    UUID        NOT NULL REFERENCES source_documents(id) ON DELETE CASCADE,
    company_id     UUID        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- What the reviewer named it, and what it is called in the warehouse.
    title          TEXT        NOT NULL,
    table_name     TEXT        NOT NULL,
    first_page     INTEGER     NOT NULL,
    last_page      INTEGER     NOT NULL,
    -- The typed columns, their multipliers and their provenance, as decided by
    -- T-P4 and edited by the reviewer in T-P7.
    columns        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- draft → applied → quarantined. Only 'applied' exists in the warehouse.
    status         TEXT        NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'applied', 'quarantined')),
    verify_status  TEXT        NOT NULL DEFAULT 'unverified'
        CHECK (verify_status IN ('verified', 'unverified', 'quarantined')),
    verify_detail  TEXT        NOT NULL DEFAULT '',
    row_count      INTEGER     NOT NULL DEFAULT 0,
    -- Which candidate on the page this was, so a re-parse of the same document
    -- updates the draft a reviewer has been editing instead of adding a second
    -- one beside it.
    candidate_key  TEXT        NOT NULL DEFAULT '',
    applied_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    applied_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_document_tables_name UNIQUE (company_id, table_name),
    CONSTRAINT uq_document_tables_candidate UNIQUE (document_id, candidate_key)
);

-- Every read of this table is "the tables of one document", in page order.
CREATE INDEX IF NOT EXISTS idx_document_tables_document
    ON document_tables(document_id, first_page);

-- The list a tenant's source page needs: what this company has published.
CREATE INDEX IF NOT EXISTS idx_document_tables_company_applied
    ON document_tables(company_id, status);

-- Where a source came from. 'tenant' for a warehouse somebody connected,
-- 'document' for one this product built out of uploaded files.
--
-- `list_sources` says which, and the reason is not bookkeeping: an agent
-- choosing between two sources should know that one of them is *derived* — its
-- numbers are this product's reading of a page, not a system of record. The
-- default is 'tenant' because every row that exists when this migration runs is
-- one somebody connected themselves.
ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'tenant';

-- Which document source belongs to which company is answered by company_id
-- already; this index answers "does this company have one yet?", which is the
-- question every publish asks first.
CREATE INDEX IF NOT EXISTS idx_db_connections_origin
    ON db_connections(company_id, origin);
