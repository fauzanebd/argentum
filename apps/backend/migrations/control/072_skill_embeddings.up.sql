-- T-K5: the vector that decides which procedures survive truncation.
--
-- `T-K3` bounds the index and drops what does not fit in `lower(name)` order,
-- which is alphabetical — an order with no relationship to the question being
-- asked. Below the bound that costs nothing, because nothing is dropped. Above
-- it, a tenant's twenty-first procedure is invisible on every turn forever, and
-- which one that is was decided by its first letter.
--
-- What is embedded is `name — when_to_use`: the index line, and therefore
-- exactly the text the model is shown before it decides whether to open a
-- skill. Embedding the body instead would rank on prose the model never sees
-- at ranking time, which is the ranker answering a different question than the
-- one the index asks.
--
-- **Nullable, and that is the whole compatibility story.** Every skill written
-- before this migration has no vector, and a tenant with no embedding
-- credentials never gets one. Both cases sort after the ranked rows and keep
-- `lower(name)` among themselves, so the feature degrades to exactly today's
-- behaviour rather than to an error.
ALTER TABLE skills
    ADD COLUMN IF NOT EXISTS embedding vector(1536),
    -- Which model produced it. Beside query_examples' `model` column and for
    -- its reason: two vectors from different models are not comparable, and a
    -- deployment that changes its embedding model needs to be able to find the
    -- rows that predate the change.
    ADD COLUMN IF NOT EXISTS embedding_model TEXT;

-- No ivfflat index, and 013 and 055 are both the precedent. A company holds at
-- most 200 skills (T-K1's fourth cap) and the ranker only runs on a company
-- over the index bound, so this is a sequential scan over tens of rows behind a
-- company_id filter. An approximate-neighbour index here would return worse
-- answers for no measurable time.

-- The partial index the ranker actually uses: a company's enabled skills that
-- have a vector. It is the same shape as idx_skills_company_enabled and exists
-- beside it because the ranking query adds `embedding IS NOT NULL` and would
-- otherwise re-scan every disabled row to find that out.
CREATE INDEX IF NOT EXISTS idx_skills_company_embedded
    ON skills(company_id) WHERE enabled AND embedding IS NOT NULL;
