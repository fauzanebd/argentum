-- Reverses 076. What is lost is the record of which examples a sweep had
-- retired and why — and because nothing was deleted, dropping the columns
-- *restores* every archived example to retrieval on the next turn.
--
-- That is the honest direction for a down migration to fail in (nothing is
-- unrecoverable) and it is worth knowing before running it on a tenant whose
-- warehouse really did change: they will be shown queries against tables that
-- no longer exist until the next sweep after re-applying.
DROP INDEX IF EXISTS idx_query_examples_live;

ALTER TABLE query_examples
    DROP COLUMN IF EXISTS archive_reason,
    DROP COLUMN IF EXISTS archived_at;
