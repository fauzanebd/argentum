-- Reversing 013 puts the ivfflat index back, which is what the up migration
-- removed on purpose: created on an empty table it has degenerate centroids,
-- and with probes=1 every query lands on the same list and returns roughly one
-- hit whatever was asked. Restoring it restores that bug.
--
-- It is written anyway, because a down that silently does nothing is worse than
-- one that faithfully restores a state somebody chose to return to — and the
-- comment above is what they should read first. If you want the index for
-- scale rather than for symmetry, build HNSW instead.
CREATE INDEX IF NOT EXISTS idx_table_embeddings_vec
    ON table_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
