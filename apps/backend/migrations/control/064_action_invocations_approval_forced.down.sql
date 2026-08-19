-- Down for 064. The index first, for `062`'s reason: dropping the column would
-- take it with it, and a down migration whose steps rely on cascade behaviour
-- breaks the day somebody adds a second index.
--
-- This down loses history rather than a capability — the proposals stay, and
-- what goes is the record of which of them a human decided only because the
-- turn had read a document. That is worth stating: after a down and an up, a
-- proposal that WAS force-approved reads as one that was not.

DROP INDEX IF EXISTS idx_action_invocations_approval_forced;

ALTER TABLE action_invocations DROP COLUMN IF EXISTS approval_forced_reason;
