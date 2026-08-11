-- Was the answer any good? (T-Q2)
--
-- Until this table, the only quality signal this product had was forty
-- synthetic questions against one demo schema. Nothing anywhere recorded
-- whether a real answer, on a real tenant's real warehouse, was right —
-- `internal/domain` held no Feedback and no Rating of any kind. So every
-- claim about reliability was a claim about the eval set.
--
-- Three shapes are in the schema rather than in the service above it.
--
--   * A separate table, not a column on `messages`. Messages are written on
--     the hot path of every turn on every channel; feedback arrives later,
--     from a different actor, and can change its mind. Putting it inline would
--     make an UPDATE on a hot row out of what is really an append elsewhere.
--   * The unique key is (message_id, actor_kind, actor_ref), so one person
--     has one vote per answer and pressing the button again replaces it. Two
--     people disagreeing about the same answer is data, not a conflict —
--     hence the actor in the key rather than the message alone.
--   * company_id and thread_id are denormalised onto the row even though
--     message_id implies both. Every read of this table is per tenant, and
--     T-Q8 joins it against agent_actions by (company_id, message_id) to
--     decide which SQL is safe to learn from. That join should not have to
--     walk back up through messages to conversation_threads to find the
--     tenant.

CREATE TABLE IF NOT EXISTS message_feedback (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id  UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    -- +1 or -1. A CHECK rather than an enum because there are exactly two
    -- values and there will not be a third: a five-star scale measures how
    -- people feel about an answer, and what this column is for is whether the
    -- answer was correct. A "neutral" third value would be indistinguishable
    -- from the absent row that already means "nobody said".
    rating     SMALLINT NOT NULL CHECK (rating IN (-1, 1)),
    -- Why, in the user's words. Optional and unbounded-in-principle, capped by
    -- the service at domain.FeedbackReasonMaxChars — a reason is the most
    -- valuable column here for anyone tuning the agent, and the least
    -- predictable in length.
    reason     TEXT NOT NULL DEFAULT '',
    -- Who said so, in the same vocabulary agent_actions uses (T-05). A widget
    -- visitor, a dashboard user and an API caller are three different kinds of
    -- witness, and averaging them without being able to tell them apart is how
    -- a tenant's own staff get outvoted by their customers' end users.
    actor_kind TEXT NOT NULL,
    actor_ref  TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_message_feedback_actor UNIQUE (message_id, actor_kind, actor_ref)
);

-- The dashboard's "what went wrong lately" list, and the only query that runs
-- without a message id in hand.
CREATE INDEX IF NOT EXISTS idx_message_feedback_company_recent
    ON message_feedback(company_id, created_at DESC);

-- T-Q8's read: the negative rows for one tenant, so a question that was
-- answered badly never becomes an example the agent learns from. Partial,
-- because the cookbook only ever asks about one of the two values and the
-- positive rows are the majority.
CREATE INDEX IF NOT EXISTS idx_message_feedback_negative
    ON message_feedback(company_id, message_id)
    WHERE rating = -1;
