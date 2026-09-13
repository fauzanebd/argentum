-- T-Z2: a resource can be restricted, and a grant is what opens it.
--
-- Two halves. `access_mode` on the four restrictable tables says whether a
-- grant is needed at all; `resource_grants` says who holds one.
--
-- Nothing changes for anybody on the day this applies. Every existing row takes
-- the column's default, 'open', which is today's behaviour exactly: every member
-- of the company reaches every agent, dashboard, source and document (roadmap 12,
-- decision 3). And no request path consults the mode until T-Z4 → T-Z6 call
-- internal/authz — so a resource restricted before then is restricted on paper,
-- which docs/coverage/access-grants.md says in as many words.
--
-- The ADD COLUMNs are safe inside cmd/api's rolling deploy. A constant default is
-- a catalogue change rather than a table rewrite on Postgres 11 and later, and no
-- repository reads these four tables with SELECT * or RETURNING *, so a pod on
-- the old code scanning by position never meets the new column.

-- 'open' or 'restricted'. A CHECK here and not on user_capabilities.capability,
-- and the difference is how each vocabulary grows: a capability is a string the
-- code can add in a commit, while a third mode would change what every caller of
-- authz.Decide means, which deserves a migration.
ALTER TABLE agents
    ADD COLUMN IF NOT EXISTS access_mode TEXT NOT NULL DEFAULT 'open'
        CHECK (access_mode IN ('open', 'restricted'));
ALTER TABLE dashboards
    ADD COLUMN IF NOT EXISTS access_mode TEXT NOT NULL DEFAULT 'open'
        CHECK (access_mode IN ('open', 'restricted'));
ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS access_mode TEXT NOT NULL DEFAULT 'open'
        CHECK (access_mode IN ('open', 'restricted'));
-- `source_documents`, the uploaded PDFs search_documents quotes from (T-P1) —
-- not `documents`, which are reports this product generated at somebody's
-- request and which no roadmap-12 ticket gates.
ALTER TABLE source_documents
    ADD COLUMN IF NOT EXISTS access_mode TEXT NOT NULL DEFAULT 'open'
        CHECK (access_mode IN ('open', 'restricted'));

-- One row per person per resource.
--
-- **The ticket's shape, (resource_kind, resource_id), cannot carry the foreign
-- key the same ticket asks for.** "A grant is dropped when its user or its
-- resource is, by foreign key" needs the id to reference one table, and a
-- polymorphic id references none: deleting an agent would leave its grants
-- behind, naming a uuid that no longer exists — a permission nobody can see,
-- which is the thing the ticket was ruling out.
--
-- So each kind has its own nullable, typed, cascading column. Exactly one is set,
-- and resource_kind says which, tied to it by a CHECK so the two cannot
-- disagree. A fifth kind is a column, an index and a CHECK arm: the right amount
-- of ceremony for widening what an admin can put behind a grant.
CREATE TABLE IF NOT EXISTS resource_grants (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_kind  TEXT NOT NULL
        CHECK (resource_kind IN ('agent', 'dashboard', 'connection', 'document')),
    agent_id       UUID REFERENCES agents(id) ON DELETE CASCADE,
    dashboard_id   UUID REFERENCES dashboards(id) ON DELETE CASCADE,
    connection_id  UUID REFERENCES db_connections(id) ON DELETE CASCADE,
    document_id    UUID REFERENCES source_documents(id) ON DELETE CASCADE,
    -- SET NULL, as on user_capabilities: an admin leaving does not revoke what
    -- they granted, it only loses the attribution.
    granted_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT resource_grants_one_resource CHECK (
        num_nonnulls(agent_id, dashboard_id, connection_id, document_id) = 1
    ),
    CONSTRAINT resource_grants_kind_matches CHECK (
        (resource_kind = 'agent')      = (agent_id IS NOT NULL) AND
        (resource_kind = 'dashboard')  = (dashboard_id IS NOT NULL) AND
        (resource_kind = 'connection') = (connection_id IS NOT NULL) AND
        (resource_kind = 'document')   = (document_id IS NOT NULL)
    )
);

-- The ticket's uniqueness — one grant per (company, user, kind, resource) — as
-- one partial index per kind, since each kind's id lives in its own column. Each
-- is also the index authz's EXISTS probe uses, and ON CONFLICT names it by its
-- columns and predicate.
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_grants_agent
    ON resource_grants (company_id, user_id, agent_id) WHERE agent_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_grants_dashboard
    ON resource_grants (company_id, user_id, dashboard_id) WHERE dashboard_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_grants_connection
    ON resource_grants (company_id, user_id, connection_id) WHERE connection_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_grants_document
    ON resource_grants (company_id, user_id, document_id) WHERE document_id IS NOT NULL;

-- The user-side view (T-Z7) asks across every kind, which no partial index above
-- can serve. There is deliberately no index for the cascades: a deleted agent
-- scans a table bounded by headcount × resources, and that is not a size an
-- index pays for.
CREATE INDEX IF NOT EXISTS idx_resource_grants_user ON resource_grants (company_id, user_id);
