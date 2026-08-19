-- Did this tool call happen on a turn that had read somebody else's document?
-- (T-P10)
--
-- **A deviation from the ticket, recorded rather than hidden.** T-P10 says
-- "Migration: none" and asks for the taint tag on the audit row. Those two
-- cannot both be true: `agent_actions` has no free-form column, and the
-- alternatives — folding a flag into `args_redacted`, or inferring the taint
-- afterwards by looking for a `search_documents` row in the same thread — are
-- both worse than a boolean. The first puts a fact about the turn inside a
-- record of one call's arguments, where nothing can filter on it; the second is
-- a join that gets the answer wrong the moment two turns share a thread, which
-- is every turn after the first.
--
-- What it is for: the question a customer security review asks is "show me
-- every write this agent performed on a turn that had read an uploaded file",
-- and that question has to be answerable by a WHERE clause. Until `T-H9` lands,
-- this column is telemetry and nothing gates on it — which is the T-Q11 shape,
-- and it is deliberate: count first, gate once the rate is known.
--
-- DEFAULT false, so every row written before this migration is what it in fact
-- was: a call on a turn that could not have read a document, because there was
-- no way to.

ALTER TABLE agent_actions
    ADD COLUMN IF NOT EXISTS document_tainted BOOLEAN NOT NULL DEFAULT false;

-- Partial, because the interesting rows are the rare ones. An index over a
-- column that is false on every row but a handful is an index that is never
-- used; one that only holds the true rows answers the review's question
-- directly and costs almost nothing to maintain.
CREATE INDEX IF NOT EXISTS idx_agent_actions_document_tainted
    ON agent_actions(company_id, created_at DESC)
    WHERE document_tainted;
