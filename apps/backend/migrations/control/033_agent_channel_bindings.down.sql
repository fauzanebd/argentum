-- Dropping the table is enough: both indexes go with it, and no other table
-- references it. Threads pinned by a binding keep their agent_id — 031 owns
-- that column, and a conversation that ran as the Ops agent still did.
DROP TABLE IF EXISTS agent_channel_bindings;
