-- Reversing 014 loses the cache-token columns, so prompt-cache reads and writes
-- stop being billable separately and the totals beside them under-report what a
-- cached turn actually cost.
ALTER TABLE usage_events
    DROP COLUMN IF EXISTS cache_create_tokens_in,
    DROP COLUMN IF EXISTS cache_read_tokens_in;
