-- Reversible, and this is the one down migration in the access track that fails
-- *open* rather than closed: dropping access_mode makes every restricted
-- resource reachable by every member again. That is acceptable only because it
-- travels with the code. The enforcement that reads access_mode (T-Z4 → T-Z6)
-- has to be reverted first, and a deployment with neither the column nor the
-- check is exactly the deployment that existed before this migration.
--
-- Run `down` on this without reverting those tickets and every authz query
-- errors on the missing column — which refuses, rather than opens.
DROP TABLE IF EXISTS resource_grants;

ALTER TABLE source_documents DROP COLUMN IF EXISTS access_mode;
ALTER TABLE db_connections DROP COLUMN IF EXISTS access_mode;
ALTER TABLE dashboards DROP COLUMN IF EXISTS access_mode;
ALTER TABLE agents DROP COLUMN IF EXISTS access_mode;
