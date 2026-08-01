-- Reverse of 041_company_actions. action_invocations first: it has no dependents,
-- and dropping company_actions does not cascade to it (the two are linked only by
-- company_id + action_kind, not by a foreign key).
DROP TABLE IF EXISTS action_invocations;
DROP TABLE IF EXISTS company_actions;
