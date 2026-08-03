-- Reversing 001 removes the tenancy itself: companies, users, connections and
-- the phone allowlist. Everything else in this directory hangs off these by
-- foreign key, so this is the last down to run and it takes the product with it.
--
-- The pgcrypto extension is deliberately left installed. It is a database-level
-- object that another schema in the same database may be using, and dropping an
-- extension somebody else depends on is a worse outcome than leaving one behind.
DROP TABLE IF EXISTS allowed_phone_numbers;
DROP TABLE IF EXISTS db_connections;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS companies;
