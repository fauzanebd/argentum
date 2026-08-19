-- Why was a human asked to decide an action their workspace auto-approves?
-- (T-H9)
--
-- `062` added the taint tag and said in as many words that it was telemetry
-- "until T-H9 lands, count first, gate once the rate is known". This is T-H9,
-- and the gate needs one thing the tag does not carry: the *reason*, on the row
-- the approver reads.
--
-- Without it the approval card is worse than no card. An admin who switched a
-- kind to auto-approve, and then finds a proposal waiting for them anyway, has
-- no way to tell a policy from a bug — and the correct reading ("this turn read
-- a file somebody else wrote, so nothing it proposes executes unattended") is
-- exactly the sentence that makes them approve it or refuse it for the right
-- reason. A boolean would need a code lookup to render that, and a code lookup
-- renders whatever this release believes rather than what was true when the
-- proposal was made.
--
-- TEXT rather than a flag for the same reason `T-H8` is still open: the
-- untrusted input a turn reads is documents today and tool results tomorrow, so
-- the column stores the sentence the gate wrote rather than a code this schema
-- would have to learn. It names the document filenames, which are already
-- stored in `source_documents.filename` and already shown to this tenant — no
-- new disclosure, and the service bounds the length before writing.
--
-- DEFAULT '' so every proposal written before this migration reads as what it
-- was: one nothing forced, because nothing could.

ALTER TABLE action_invocations
    ADD COLUMN IF NOT EXISTS approval_forced_reason TEXT NOT NULL DEFAULT '';

-- Partial, and the same argument `062`'s index carries: the rows worth finding
-- are the rare ones. "Show me every action this agent proposed on a turn that
-- had read somebody else's file" is the question a security review asks, and it
-- should be a WHERE clause rather than a scan.
CREATE INDEX IF NOT EXISTS idx_action_invocations_approval_forced
    ON action_invocations(company_id, proposed_at DESC)
    WHERE approval_forced_reason <> '';
