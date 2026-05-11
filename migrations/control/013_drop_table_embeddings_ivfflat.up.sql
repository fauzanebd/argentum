-- ivfflat created on an empty table at migration 011 has degenerate
-- centroids: with default probes=1 and lists=100, every query lands on the
-- same list and TopK returns ~1 hit regardless of input (always
-- tbMaster_Size in the affected tenant). At current scale (~hundreds of
-- rows per source) a sequential scan is sub-millisecond and gives 100%
-- recall. Re-add HNSW (not ivfflat) when any source crosses ~10k vectors.
DROP INDEX IF EXISTS idx_table_embeddings_vec;
