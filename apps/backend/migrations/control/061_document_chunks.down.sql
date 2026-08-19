-- Down for 061. The indexes go with the table; the vector extension stays,
-- because 055 and 011 use it and a down migration that removed a shared
-- extension would take two unrelated features down with it.

DROP INDEX IF EXISTS idx_document_chunks_company;
DROP INDEX IF EXISTS idx_document_chunks_tsv;
DROP TABLE IF EXISTS document_chunks;
