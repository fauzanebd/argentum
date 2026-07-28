-- Documents that no thread produced (T-A2).
--
-- Filed as `032_documents_api`, landing as 027 — the sixth consecutive ticket
-- whose reserved number was already spent. `01-tickets.md` says those numbers
-- are not binding; golang-migrate only applies versions above the schema's
-- current one, so taking a stale number strands the migration.
--
-- `POST /v1/reports/render` takes a spec and returns a file. There is no LLM,
-- no conversation and therefore no thread — which is the first time a document
-- has existed without one, and the reason thread_id has to become nullable.
--
-- The foreign key and its ON DELETE CASCADE stay: the agent path and the
-- agentic door (`POST /v1/reports`) both still have a thread, and a document
-- belonging to a deleted conversation should still go with it.
--
-- `source` and `thread_id` are independent, and reading one off the other is
-- the mistake this comment exists to prevent. `source = 'api'` with a
-- non-null thread_id is the *normal* shape for the agentic door: it ran a real
-- turn on an `api`-channel thread. What is unique to the render door is the
-- null thread, not the source.

ALTER TABLE documents
    ALTER COLUMN thread_id DROP NOT NULL;

-- Which door produced this: 'agent' for the generate_document tool inside a
-- turn, 'api' for either /v1 door. DEFAULT 'agent' backfills every existing
-- row correctly, because until this migration there was no other way to make
-- one.
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent';

-- Which credential paid for it. ON DELETE SET NULL rather than CASCADE: a
-- revoked-and-deleted key must not take a tenant's documents with it. The
-- attribution is lost, the artifact is not.
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;

-- What `GET /v1/documents` pages on. The existing idx_documents_company is a
-- bare company_id index and cannot serve the keyset predicate
-- `(created_at, id) < (?, ?)` ordered by created_at DESC, which is every page
-- after the first.
CREATE INDEX IF NOT EXISTS idx_documents_company_created
    ON documents(company_id, created_at DESC);
