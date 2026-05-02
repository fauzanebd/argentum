# Migrations

Argentum has two distinct schemas. They live in separate folders because they
target different databases and have different lifecycles.

## `control/`

The Argentum control plane Postgres database. Stores companies, users,
encrypted DB connection strings, allowed phone numbers, conversation threads,
messages, usage events, and credit balances.

These migrations run automatically on backend startup via the `golang-migrate`
runner wired in `cmd/api/main.go`. They are also applied at first-run by the
Postgres container's `docker-entrypoint-initdb.d` mount in
`docker-compose.yml`.

Filename convention: golang-migrate expects `NNN_description.up.sql` (and
optional `NNN_description.down.sql`). Each `.up.sql` is one forward migration.
We omit `.down.sql` today — backups are the recovery story.

## `demo_tenant/`

A retail star-schema fixture (dim_date, dim_customers, dim_products,
fact_sales) plus seed data. **Used only for local development and demos** to
simulate a customer's analytical database.

These migrations are mounted at the demo tenant Postgres container in
`docker-compose.yml` and are *never* applied to a real tenant DB. Production
tenant analytical databases are user-supplied; Argentum introspects them at
runtime via `Conn.ExtractSchema()` and never modifies them (read-only
transactions are physically enforced by the driver layer).

If you add a new sample dataset for testing other supported DB types
(e.g. MySQL), put it under `demo_tenant/` with a clear name prefix such as
`mysql_001_*.sql`.
