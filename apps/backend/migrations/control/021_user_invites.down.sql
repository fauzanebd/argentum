-- Down leaves pending users behind as ordinary rows: dropping activated_at
-- makes them indistinguishable from active accounts, so a revert also has to
-- remove the accounts that only ever existed as placeholders.
DELETE FROM users WHERE activated_at IS NULL;

DROP INDEX IF EXISTS idx_users_company_active;
ALTER TABLE users DROP COLUMN IF EXISTS deactivated_at;
ALTER TABLE users DROP COLUMN IF EXISTS activated_at;

DROP TABLE IF EXISTS user_invites;
