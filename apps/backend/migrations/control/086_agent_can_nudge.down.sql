-- Drops the flag, and with it every admin's decision to let an agent ask.
--
-- Unlike 043's and 081's backfills, whose `down` is `SELECT 1;`, there is nothing
-- to protect by keeping it: the release this rolls back to has no nudge_agent to
-- offer, so a kept flag would reach nothing — and the release before this one
-- does not read the column, so dropping it breaks no pod still running.
ALTER TABLE agents DROP COLUMN IF EXISTS can_nudge;
