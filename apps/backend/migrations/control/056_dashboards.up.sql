-- T-D5: native dashboards — a dashboard Argentum owns, executes and audits.
--
-- What this replaces is not an analytics engine. Metabase here is a chart
-- renderer, a dashboard host and a link server; the hard parts (schema
-- introspection, read-only execution across three dialects, tenant connection
-- pooling, the metric registry, the CVD-gated palette, the agent that writes the
-- SQL) are already ours. What a Metabase card costs us is everything around the
-- edges: it re-executes on Metabase's own connection so we cannot say when it
-- last ran or who read the customer's warehouse, its public link has no expiry
-- and no revocation, its rows are uncapped because Metabase runs them, and every
-- tenant DSN has to be mirrored into a second system to make any of it work.
--
-- A row here stores a QUESTION AND A COLUMN MAPPING, never values. That single
-- rule is the difference between a dashboard and a screenshot: the panel SQL
-- carries {{tokens}} that bind at request time, and a filter's default window is
-- a preset name ('last_30d') rather than two timestamps, which are correct on
-- the day they were saved and quietly wrong every day after.
--
-- Three decisions this table makes, and what each rejected:
--
-- 1. ONE JSONB COLUMN, NOT FIVE TABLES. A dashboard is authored as a unit by one
--    tool call, carries a spec_version, and is read whole on every view.
--    Normalising panels, filters, mappings and layout into their own tables buys
--    a query nobody has asked for and costs a migration per spec field plus a
--    three-way join on the hot path. The spec is validated in Go
--    (internal/dashboard/spec) before it is written, which is where the rules
--    can say why they exist.
--
-- 2. A NEW TABLE, NOT COLUMNS ON saved_dashboards. That row (006) is a pointer
--    at a Metabase object, and its thread_id is NOT NULL ... ON DELETE CASCADE —
--    so tidying up the chat thread that happened to create a dashboard would
--    delete the dashboard somebody opens every Monday. Here thread_id is
--    nullable provenance and does not cascade. 006's rows stay read-only through
--    the deprecation window and are dropped in T-D16; nothing converts, because a
--    Metabase card's definition lives in Metabase and not in our database.
--
-- 3. source_id IS ON DELETE RESTRICT, which differs from 039_metric_definitions
--    on purpose. Deleting a connection a dashboard reads should fail loudly at
--    the delete rather than silently empty the dashboard. A metric is one
--    definition an admin can rewrite in a minute; a dashboard is a dozen panels
--    somebody's Monday depends on, and the person deleting the connection is the
--    one who can still choose not to.
--
-- Forward-compatible by construction (docs/AGENTS.md §2): new table only,
-- nothing a running binary reads is touched, so the previous release keeps
-- working against this schema.
CREATE TABLE IF NOT EXISTS dashboards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Provenance, not ownership: which conversation produced this dashboard, if
    -- one did. NULL for a dashboard created through the API, and SET NULL rather
    -- than CASCADE when the thread goes — see decision 2 above.
    thread_id    UUID REFERENCES conversation_threads(id) ON DELETE SET NULL,
    -- The default source every panel inherits. RESTRICT: see decision 3.
    source_id    UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    -- The dashboard spec: panels, mappings, filters, layout. Shaped by
    -- internal/dashboard/spec.Dashboard and validated before every write.
    spec         JSONB NOT NULL,
    -- Denormalised out of the JSON so a reader can decide whether it understands
    -- the row before it parses it, and so a future migration can find the rows
    -- it has to rewrite with an index rather than a full scan.
    spec_version INTEGER NOT NULL DEFAULT 1,
    -- NULL means "do not auto-refresh". The resolver floors this server-side;
    -- the column stores what was asked for, not what will be honoured, so
    -- lowering the floor later does not need a data migration.
    refresh_secs INTEGER,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The list page: a company's dashboards, newest first.
CREATE INDEX IF NOT EXISTS idx_dashboards_company ON dashboards(company_id, created_at DESC);
-- "What did this thread produce?" — partial, because most rows have no thread.
CREATE INDEX IF NOT EXISTS idx_dashboards_thread ON dashboards(company_id, thread_id) WHERE thread_id IS NOT NULL;
