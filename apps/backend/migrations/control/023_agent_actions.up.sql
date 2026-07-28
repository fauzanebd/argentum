-- Agent action audit log (T-05, finding S-5).
--
-- `usage_events` records what a turn cost, not what it did. Before the agent
-- can act on anyone's behalf there has to be an immutable record of every tool
-- it ran, under whose authority, with which arguments, and what came back.
--
-- Filed as T-05's `021_agent_actions`, renumbered to 023: golang-migrate only
-- applies versions above the schema's current one, and 021/022 are already
-- taken by T-04's invites and T-R5's report branding.
--
-- Neither thread_id nor message_id carries a foreign key, and that is the
-- point. `DELETE /api/threads/:id` exists, so a CASCADE would let a user erase
-- the record of what the agent did in a thread by deleting the thread, and a
-- SET NULL would erase which thread it was. The log outlives its subject. The
-- company FK stays because a deleted tenant takes its whole world with it.

CREATE TABLE IF NOT EXISTS agent_actions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id      UUID,
    message_id     UUID,
    -- user | schedule | watcher (T-08) | api_key (T-13). Text rather than an
    -- enum type: T-13 and T-19 each add a kind, and ALTER TYPE ... ADD VALUE
    -- cannot run inside the transaction golang-migrate wraps a migration in.
    actor_kind     TEXT NOT NULL,
    actor_ref      TEXT NOT NULL DEFAULT '',
    channel        TEXT NOT NULL DEFAULT '',
    tool_name      TEXT NOT NULL,
    source_id      TEXT NOT NULL DEFAULT '',
    args_redacted  JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- sha256 of the arguments as the tool received them, before redaction, so
    -- two calls that differ only in a redacted field are still distinguishable.
    args_hash      TEXT NOT NULL,
    -- ok | error | blocked | truncated
    result_status  TEXT NOT NULL,
    error_text     TEXT,
    -- NULL for tools that return no rows at all, which is different from a
    -- query that returned zero.
    rows_returned  INTEGER,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The audit endpoint reads a company's log newest-first over a time window.
CREATE INDEX IF NOT EXISTS idx_agent_actions_company_created
    ON agent_actions(company_id, created_at DESC);

-- Per-thread replay: "what did the agent do in this conversation".
CREATE INDEX IF NOT EXISTS idx_agent_actions_thread
    ON agent_actions(thread_id);
