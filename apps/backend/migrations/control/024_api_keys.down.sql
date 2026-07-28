-- Dropping the table revokes every key in it, which is the correct behaviour
-- for a rollback: a credential this schema no longer understands must stop
-- working rather than fall through to some other check.
DROP INDEX IF EXISTS idx_api_keys_company_created;

DROP TABLE IF EXISTS api_keys;
