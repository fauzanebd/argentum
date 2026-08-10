-- Reversing this drops the identity every widget conversation is keyed by. The
-- threads and their messages survive, but nothing can find them again: a
-- visitor returning to the page starts a new conversation with no history, and
-- the old rows become unreachable from every read path. Nothing else in the
-- schema references the column, so the drop is safe — it is the data that is
-- not recoverable.

DROP INDEX IF EXISTS idx_threads_embed_user;
ALTER TABLE conversation_threads DROP COLUMN IF EXISTS embed_user_ref;
