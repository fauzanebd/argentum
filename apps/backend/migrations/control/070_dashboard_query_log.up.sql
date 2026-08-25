-- T-D9 · What ran against a tenant's warehouse on a dashboard's behalf.
--
-- **Its own table, not rows in `agent_actions`, and the reason is retention.**
-- `agent_actions.args_redacted` holds redacted arguments by design, which is
-- why T-H6 exempts audit rows from erasure: they carry no tenant content and
-- should outlive conversations. `sql_text` below is the rendered statement
-- **verbatim, literals included**. Putting it in `agent_actions` forces one of
-- two bad outcomes — redact it and lose the question "what ran against my
-- database last month", or keep it and void T-H6's exemption for that whole
-- table.
--
-- The second argument is structural: `WithAudit` decorates `interfaces.Tool`,
-- and a share-page render or a scheduled refresh is not a tool call. Writing
-- into `agent_actions` from the dashboard resolver would be a second write path
-- into a table whose design is one row per tool execution written in one place.
--
-- **A row is written only on a cache miss** (T-D8). With request collapsing and
-- a 60s TTL, misses are bounded by panels ÷ TTL per dashboard per replica
-- regardless of how many people are watching — roughly a dozen rows a minute in
-- the worst case and near zero in steady state. That is not a concession to
-- volume: the event worth recording is "the customer's warehouse was read", not
-- "a browser rendered a number it already had".
CREATE TABLE IF NOT EXISTS dashboard_query_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- No foreign key, deliberately: the log outlives the dashboard. "What ran
    -- against my database last month" must stay answerable after the dashboard
    -- that ran it is deleted, which a CASCADE would silently prevent.
    dashboard_id UUID,
    panel_id     TEXT NOT NULL,
    source_id    UUID NOT NULL,
    -- user | share | schedule. TEXT rather than an enum, mirroring
    -- agent_actions.actor_kind so the two logs read side by side — and for that
    -- column's own stated reason: ALTER TYPE ... ADD VALUE cannot run inside
    -- the transaction golang-migrate wraps a migration in.
    actor_kind   TEXT NOT NULL,
    actor_ref    TEXT NOT NULL DEFAULT '',
    sql_text     TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}'::jsonb,
    row_count    INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dashboard_query_log_company
    ON dashboard_query_log(company_id, created_at DESC);
