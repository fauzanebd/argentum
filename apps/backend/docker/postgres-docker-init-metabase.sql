-- Runs once when postgres_data volume is empty (official entrypoint convention).
-- Metabase connects with MB_DB_DBNAME=metabase_app; without this DB the container
-- crashes during startup migrations (JdbcException "database metabase_app does not exist").
CREATE DATABASE metabase_app;
