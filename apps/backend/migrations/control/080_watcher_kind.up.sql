-- A watcher can watch a source's freshness, not only a metric's value (T-F3).
--
-- T-08's watcher is "evaluate a metric on a cron, fire when it breaches", and
-- the table says so: metric_id is NOT NULL with an FK. T-F1 gave sources a
-- freshness verdict, and the natural second question — "tell me when the data
-- stops arriving" — has no metric behind it. A metric whose value is the age of
-- a load would be the workaround, and it is a bad one: it would have to be
-- written per source in SQL the tenant maintains, and its breach would say
-- "revenue_freshness is 4300" rather than "sales has not loaded since Tuesday".
--
-- **kind, not a separate table.** Everything a freshness watcher needs is
-- already here and already correct: the cron, the timezone, the cooldown, the
-- dedicated thread, the channel list, the dry-run-before-enable rule, and the
-- event history. A second table would duplicate all of it to change what gets
-- evaluated, which is the one column that differs.
ALTER TABLE watchers
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'metric';

-- The source a freshness watcher watches. NULL for every metric watcher, which
-- is every row that exists today.
--
-- ON DELETE CASCADE, matching metric_id above and for the same reason: a
-- watcher whose subject is gone is not a watcher that should keep ticking, and
-- the alternative — SET NULL — leaves a row the fire path has to defend itself
-- against forever.
ALTER TABLE watchers
    ADD COLUMN IF NOT EXISTS source_id UUID REFERENCES db_connections(id) ON DELETE CASCADE;

-- metric_id has to become nullable, because a freshness watcher has no metric.
--
-- This is the one destructive-shaped statement in the file and it is not:
-- dropping NOT NULL widens what the column accepts and invalidates nothing that
-- is in it. Every existing row keeps its metric, and the CHECK below is what
-- stops the relaxation from admitting a metric watcher with no metric.
ALTER TABLE watchers
    ALTER COLUMN metric_id DROP NOT NULL;

-- The invariant the two nullable columns now need, stated where the database
-- can enforce it rather than only in the service.
--
-- Without this, `DROP NOT NULL` above would let a bug — or a hand-written
-- INSERT during an incident — create a metric watcher with no metric, which
-- the fire path would skip every tick while the dashboard showed it as enabled.
-- A watcher that is silently never going to fire is the worst row this table
-- can hold.
ALTER TABLE watchers
    DROP CONSTRAINT IF EXISTS watchers_subject_matches_kind;
ALTER TABLE watchers
    ADD CONSTRAINT watchers_subject_matches_kind CHECK (
        (kind = 'metric'    AND metric_id IS NOT NULL) OR
        (kind = 'freshness' AND source_id IS NOT NULL)
    );

-- The fire path reads a freshness watcher by source the way it reads a metric
-- watcher by metric (idx_watchers_metric, 040:70).
CREATE INDEX IF NOT EXISTS idx_watchers_source ON watchers(source_id);
