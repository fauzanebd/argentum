-- Dropping the table is the whole rollback: no other table references it, and
-- a company with no profile is the state every tenant was in before T-B1.
DROP TABLE IF EXISTS company_profiles;
