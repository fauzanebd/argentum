-- Did anybody click the suggestion? (T-U13)
--
-- T-Q10 puts next-step suggestions under every answer, and a suggestion nobody
-- clicks is worse than no suggestion: it is a row of buttons occupying the space
-- below the thing the reader came for, plus one light-model call on every turn.
-- This table is what says which of those two it is.
--
-- It is also the first signal this product has about **what customers actually
-- want next**, as opposed to what forty synthetic eval questions ask. The eval
-- set is written by us; a pick is a business user choosing, in their own
-- workspace, out of options the agent proposed against their own data.
--
-- Four shapes are in the schema rather than in the service above it.
--
--   * A separate table, not a column on `messages`. Same reason 054 gives for
--     feedback: messages are written on the hot path of every turn, and a pick
--     arrives later, from a reader, and is an append.
--   * NO unique key. This is the deliberate difference from message_feedback,
--     whose (message_id, actor_kind, actor_ref) key exists so pressing the
--     button again REPLACES a verdict. A pick is not a verdict — it is an event.
--     Somebody who clicks two of the three chips has told us both were worth
--     clicking, and collapsing that to one row would delete the more interesting
--     half of the answer.
--   * `recommended` is stored rather than derived. The chip's own flag lives
--     inside the message's JSONB metadata, and the question this table exists to
--     answer — "does marking one as recommended change what people click?" —
--     would otherwise need a JSONB traversal per row against a spec version that
--     may have moved on. It is three bytes and it is the whole experiment.
--   * `label` is copied, not referenced. The suggestion lives in the message's
--     metadata and a future edit or a spec migration could change it; what was
--     on the button at the moment somebody pressed it is the thing being
--     recorded. Same argument dashboard_query_log makes for storing sql_text
--     inline.
--
-- company_id is denormalised onto the row even though message_id implies it, for
-- 054's reason: every read of this table is per tenant, and it should not have
-- to walk back up through messages to conversation_threads to find one.

CREATE TABLE IF NOT EXISTS suggestion_picks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    message_id  UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    -- Which chip, 0-based, in the order they were rendered. Kept alongside the
    -- label because position is what a layout experiment moves and the label is
    -- what the reader read.
    idx         INTEGER NOT NULL,
    -- Whether the chip the reader chose was the one the agent marked. The
    -- comparison this table exists to make.
    recommended BOOLEAN NOT NULL DEFAULT false,
    -- The chip's text as it was rendered. Capped by the service at the same 48
    -- characters the chip is truncated to.
    label       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The only query that runs without a message id in hand: pick-rate for one
-- tenant over a window.
CREATE INDEX IF NOT EXISTS idx_suggestion_picks_company_recent
    ON suggestion_picks(company_id, created_at DESC);
