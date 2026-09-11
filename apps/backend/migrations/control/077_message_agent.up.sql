-- Which agent wrote this message (T-N1).
--
-- 031 gave `agent_actions` and `usage_events` an agent_id and gave `messages`
-- none. The gap is invisible while a thread holds one agent — the thread's own
-- agent_id is the answer — and it stops being invisible the moment a
-- conversation can hold two (T-N2). Two readers break at once: the person
-- reading the transcript, and ChatRunner.hydrateMemory, which replays prior
-- messages into the next turn's context and would replay several personas as
-- one undifferentiated `assistant` voice.
--
-- **No foreign key**, which is 031's stated choice for agent_actions and the
-- same reasoning: this row must outlive what it describes. "Which agent said
-- that" is asked about deleted agents more often than about live ones, and an
-- FK would either delete the evidence or block the deletion.
--
-- **No index.** Transcript reads are by (thread_id, created_at), which
-- 002_threading.up.sql:39 already covers, and the agent id is a column those
-- queries return rather than one they filter on. An index nobody's WHERE clause
-- names is write cost on a table that only ever grows.
ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS agent_id UUID;

-- Backfill: an assistant row belongs to whichever agent its thread runs as.
--
-- Assistant rows only, deliberately. A `user` row has no agent and NULL there
-- is the truthful answer rather than a missing one — the moment this column
-- means "the agent it was addressed to" on some rows and "the agent that wrote
-- it" on others, T-N3's addressing becomes unreadable.
--
-- Threads with a NULL agent_id are left alone: they ran as the company default,
-- and which agent that was at the time is not recoverable from this table. A
-- NULL here reads as "unattributed", which is true, rather than as a guess.
UPDATE messages m
   SET agent_id = t.agent_id
  FROM conversation_threads t
 WHERE m.thread_id = t.id
   AND m.role = 'assistant'
   AND t.agent_id IS NOT NULL
   AND m.agent_id IS NULL;
