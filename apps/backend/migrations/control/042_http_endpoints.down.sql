-- Reverse of 042_http_endpoints. One table, no dependents: http_endpoints is
-- referenced by nothing (an action_invocation names an endpoint by string, not by
-- foreign key, so the record of a past call outlives the endpoint it called).
DROP TABLE IF EXISTS http_endpoints;
