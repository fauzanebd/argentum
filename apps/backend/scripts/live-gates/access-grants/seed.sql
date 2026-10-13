-- Roadmap 12's live gate (RIZ-25): the rows every arm in arms.sh,
-- browser-arms.mjs and README.md reads. **Local stack only** — run it against
-- the compose Postgres's scratch
-- database, never a tenant's and never the remote one `apps/backend/.env` names.
--
--   psql -h localhost -U metabase -d riz25 -v ON_ERROR_STOP=1 -f seed.sql
--
-- **Re-running it is a reset, not an append.** Every row has a fixed uuid under
-- one of two company ids, and the first block deletes both companies before
-- anything is written, so a second run gives back exactly the state the arms
-- start from — every resource open, nobody granted anything, all four Payroll
-- links as seeded. An arm that restricted HR and stopped half way is undone by
-- seeding again, which is the property arms.sh's "fresh seed" means. Nothing
-- outside those two companies is touched. (A delete and a fresh insert rather
-- than ON CONFLICT DO NOTHING: an upsert that skips existing rows would keep a
-- previous run's restrictions and grants, which is the opposite of a reset.)
--
-- Ids are readable on purpose: `a…` is company A, `b…` company B, and the second
-- group names the table (0001 users, 0002 agents, 0003 dashboards, 0004
-- dashboard links, 0005 sources, 0006 uploaded documents, 0007 their tables,
-- 0008 conversations, 0009 messages, 000a generated documents, 000b their links,
-- 000c proposals, 000d metrics, 000e bindings).
--
-- Two values are tied to the scratch environment in README.md and nothing else:
--   * users' password is `riz25-scratch-pass` (argon2id below);
--   * dsn_encrypted is sealed under ARGENTUM_DSN_KEY
--     000102…1e1f — a key that exists only so a seed can carry a ciphertext.
--     Both DSNs are the compose demo warehouse's own `demo/demo`, not a secret.

-- ---------------------------------------------------------------------------
-- This machine only, like arms.sh and browser-arms.mjs. The seed deletes two
-- companies and creates admin accounts whose password is in README.md, so it
-- must not reach a server somewhere else because a shell's PG* variables did.
--
-- This asks the client's side of the connection — the host psql dialled —
-- rather than inet_server_addr(). The compose Postgres runs in a container and
-- reports its bridge address (172.x), so the server's own view cannot tell the
-- local stack from a remote host. PGHOSTADDR overrides the host psql reports,
-- and a service file can carry one, so a run with either set is refused too.
-- `-v force=1` is the override, and there is no reason to use it.
-- ---------------------------------------------------------------------------
\if :{?force}
\else
  \set riz25_env `printf '%s|%s' "${PGHOSTADDR:-}" "${PGSERVICE:-}"`
  SELECT (:'HOST' IN ('localhost', '127.0.0.1', '::1') OR :'HOST' LIKE '/%')
         AND :'riz25_env' IN ('|', '127.0.0.1|', '::1|') AS riz25_local \gset
  \if :riz25_local
  \else
    \echo 'seed.sql: refusing host' :'HOST' '(PGHOSTADDR|PGSERVICE:' :'riz25_env' ') - it writes, and only runs against this machine (-v force=1 overrides)'
    DO $$ BEGIN RAISE EXCEPTION 'seed.sql refused a non-local server'; END $$;
    \quit
  \endif
\endif

BEGIN;

-- ---------------------------------------------------------------------------
-- Reset. Dashboards first: dashboards.source_id is ON DELETE RESTRICT (056), so
-- a company delete that reached db_connections before dashboards would fail.
-- ---------------------------------------------------------------------------
DELETE FROM dashboards WHERE company_id IN
    ('a0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001');
DELETE FROM companies WHERE id IN
    ('a0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001');

-- ---------------------------------------------------------------------------
-- Companies, credit, people.
-- ---------------------------------------------------------------------------
INSERT INTO companies (id, name, slug) VALUES
    ('a0000000-0000-4000-8000-000000000001', 'RIZ-25 Gate A', 'riz25-gate-a'),
    ('b0000000-0000-4000-8000-000000000001', 'RIZ-25 Gate B', 'riz25-gate-b');

-- A turn is budget-checked before it is enqueued; an empty balance would answer
-- 402 where an arm expects its 202.
INSERT INTO company_credits (company_id, balance_micro_usd, monthly_grant_micro_usd) VALUES
    ('a0000000-0000-4000-8000-000000000001', 50000000, 0),
    ('b0000000-0000-4000-8000-000000000001', 50000000, 0);

INSERT INTO users (id, company_id, email, password_hash, role, activated_at) VALUES
    -- Admin A: restricts, grants, and is refused like anyone when not granted.
    ('a0000000-0001-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'riz25-admin@example.test',
     '$argon2id$v=19$m=65536,t=3,p=2$zbW8Nhp8uON/D57Wk7FvmA$shHSv+Ty4S3syHAIN0l0MmXc1NGw6vKySiYwio/q/gs',
     'admin', now()),
    -- Member M: the person every refusal is about.
    ('a0000000-0001-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001',
     'riz25-member@example.test',
     '$argon2id$v=19$m=65536,t=3,p=2$zbW8Nhp8uON/D57Wk7FvmA$shHSv+Ty4S3syHAIN0l0MmXc1NGw6vKySiYwio/q/gs',
     'member', now()),
    -- A second admin, so "acknowledge twice keeps the first admin" has a second.
    ('a0000000-0001-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001',
     'riz25-admin2@example.test',
     '$argon2id$v=19$m=65536,t=3,p=2$zbW8Nhp8uON/D57Wk7FvmA$shHSv+Ty4S3syHAIN0l0MmXc1NGw6vKySiYwio/q/gs',
     'admin', now()),
    -- Company B's one person: the cross-company id every "not found" arm names,
    -- and the caller of the 50-dashboard and 100-conversation pages.
    ('b0000000-0001-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001',
     'riz25-b@example.test',
     '$argon2id$v=19$m=65536,t=3,p=2$zbW8Nhp8uON/D57Wk7FvmA$shHSv+Ty4S3syHAIN0l0MmXc1NGw6vKySiYwio/q/gs',
     'admin', now());

-- ---------------------------------------------------------------------------
-- Agents. HR is the company default, so every "default falls through" arm has
-- it to fall through from. The roster orders default first, then by name
-- (agent_repo.go), so the next agent after HR is Finance. An empty
-- allowed_tools holds every registered tool, which is what the two paid arms
-- need (update_dashboard, search_documents).
-- ---------------------------------------------------------------------------
INSERT INTO agents (id, company_id, name, description, is_default, created_at) VALUES
    ('a0000000-0002-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'HR', 'People and payroll questions', true,  now() - interval '3 days'),
    ('a0000000-0002-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001',
     'Finance', 'Revenue, cost and budget questions', false, now() - interval '3 days' + interval '1 minute'),
    ('a0000000-0002-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001',
     'Ops', 'Inventory and fulfilment questions', false, now() - interval '3 days' + interval '2 minutes'),
    -- Exists only to be deleted below: T-Z10's fifth shape is a conversation
    -- whose only agent is gone, which messages.agent_id (no foreign key, 077)
    -- still names.
    ('a0000000-0002-4000-8000-000000000009', 'a0000000-0000-4000-8000-000000000001',
     'Retired', 'Deleted by the seed', false, now() - interval '3 days'),
    ('b0000000-0002-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001',
     'Analyst', 'Company B''s only agent', true, now() - interval '3 days');

-- ---------------------------------------------------------------------------
-- Sources: the compose demo warehouse's two databases. HR warehouse is the one
-- T-Z6 restricts; the second has no label, so the access read names it by its
-- engine — the first live run of COALESCE(NULLIF(r.label, ''), r.db_type).
-- ---------------------------------------------------------------------------
INSERT INTO db_connections (id, company_id, db_type, label, dsn_encrypted, is_default) VALUES
    ('a0000000-0005-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'postgres', 'HR warehouse',
     '\x4152474b01630dcd294a259883e09da73644b8e73acb803892073eda3fba65b31236bc36ec194de113e61ede24bfd5c594a3e449174cdafba2afb2b11a45172f2269a7a627deacd74aba255a578f1da0e035458d5a6fa74f581841d3b96c005effd8887e8c',
     false),
    ('a0000000-0005-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001',
     'postgres', '',
     '\x4152474b01630dcd292efc0a578235f1722ed14782b0dbccea9460120dabd7e85e7e065ecd6cde0b272db6c7bfd01d6b09532a44f63f9c363cfe41d133fceef48c41f5bb2a21b81a9c59dc016cffdd46e8df30f574d42887176ccdc2ccad99c36e50d0d2e237ea',
     true),
    ('b0000000-0005-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001',
     'postgres', 'B warehouse',
     '\x4152474b01630dcd292efc0a578235f1722ed14782b0dbccea9460120dabd7e85e7e065ecd6cde0b272db6c7bfd01d6b09532a44f63f9c363cfe41d133fceef48c41f5bb2a21b81a9c59dc016cffdd46e8df30f574d42887176ccdc2ccad99c36e50d0d2e237ea',
     true);

-- Finance is scoped to both sources, so the T-Z8 key that lists Finance alone is
-- also T-Z6's "an agent scoped to the restricted source is still accepted".
INSERT INTO agent_sources (agent_id, connection_id) VALUES
    ('a0000000-0002-4000-8000-000000000001', 'a0000000-0005-4000-8000-000000000001'),
    ('a0000000-0002-4000-8000-000000000002', 'a0000000-0005-4000-8000-000000000001'),
    ('a0000000-0002-4000-8000-000000000002', 'a0000000-0005-4000-8000-000000000002'),
    ('a0000000-0002-4000-8000-000000000003', 'a0000000-0005-4000-8000-000000000002');

-- One metric per source, for RIZ-27's arm: payroll_cost on the HR warehouse,
-- which a member not granted it no longer sees, and net_revenue on the open one,
-- which they do. RIZ-25 seeded a single `headcount` here to re-observe §17c's
-- gap; it had grain 'none' and a column the warehouse lacks, which no PUT could
-- save, and the arm now round-trips the admin's edit (R7). Both templates are
-- real against the compose warehouses, so validate-on-save passes.
INSERT INTO metric_definitions (id, company_id, source_id, key, label, description, sql_template,
                                value_column, grain, unit, currency) VALUES
    ('a0000000-000d-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000001', 'payroll_cost', 'Payroll cost',
     'Monthly salary of employees who joined in the window',
     'SELECT COALESCE(SUM(monthly_salary), 0) AS value FROM employees WHERE join_date >= {{from}} AND join_date < {{to}}',
     'value', 'month', 'currency', 'IDR'),
    ('a0000000-000d-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000002', 'net_revenue', 'Net revenue',
     'Sales amount of orders recorded in the window',
     'SELECT COALESCE(SUM(sales_amount), 0) AS value FROM fact_sales WHERE created_at >= {{from}} AND created_at < {{to}}',
     'value', 'month', 'currency', 'IDR');

-- ---------------------------------------------------------------------------
-- Conversations (company A), the five T-Z10 shapes plus a plain Finance one.
-- All dashboard-channel and M's, so both A and M read them in the company list.
-- ---------------------------------------------------------------------------
INSERT INTO conversation_threads (id, company_id, channel, user_id, title, agent_id, last_message_at) VALUES
    -- 01: runs as HR. Hidden by its own agent.
    ('a0000000-0008-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'Headcount by department', 'a0000000-0002-4000-8000-000000000001', now() - interval '6 hours'),
    -- 02: runs as Finance, HR in the room. Hidden by the room.
    ('a0000000-0008-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'Budget room with HR', 'a0000000-0002-4000-8000-000000000002', now() - interval '5 hours'),
    -- 03: runs as Finance; HR answered and was then removed from the room.
    -- Hidden by the messages arm of the LATERAL — the arm it exists for.
    ('a0000000-0008-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'HR answered then left', 'a0000000-0002-4000-8000-000000000002', now() - interval '4 hours'),
    -- 04: unpinned, nothing attributed: judged as the company default, HR.
    ('a0000000-0008-4000-8000-000000000004', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'Unpinned, before 077', NULL, now() - interval '3 hours'),
    -- 05: its only agent was deleted. Restricts nothing, so it stays listed.
    ('a0000000-0008-4000-8000-000000000005', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'Retired agent conversation', 'a0000000-0002-4000-8000-000000000009', now() - interval '2 hours'),
    -- 06: Finance only. Always readable; T-Z11's Finance document lives here.
    ('a0000000-0008-4000-8000-000000000006', 'a0000000-0000-4000-8000-000000000001', 'dashboard',
     'a0000000-0001-4000-8000-000000000002', 'Quarterly revenue', 'a0000000-0002-4000-8000-000000000002', now() - interval '1 hour');

INSERT INTO thread_participants (thread_id, agent_id, added_by) VALUES
    ('a0000000-0008-4000-8000-000000000002', 'a0000000-0002-4000-8000-000000000002', 'a0000000-0001-4000-8000-000000000002'),
    ('a0000000-0008-4000-8000-000000000002', 'a0000000-0002-4000-8000-000000000001', 'a0000000-0001-4000-8000-000000000002'),
    ('a0000000-0008-4000-8000-000000000006', 'a0000000-0002-4000-8000-000000000002', 'a0000000-0001-4000-8000-000000000002');

INSERT INTO messages (id, thread_id, role, content, agent_id, created_at) VALUES
    ('a0000000-0009-4000-8000-000000000011', 'a0000000-0008-4000-8000-000000000001', 'user', 'How many people work in each department?', NULL, now() - interval '6 hours'),
    ('a0000000-0009-4000-8000-000000000012', 'a0000000-0008-4000-8000-000000000001', 'assistant', 'Engineering has 2 people, Finance 1, HR 1 and Sales 1.', 'a0000000-0002-4000-8000-000000000001', now() - interval '6 hours' + interval '5 seconds'),
    ('a0000000-0009-4000-8000-000000000021', 'a0000000-0008-4000-8000-000000000002', 'user', 'What is the hiring budget?', NULL, now() - interval '5 hours'),
    ('a0000000-0009-4000-8000-000000000022', 'a0000000-0008-4000-8000-000000000002', 'assistant', 'The hiring budget is set by Finance each quarter.', 'a0000000-0002-4000-8000-000000000002', now() - interval '5 hours' + interval '5 seconds'),
    ('a0000000-0009-4000-8000-000000000031', 'a0000000-0008-4000-8000-000000000003', 'user', '@HR what is the average salary?', NULL, now() - interval '4 hours'),
    ('a0000000-0009-4000-8000-000000000032', 'a0000000-0008-4000-8000-000000000003', 'assistant', 'The average monthly salary is shown in the payroll report.', 'a0000000-0002-4000-8000-000000000001', now() - interval '4 hours' + interval '5 seconds'),
    ('a0000000-0009-4000-8000-000000000041', 'a0000000-0008-4000-8000-000000000004', 'user', 'Who joined most recently?', NULL, now() - interval '3 hours'),
    ('a0000000-0009-4000-8000-000000000042', 'a0000000-0008-4000-8000-000000000004', 'assistant', 'The most recent hire joined this year.', NULL, now() - interval '3 hours' + interval '5 seconds'),
    ('a0000000-0009-4000-8000-000000000051', 'a0000000-0008-4000-8000-000000000005', 'user', 'Summarise last week.', NULL, now() - interval '2 hours'),
    ('a0000000-0009-4000-8000-000000000052', 'a0000000-0008-4000-8000-000000000005', 'assistant', 'Last week was quiet.', 'a0000000-0002-4000-8000-000000000009', now() - interval '2 hours' + interval '5 seconds'),
    ('a0000000-0009-4000-8000-000000000061', 'a0000000-0008-4000-8000-000000000006', 'user', 'What was revenue last quarter?', NULL, now() - interval '1 hour'),
    ('a0000000-0009-4000-8000-000000000062', 'a0000000-0008-4000-8000-000000000006', 'assistant', 'Revenue is in the Sales overview dashboard.', 'a0000000-0002-4000-8000-000000000002', now() - interval '1 hour' + interval '5 seconds'),
    -- A reply that embeds Payroll: the chat renders a /dashboards/<id> link as
    -- the dashboard itself, which is where T-Z5's browser arm reads
    -- "restricted" rather than "could not be loaded".
    ('a0000000-0009-4000-8000-000000000063', 'a0000000-0008-4000-8000-000000000006', 'assistant', 'Salary cost sits on the payroll dashboard: [Payroll](/dashboards/a0000000-0003-4000-8000-000000000001)', 'a0000000-0002-4000-8000-000000000002', now() - interval '1 hour' + interval '10 seconds');

-- The fifth shape's agent goes now: conversation_threads.agent_id is SET NULL,
-- the message keeps its id.
DELETE FROM agents WHERE id = 'a0000000-0002-4000-8000-000000000009';

-- ---------------------------------------------------------------------------
-- Dashboards. Payroll reads the HR warehouse; the others the unnamed one.
-- ---------------------------------------------------------------------------
INSERT INTO dashboards (id, company_id, source_id, title, spec, created_by) VALUES
    ('a0000000-0003-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000001', 'Payroll',
     '{"spec_version":1,"title":"Payroll","source_id":"a0000000-0005-4000-8000-000000000001","panels":[{"id":"p1","title":"Monthly salary by department","viz":"table","layout":{"x":0,"y":0,"w":12,"h":4},"sql":"SELECT department, sum(monthly_salary) AS total FROM employees GROUP BY department ORDER BY department","map":{"label":"department","series":["total"]}}]}',
     'a0000000-0001-4000-8000-000000000001'),
    ('a0000000-0003-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000002', 'Sales overview',
     '{"spec_version":1,"title":"Sales overview","source_id":"a0000000-0005-4000-8000-000000000002","panels":[{"id":"p1","title":"Orders","viz":"kpi","layout":{"x":0,"y":0,"w":4,"h":2},"sql":"SELECT count(*) AS orders FROM fact_sales","map":{"value":"orders"}}]}',
     'a0000000-0001-4000-8000-000000000001'),
    ('a0000000-0003-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000002', 'Inventory',
     '{"spec_version":1,"title":"Inventory","source_id":"a0000000-0005-4000-8000-000000000002","panels":[{"id":"p1","title":"Products","viz":"kpi","layout":{"x":0,"y":0,"w":4,"h":2},"sql":"SELECT count(*) AS products FROM dim_products","map":{"value":"products"}}]}',
     'a0000000-0001-4000-8000-000000000001'),
    -- No links at all: Q15's "restrict a dashboard with no shares" → 0.
    ('a0000000-0003-4000-8000-000000000004', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0005-4000-8000-000000000002', 'Scratch board',
     '{"spec_version":1,"title":"Scratch board","source_id":"a0000000-0005-4000-8000-000000000002","panels":[{"id":"p1","title":"Customers","viz":"kpi","layout":{"x":0,"y":0,"w":4,"h":2},"sql":"SELECT count(*) AS customers FROM dim_customers","map":{"value":"customers"}}]}',
     'a0000000-0001-4000-8000-000000000001');

-- Company B: exactly fifty dashboards, so B's GET /api/dashboards is LoadAccess
-- over fifty ids — the statement count log_statement=all is read for.
INSERT INTO dashboards (id, company_id, source_id, title, spec, created_by)
SELECT format('b0000000-0003-4000-8000-%s', lpad(to_hex(i), 12, '0'))::uuid,
       'b0000000-0000-4000-8000-000000000001', 'b0000000-0005-4000-8000-000000000001',
       'B board ' || i,
       '{"spec_version":1,"title":"B board","source_id":"b0000000-0005-4000-8000-000000000001","panels":[{"id":"p1","viz":"kpi","layout":{"x":0,"y":0,"w":4,"h":2},"sql":"SELECT 1 AS one","map":{"value":"one"}}]}',
       'b0000000-0001-4000-8000-000000000001'
  FROM generate_series(1, 50) AS i;

-- Payroll's four links: two live, one revoked, one expired. The tokens are the
-- strings in arms.sh; only their sha256 is stored, as the API stores it.
INSERT INTO dashboard_shares (id, company_id, dashboard_id, token_hash, created_by, expires_at, revoked_at) VALUES
    ('a0000000-0004-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0003-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-payroll-live-1', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() + interval '7 days', NULL),
    ('a0000000-0004-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0003-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-payroll-live-2', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() + interval '7 days', NULL),
    ('a0000000-0004-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0003-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-payroll-revoked', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() + interval '7 days', now() - interval '1 day'),
    ('a0000000-0004-4000-8000-000000000004', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0003-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-payroll-expired', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() - interval '1 day', NULL);

-- ---------------------------------------------------------------------------
-- Uploaded documents (Knowledge). Payroll 2026.pdf is the one T-Z6 restricts;
-- its chunk is the passage the paid search arm must not quote, and its table is
-- the one served under its own id.
-- ---------------------------------------------------------------------------
INSERT INTO source_documents (id, company_id, filename, content_sha256, byte_size, page_count, storage_key, status, uploaded_by) VALUES
    ('a0000000-0006-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'Payroll 2026.pdf',
     encode(sha256('riz25 payroll'::bytea), 'hex'), 2048, 2,
     'knowledge/a0000000-0000-4000-8000-000000000001/a0000000-0006-4000-8000-000000000001.pdf', 'parsed',
     'a0000000-0001-4000-8000-000000000001'),
    ('a0000000-0006-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 'Employee Handbook 2026.pdf',
     encode(sha256('riz25 handbook'::bytea), 'hex'), 2048, 1,
     'knowledge/a0000000-0000-4000-8000-000000000001/a0000000-0006-4000-8000-000000000002.pdf', 'parsed',
     'a0000000-0001-4000-8000-000000000001');

INSERT INTO document_tables (id, document_id, company_id, title, table_name, first_page, last_page, columns, status, row_count) VALUES
    ('a0000000-0007-4000-8000-000000000001', 'a0000000-0006-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'Director salaries', 'doc_payroll_2026_directors', 2, 2,
     '[{"name":"role","type":"text"},{"name":"monthly_salary","type":"numeric"}]', 'draft', 2);

INSERT INTO document_chunks (document_id, company_id, ordinal, page_from, page_to, heading_path, content, source_name) VALUES
    ('a0000000-0006-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 1, 2, 2,
     'Payroll 2026 > Directors',
     'Director salary schedule for 2026. The Finance Director''s monthly salary is IDR 87,500,000 and the Operations Director''s monthly salary is IDR 79,000,000. Director salary reviews happen every March.',
     'Payroll 2026.pdf'),
    ('a0000000-0006-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 1, 1, 1,
     'Employee Handbook > Leave',
     'Every employee receives 14 days of annual leave. Salary questions go to the HR team; the handbook does not list any director salary.',
     'Employee Handbook 2026.pdf');

-- ---------------------------------------------------------------------------
-- Generated documents (T-Z11, T-Z13). HR's conversation produced two: a deck
-- (pptx — the dashboard offers Share only on a deck, a PDF or a video, so this
-- is the one T-Z13's links hang off) and a one-page carousel (what pages and
-- carousel serve). Finance's is a carousel too, and the fourth has no
-- conversation — the render door's — and nothing to inherit. README.md uploads
-- their plans, pages and manifests to MinIO.
-- ---------------------------------------------------------------------------
INSERT INTO documents (id, company_id, thread_id, format, filename, storage_key, size_bytes, page_count) VALUES
    ('a0000000-000a-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0008-4000-8000-000000000001',
     'pptx', 'HR headcount deck.pptx',
     'documents/a0000000-0000-4000-8000-000000000001/a0000000-0008-4000-8000-000000000001/a0000000-000a-4000-8000-000000000001.pptx', 4096, 0),
    ('a0000000-000a-4000-8000-000000000004', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0008-4000-8000-000000000001',
     'carousel', 'HR headcount.zip',
     'documents/a0000000-0000-4000-8000-000000000001/a0000000-0008-4000-8000-000000000001/a0000000-000a-4000-8000-000000000004.zip', 4096, 1),
    ('a0000000-000a-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0008-4000-8000-000000000006',
     'carousel', 'Quarterly revenue.zip',
     'documents/a0000000-0000-4000-8000-000000000001/a0000000-0008-4000-8000-000000000006/a0000000-000a-4000-8000-000000000002.zip', 4096, 1),
    ('a0000000-000a-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000001', NULL,
     'carousel', 'Rendered brief.zip',
     'documents/a0000000-0000-4000-8000-000000000001/api/a0000000-000a-4000-8000-000000000003.zip', 4096, 1);

-- Two live links on HR's document, minted while HR was open. The first is
-- T-Z13's — held shut while HR is restricted and playing again after. The
-- second exists to be revoked by T-Z11's "DELETE …/shares/<id> is not refused"
-- arm, so that arm never takes T-Z13's link away.
INSERT INTO report_shares (id, company_id, document_id, token_hash, created_by, expires_at) VALUES
    ('a0000000-000b-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'a0000000-000a-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-hr-doc-link', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() + interval '7 days'),
    ('a0000000-000b-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', 'a0000000-000a-4000-8000-000000000001',
     encode(sha256(convert_to('riz25-hr-doc-link-2', 'UTF8')), 'hex'), 'a0000000-0001-4000-8000-000000000001', now() + interval '7 days');

-- ---------------------------------------------------------------------------
-- Proposals (T-Z14): one raised in HR's conversation, one with none.
-- ---------------------------------------------------------------------------
INSERT INTO company_actions (company_id, action_kind, enabled, requires_approval, allowed_roles, created_by) VALUES
    ('a0000000-0000-4000-8000-000000000001', 'send_message', true, true, '["admin","member"]',
     'a0000000-0001-4000-8000-000000000001');

INSERT INTO action_invocations (id, company_id, thread_id, action_kind, params_redacted, idempotency_key, status) VALUES
    ('a0000000-000c-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'a0000000-0008-4000-8000-000000000001',
     'send_message', '{"text":"Payroll is ready for review"}', 'riz25-hr-proposal', 'proposed'),
    ('a0000000-000c-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000001', NULL,
     'send_message', '{"text":"Weekly digest"}', 'riz25-no-thread-proposal', 'proposed');

-- The audit row the turn that raised HR's proposal would have written. §20d's
-- documented gap is that GET /api/audit/actions — the admin's company-wide
-- ledger — lists it, parameters included, to an admin HR is hidden from.
INSERT INTO agent_actions (company_id, thread_id, actor_kind, actor_ref, channel, tool_name, args_redacted,
                           args_hash, result_status, agent_id) VALUES
    ('a0000000-0000-4000-8000-000000000001', 'a0000000-0008-4000-8000-000000000001', 'user',
     'a0000000-0001-4000-8000-000000000002', 'dashboard', 'propose_action',
     '{"action_kind":"send_message","params":{"text":"Payroll is ready for review"}}', 'riz25-propose',
     'ok', 'a0000000-0002-4000-8000-000000000001');

-- ---------------------------------------------------------------------------
-- A channel binding made before HR was restricted (T-Z8): Settings → Agents
-- should show it silenced once HR is restricted. Discord, with no Discord
-- credentials seeded, so it can never deliver anything.
-- ---------------------------------------------------------------------------
INSERT INTO agent_channel_bindings (id, company_id, agent_id, channel, external_id) VALUES
    ('a0000000-000e-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001',
     'a0000000-0002-4000-8000-000000000001', 'discord', 'riz25-hr-room');

-- ---------------------------------------------------------------------------
-- Company B: a hundred conversations of fifty messages each, for the
-- AgentsByThread EXPLAIN ANALYZE (§11d) and for B's GET /api/threads, which
-- runs it over the whole page.
-- ---------------------------------------------------------------------------
INSERT INTO conversation_threads (id, company_id, channel, user_id, title, agent_id, last_message_at)
SELECT format('b0000000-0008-4000-8000-%s', lpad(to_hex(i), 12, '0'))::uuid,
       'b0000000-0000-4000-8000-000000000001', 'dashboard', 'b0000000-0001-4000-8000-000000000001',
       'B conversation ' || i,
       CASE WHEN i % 2 = 0 THEN 'b0000000-0002-4000-8000-000000000001'::uuid END,
       now() - make_interval(mins => i)
  FROM generate_series(1, 100) AS i;

INSERT INTO messages (thread_id, role, content, agent_id, created_at)
SELECT format('b0000000-0008-4000-8000-%s', lpad(to_hex(t), 12, '0'))::uuid,
       CASE WHEN m % 2 = 1 THEN 'user' ELSE 'assistant' END,
       'message ' || m,
       CASE WHEN m % 2 = 0 THEN 'b0000000-0002-4000-8000-000000000001'::uuid END,
       now() - make_interval(mins => t) - make_interval(secs => 50 - m)
  FROM generate_series(1, 100) AS t, generate_series(1, 50) AS m;

COMMIT;

ANALYZE conversation_threads;
ANALYZE messages;
ANALYZE thread_participants;
