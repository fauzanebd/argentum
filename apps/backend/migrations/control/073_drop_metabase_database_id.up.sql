-- T-D16 (part 1 of 2) · The Metabase linkage column, dropped.
--
-- `004_metabase_tenant_connections` added `metabase_database_id` and a partial
-- unique index over it so a registered postgres DSN could be mirrored to a
-- Metabase "Database" over the REST API. T-D15 removed Metabase and with it
-- every reader of this column: `domain.Connection.MetabaseDatabaseID`, the
-- SELECT/scan/UPDATE in `connection_repo.go`, and three call sites in
-- `company_service.go`. Nothing in this tree has read it since.
--
-- **Why this is only half of T-D16.** The ticket also drops `saved_dashboards`,
-- and that table is still read by the release this migration ships in — the
-- archived-list handler and the two thread-delete cascades are removed in the
-- *same* commit as this file. `workspace-context.md` §6 is the constraint:
-- `cmd/api` applies migrations before serving, so during a rolling deploy the
-- new schema meets the old binary, and dropping a table that binary still reads
-- faults it on every archived-list read. The column has already served its
-- release of not being read; the table has not. So the table's drop is
-- `074_drop_saved_dashboards`, to be landed one release after this one — the
-- same add-then-remove-across-two-releases rule, applied to the half that needs
-- it rather than to both halves out of caution.
--
-- The index goes first: dropping the column would take it anyway, but naming it
-- keeps the down migration's job symmetrical and readable.
DROP INDEX IF EXISTS idx_db_connections_metabase_database_id_unique;

ALTER TABLE db_connections DROP COLUMN IF EXISTS metabase_database_id;
