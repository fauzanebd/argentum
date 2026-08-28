-- Reverses 072. Dropping these costs one embedding call per skill to rebuild,
-- which is the cheapest thing in this schema to lose: unlike 069's bodies, a
-- vector is derived and the text it was derived from is still in the row beside
-- it.
DROP INDEX IF EXISTS idx_skills_company_embedded;

ALTER TABLE skills
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding;
