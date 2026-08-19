-- Down for 060.
--
-- The column goes last and the table first, so a partially-applied down leaves
-- the surviving half consistent: `document_tables` referencing a `db_connections`
-- row is the direction the code reads, and dropping the column while the drafts
-- still exist would leave every publish path looking at a source it cannot
-- classify.
--
-- Nothing here touches the document warehouse. Its schemas hold the published
-- rows, and dropping them from a control-database migration would delete a
-- tenant's data because somebody rolled back a release.

DROP INDEX IF EXISTS idx_db_connections_origin;
DROP INDEX IF EXISTS idx_document_tables_company_applied;
DROP INDEX IF EXISTS idx_document_tables_document;
DROP TABLE IF EXISTS document_tables;

ALTER TABLE db_connections DROP COLUMN IF EXISTS origin;
