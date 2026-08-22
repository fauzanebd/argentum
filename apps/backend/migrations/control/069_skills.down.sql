-- Reverses 069. Every procedure a tenant wrote goes with it, which is the
-- honest cost of a down migration on a table whose contents are authored rather
-- than derived: there is nowhere else to put them, and unlike a cache or an
-- index they cannot be rebuilt from anything the system still holds.
--
-- Bindings first, though the CASCADE would do it: a down migration that relies
-- on a foreign key it is about to drop reads as if the order did not matter.
DROP INDEX IF EXISTS idx_agent_skills_skill;
DROP TABLE IF EXISTS agent_skills;

DROP INDEX IF EXISTS idx_skills_company_enabled;
DROP INDEX IF EXISTS idx_skills_company_name;
DROP TABLE IF EXISTS skills;
