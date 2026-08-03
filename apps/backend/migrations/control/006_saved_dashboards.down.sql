-- Reversing 006 drops the record of which Metabase dashboards the agent built.
-- The dashboards themselves live in Metabase and survive; what is lost is the
-- link back to the thread that asked for each one.
DROP TABLE IF EXISTS saved_dashboards;
