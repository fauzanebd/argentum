-- Reverses 077. Dropping the column discards the attribution of every message
-- written while it existed; the backfill is reconstructible from
-- conversation_threads.agent_id but a multi-agent thread's is not, which is the
-- honest direction for this to fail in — it loses data that only exists here,
-- and says so, rather than leaving a half-attributed table behind.
ALTER TABLE messages
    DROP COLUMN IF EXISTS agent_id;
