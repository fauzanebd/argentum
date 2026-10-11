# Roadmap 12's live gate — access grants on a real stack

Every arm of [`live-gate-backlog.md`](../../../../../docs/coverage/live-gate-backlog.md)
§7c that does not need Slack or production, written down so it can be run again.
The first run was RIZ-25 (2026-10-11); its results are in §7c and in
[`access-grants.md`](../../../../../docs/coverage/access-grants.md). Phase 3az's
recipe (2026-09-13) lived only in prose and had to be rebuilt from it — this
directory is what that cost.

| File | What it is |
| --- | --- |
| `seed.sql` | Two companies with fixed ids: admin A, member M and a second admin in A; HR (the default), Finance, Ops; Payroll with four links; the HR warehouse; Payroll 2026.pdf and its table; T-Z10's five conversation shapes; generated documents and their links; two proposals; a channel binding; and company B with fifty dashboards and a hundred conversations of fifty messages. **Running it again resets both companies.** |
| `arms.sh` | Every API arm (R4), the gap re-observations (§17c, §19e, §20d) and the malformed-id sweep (R9). `PASS`/`FAIL` per line, non-zero exit on a `FAIL`. |
| `browser-arms.mjs`, `cdp.mjs` | The browser arms (R6) over the DevTools protocol, with a screenshot per step. No dependency beyond Node and Chrome. |

**Everything here writes, and refuses anything that is not this machine.** Never
point it at a tenant, the remote control database `apps/backend/.env` names, or
production.

## 1. The stack

```bash
export PATH=$PATH:/usr/local/go/bin
D=/usr/local/bin/docker                       # the nix docker on PATH is too old for the daemon
$D start argentum_postgres argentum_redis argentum_minio argentum_postgres_demo
#   or: docker compose up -d postgres postgres_demo redis minio   (from apps/backend)
export PGHOST=localhost PGPORT=5432 PGUSER=metabase
export PGPASSWORD=$($D inspect argentum_postgres --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^POSTGRES_PASSWORD=//p')
psql -d postgres -c 'CREATE DATABASE riz25'
```

Build the binaries explicitly — `go run` can serve one older than the tree — and
run them from a directory **with no `.env`**, so nothing in `apps/backend/.env`
(a remote database, live Twilio and Slack credentials) is ever loaded:

```bash
cd apps/backend && go build -o /tmp/riz25/bin/ ./cmd/api ./cmd/worker
mkdir -p /tmp/riz25/run && ln -sfn "$PWD/config" /tmp/riz25/run/config && ln -sfn "$PWD/migrations" /tmp/riz25/run/migrations

export ENV=development PORT=8080 DB_HOST=localhost DB_PORT=5432 DB_USER=metabase DB_PASSWORD=$PGPASSWORD DB_NAME=riz25
export REDIS_URL=redis://localhost:6380/3 ASYNQ_REDIS_URL=redis://localhost:6380/3
export ARGENTUM_JWT_SECRET=riz25-scratch-jwt-secret-not-for-production
export ARGENTUM_DSN_KEY=000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f   # seed.sql's ciphertexts are sealed under this
export LLM_API_KEY=unset-no-model-for-this-arm     # a real key only for §5's two paid arms
export METRICS_TOKEN=riz25-metrics
export MINIO_ENDPOINT=localhost:9000 MINIO_ACCESS_KEY=minioadmin MINIO_SECRET_KEY=minioadmin MINIO_BUCKET=argentum-documents
cd /tmp/riz25/run && /tmp/riz25/bin/api        # migrates riz25 to head on boot
```

Redis **DB 3** is the gate's. Before any worker runs, flush it and check that the
only registered server is yours — a stale worker from another session would eat
the turns:

```bash
$D exec argentum_redis redis-cli -n 3 FLUSHDB
$D exec argentum_redis redis-cli -n 3 zrange asynq:servers 0 -1   # then compare with: pgrep -fl bin/worker
```

The objects the generated documents point at (one plan, two carousels, one
deck), uploaded with the `mc` inside the MinIO container:

```bash
mkdir -p /tmp/riz25/objs && cd /tmp/riz25/objs
echo '/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////wgALCAABAAEBAREA/8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPxA=' | base64 -d > 01.jpg
echo '{"caption":"RIZ-25 gate slide","alts":["A single gate slide"],"pages":1}' > carousel.json
echo '{"version":1,"title":"RIZ-25 gate plan","scenes":[{"kind":"title","title":"Gate"}]}' > plan.json
echo 'RIZ-25 gate deck placeholder' > deck.pptx
$D cp /tmp/riz25/objs argentum_minio:/tmp/riz25objs
$D exec argentum_minio sh -c '
  mc alias set local http://localhost:9000 minioadmin minioadmin >/dev/null
  co=local/argentum-documents/documents/a0000000-0000-4000-8000-000000000001
  hr=$co/a0000000-0008-4000-8000-000000000001
  mc cp -q /tmp/riz25objs/plan.json  $hr/a0000000-000a-4000-8000-000000000001.plan.json
  mc cp -q /tmp/riz25objs/deck.pptx  $hr/a0000000-000a-4000-8000-000000000001.pptx
  for d in $hr/a0000000-000a-4000-8000-000000000004 \
           $co/a0000000-0008-4000-8000-000000000006/a0000000-000a-4000-8000-000000000002 \
           $co/api/a0000000-000a-4000-8000-000000000003; do
    mc cp -q /tmp/riz25objs/01.jpg $d/01.jpg; mc cp -q /tmp/riz25objs/carousel.json $d/carousel.json
  done'
```

The dashboard (for §4): `pnpm --filter dashboard dev --port 5173` with
`VITE_API_BASE_URL=http://localhost:8080`, and headless Chrome:
`"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --remote-debugging-port=9333 --user-data-dir=/tmp/riz25/chrome --no-first-run about:blank`.

## 2. Seed, then the API arms

```bash
psql -d riz25 -v ON_ERROR_STOP=1 -f seed.sql
login() { curl -s -X POST localhost:8080/api/auth/login -H 'content-type: application/json' \
  -d "{\"email\":\"$1\",\"password\":\"riz25-scratch-pass\"}" | jq -r .access_token; }
PGDATABASE=riz25 METRICS_TOKEN=riz25-metrics ADMIN2_TOKEN=$(login riz25-admin2@example.test) \
  ./arms.sh http://localhost:8080 "$(login riz25-admin@example.test)" "$(login riz25-member@example.test)"
```

A run takes about four minutes, most of it waiting out the API's own rate limit.
**On the commit this landed with, every line passes except the 44 `R9` lines**
— the malformed-id 500s RIZ-25 found and filed as one follow-up (§7c's `T-Z1`
row). Those lines are that follow-up's acceptance test. Reseed before a second
run.

## 3. The arms a script cannot make

Each on a fresh seed unless it says otherwise.

**Migrations round trip (R2)** — a scratch database, never the stack's:

```bash
psql -d postgres -c 'CREATE DATABASE riz25_mig'
U="postgres://metabase@localhost:5432/riz25_mig?sslmode=disable"; M="migrate -path ../../../migrations/control -database $U"
$M goto 82
for n in 83 84 85; do $M goto $n && $M down 1 && $M up 1; done
$M up && psql -d riz25_mig -Atc 'SELECT version, dirty FROM schema_migrations'   # 89|f
```

Never `down` past 85: 086-089 are not this gate's. The backfill arms need rows
that exist *before* 084 and 085 — `goto 83`, insert an agent, dashboard, source,
document and API key, `goto 85`, read back `access_mode = 'open'` and
`agent_ids = '{}'`.

**`LoadAccess` is one statement (R3)** — `ALTER SYSTEM SET log_statement = 'all'`
and `SELECT pg_reload_conf()`, then as company B's user (`riz25-b@example.test`)
`GET /api/dashboards` (fifty ids) and `GET /api/threads` (a hundred), and read
`docker logs argentum_postgres`. Reset with `ALTER SYSTEM RESET log_statement`.

**`AgentsByThread` (R3)** — the statement from `thread_agents.go`, over company A's
six conversations, and `EXPLAIN (ANALYZE, BUFFERS)` over company B's hundred.

**The T-Z5 race (R5)** — two sessions. (1) Session 1: `BEGIN; SELECT access_mode
FROM dashboards WHERE id = '<payroll>' FOR SHARE;` — restrict Payroll through the
API, which must block — insert a `dashboard_shares` row, `COMMIT`: the API returns
with that row revoked. (2) Session 1: `BEGIN; UPDATE dashboards SET access_mode =
'restricted' WHERE id = '<inventory>';` — mint a link through the API, which must
block — `COMMIT`: the mint answers `409`.

**The control database stopped (R4)** — `docker stop argentum_postgres`, then
`GET /api/dashboards/<id>` as M: `503`, never `200`. Start it again.

**The worker (R7)** — the worker with the same environment and **no model key**.
As M, `POST /api/scheduled-tasks` with `cron_expression: "* * * * *"`; restrict
HR; wait for the minute. The run fails with the sentence, the task reads
`enabled=false, disabled_reason=creator_not_granted`, an `access.refused` row
with `door: job` exists, and `PATCH …/:id {"enabled": true}` clears the reason.

## 4. The browser arms (R6)

```bash
psql -d riz25 -v ON_ERROR_STOP=1 -f seed.sql
node --experimental-websocket browser-arms.mjs /tmp/riz25/screens
```

Twenty-one arms, about four minutes. It restricts as it goes; reseed afterwards.

## 5. The two paid arms (R8)

Only with the owner's spend approval, and only through the dashboard chat — never
WhatsApp, Slack or email. Restart the worker with the model's variables
(`LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL`, `LLM_INTERFACE`) passed explicitly
to that process, `LLM_WIRE_TAP_DIR` set so the tool results the model saw can be
read, and `EMBEDDING_ENABLED=false`.

- **T-Z12, at most four turns.** Restrict Payroll and grant it to A. As M: *which
  dashboards can I edit?*, then *rename the Payroll dashboard to Gaji* with its id.
  The tool result carries `"restricted":true`, `updated_at` does not move, no
  `create_dashboard` follows, the worker writes an `access.refused` row. Then A
  sends the same, and it is renamed.
- **T-Z6, at most three turns.** Restrict Payroll 2026.pdf. As M: *what does the
  payroll document say about director salary?* The wire tap's `search_documents`
  results carry no Payroll passage, no document id and no salary.

The first run cost about $0.015 across both. Stop the worker and flush Redis DB 3
afterwards.
