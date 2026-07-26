-- Repair for demo databases seeded before 002 gained its TRIM.
--
-- TO_CHAR(d, 'Month') and TO_CHAR(d, 'Day') pad to nine characters, so the
-- original seed stored 'December ' and 'Monday   '. The obvious filter —
-- WHERE month_name = 'December' — then returns zero rows against a table that
-- clearly holds December data, and the agent has no way to see why.
--
-- Found by the T-01 eval run: correct SQL, empty result, invented total.
-- Idempotent, so running it against an already-correct database is a no-op.

\c demo_analytics;

UPDATE dim_date SET month_name = TRIM(month_name) WHERE month_name <> TRIM(month_name);
UPDATE dim_date SET day_name   = TRIM(day_name)   WHERE day_name   <> TRIM(day_name);
