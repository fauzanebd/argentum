-- Retention and erasure (T-H6).
--
-- `messages.content` and `messages.tool_calls` have held tenant data
-- indefinitely since 002. A retention mechanism existed, but only for the
-- API-observability tables (`internal/apiobs/recorder.go`) — nothing covered
-- the transcripts, and there was no erasure route at all.
--
-- Under UU PDP 27/2022 the customer is the *pengendali data* and carries the
-- erasure obligation. They cannot discharge it without an API from us.

-- 0 means "keep forever", which is what every existing row did before this
-- migration and therefore what every existing row must keep doing. A NOT NULL
-- DEFAULT 0 is forward-compatible in both directions: an old binary never
-- reads the column, and a new binary reading a row written by an old one gets
-- the behaviour that row already had.
ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS message_retention_days INTEGER NOT NULL DEFAULT 0;

-- The written completion record the ticket asks for. An erasure that leaves no
-- trace cannot be evidenced to a regulator or to the person who requested it,
-- and "we deleted it, trust us" is the answer this table exists to replace.
--
-- It is deliberately NOT deleted by an erasure — including the erasure that
-- created it. The row holds counts and timestamps, never content: the whole
-- point is that it survives the thing it describes.
CREATE TABLE IF NOT EXISTS data_erasures (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Who asked. SET NULL rather than CASCADE: a user who leaves the company
    -- must not take the record of what they erased with them.
    requested_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    -- 'all' erases every conversation; 'retention' is a purge tick recording
    -- what the nightly job removed. Text rather than an enum for 023's reason:
    -- ALTER TYPE ... ADD VALUE cannot run inside the transaction golang-migrate
    -- wraps a migration in, and a third scope is foreseeable.
    scope         TEXT NOT NULL,
    -- running | completed | failed. A row is written before the delete and
    -- updated after it, so a crash mid-erasure leaves evidence that it was
    -- attempted rather than no evidence at all.
    status        TEXT NOT NULL DEFAULT 'running',
    threads_deleted  INTEGER NOT NULL DEFAULT 0,
    messages_deleted INTEGER NOT NULL DEFAULT 0,
    error_text    TEXT,
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ
);

-- The dashboard reads a company's erasure history newest-first, which is also
-- the shape a questionnaire answer is assembled from.
CREATE INDEX IF NOT EXISTS idx_data_erasures_company_requested
    ON data_erasures(company_id, requested_at DESC);
