-- Drops the table and, with it, every native dashboard. There is nothing to
-- preserve on the way out: a dashboard's definition lives entirely in this row,
-- and the Metabase rows it was written to replace are untouched in
-- saved_dashboards. The indexes go with the table.
DROP TABLE IF EXISTS dashboards;
