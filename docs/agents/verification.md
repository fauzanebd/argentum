# Verification Gates

**A change is not done because it compiles, and not done because you read it and
it looks right.** Find your change type below, run the gate, paste the output.

Rule that overrides everything else on this page: **paste real command output.**
"Tests pass" without the output is not evidence. If a gate cannot be run, say so
explicitly and say why — do not silently substitute inspection.

---

## Universal minimum

Every backend change:
```bash
cd apps/backend
go build ./... && go vet ./... && go test -race ./...
```

Every frontend change:
```bash
pnpm --filter dashboard build   # or landing / widget; runs tsc -b then vite build
pnpm --filter dashboard lint
```

Changed a Go struct that crosses the API boundary:
```bash
make types                      # regenerate packages/api-types
git diff --exit-code packages/api-types   # must be committed, not left dirty
```

Changed anything in `packages/`:
```bash
pnpm --filter dashboard build && pnpm --filter @argentum/widget-app build
```
(`--filter widget` matches no workspace project and pnpm exits 0 on an empty
match, which is how this line built nothing from `T-V1` until 2026-09-04.)
A `packages/` change that only builds one consumer is not verified.

If the universal minimum fails, nothing below matters. Fix it first.

---

## By change type

### Added or changed a repository method

```bash
go test ./internal/adapters/postgres/... -run TestXxx -v
```

**Also verify by inspection and state that you did:**
- [ ] Every query is parameterized
- [ ] Every tenant-scoped query has `company_id` in the `WHERE` clause
- [ ] `sql.ErrNoRows` maps to a domain error, not returned raw
- [ ] Rows and statements are closed

### Added or changed an app service

```bash
go test ./internal/app/... -race -v
```

Test with fakes for repositories and LLMs — **never a live LLM in a unit test.**
Cover: the happy path, one repository error, and one boundary condition
(empty list, zero value, nil optional dependency).

### Added or changed an agent tool

Static:
```bash
go test ./internal/tools/... -race -v
```

Live, and required — a tool that passes unit tests can still confuse the agent:
```bash
docker-compose --profile dev up -d postgres postgres_demo redis metabase
go run ./cmd/api & go run ./cmd/worker &
# ask a question through the dashboard that must use the new tool
```

- [ ] Tool is called when it should be — check the worker log for the tool name
- [ ] Tool is **not** called when it shouldn't be (ask an unrelated question)
- [ ] The result payload gives the agent what it needs for its next step
- [ ] Malformed arguments degrade instead of erroring
- [ ] Missing tenant context is rejected
- [ ] Usage event recorded, if it costs money
- [ ] `make eval` — pass rate did not drop below the baseline in
      [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md)

### Changed the system prompt or `agents.yaml`

**Mandatory:** `make eval` before and after. Paste both pass rates.

Prompt changes are the highest-variance edits in this codebase. Six historical
commits changed agent behaviour with no measurement, including one model-default
reversal. Do not add to that list.

As of `T-01` this is a real command, not an aspiration:

```bash
DB_HOST=localhost DB_PORT=5432 DB_USER=metabase DB_NAME=argentum \
REDIS_URL=localhost:6385 METABASE_URL=http://localhost:3000 \
METABASE_PUBLIC_URL=http://localhost:3000 make eval
```

It takes ~13 minutes and ~$0.03. `cmd/eval` refuses to run against a non-local
`DB_HOST` (finding `E-2`) — it writes real rows into the control DB. Use
`-only <category>` while iterating and the full set before you commit.

- [ ] Eval pass rate ≥ the committed baseline (**96.8%**, 30/31, 2026-07-27)
- [ ] Mean tokens in/out did not increase materially without a stated reason
- [ ] Indonesian cases still reply in Indonesian
- [ ] Rupiah magnitude formatting still correct
- [ ] `eval-baseline.md` updated with the new number, date and model

### Changed `config/guardrails.yaml`

```bash
go test ./internal/guardrails/... -v
```

Every rule change needs **both directions** in the golden suite: a case that must
be blocked, and the specific false positive that motivated the change.

- [ ] New must-block case added
- [ ] New must-pass case added, matching the real false positive
- [ ] Existing golden cases still green — a widened regex commonly unblocks injection
- [ ] `make eval` guardrail-refusal cases still pass

Historical false positives that must stay passing: "create a dashboard",
"update me on sales", CSS `margins`, "integer target", benign follow-ups ("ok",
"why?"), Indonesian particles.

### Added a migration

```bash
# forward
go run ./cmd/api    # applies on boot; watch the log

# round-trip
migrate -path migrations/control -database "$DATABASE_URL" up
migrate -path migrations/control -database "$DATABASE_URL" down 1
migrate -path migrations/control -database "$DATABASE_URL" up
```

- [ ] Both `.up.sql` and `.down.sql` exist
- [ ] Round-trip is clean
- [ ] Forward-compatible: old code still runs against the new schema (no drops, no
      renames, no new NOT NULL without a default)
- [ ] Foreign keys have the intended `ON DELETE` behaviour
- [ ] Indexes exist for the query patterns you added
- [ ] Migration number was not already claimed — `ls migrations/control/ | tail -3`

### Added or changed an HTTP endpoint

```bash
go test ./internal/transport/http/... -race -v
curl -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/your-endpoint
```

- [ ] Registered in the correct group in `cmd/api/router.go` (`authed` vs public)
- [ ] `AdminOnly()` applied if it mutates credentials or company config
- [ ] Company-scoped from the JWT claims, never from a request body field
- [ ] 401 unauthenticated, 403 wrong role, 404 cross-tenant id
- [ ] Documented in `apps/backend/docs/`
- [ ] Postman collection updated

### Changed a tenant DB driver

```bash
go test ./internal/adapters/db/... -v
# then against a real instance of that engine
```

- [ ] Queries still run inside a read-only transaction (or the documented
      equivalent — SQL Server rejects the read-only tx option; see `437ce57`)
- [ ] Statement timeout still applied
- [ ] Row cap still enforced
- [ ] `ExtractSchema()` returns tables, columns, and relationships
- [ ] A mutation attempt fails at the driver level, not just at the guardrail

### Changed the frontend

```bash
pnpm build && pnpm lint
pnpm dev   # then exercise the flow
```

- [ ] Flow works in the browser — screenshot it
- [ ] Loading and error states render
- [ ] Mobile viewport is not broken (the chat UI has had regressions here)
- [ ] Wire types come from `@argentum/api-types`, not from a hand-written mirror
- [ ] No `console.log` left behind

### Changed the WebSocket contract

- [ ] The event struct is in `internal/app/event_bus.go`, so `make types` regenerates the frontend's copy of it
- [ ] Unknown event types are ignored by the client, not fatal
- [ ] Reconnect still works — kill the API, restart, confirm the stream resumes
- [ ] Event schema documented in `apps/backend/docs/`
- [ ] `make types` run and `packages/api-types` committed

### Changed metering or pricing

```bash
go test ./internal/app/... -run TestPricing -v
```

- [ ] Cost math verified by hand for one known case
- [ ] Anthropic cache multipliers correct (1.25× create, 0.10× read)
- [ ] Unknown model falls back to `DefaultPricing`, does not produce zero cost
- [ ] `usage_events` row appears with the expected `event_type`
- [ ] Credit decrement still happens

### Changed embed auth or the widget

Treat every change here as security-relevant. The embed surface is the only one
reachable from a third party's web page.

```bash
go test ./internal/transport/http/handlers/... -run TestEmbed -race -v
go test ./internal/auth/... -race -v
```

Backend:
- [ ] Forged HMAC signature → 401
- [ ] Correct signature from a non-allowlisted origin → 403
- [ ] Expired `exp`, and `exp` further out than the allowed ceiling → 401
- [ ] Revoked embed key → 401 immediately
- [ ] Empty or wildcard `allowed_origins` cannot be saved
- [ ] Signature comparison uses `hmac.Equal` — **grep the diff**, `==` is a
      timing leak
- [ ] Embed token rejected on `/api/threads`, `/api/settings`, and every
      `AdminOnly` route
- [ ] Dashboard access token rejected on `/api/embed/*`
- [ ] Thread read scoped to the token's `embed_user_ref` — another user's thread id
      returns not-found
- [ ] Audit rows carry `actor_kind=embed`

Widget client:
- [ ] `postMessage` from an unexpected origin is ignored — test it by posting from
      a hostile origin in a scratch page
- [ ] Session token never appears in the iframe URL, `document.referrer`, or a
      server access log
- [ ] Bundle sizes within budget — paste the actual gzipped numbers
- [ ] Host page CSS cannot alter the widget, and the widget cannot alter the host
- [ ] Token expiry triggers re-identify rather than a silently dead widget
- [ ] Works in Chrome, Safari, and Firefox

**Gate:** paste the forgery-matrix test output, then a live transcript: mint a
session, send a turn, receive streamed events. Plus one deliberately forged
request showing the rejection.

### Changed anything security-relevant

Auth, crypto, RBAC, guardrails, tenant scoping, API keys:

```bash
go test ./internal/auth/... ./internal/crypto/... ./internal/guardrails/... -race -v
```

- [ ] Negative tests exist: wrong key, expired token, wrong role, cross-tenant id
- [ ] No secret in any log line, error message, or API response
- [ ] The relevant hard rule in [`../AGENTS.md`](../AGENTS.md) §2 still holds

---

## The end-to-end smoke test

Run before declaring any multi-ticket milestone complete.

```bash
cd apps/backend
docker-compose --profile dev up -d postgres postgres_demo redis metabase
go run ./cmd/api & go run ./cmd/worker &
cd ../.. && pnpm --filter dashboard dev
```

1. Sign up a fresh company.
2. Add the demo DSN
   (`postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable`).
3. Ask "What were our total sales last month?" — expect a streamed answer with a
   visible `run_sql` tool card.
4. Ask "Buatkan grafik penjualan per bulan" — expect an Indonesian reply and a
   dashboard link.
5. Ask "How do I center a div?" — expect the guardrail refusal.
6. Create a scheduled task for 1 minute out; confirm it fires and writes to its
   thread.
7. Check `/api/usage/summary` — non-zero cost with sensible per-model breakdown.

All seven pass = the system is not broken. Fewer than seven = say which failed.

---

## Anti-patterns in verification

| Don't | Do |
| ----- | -- |
| "I reviewed the code and it looks correct" | Run the gate, paste the output |
| "Tests pass" | Paste the test output |
| "It should work" | Make it work, then show it working |
| Testing only the happy path | One error case and one boundary case, minimum |
| Mocking the thing you are testing | Mock its dependencies, not the subject |
| Live LLM in a unit test | Fake in unit tests; live LLM only in `make eval` |
| Skipping the gate because the change is small | Small changes to `chat_runner.go` have broken every channel at once |
