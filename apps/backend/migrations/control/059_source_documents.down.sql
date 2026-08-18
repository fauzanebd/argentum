-- Drops the record of every PDF a tenant uploaded (T-P1).
--
-- The objects themselves are not touched, because this file cannot reach the
-- bucket: reversing this migration leaves the stored PDFs orphaned under
-- `source-documents/<company>/<sha>.pdf` with nothing pointing at them. That is
-- the honest outcome and it is worth stating rather than pretending a schema
-- change can clean an object store. A deployment reversing this on purpose
-- should empty that prefix itself.
DROP TABLE IF EXISTS source_documents;
