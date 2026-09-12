-- T-Z1: a capability is a named power, and a user is granted it.
--
-- A capability has no object — speaking a question, approving an action,
-- exporting data — which is why it is its own table rather than a row in 084's
-- resource grants with a NULL resource_id. A nullable id makes every query
-- ambiguous about what NULL means, and the dashboard would render two unrelated
-- ideas in one list (docs/plan/12-access-grants-roadmap.md §1a).
--
-- No backfill, and there is nothing to backfill: nobody holds anything on the
-- day this applies, and no route asks for anything either (cmd/api/policy.go,
-- capabilityPolicy, which is empty). The day a route that already exists starts
-- asking for a capability is the day a backfill becomes a question, and it
-- belongs to that ticket — not to this one, which would be guessing.
--
-- `capability` is TEXT with no CHECK. The vocabulary is closed in Go
-- (domain.Capability), and the service refuses an unknown string before it can
-- reach this table. A CHECK here would make every new member of the vocabulary
-- a migration, applied during a rolling deploy in which the new code can run
-- against the old constraint and have its first grant refused.

CREATE TABLE IF NOT EXISTS user_capabilities (
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- A grant dies with its user. A grant row that outlives its subject is a
    -- permission nobody can see.
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    capability  TEXT NOT NULL,
    -- Who decided. SET NULL rather than CASCADE: an admin leaving the company
    -- does not revoke what they granted, it only loses the attribution. The
    -- alternative is a departure that silently takes voice away from everyone
    -- that admin ever set up.
    granted_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Unique on the first three, and the primary key is that uniqueness. It is
    -- also the only index the read path needs: every lookup is one user of one
    -- company, which is this key's prefix.
    PRIMARY KEY (company_id, user_id, capability)
);
