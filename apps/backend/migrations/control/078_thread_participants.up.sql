-- Several agents in one conversation (T-N2).
--
-- `conversation_threads.agent_id` (031) says which agent a conversation *is*.
-- Membership is a different question with a different cardinality, and there
-- was nowhere to put it. This table is that place; nothing routes to it until
-- T-N3, so a deployment that applies this migration behaves identically.
--
-- **`conversation_threads.agent_id` keeps its column and its meaning**, and
-- gains a name: the *default speaker*, who answers a message that addresses
-- nobody. Deliberately not migrated away into this table. The common path — one
-- agent, no addressing — stays a single row on a table the resolver already
-- reads, and ChatRunner.resolveAgent's fallback chain is untouched.
CREATE TABLE IF NOT EXISTS thread_participants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    thread_id  UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,

    -- ON DELETE CASCADE, which is the opposite of conversation_threads.agent_id's
    -- ON DELETE SET NULL, and the difference is the point: a deleted agent must
    -- not strand a *conversation*, but it should certainly leave the *room*. A
    -- participant row naming no agent is not a fact about anything.
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,

    -- SET NULL rather than CASCADE: a departed employee must not delete the
    -- rooms they set up. The column answers "who added this agent", and "a
    -- person who no longer works here" is a better answer than the row's
    -- absence.
    added_by   UUID REFERENCES users(id) ON DELETE SET NULL,

    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One row per agent per thread. The service refuses a duplicate before it
    -- reaches here; this is what makes the backfill re-runnable and what stops
    -- two concurrent adds from both winning.
    UNIQUE (thread_id, agent_id)
);

-- The only read pattern: "who is in this conversation", on opening it. The
-- UNIQUE constraint's index leads with thread_id and would serve it, but it is
-- an implementation detail of a constraint and not a promise; this is the
-- promise.
CREATE INDEX IF NOT EXISTS idx_thread_participants_thread
    ON thread_participants(thread_id);

-- **There is deliberately no backfill, and this table is deliberately not the
-- whole room.**
--
-- The obvious migration inserts one row per existing thread naming its
-- agent_id. It was written that way first and then removed, because it makes
-- this table the only answer to "who is in this conversation" — and threads are
-- created by six paths (the dashboard's POST, and the WhatsApp, Discord, Lark,
-- Slack and API enqueue paths), so every one of them would have to remember to
-- write a row here. That is the shape of the T-K1 defect: a binding table
-- written by one path and read by another, invisible to every unit test, wrong
-- for five days.
--
-- Instead `conversation_threads.agent_id` remains the default speaker and is
-- read as an implicit member, and this table holds *the others*.
-- ThreadParticipantService.List returns the union, so a thread nobody has ever
-- added an agent to is a room of one with no rows here at all — which is every
-- thread that exists today, without a backfill having had to touch them.
--
-- The cost is one join's worth of assembly in the service instead of one query.
-- What it buys is that no write path can forget, including the five that were
-- written before this table existed and the ones written after it.
