-- Reversing this deletes every freshness watcher rather than leaving it.
--
-- It has to: metric_id goes back to NOT NULL, and a freshness watcher has none.
-- The alternative — invent a metric for it — is worse than losing the row,
-- because the row would come back as a *metric* watcher pointed at something
-- nobody chose, on a cron, delivering to a channel.
--
-- The events those watchers wrote go with them through watcher_events'
-- ON DELETE CASCADE, which is the same thing that happens when a tenant deletes
-- a watcher by hand.
DELETE FROM watchers WHERE kind = 'freshness';

ALTER TABLE watchers DROP CONSTRAINT IF EXISTS watchers_subject_matches_kind;
DROP INDEX IF EXISTS idx_watchers_source;
ALTER TABLE watchers DROP COLUMN IF EXISTS source_id;
ALTER TABLE watchers DROP COLUMN IF EXISTS kind;
ALTER TABLE watchers ALTER COLUMN metric_id SET NOT NULL;
