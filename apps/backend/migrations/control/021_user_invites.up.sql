-- Team invites, and the account lifecycle they imply.
--
-- Filed as T-04's `027_user_invites`, renumbered to 021 because golang-migrate
-- only applies versions greater than the schema's current one: landing 027 now
-- would strand 021–026 forever, and T-05/T-06 are already filed against those
-- numbers.
--
-- A user reaches `activated_at IS NOT NULL` two ways: signup (immediate) or
-- accepting an invite. Until then the row exists only to reserve the email —
-- `users.email` is globally UNIQUE, so reserving it at invite time is what
-- stops two companies inviting the same address and the second accept failing
-- with a constraint error the invitee cannot act on.

CREATE TABLE IF NOT EXISTS user_invites (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    email        TEXT NOT NULL,
    role         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ,
    invited_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_invites_company ON user_invites(company_id);

-- Lookup on accept is by token hash alone, and the hash column is already
-- UNIQUE, so no further index is needed for that path.

-- At most one live invite per address per company. Re-inviting deletes the old
-- row rather than accumulating tokens that all still open the same door.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_invites_open
    ON user_invites(company_id, lower(email))
    WHERE accepted_at IS NULL;

ALTER TABLE users ADD COLUMN IF NOT EXISTS activated_at   TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deactivated_at TIMESTAMPTZ;

-- Every account that predates this migration signed itself up, so it is
-- active. Without this backfill the new login check locks out every existing
-- user the moment the binary rolls.
UPDATE users SET activated_at = created_at WHERE activated_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_users_company_active
    ON users(company_id)
    WHERE activated_at IS NOT NULL AND deactivated_at IS NULL;
