-- Reversing 008 loses each source's description, and with it the catalog line
-- the agent reads when deciding which database a question is about. The
-- descriptions are LLM-generated and can be regenerated, at the cost of one
-- model call per source.
ALTER TABLE db_connections
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS description_source;
