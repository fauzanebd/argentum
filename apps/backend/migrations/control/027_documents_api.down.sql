-- The rows have to go before the constraint comes back.
--
-- `SET NOT NULL` scans the table and fails on the first null it finds, so a
-- database that has served the render door even once cannot roll this back
-- while those documents exist. Deleting them is the only honest option: the
-- schema being restored is one in which they cannot be represented.
--
-- Their objects stay in the bucket. An orphaned object costs storage; a
-- rollback that reaches into object storage and deletes a tenant's files
-- because a deploy went backwards costs the files.
--
-- The documents that survive lose their provenance, which is the second cost
-- of this rollback and the less obvious one: dropping `source` and
-- `api_key_id` means an agentic-door document — which has a thread and so is
-- not deleted above — comes back as `source = 'agent'` with no credential on
-- it when the up migration re-adds the columns with their defaults. The
-- artifact is intact; the record of which key paid for it is not. Verified in
-- T-A2's gate, which ran this round trip against a database holding both
-- kinds of row.
DELETE FROM documents WHERE thread_id IS NULL;

DROP INDEX IF EXISTS idx_documents_company_created;

ALTER TABLE documents
    DROP COLUMN IF EXISTS api_key_id;

ALTER TABLE documents
    DROP COLUMN IF EXISTS source;

ALTER TABLE documents
    ALTER COLUMN thread_id SET NOT NULL;
