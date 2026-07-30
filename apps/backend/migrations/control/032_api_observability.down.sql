-- Reverse of 032_api_observability (T-A5). Dropping a table drops its indexes,
-- but they are named first so a tree left half-migrated by a failed `up` comes
-- back clean rather than colliding on the next attempt.
DROP INDEX IF EXISTS idx_api_request_errors_company_recent;
DROP INDEX IF EXISTS idx_api_request_errors_key_recent;
DROP TABLE IF EXISTS api_request_errors;

DROP INDEX IF EXISTS idx_api_request_stats_company_window;
DROP TABLE IF EXISTS api_request_stats;
