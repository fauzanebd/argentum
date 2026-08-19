-- The document warehouse: where rows extracted from PDFs live (T-P6).
--
-- **This is a different database from the control plane, and that is the whole
-- point of the file.** The agent executes model-written SQL against whatever a
-- source points at. Point a source at the control database and one clever
-- SELECT reads `api_keys`, `company_llm_credentials`, `db_connections` and
-- every other tenant's rows — which is the roadmap's Decision 4, and the reason
-- this directory exists rather than a `doc_*` schema inside `argentum`. The
-- precedent for a second Postgres in this deployment is `postgres_demo`.
--
-- Applied the way `migrations/demo_tenant/` is: mounted at
-- /docker-entrypoint-initdb.d on the compose service, or run by hand against a
-- managed instance. There is no golang-migrate version table here on purpose —
-- this file is idempotent bootstrap, and everything that follows it is created
-- per company at publish time by `internal/docwarehouse`, because a schema per
-- tenant cannot be a static migration.
--
-- What a publish creates, for the reader of this file who is about to go
-- looking for it in SQL and will not find it:
--
--   * schema  doc_<company>          — one per company, created on first publish
--   * role    doc_<company>_reader   — LOGIN, with USAGE on that schema and
--                                      SELECT on its tables, and nothing else
--   * tables  <slug>                 — one per applied document table, with
--                                      source_page and source_row on every row
--
-- The DSN handed to the agent authenticates as the reader role. It is stored
-- encrypted in `db_connections` exactly like a tenant's own warehouse DSN, and
-- the read-only transaction plus `internal/sqlguard` apply to it exactly as
-- they do to one.

-- Nobody creates anything in `public`, including a role that finds its way in.
-- The tables live in per-company schemas and `public` is left empty rather than
-- left open: a reader role that could CREATE here could leave itself an object
-- another company's role can read.
REVOKE ALL ON SCHEMA public FROM PUBLIC;

-- No role may connect to a database it was not created for, and no reader role
-- may read the catalogue-level information that would tell it which other
-- schemas exist. `pg_catalog` visibility cannot be revoked, so the isolation
-- that matters is the one above — USAGE is granted on exactly one schema — and
-- the assertion this product actually keeps is the acceptance test:
-- `SELECT … FROM companies` through a document source must fail.
COMMENT ON SCHEMA public IS
    'Deliberately empty. Extracted document rows live in doc_<company> schemas created at publish time by internal/docwarehouse.';
