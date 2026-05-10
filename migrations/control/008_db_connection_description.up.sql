-- Per-connection description used as a hint for the LLM agent when picking
-- which data source to query. `description_source` tracks how the value got
-- there: '' (uninitialised, autogen pending), 'auto' (LLM-generated), or
-- 'manual' (user-edited; never overwritten by autogen).
ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS description        TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS description_source TEXT NOT NULL DEFAULT '';
