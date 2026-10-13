-- RIZ-89 down. With the previous release back, the lazy grant in CheckBudget
-- resumes, so every company that was waiting here is credited at its next
-- budget check. No balance is touched by this file.
DROP TABLE IF EXISTS credit_grant_requests;
