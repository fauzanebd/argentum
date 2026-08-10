-- The widget as a channel (T-20).
--
-- Filed as `*_thread_embed`, next free on landing: 051 is embed_keys, so this
-- is 052. The ticket's original 029 was spent by webhook_delivery in T-A2.
--
-- **Why a column of its own rather than reusing `api_user_ref`.** Both are
-- "a string the tenant chose for one of their own people", and reusing the
-- column would have been one migration shorter. It is wrong for the same reason
-- `T-A3` refuses to let a `/v1` key append to a dashboard thread: the two
-- surfaces are reached with different credentials. An API key is a server-side
-- secret and an embed session is minted for a browser, so a filter that forgot
-- to also compare `channel` would let one read the other's conversations — and
-- a filter that forgets is exactly the failure the separate column makes
-- impossible rather than merely unlikely.

ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS embed_user_ref TEXT;

-- The resolve path's read: this visitor's newest live conversation. Partial,
-- because every thread from the other six channels has a NULL here and none of
-- them is ever looked up this way — the index covers widget threads only.
--
-- Not UNIQUE, which the ticket's line suggests. A visitor accumulates threads
-- over time: the idle-gap fork opens a new one and the old ones stay readable,
-- exactly as they do for WhatsApp and Discord. Uniqueness on
-- (company_id, embed_user_ref) would forbid that; uniqueness including `id`
-- would be satisfied by the primary key alone and enforce nothing. What the
-- index is actually for is making `ORDER BY last_message_at DESC LIMIT 1` a
-- lookup instead of a scan.
CREATE INDEX IF NOT EXISTS idx_threads_embed_user
    ON conversation_threads(company_id, embed_user_ref, last_message_at DESC)
    WHERE embed_user_ref IS NOT NULL;
