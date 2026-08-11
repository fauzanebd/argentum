-- The per-tenant query cookbook (T-Q8).
--
-- Every turn rediscovers the tenant's warehouse from scratch. The table picker
-- narrows which tables to read; nothing carries forward how this company's
-- questions map onto its own schema — that "revenue" means SUM(sales_amount)
-- here and SUM(net_total) somewhere else, that the fiscal year starts in
-- April, that "active customers" excludes the two internal test accounts.
--
-- All of it is already recorded. `agent_actions` holds the SQL of every
-- run_sql call (args_redacted, redacted only for credential-shaped values),
-- the row count and the outcome, joined to the question by message_id. This
-- table is that history, distilled and indexed, so a turn can be shown three
-- worked examples from its own tenant before it writes a query.
--
-- What the schema encodes:
--
--   * source_id, not just company_id. A query is only an example for the
--     warehouse it ran against — the same question against a different source
--     is a different answer, and offering the wrong dialect's SQL is worse
--     than offering none.
--   * origin_message_id, kept and enforced unique. It is what makes the
--     example auditable ("where did this come from?") and what stops the
--     harvester writing the same turn twice on a re-run.
--   * uses INTEGER, and last_used_at. An example that keeps being retrieved
--     is one that keeps matching real questions; one that never surfaces is a
--     candidate for pruning. Neither is read by the retrieval path — this is
--     bookkeeping for whoever tunes the cookbook later.
--   * No feedback column. Whether an answer was any good lives in
--     message_feedback (T-Q2), and the harvester consults it before writing.
--     Copying a verdict here would let the copy go stale the moment somebody
--     changed their mind.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS query_examples (
    id                BIGSERIAL PRIMARY KEY,
    company_id        UUID         NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_id         UUID         NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
    -- The user's question, as they asked it.
    question          TEXT         NOT NULL,
    -- The SQL that answered it, as executed.
    sql_text          TEXT         NOT NULL,
    -- What the query returned, for the model to judge whether the example is
    -- the shape it wants. A count, not the rows: the rows are the tenant's
    -- data and have no business being replayed into a later turn's prompt.
    row_count         INTEGER      NOT NULL DEFAULT 0,
    -- Which turn this came from. ON DELETE CASCADE so deleting a conversation
    -- deletes what was learned from it — a tenant who clears a thread has not
    -- consented to its questions living on in a prompt.
    origin_message_id UUID         NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    embedding         vector(1536) NOT NULL,
    model             TEXT         NOT NULL,
    uses              INTEGER      NOT NULL DEFAULT 0,
    last_used_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT uq_query_examples_origin UNIQUE (origin_message_id)
);

CREATE INDEX IF NOT EXISTS idx_query_examples_company ON query_examples(company_id, source_id);

-- No ivfflat index, deliberately, and migration 013 is the precedent: it
-- dropped exactly such an index from table_embeddings. An ivfflat index over a
-- few hundred rows is slower than the sequential scan it replaces and returns
-- approximate neighbours for the privilege. A tenant with enough examples for
-- it to pay is a tenant worth measuring first.
