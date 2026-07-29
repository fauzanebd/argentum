-- One turn, one agent (T-S2).
--
-- 030 stored a roster nothing read. These three columns are what make a row in
-- `agents` decide a turn: which agent a conversation belongs to, which agent
-- made a tool call, and which agent spent the money.

-- The thread's agent. ON DELETE SET NULL rather than CASCADE or RESTRICT: a
-- conversation must not become unusable — or worse, disappear — because an
-- admin tidied the roster. A NULL here falls back to the company default at
-- turn time, which is the same path a thread that never named an agent takes.
--
-- Nothing sets this column yet. The dashboard picker is T-S3 and the channel
-- bindings are T-S4; until one of them lands every thread is NULL and every
-- turn resolves to the company default, which is exactly what 030's backfill
-- created.
ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;

-- The audit log's agent, deliberately without a foreign key — the same
-- reasoning its thread_id already carries (023_agent_actions.up.sql:20). The
-- log is append-only and must outlive what it describes: "which agent ran this
-- query" is a question asked *about deleted agents* more often than about live
-- ones, and an FK would either delete the evidence or block the deletion.
ALTER TABLE agent_actions
    ADD COLUMN IF NOT EXISTS agent_id UUID;

-- Same shape, same reason, plus the index that makes it answerable: "what does
-- the Finance agent cost us" is the first question a customer with four agents
-- asks, and it is a per-company rollup over a table that only ever grows.
ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS agent_id UUID;

CREATE INDEX IF NOT EXISTS idx_usage_events_company_agent
    ON usage_events(company_id, agent_id);

-- No matching index on agent_actions, deliberately. Its reads are by company
-- and window, by thread, or by request id — all three already indexed (023,
-- 026) — and the agent id is a column those queries return rather than one
-- they filter on. An index nobody's WHERE clause names is write cost on the
-- one table in the schema that only ever grows.
