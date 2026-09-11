-- When was this source last loaded (T-F1).
--
-- Everything this product built to make a number trustworthy compares the
-- *answer* to the *result set*. CheckGrounding checks that every figure in the
-- reply appears in the rows a tool returned; CheckFabrication checks the turn
-- retrieved anything at all. Neither can see that the result set is a day old.
-- So a warehouse whose nightly load failed at 02:00 produces an answer that is
-- grounded, cited, non-fabricated and wrong, with every guard vouching for it.
--
-- The shape is dbt's `source freshness`: an expression the tenant supplies that
-- reports when their data last loaded, and two thresholds over it.
--
-- **Nullable, no default, no backfill.** Every source that exists today has no
-- expression and therefore reports `unknown`, which says nothing to anybody —
-- so a deployment that applies this migration and configures nothing behaves
-- exactly as it did. Absence cannot mean "stale" for the same reason 068's
-- absence cannot mean "deny all": a row written before the feature existed must
-- keep the behaviour it had.
--
-- **freshness_sql is tenant-supplied SQL that this product runs on a schedule
-- against the customer's database**, which is precisely T-H4's category. It is
-- validated through internal/sqlguard on save — a single SELECT, no mutating
-- keyword — and runs inside the same read-only transaction every other tenant
-- query does.
ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS freshness_sql TEXT;

-- Minutes, not intervals: the two readers are Go code and a settings form, and
-- both want an integer. Nullable rather than 0-defaulted so "never configured"
-- and "configured to zero" stay distinguishable — a 0 stale threshold would
-- report every source stale the instant it loaded.
ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS freshness_warn_after_mins INTEGER;

ALTER TABLE db_connections
    ADD COLUMN IF NOT EXISTS freshness_stale_after_mins INTEGER;

-- No index. The only read is "give me this source", which already loads the
-- whole row by primary key; nothing filters or orders on a freshness column.
