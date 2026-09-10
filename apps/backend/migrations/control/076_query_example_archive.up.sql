-- T-Q15: the cookbook learns to forget.
--
-- `055` wrote `uses` and `last_used_at` and said so in its own comment: "an
-- example that keeps being retrieved is one that keeps matching real
-- questions; one that never surfaces is a candidate for pruning. Neither is
-- read by the retrieval path — this is bookkeeping for whoever tunes the
-- cookbook later." Nobody became that person. `MarkUsed` has been writing both
-- columns on every retrieval since T-Q8 shipped and `TopK` reads neither, so
-- an example harvested in March ranks against one harvested yesterday on
-- cosine distance alone, forever.
--
-- The sharper problem is not age, it is drift. `source_id` cascades, so
-- deleting a warehouse takes its examples with it — but renaming a table
-- *inside* a live warehouse does not. The example keeps ranking, and the model
-- is shown a worked query that would now fail, on the one surface whose entire
-- purpose is "imitate this". `055`'s answer to that was
-- `DeleteByCompany` — its comment calls it "the escape hatch for a tenant
-- whose schema changed underneath it, where every example is now wrong and the
-- fastest fix is to forget and re-harvest". An all-or-nothing hammer, swung by
-- hand, for a condition that is almost never all-or-nothing.
--
-- Two columns, and the design is that neither deletes anything:
--
--   * archived_at — set, an example is invisible to retrieval and to nothing
--     else. The row stays. `origin_message_id` is still unique against it, so
--     the harvester will not silently re-learn what a sweep just archived; if
--     the tenant renames the table back, an admin can clear the column and the
--     example returns with its `uses` history intact. Deleting would make
--     "re-harvest" the only recovery, which is the hammer again.
--   * archive_reason — why, in one machine-readable word. `schema_drift` and
--     `unused` today. It exists because "the cookbook shrank" is a question
--     somebody will ask, and the two causes want completely different
--     responses: one means the warehouse moved, the other means the examples
--     were never any good.
--
-- The partial index is the point of the whole migration. Retrieval filters
-- `archived_at IS NULL` on every turn, and a partial index over the live rows
-- is smaller than the full one it replaces — a tenant whose cookbook is half
-- archived pays for half of it.

ALTER TABLE query_examples
    ADD COLUMN IF NOT EXISTS archived_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS archive_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_query_examples_live
    ON query_examples(company_id, source_id)
    WHERE archived_at IS NULL;
