# Sprint 1 Tickets

Each ticket is an independently executable unit. Format is defined in
[`../agents/task-template.md`](../agents/task-template.md).

**App shorthand:** `BE` = `apps/backend/`, `FE` = `apps/dashboard/`,
`LP` = `apps/landing/`, `WID` = `apps/widget/`, `PKG` = `packages/`.
Single monorepo as of `T-00b` — a ticket spanning BE and FE is **one commit**.

**Migration numbers are pre-assigned.** The last applied migration is `020`.
Claim your number, do not renumber, and always write both `.up.sql` and
`.down.sql`.

| Ticket | Migration |
| ------ | --------- |
| T-05   | `021_agent_actions` |
| T-06   | `022_metric_definitions` |
| T-08   | `023_watchers` |
| T-10   | `024_actions` |
| T-13   | `025_api_keys` |
| T-15   | `026_outbound_webhooks` |
| T-04   | `027_user_invites` |
| T-19   | `028_embed_keys` |
| T-20   | `029_thread_embed` |

---

# Week 1 — Safe to change

## T-00 · Environment re-warm
**Repo:** BE, FE · **Size:** 0.5d · **Deps:** none · **Priority:** P0

Nine weeks since the last commit. Prove the system still runs before changing it.

**Do:**
1. `docker-compose --profile dev up -d postgres postgres_demo redis metabase`
2. `go build ./...` — expect `go.mod` at 1.26.1 to pull a toolchain.
3. `go run ./cmd/api`, `go run ./cmd/worker` — migrations should self-apply.
4. `cd ../argentum-dashboard && pnpm install && pnpm dev` (still three separate
   repos at this point — `T-00b` consolidates them)
5. Sign up, add the demo DSN
   (`postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable`), ask
   one question end-to-end, confirm streaming works.
6. Record every breakage in `docs/coverage/environment-notes.md` (create it).

**Gate:** one screenshot or transcript of a successful streamed chat answer
against the demo tenant. If anything needed fixing, list the fix.

---

## T-00b · Consolidate into a monorepo
**Repo:** all · **Size:** 1.5d · **Deps:** T-00 · **Priority:** P0 · **Never cut**

Three repos, but every feature already ships as two commits in two of them
(`feb7a47`+`d11edef`, `17f81f5`+`135ca35`, `8cf653b`+`432d6f0`). That means no
atomic commit, no atomic revert, no CI that checks both halves agree, and — with
`T-19`→`T-23` adding a fourth repo — a widget forced to duplicate the dashboard's
chat components.

**Run this after `T-00` and before `T-01`.** Re-warm first so that if something
breaks you know whether it was nine weeks of drift or your own migration. Never
mid-sprint: a restructure in week 4 invalidates every in-flight agent's file paths.

### Target layout

```
argentum/                        ← single repo
├── apps/
│   ├── backend/                 from `argentum`
│   ├── dashboard/               from `argentum-dashboard`
│   ├── landing/                 from `argentum-landing`
│   └── widget/                  created later, in T-21
├── packages/
│   ├── api-types/               scaffolded here, filled by T-02b
│   └── chat-ui/                 created later, in T-21
├── docs/                        this documentation — now tracked
├── .github/workflows/           one pipeline, path-filtered
├── pnpm-workspace.yaml
├── Makefile
└── .gitignore                   union of the three, de-duplicated
```

### Do

**1. Create the repo with history preserved.**

```bash
cd /Users/rizkal/Work/smartsoft
git init argentum-mono && cd argentum-mono
git commit --allow-empty -m "chore: init monorepo"

git subtree add --prefix=apps/backend   ../argentum/argentum            main
git subtree add --prefix=apps/dashboard ../argentum/argentum-dashboard  main
git subtree add --prefix=apps/landing   ../argentum/argentum-landing    main
```

Then move `docs/` in, commit, and only once everything below passes, swap the
directory into place and archive the three originals read-only.

**2. Leave `go.mod` alone.** `apps/backend/go.mod` keeps
`module github.com/fauzanebd/argentum` despite living in a subdirectory. A Go
module path is a namespace, not a filesystem path, and nothing external imports
this module — so **zero import rewrites across ~120 files**. Do not "tidy" this.
Add a comment in `go.mod` explaining why, or the next person will fix it and cause
a 120-file diff.

**3. pnpm workspace.**
```yaml
# pnpm-workspace.yaml
packages:
  - 'apps/dashboard'
  - 'apps/landing'
  - 'apps/widget'
  - 'packages/*'
```
Keep each app's `package.json` and its own lockfile resolution; do **not** attempt
to unify React 18 (dashboard/landing) with Preact (widget). They are separate
workspace members for a reason.

**4. Top-level `Makefile`** — the single entry point agents and CI both use:
```make
dev-infra:  cd apps/backend && docker-compose --profile dev up -d postgres postgres_demo redis metabase
api:        cd apps/backend && go run ./cmd/api
worker:     cd apps/backend && go run ./cmd/worker
test:       cd apps/backend && go test -race ./...
vet:        cd apps/backend && go vet ./...
eval:       cd apps/backend && go run ./cmd/eval -set testdata/eval/golden.yaml
types:      # filled in by T-02b
web:        pnpm --filter dashboard dev
build:      cd apps/backend && go build ./... && pnpm -r build
```

**5. One CI workflow, path-filtered.** Replace the three pipelines with jobs
gated on `dorny/paths-filter`:

| Job | Fires on | Runs |
| --- | -------- | ---- |
| `backend` | `apps/backend/**` | vet, `test -race`, build api + worker + **discord** |
| `web` | `apps/{dashboard,landing,widget}/**`, `packages/**` | `pnpm -r build`, `pnpm -r lint` |
| `types` | `apps/backend/**` | `make types` then `git diff --exit-code packages/api-types` |
| `docker` | tags `v*.*.*` | build + push GHCR images, context `apps/backend` |

**Delete the current `paths:` filter on the whole workflow** — today a non-Go
change skips CI entirely (finding Q-3). Path filtering belongs per-job, not on the
trigger.

**6. Fix the Docker build context.** `Dockerfile.api`, `Dockerfile.worker`, and
`Dockerfile.discord` move to `apps/backend/`; the build context in CI becomes
`apps/backend`. Verify each image still builds — this is the step most likely to
silently break.

**7. Reconfigure Cloudflare Pages — the only step that can break production.**
For each of the two Pages projects, set:
- Root directory: `apps/dashboard` / `apps/landing`
- Build command: `pnpm install --frozen-lockfile && pnpm build`
- Output directory: `dist`

`apps/dashboard/functions/` (the SPA-fallback Pages middleware) must still be
picked up relative to the new root. **Deploy a preview branch and confirm before
pointing production at the monorepo.** You already spent four commits fighting
Pages (`a715171`→`9e9899f`); budget for that again.

**8. Settle the owner mismatch while you are here.** `go.mod` says `fauzanebd`,
GHCR and CI say `haritsrizkall`. Pick one for the new remote and note the decision
in `apps/backend/README.md`. (Keep the module path as-is regardless — see step 2.)

**9. Update `.gitignore`** as the de-duplicated union of the three, with paths
re-rooted: `apps/*/node_modules`, `apps/*/dist`, `apps/backend/.env`.

**10. Delete the stray** `apps/dashboard/scratch-chat-page-plan.md` (finding P-5)
— it is a one-line artifact and this is the natural moment.

### Notes for the implementer

- `git subtree add` produces a merge commit per app. **Know exactly what survives
  — verified, not assumed:**

  | Command | Works? |
  | ------- | ------ |
  | `git blame apps/backend/internal/app/chat_runner.go` | ✅ attributes to real pre-migration commits (`d782129`, `dcd0355`, `94fe370`, …) |
  | Original SHAs still resolve — `git show 3891579` | ✅ subtree does not rewrite commits |
  | `git log -- apps/backend/<path>` | ❌ shows only post-migration commits |
  | `git log --full-history -- <path-without-the-apps/backend-prefix>` | ✅ full pre-migration history |

  Old commits recorded old paths, so path-filtered `log` does not cross the merge.
  Blame does, because rename detection handles it. `--follow` does not help.

  **This is the right trade.** `git filter-repo --to-subdirectory-filter` would fix
  path-filtered `log`, but it rewrites every commit — so `3891579`, `d782129` and
  the ~20 other SHAs cited throughout `docs/research/` and `docs/coverage/` would
  cease to exist, and the archived originals would no longer correspond. Blame plus
  stable SHAs is worth more than `log`-by-path, which has a one-flag workaround.
- Do **not** delete the three original repos. Archive them read-only on the remote.
  They are the fallback if a deploy reconfiguration goes wrong.
- Do not attempt Turborepo or Nx yet. Two frontends and one Go module do not need a
  build orchestrator; `pnpm -r` plus the Makefile is enough. Revisit if
  `packages/` exceeds four members.

### Acceptance
- [x] One repo, all three histories in the graph — 75 commits
- [x] `git blame apps/backend/internal/app/chat_runner.go` reaches `d782129`; dashboard blame reaches `0687da5`
- [x] Original SHAs still resolve, so every citation in `docs/` stays valid
- [x] `cd apps/backend && go build ./... && go vet ./... && go test ./...` — identical to the `T-00` baseline (build OK, vet clean, same 3 passing packages)
- [x] **Zero changes to any Go import path** — tree diff vs. the original shows no `.go` differences at all
- [x] `pnpm -r build` builds dashboard and landing
- [x] `pnpm -r lint` passes (and now actually runs — see Q-11)
- [ ] All three Docker images build from the new context — **UNVERIFIED, Docker was not running**
- [ ] Cloudflare Pages preview deploys succeed for both frontends **before** production is repointed
- [ ] CI: a docs-only change runs no app jobs; a backend-only change runs `backend` but not `web`
- [x] `cmd/discord` builds in CI (it never did before)
- [x] `docs/` is tracked — both workspace `docs/` and the recovered `apps/backend/docs/`

### Gate

Paste: (a) blame output proving history survived for one file per app, (b) the full
backend build/vet/test output, (c) a tree diff vs. the originals showing no `.go`
changes, (d) Cloudflare preview URLs for both frontends, (e) a CI run showing
correct per-job path filtering.

### Status — 2026-07-26

Local migration **complete** at `/Users/rizkal/Work/smartsoft/argentum-mono`,
commits `eef3cb5` (migration) and the lint fix on top. All local gates green. The
three original repos are untouched.

Outstanding, and each needs a human:
1. **Docker image builds** — Docker Desktop was not running; the three Dockerfiles
   are unverified against the `apps/backend` context.
2. **Cloudflare Pages** — two projects need their root directory and build command
   repointed, verified on a preview branch first.
3. **Remote** — `git remote add` needs an owner decision (`fauzanebd` vs
   `haritsrizkall`) and a repo name.
4. **Directory swap** — move `argentum-mono` into place and archive the originals.

See `docs/coverage/migration-notes.md` for the exact steps.

### Out of scope
- `packages/api-types` contents — scaffold the directory only; `T-02b` fills it
- `packages/chat-ui` — created in `T-21` when the widget needs it
- Turborepo / Nx / remote caching
- Renaming the Go module path

---

## T-01 · Eval harness
**Repo:** BE · **Size:** 3d · **Deps:** T-00 · **Priority:** P0 · **Never cut**

The system has no way to know whether a prompt or model change helped. Build one.

**Do:**
- `cmd/eval/main.go` — CLI: `go run ./cmd/eval -set testdata/eval/golden.yaml [-model X] [-out report.json]`.
- `internal/eval/` — runner, scorer, report types.
- Golden set at `testdata/eval/golden.yaml`, **≥30 cases** against the demo
  tenant's star schema (`fact_sales`, `dim_customers`, `dim_products`, `dim_date`).
  Case shape:
  ```yaml
  - id: rev-monthly-total
    question: "What was total revenue last month?"
    lang: en
    expect:
      kind: numeric          # numeric | contains | sql_shape | refusal | tool_called
      value: 1234567.89      # for numeric
      tolerance: 0.01
      must_call: [run_sql]
      must_not_call: [create_dashboard]
  ```
- Cover these categories, minimum counts: simple aggregate ×6, time-window ×5,
  grouping/top-N ×4, multi-source disambiguation ×3, chart/dashboard request ×3,
  Indonesian-language ×5 (including rupiah magnitude formatting), guardrail
  refusal ×4 (off-topic, injection, SQL mutation).
- Runner must go through the **real** `ChatRunner` path — same agent factory, same
  guardrails, same tools — against a seeded demo tenant. Not a mocked LLM.
- Score: per-case pass/fail plus aggregate `pass_rate`, `mean_tokens_in`,
  `mean_tokens_out`, `mean_latency_ms`, `mean_cost_usd`.
- `make eval` target. Write the first run to `docs/coverage/eval-baseline.md`.

**Notes for the implementer:**
- Numeric comparison must tolerate formatting: strip currency symbols, magnitude
  suffixes (Juta/Miliar/Triliun/K/M/B), and thousands separators before parsing.
- Language check: assert the reply's language matches `lang`. A cheap
  heuristic (Indonesian stopword ratio) beats an LLM judge for this and costs nothing.
- Guardrail cases assert the refusal *message*, not just non-answering.

**Acceptance:**
- [ ] `make eval` runs offline against local infra, no cloud dependency except the LLM
- [ ] ≥30 cases across all listed categories
- [ ] Report includes pass rate, token, latency, and cost aggregates
- [ ] Baseline committed to `docs/coverage/eval-baseline.md`

**Gate:** paste the full report summary. State the baseline pass rate as a number.

---

## T-02 · Test coverage for CRITICAL packages + real CI gate
**Repo:** BE · **Size:** 3d · **Deps:** none (parallel with T-01) · **Priority:** P0 · **Never cut**

**Tests to write** (see `../coverage/test-coverage.md` for the risk ranking):

| Package | Must cover |
| ------- | ---------- |
| `internal/crypto` | Encrypt/decrypt round-trip; wrong key fails; malformed ciphertext errors rather than panics; key-length validation |
| `internal/tenantctx` | Every getter returns "" for an unset key; values do not leak across derived contexts |
| `internal/guardrails` | **Golden suite: every rule in `config/guardrails.yaml` gets ≥1 must-block and ≥1 must-pass case.** Include the specific false positives the YAML comments describe: "create a dashboard", "update me on sales", CSS `margins`, "integer target", "linked list", benign follow-ups ("ok", "why?"), Indonesian particles. LLM patterns get a stub LLM returning TRUE/FALSE. |
| `internal/app` (pricing) | `RecordLLM` cost math incl. cache multipliers (1.25× create, 0.10× read); unknown model falls back to `DefaultPricing`; zero tokens → zero cost |
| `internal/app` (threading) | `continueOrFork` decision table: under idle threshold → continue; over → classifier RELATED continues / NEW forks; classifier error → safe default. Fake classifier + fake repos |
| `internal/app` (cron) | `validateCron`, `normalizeTimezone`, `nextFire` — including DST boundaries and invalid IANA names |
| `internal/config` | All 7 `Effective*()` fallback chains; `WorkerQueueMap()` CSV parsing incl. malformed input; `DatabaseURL()` escaping with special characters in the password; `redisDialAddr()` for URI and bare-host forms |
| `internal/auth` | Argon2id hash/verify; JWT sign/verify; expired token rejected; refresh token rejected on an access-token route |
| `internal/tools` | `run_sql` byte-cap trimming loop (wide rows shrink and set `truncated`); `ResolveSource` with 0 / 1 / many sources and an explicit `source_id`; empty-company-ID rejection |

**CI changes** in `.github/workflows/ci.yaml`:
- `GO_VERSION` → `'1.26'` (matches `go.mod`, stops the silent toolchain download)
- add `go vet ./...`
- add `go test -race -count=1 ./...`
- add `go build -o discord ./cmd/discord` — currently never compiled in CI
- add `golangci-lint run` with a committed `.golangci.yml` (start narrow:
  `errcheck`, `govet`, `staticcheck`, `ineffassign`, `unused`)
- add a frontend job: `pnpm install --frozen-lockfile && pnpm build && pnpm lint`
- **remove the `paths:` filter** — it currently means non-Go changes skip CI entirely

**Acceptance:**
- [ ] Every CRITICAL package from the coverage doc has tests
- [ ] Guardrail golden suite covers every rule, both directions
- [ ] CI fails when a test fails (prove it: push a deliberately broken test, observe red, revert)
- [ ] `cmd/discord` builds in CI

**Gate:** `go test -race ./... 2>&1 | tail -40` — paste it. Plus the CI run URL
showing red on the deliberate break and green after revert.

---

## T-02b · Generate TS types from Go structs
**Repo:** BE, PKG, FE · **Size:** 1d · **Deps:** T-00b, T-02 · **Priority:** P1

Today `apps/dashboard/src/features/*/types.ts` hand-mirrors Go JSON tags and
nothing checks that they agree. A renamed field or a changed type is a runtime
surprise, found by a user. The monorepo makes this mechanically fixable, so fix it.

**Do:**
- Pick a generator. `tygo` is the least-effort fit: reads Go source, respects
  `json` tags, no annotations required. Evaluate `go-jsonschema` +
  `json-schema-to-typescript` only if `tygo` cannot express the domain types.
- Config covering `internal/domain` and `pkg/models` — the two packages whose
  types cross the wire. Output to `packages/api-types/src/`.
- `make types` regenerates; the output is **committed**, so a reviewer sees
  contract changes in the diff.
- CI job `types`: run `make types`, then `git diff --exit-code packages/api-types`.
  A Go struct change without a regenerated type is a red build.
- Migrate the dashboard's hand-written types to import from
  `@argentum/api-types`, one feature at a time. Delete each `types.ts` only once
  its feature compiles against the generated types.
- Where the generated shape and the hand-written one disagree, **the Go struct is
  the truth** — but check each disagreement before deleting: some are real bugs
  worth a line in the report.

**Notes for the implementer:**
- WebSocket event types (`ChatEvent`, `ToolCallEvent`) matter most — that is the
  contract with the least documentation and the most drift risk.
- Go `map[string]interface{}` generates as `Record<string, unknown>`, which is
  correct but weak. Do not hand-strengthen it in the generated file; if a metadata
  shape deserves a type, give it one in Go.
- `time.Time` → `string`. Make sure the dashboard's date handling still expects a
  string, not a `Date`.

**Acceptance:**
- [ ] `make types` produces types for every domain type crossing the API
- [ ] Dashboard compiles against `@argentum/api-types` with its hand-written
      duplicates deleted
- [ ] CI fails when a Go struct changes without regeneration — prove it and revert
- [ ] Any real Go↔TS mismatch found during migration is listed in the report

**Gate:** paste the CI failure from a deliberate Go field rename, then the pass
after `make types`. List every mismatch the migration surfaced.

---

## T-02c · Fix primary-model metering on streaming turns
**Repo:** BE · **Size:** 1d · **Deps:** T-02 · **Priority:** P0 · **Never cut**

**Finding Q-12, observed live in the `T-00` smoke test.** A full multi-step agent
turn recorded **zero** usage events for the primary model. The only `llm_call` row
was `gpt-5-mini` — the light model behind guardrails. Under the current default
provider, the dominant cost of every chat turn is invisible.

**Must land before `T-03`**, whose budget check would otherwise gate on a number
that is always near zero — enforcement that silently never triggers is worse than
no enforcement, because it looks like it works.

**Do:**
- Confirm the mechanism first. `MeteredLLM.wrapStream`
  (`internal/app/metering_llm.go:136`) only calls `record()` when the provider put
  usage in stream event metadata. Determine what `agent-sdk-go` sends for the
  OpenAI interface — the strong hypothesis is a missing
  `stream_options: {"include_usage": true}`, but verify before changing anything.
- Fix at the source if possible (request usage in the stream). If `agent-sdk-go`
  cannot be made to emit it, fall back to counting tokens locally with a tokenizer
  and record with a `estimated: true` metadata flag — an approximate cost beats a
  silent zero.
- **Add a loud failure mode.** A completed streaming turn that produced no usage
  event must log at `Warn` with company, model, and interface. Silence is what let
  this survive; make it noisy.
- Add a metric: usage-events-per-turn, so a future regression is visible on a
  dashboard rather than discovered by a smoke test.
- Regression test: fake LLM emitting a stream *with* usage and one *without*;
  assert the with-usage case records, and the without-usage case records an
  estimate and logs a warning.

**Acceptance:**
- [ ] A streaming turn on an OpenAI-interface provider records a non-zero `llm_call`
- [ ] A streaming turn on Anthropic still records, including cache tokens (no regression on `74f5419`)
- [ ] Zero-usage streams warn loudly rather than passing silently
- [ ] `cost_by_model_usd` shows the primary model after one chat turn

**Gate:** repeat the `T-00` smoke test — signup, connection, one analytical
question — then paste `/api/usage/summary`. The primary model must appear with
non-zero tokens. Compare against the pre-fix output recorded in
`../coverage/environment-notes.md` C-2.

---

## T-03 · Enforce credits with graceful degradation
**Repo:** BE, FE · **Size:** 1d · **Deps:** T-02, **T-02c** · **Priority:** P0

**Finding B-1:** `UsageService.append` decrements the balance and ignores the
result. Nothing checks it. A tenant on platform LLM keys can spend without limit.

**Do:**
- `UsageService.CheckBudget(ctx, companyID) (BudgetState, error)` returning
  `BudgetOK` / `BudgetWarning` (<20% remaining) / `BudgetExhausted` (≤0).
- Check in `ChatEnqueuer` **before** enqueueing, not in the worker — fail fast
  and don't pay for a task that gets refused.
- `BudgetExhausted` → `HTTP 402` with a clear message; on WhatsApp/Discord/Lark,
  a plain-language reply, not a stack trace.
- **Never block a tenant using their own LLM key.** If
  `company_llm_credentials` has a primary row, skip the check — they pay their
  provider directly.
- `BudgetWarning` → include a `budget_warning` field in the chat response; FE
  shows a dismissible banner.
- Redis-cache the balance for 60s so the check doesn't add a query per turn.
- Config: `CREDITS_ENFORCEMENT_ENABLED` (default `true`),
  `CREDITS_WARNING_THRESHOLD_PCT` (default `20`).

**Acceptance:**
- [ ] Tenant at zero balance gets 402 with an actionable message, no LLM call made
- [ ] Tenant with own LLM credentials is never blocked
- [ ] Warning banner appears below the threshold
- [ ] Kill switch restores today's behaviour

**Gate:** integration test — seed a company with 0 credits, POST `/api/chat`,
assert 402 **and** assert zero new `usage_events` rows. Repeat with a BYO-LLM
company and assert 200.

---

## T-04 · Apply RBAC + team invites
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-02 · **Priority:** P0 · **Never cut**
**Migration:** `027_user_invites`

**Findings S-1, S-2.** `AdminOnly()` exists and is applied to nothing. Nine
credential/config-mutating routes are open to any member.

**Do:**
1. Apply `middleware.AdminOnly()` to: `PUT /api/connections/:id/dsn`,
   `DELETE /api/connections/:id`, `PUT /api/settings`, `POST /api/phones`,
   `DELETE /api/phones/:phone`, `PUT|DELETE /api/discord`,
   `POST|DELETE /api/discord/users`, `PUT|DELETE /api/lark`,
   `POST|DELETE /api/lark/users`, `DELETE /api/scheduled-tasks/:id`,
   and every LLM-credential route.
2. Team management (new `UserHandler` routes, admin-only):
   - `GET /api/users` — list company users
   - `POST /api/users/invite` — `{email, role}` → create a pending user + a
     single-use invite token (hashed at rest, 7-day TTL)
   - `POST /api/auth/accept-invite` — `{token, password}` → activate (public route)
   - `PATCH /api/users/:id` — change role
   - `DELETE /api/users/:id` — deactivate; **never** allow removing the last admin
3. FE: Settings → Team tab. Invite form, member list with role badges, revoke.
4. Migration `027_user_invites` — `user_invites` table (`company_id`, `email`,
   `role`, `token_hash`, `expires_at`, `accepted_at`, `invited_by`), plus a
   nullable `users.activated_at` so a pending user cannot log in before accepting.

**Acceptance:**
- [ ] Member JWT gets 403 on every route listed in step 1
- [ ] Admin JWT succeeds on all of them
- [ ] Invite → accept → login works end to end
- [ ] Last admin cannot be removed or demoted

**Gate:** table-driven test over every gated route × {admin, member} asserting
{200-ish, 403}. Paste the test output.

---

## T-05 · Agent action audit log
**Repo:** BE · **Size:** 1.5d · **Deps:** T-02 · **Priority:** P0
**Migration:** `021_agent_actions`

**Finding S-5.** `usage_events` records cost, not behaviour. Before the agent can
act, there must be an immutable record of what it did.

**Do:**
- Table `agent_actions`: `id`, `company_id`, `thread_id`, `message_id`,
  `actor_kind` (`user`|`schedule`|`watcher`|`api_key`), `actor_ref`, `channel`,
  `tool_name`, `source_id`, `args_redacted` (jsonb), `args_hash`,
  `result_status` (`ok`|`error`|`blocked`|`truncated`), `error_text`,
  `rows_returned`, `duration_ms`, `created_at`. Index on
  `(company_id, created_at desc)` and `(thread_id)`.
- `domain.AgentAction` + `AgentActionRepository`; `adapters/postgres/agent_action_repo.go`.
- Record from a **wrapper around the tool interface**, not inside each tool —
  `tools.WithAudit(tool, repo)` decorating every tool in `cmd/worker/main.go`.
  One integration point, no per-tool duplication.
- `args_redacted` must strip anything DSN-shaped. **Full SQL text is retained** —
  it is the point of the log — but never a credential.
- `GET /api/audit/actions?from&to&thread_id&tool&limit&offset`, admin-only.
- Append-only: repository exposes no update or delete.

**Acceptance:**
- [ ] Every tool call produces exactly one row, success or failure
- [ ] A blocked guardrail turn records `result_status=blocked`
- [ ] No row contains a decrypted DSN or API key
- [ ] Audit endpoint is admin-gated and company-scoped

**Gate:** run one demo chat that calls `get_schema` + `run_sql` +
`create_visualization`; paste the resulting three rows (redacted args visible).
Then `grep` the table dump for the demo DSN password and show zero matches.

---

# Week 2 — Authoritative numbers

## T-06 · Metric registry
**Repo:** BE, FE · **Size:** 3d · **Deps:** T-02 · **Priority:** P0 · **Never cut**
**Migration:** `022_metric_definitions`

**The accuracy foundation.** Today every question re-derives its SQL, so the same
question can produce two different numbers. Watchers cannot exist on top of that.

**v1 shape — deliberately narrow. Do not add dimensions, joins, or a DSL:**

```sql
CREATE TABLE metric_definitions (
  id            uuid PRIMARY KEY,
  company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  source_id     uuid NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  key           text NOT NULL,            -- 'revenue', 'active_customers'
  label         text NOT NULL,
  description   text NOT NULL,            -- what the agent reads to decide relevance
  sql_template  text NOT NULL,            -- must contain {{from}} and {{to}}
  value_column  text NOT NULL,
  grain         text NOT NULL,            -- day|week|month|quarter|year
  unit          text NOT NULL,            -- currency|count|percent|ratio
  currency      text,
  higher_is_better boolean NOT NULL DEFAULT true,
  enabled       boolean NOT NULL DEFAULT true,
  created_by    uuid, created_at timestamptz, updated_at timestamptz,
  UNIQUE (company_id, key)
);
```

- `{{from}}` / `{{to}}` are bound as **parameters**, never string-interpolated.
  Reject templates containing anything but a single SELECT (reuse the guardrail
  mutation patterns).
- Validation on save: render with a trailing-7-day window, execute via
  `ExecuteReadOnly`, assert exactly one row and that `value_column` is numeric.
  **A metric that does not validate cannot be saved.**
- CRUD API `/api/metrics` — read for members, write admin-only.
- FE: Settings → Metrics tab. Create/edit form with a "Test" button showing the
  rendered SQL and the returned value.

**Acceptance:**
- [ ] Saving an invalid metric fails with a specific reason
- [ ] Non-SELECT templates rejected
- [ ] Window params are bound, not interpolated (prove with a `'; DROP` style value)
- [ ] Same metric queried twice returns an identical number

**Gate:** define three demo-tenant metrics (revenue, order count, AOV). Paste each
one's validated value. Attempt one injection payload in the window param and show
the failure.

---

## T-07 · `list_metrics` + `query_metric` tools
**Repo:** BE · **Size:** 1.5d · **Deps:** T-06 · **Priority:** P0 · **Never cut**

**Do:**
- `internal/tools/list_metrics.go` — returns key, label, description, unit, grain
  per enabled metric.
- `internal/tools/query_metric.go` — params: `metric_key`, `from`, `to`,
  optional `compare_to` (`previous_period` | `same_period_last_year`). Returns
  value, comparison value, delta, delta percentage, and the window used.
- Register both in `cmd/worker/main.go`, wrapped by the T-05 audit decorator.
- Inject the metric catalog into the turn context in `ChatRunner`, alongside the
  source catalog — same pattern as `withSourcesContext`.
- System prompt: add a rule ranked above the `run_sql` guidance — *if a defined
  metric answers the question, use `query_metric`; only fall back to `run_sql` for
  questions no metric covers, and say so.*
- Meter `query_metric` as a `sql_query` event.

**Acceptance:**
- [ ] "What was revenue last month?" calls `query_metric`, not `run_sql`
- [ ] A question with no matching metric still works via `run_sql`
- [ ] `compare_to` returns a correct delta
- [ ] Unknown `metric_key` returns a helpful error listing available keys

**Gate:** eval run with metric-specific cases added. Paste the before/after pass
rate **and** the token delta — this should reduce mean input tokens measurably.

---

## T-07b · Fix guardrail over-reach
**Repo:** BE · **Size:** 0.5d · **Deps:** T-02 · **Priority:** P1

**Findings Q-4, Q-6.** Redaction rules break legitimate BI output; the
system-prompt-leak rule false-positives on "what can you do?".

**Do:**
- Narrow `redact_nik`: require NIK context nearby (`nik`, `ktp`, `no. identitas`)
  rather than matching any 16-digit run — it currently blanks order IDs and
  account numbers.
- Make `redact_emails` and `redact_phone_numbers` **configurable per company**
  (`companies.pii_redaction_mode`: `strict` | `contact_ok` | `off`, default
  `strict`). A tenant asking for a customer contact list must be able to get one.
- Narrow `block_system_prompt_leak` to leak-shaped phrasing only (e.g. "my
  instructions are", "my system prompt is") instead of the bare phrase "you are
  an ai".
- Every change needs a golden case in the T-02 guardrail suite, both directions.

**Acceptance:**
- [ ] "list top 10 customers with their emails" returns emails under `contact_ok`
- [ ] Under `strict`, it still redacts
- [ ] A 16-digit order ID survives; a labelled NIK is still redacted
- [ ] "What can you do?" answers normally

**Gate:** guardrail golden suite green, with the four new cases visible in output.

---

# Week 3 — It tells you first

## T-08 · Watchers domain + evaluation loop
**Repo:** BE · **Size:** 3d · **Deps:** T-06, T-07 · **Priority:** P0 · **Never cut**
**Migration:** `023_watchers`

**The wedge.** This is the ticket that changes how a company works.

```sql
CREATE TABLE watchers (
  id uuid PRIMARY KEY,
  company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  metric_id  uuid NOT NULL REFERENCES metric_definitions(id) ON DELETE CASCADE,
  name text NOT NULL,
  window_grain text NOT NULL,          -- day|week|month
  comparator text NOT NULL,            -- gt|lt|pct_change_gt|pct_change_lt|no_data
  threshold numeric NOT NULL,
  compare_to text,                     -- previous_period|same_period_last_year
  cron_expression text NOT NULL,
  timezone text NOT NULL DEFAULT 'UTC',
  channels jsonb NOT NULL,             -- [{channel, ref}] — WA phone, Discord channel, Lark chat, dashboard
  cooldown_minutes int NOT NULL DEFAULT 720,
  enabled boolean NOT NULL DEFAULT false,   -- REQUIRES a passing dry-run to enable
  last_fired_at timestamptz,
  last_dry_run_at timestamptz,
  created_by uuid, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE watcher_events (
  id uuid PRIMARY KEY,
  watcher_id uuid NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
  company_id uuid NOT NULL,
  fired_at timestamptz NOT NULL,
  metric_value numeric, comparison_value numeric, delta_pct numeric,
  breached boolean NOT NULL,
  suppressed_reason text,              -- 'cooldown' | 'disabled' | null
  thread_id uuid, message_id uuid,
  delivery_status jsonb                -- per-channel outcome
);
```

**Do:**
- `internal/app/watcher_service.go`: CRUD, `DryRun`, `HandleFire`.
- Reuse the existing `asynq.PeriodicTaskManager` pattern from scheduled tasks —
  a second DB-backed config provider emitting `watcher:eval` tasks. **Do not
  build a second scheduler.**
- `watcher:eval` handler:
  1. Evaluate the metric for the current window via the same code path as
     `query_metric` (no duplicate SQL logic).
  2. Apply the comparator. Not breached → record `watcher_events` row, stop.
  3. Breached but inside cooldown → record with `suppressed_reason='cooldown'`, stop.
  4. Breached → enqueue a `chat:run` into a dedicated watcher thread with a
     briefing prompt: the metric, the values, the delta, and an instruction to
     explain the likely drivers in ≤120 words and name what to check next.
  5. On completion, deliver to every configured channel.
- `DryRun` evaluates the last N periods and reports how many times it *would* have
  fired. **`enabled` cannot be set true without a dry-run in the last 24h** —
  this is the guard against the trust-destroying false alarm.
- Config: `WATCHER_MAX_PER_COMPANY` (default 20), `WATCHER_ENABLED` kill switch.

**Acceptance:**
- [ ] Non-breaching evaluation writes an event row and sends nothing
- [ ] Breach fires an agent turn and delivers to all configured channels
- [ ] Cooldown suppresses a second fire and records why
- [ ] Enabling without a recent dry-run is rejected
- [ ] Deleting a metric cascades to its watchers

**Gate:** on the demo tenant, define a watcher guaranteed to breach
(`revenue lt 999999999`), let it fire, and paste (a) the `watcher_events` row,
(b) the agent's generated message, (c) the delivery status JSON. Then a
non-breaching watcher showing silence.

---

## T-09 · Watchers UI
**Repo:** FE · **Size:** 2d · **Deps:** T-08 · **Priority:** P0 · **Never cut**

**Do:**
- `src/features/watchers/`: list page, create/edit form, event history sheet.
- Form: metric picker (from `/api/metrics`), comparator, threshold, window,
  cron (reuse `features/scheduled-tasks/cron-presets.ts`), timezone, channel
  multi-select with a target ref per channel, cooldown.
- **Dry-run is a required step in the form.** Show "would have fired N times in
  the last 12 periods" before the Enable toggle unlocks.
- Sidebar nav entry beside Scheduled Tasks.
- Event history: fired-at, value, comparison, delta, breached, suppressed reason,
  delivery status, link to the generated thread.

**Acceptance:**
- [ ] Cannot enable a watcher without running a dry-run
- [ ] Dry-run result is shown before enabling
- [ ] Event history renders suppressed and delivered events distinguishably
- [ ] `pnpm build` clean

**Gate:** screenshots of create → dry-run → enable → fired event with its thread.

---

# Week 4 — It does things

## T-10 · Action framework
**Repo:** BE · **Size:** 2.5d · **Deps:** T-05 · **Priority:** P1
**Migration:** `024_actions`

Write-capable agency, gated. **Never route this through `run_sql`** — tenant SQL
stays read-only, permanently.

**Do:**
- `company_actions`: `company_id`, `action_kind`, `enabled`, `requires_approval`
  (default `true`), `config_encrypted`, `allowed_roles`.
- `action_invocations`: `id`, `company_id`, `thread_id`, `message_id`,
  `action_kind`, `params_redacted`, `idempotency_key` (unique per company),
  `status` (`proposed`|`approved`|`rejected`|`executed`|`failed`|`expired`),
  `proposed_at`, `decided_at`, `decided_by`, `executed_at`, `result`, `error_text`.
  Proposals expire after 24h.
- `internal/actions/` — `Action` interface: `Kind()`, `Describe(params)` (the
  human-readable sentence shown for approval), `Validate(params)`, `Execute(ctx, params)`.
- `internal/app/action_service.go` — `Propose`, `Approve`, `Reject`, `Execute`.
  Execution is idempotent on `idempotency_key`.
- Agent-facing tool `propose_action`: returns the invocation id and a message
  telling the user approval is needed. **The tool cannot execute.** Only the
  approval endpoint can.
- `requires_approval=false` is permitted per company per action kind but must be
  an explicit admin opt-in, and still writes to `agent_actions`.

**Acceptance:**
- [ ] Agent can propose but never execute
- [ ] Approving executes exactly once; approving twice does not double-execute
- [ ] Rejecting leaves no side effect
- [ ] A proposal older than 24h cannot be approved
- [ ] Every proposal and decision appears in `agent_actions`

**Gate:** unit tests for the state machine including double-approve and expiry.
Paste output.

---

## T-11 · Approval UI + events
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-10 · **Priority:** P1

**Do:**
- New WS event type `action_proposed` carrying the invocation id and
  `Describe()` text.
- `GET /api/actions/pending`, `POST /api/actions/:id/approve`,
  `POST /api/actions/:id/reject`.
- FE: inline approval card in the chat stream (reuse `tool-call-card.tsx`
  styling) — description, params, Approve / Reject, and the resulting state.
- A pending-approvals badge in the app shell.
- **Dashboard-only for this sprint.** Chat-native approval (WhatsApp reply
  "YES") is Sprint 2 — see `backlog.md`.

**Acceptance:**
- [ ] Proposal appears live in the chat stream without a refresh
- [ ] Approve executes and the card reflects the outcome
- [ ] Reject is terminal
- [ ] Non-permitted roles see the card read-only

**Gate:** recording or screenshot sequence of propose → approve → executed.

---

## T-12a · Action: `send_message`
**Repo:** BE · **Size:** 1d · **Deps:** T-10 · **Priority:** P1

The action that makes watchers useful — the agent can brief people, not just the
person who asked.

**Do:** params `channel`, `target_ref`, `body`, optional `attach_document_id`.
Targets restricted to already-allowlisted refs (WhatsApp phones, Discord/Lark
allowlists) — **an action must never be able to message an arbitrary number.**
Reuse the existing outbound providers.

**Gate:** propose → approve → message arrives on a real channel. Then attempt a
non-allowlisted target and show the rejection.

---

## T-12b · Action: `http_action`
**Repo:** BE · **Size:** 1.5d · **Deps:** T-10 · **Priority:** P2 · **Cut #4**

Generic authenticated outbound call, so a company can wire Argentum into
whatever they already run (ticket systems, ERP, internal endpoints).

**Do:** per-company registered endpoints only — `{name, method, url_template,
header_template, body_schema}`, credentials encrypted with the DSN cipher. The
agent picks a **registered name**, never a raw URL. Enforce an allowlist of
hosts, a 10s timeout, no redirects, and block private/link-local IP ranges
(SSRF).

**Gate:** register a local test endpoint, propose, approve, observe the request.
Then attempt `http://169.254.169.254/` and show it blocked.

---

# Week 5 — Other agents call it

## T-13 · Scoped API keys
**Repo:** BE, FE · **Size:** 2d · **Deps:** T-04 · **Priority:** P1
**Migration:** `025_api_keys`

**Finding P-2.** Everything requires a human JWT, so nothing can integrate.

**Do:**
- `api_keys`: `id`, `company_id`, `name`, `key_prefix` (shown in UI),
  `key_hash` (Argon2id — reuse `internal/auth`), `scopes` (text[]),
  `created_by`, `last_used_at`, `expires_at`, `revoked_at`.
- Scopes: `read:metrics`, `read:threads`, `write:chat`, `read:usage`,
  `read:audit`, `write:actions`. Deny by default.
- `middleware.APIKeyAuth()` — accepts `Authorization: Bearer arg_<prefix>_<secret>`,
  sets company + `actor_kind=api_key` + `actor_ref` on the context so T-05 audit
  rows attribute correctly.
- Per-key rate limiting, separate bucket from the user limiter.
- Plaintext shown **once** at creation, never retrievable.
- FE: Settings → API Keys tab. Create, copy-once, list with prefix + last-used,
  revoke.

**Acceptance:**
- [ ] Key without the needed scope gets 403
- [ ] Revoked key gets 401 immediately
- [ ] Expired key gets 401
- [ ] Audit rows attribute to `api_key` with the key id
- [ ] Plaintext appears in exactly one response, ever

**Gate:** table-driven scope test. Paste output. Plus a `curl` transcript of a
successful and a revoked call.

---

## T-14 · MCP server
**Repo:** BE · **Size:** 2.5d · **Deps:** T-13 · **Priority:** P1 · **Cut #2**

"Agent ready", literally: any MCP client — Claude Code, a customer's own agent —
can use Argentum's tools.

**Do:**
- `cmd/mcp/main.go`, exposing over MCP: `list_sources`, `get_schema`,
  `list_metrics`, `query_metric`, `run_sql`, `create_visualization`,
  `create_dashboard`, `list_watchers`.
- **Hard rule: import `internal/tools`. Do not reimplement any tool.** Any
  divergence between the MCP surface and the agent surface is a bug.
- Auth by API key → resolves company → same `tenantctx` scoping as the worker.
- Every call writes an `agent_actions` row with `actor_kind=api_key`.
- Same metering path — MCP usage bills like agent usage.
- Ship `docs/mcp/setup.md` with a copy-pasteable client config.

**Acceptance:**
- [ ] Claude Code connects with an API key and lists tools
- [ ] `query_metric` over MCP returns the same value as the dashboard
- [ ] Scope enforcement holds (a `read:metrics`-only key cannot `run_sql`)
- [ ] Calls appear in the audit log and in usage

**Gate:** transcript of an MCP client retrieving a metric, plus the matching
audit row and usage event.

---

## T-15 · Outbound webhooks
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-08 · **Priority:** P2 · **Cut #1**
**Migration:** `026_outbound_webhooks`

**Do:** per-company subscriptions to `watcher.breached`, `action.executed`,
`scheduled_task.completed`. HMAC-SHA256 signature (mirror the inbound
verification style), asynq-backed delivery with exponential retry, delivery log,
auto-disable after 20 consecutive failures.

**Gate:** local receiver, trigger a watcher breach, show the signed payload
verifying against the secret.

---

# Week 6 — Shippable

## T-16 · Iteration budget + anti-fabrication
**Repo:** BE · **Size:** 2d · **Deps:** T-01 · **Priority:** ~~P1~~ **P0** · **No longer cuttable**

**Finding Q-5, escalated to P0 after being observed live.** The 3-iteration cap does
not merely truncate deep work — it makes the agent **fabricate**. In the `T-00`
smoke test the agent exhausted its budget on schema lookups plus one date probe,
never ran the aggregation, and reported *"Total Sales for December 2024:
$1,234,567.89"* against a true 3,863,405,700.00. Right month, right currency,
confident prose, invented number. Full reproduction in
[`../coverage/environment-notes.md`](../coverage/environment-notes.md) C-1.

Moved earlier in the sprint and its dependency changed from `T-17` (tracing) to
`T-01` (evals) — evals are what prove the fix works; tracing is a nice-to-have here.

**Do:**
- Replace the fixed cap with a per-turn budget: max iterations (default 8), max tool
  calls (default 12), max cumulative tokens, wall-clock ceiling. Per-company
  configurable.
- **On exhaustion the agent must say what it could not finish.** Inject an explicit
  final-turn instruction when the budget runs out: state the question, state what
  was retrieved, state what was not, and ask whether to continue. Never emit a
  figure that did not come from a tool result.
- **Add a guardrail rule for numeric fabrication.** Output-scope: if the reply
  states a specific monetary or metric value and no `run_sql` / `query_metric`
  result was returned in the turn, block and replace with an honest "I wasn't able
  to complete the query" message. This is a blunt instrument and will need tuning —
  but the failure it prevents is the one that loses a customer.
- Emit an `iteration` WS event so the UI shows progress rather than a silent stall.
- Keep `agents.yaml` and `WithMaxIterations` in sync, or delete the YAML value and
  make Go authoritative — do not leave two sources of truth.

**Acceptance:**
- [ ] A question needing 5+ steps completes
- [ ] Budget exhaustion produces an explicit incomplete-answer message, never a number
- [ ] The exact smoke-test question returns the correct order of magnitude, or admits failure
- [ ] No regression in mean cost per answer

**Gate:** re-run the C-1 reproduction — "What were our total sales last month?"
against the demo tenant. Paste the reply and the true value side by side. Then the
full eval set: pass rate up, no cost regression.

---

## T-17 · Observability: Prometheus + tracing
**Repo:** BE · **Size:** 2d · **Deps:** none · **Priority:** P1 · **Cut #3 (tracing only)**

**Findings O-1, O-2, S-3.**

**Do:**
- Replace the custom JSON `/metrics` with Prometheus exposition
  (`promhttp`). Keep the existing counters, add: turn duration histogram,
  per-tool duration, LLM latency by model, queue depth, watcher fires, action
  executions.
- **Move `/metrics` off the public router** — bind an internal listener on a
  separate port, or require an admin JWT / metrics token. It currently exposes
  cost data publicly.
- OTel spans: one per turn, child spans for guardrails, memory hydration,
  embedding, each tool call, LLM call. OTLP exporter behind
  `OTEL_EXPORTER_OTLP_ENDPOINT`; no-op when unset.
- ServiceMonitor template in the Helm chart, gated on `metrics.serviceMonitor.enabled`.

**Gate:** `curl` the Prometheus endpoint and paste the exposition. Paste one
trace waterfall for a tool-calling turn showing LLM vs SQL time split.

---

## T-18 · Launch hygiene
**Repo:** BE, FE, LP · **Size:** 1.5d · **Deps:** all · **Priority:** P1

**Do:**
- **Landing page (P-1):** remove the Telegram claim, add Discord and Lark. Add
  watchers/proactive-alerts messaging — it is now the headline capability.
- Backfill `.down.sql` for migrations 001–014 (Q-7), or document explicitly that
  they are irreversible and why.
- `apps/backend/docs/`: document the WebSocket event schema (`started`, `delta`,
  `thinking`, `tool_call`, `tool_result`, `action_proposed`, `iteration`, `error`,
  `final`), the agent tool contracts, and API docs for metrics / watchers /
  actions / api-keys.
- Update `apps/backend/README.md` architecture diagram — it predates Discord,
  Lark, SQL Server, and the worker's periodic manager.
- Add a root `README.md` for the monorepo: layout, per-app quickstart, the
  `Makefile` targets, and the `go.mod` module-path note from `T-00b`.
- Refresh `docs/coverage/feature-coverage.md` to sprint-end reality.
- Final eval run → `docs/coverage/eval-sprint1.md`, compared against baseline.

**Gate:** final eval score ≥ baseline. Paste both numbers. Landing page
screenshot showing only shipped channels.

---

# Weeks 7–8 — Embeddable widget

**Goal:** a company running its own internal website — React, Vue, Angular, or
plain HTML — drops in a script tag or an npm component and their staff talk to
Argentum without leaving that page.

**Audience decision (locked):** the tenant's **own staff** on the tenant's own
internal site. Identity is asserted by the tenant's backend via HMAC signature.
Anonymous / public-facing embedding is explicitly out of scope — see
[`backlog.md`](backlog.md).

**Scope decision (locked):** chat only. No dashboard rendering, no alert feed.

## Architecture (decided — do not re-litigate in the tickets)

```
 Tenant's internal site (any framework)
   │
   │ 1. Tenant BACKEND computes, server-side:
   │       sig = HMAC-SHA256(embed_secret, "{user_ref}:{exp}")
   │    and hands {user_ref, exp, sig} to its own page.
   │    embed_secret NEVER reaches the browser.
   │
   ├─ <script src="…/argentum-widget.js"> or <ArgentumWidget/>
   │       Argentum.init({ clientKey, user: { ref, name, exp, sig } })
   │
   │ 2. Loader mounts an IFRAME (origin: Argentum's widget host).
   │    Token material crosses via postMessage after a ready handshake —
   │    never in the iframe URL, so it stays out of Referer and access logs.
   │
   ▼
 Widget app inside the iframe
   │ 3. POST /api/embed/session  { client_key, user_ref, exp, sig }
   │       ← Argentum verifies Origin + HMAC + expiry
   │       → 15-minute embed session JWT
   │ 4. POST /api/embed/chat  +  WS /api/embed/threads/:id/stream
   ▼
 Existing chat pipeline — ChatEnqueuer → asynq → worker → ChatRunner
```

**Why HMAC identity rather than a server-to-server token exchange:** no extra
network round-trip for the integrator, stateless on our side, and it is the
pattern developers already know from Intercom and Crisp. The short-lived session
JWT on top gives us revocation and TTL control that raw HMAC alone would not.

**Why an iframe rather than mounting into the host DOM:** CSS isolation (the
host's Tailwind or Bootstrap cannot break the widget, and we cannot break their
page), JS isolation, and a real origin boundary around the session token. The
cost is having to bridge sizing and open/close over `postMessage`, which is
mechanical.

---

## T-19 · Embed auth: keys, HMAC identity, session tokens
**Repo:** BE, FE · **Size:** 2.5d · **Deps:** T-04, T-13 · **Priority:** P0 (of this phase)
**Migration:** `028_embed_keys`

The security foundation. Get this wrong and a tenant's data is one forged request
away. Build it before any UI exists.

**Do:**
- Table `embed_keys`: `id`, `company_id`, `name`, `client_key` (public, prefix
  `argw_pub_…`, indexed, shown in UI), `secret_hash` (Argon2id — reuse
  `internal/auth`), `allowed_origins` (text[]), `enabled`, `created_by`,
  `last_used_at`, `revoked_at`, `created_at`.
- **`allowed_origins` is mandatory and cannot be `*`.** Reject a save with an
  empty list or a wildcard entry. Exact scheme+host+port matching only — no
  suffix matching (`https://evil-acme.com` must not match `acme.com`).
- `POST /api/embed/session` (public route, **not** behind `middleware.Auth`):
  1. Resolve company from `client_key`. Unknown or revoked → 401.
  2. Verify the `Origin` header against `allowed_origins`. Mismatch → 403,
     logged with the offending origin.
  3. Recompute `HMAC-SHA256(secret, "{user_ref}:{exp}")`, compare with
     `hmac.Equal` — **constant time, never `==`**.
  4. Reject `exp` in the past or more than 24h out (a tenant minting eternal
     signatures defeats the TTL).
  5. Issue an embed session JWT: 15-minute TTL, claims `company_id`,
     `embed_user_ref`, `token_type=embed`, `key_id`. Distinct token type so an
     embed token can never satisfy `middleware.Auth` on a dashboard route.
- `POST /api/embed/session/refresh` — same identity material, new session JWT.
  No refresh cookie; the host page re-signs. Keeps the widget stateless.
- `middleware.EmbedAuth()` — validates the embed token, rejects
  `token_type != embed`, sets `company_id` and `embed_user_ref` on the context.
  **Sets no user id and no role**, so an embed session cannot reach any
  `AdminOnly` route even by accident.
- Per-`(company_id, embed_user_ref)` rate limit, separate Redis bucket from the
  user and API-key limiters.
- FE (dashboard): Settings → Embed tab. Create key, copy secret **once**, manage
  origin allowlist, revoke. Admin-only. Show the exact backend snippet for
  signing in Go, Node, Python, and PHP — the integrator's first five minutes
  decide whether they finish.

**Notes for the implementer:**
- Reuse `internal/auth` hashing and the key-management shape from T-13. Do not
  build a second key system — but keep the tables separate: an API key is
  server-side and broadly scoped, an embed key is browser-visible and narrowly
  scoped. Merging them would leak scope.
- `client_key` is public by design. It identifies, it does not authorize. All
  authorization comes from the origin check plus the HMAC.

**Acceptance:**
- [ ] Valid signature + allowed origin → session JWT
- [ ] Tampered `user_ref` → 401
- [ ] Correct signature from a non-allowlisted origin → 403
- [ ] Expired `exp` → 401; `exp` more than 24h out → 401
- [ ] Revoked key → 401 immediately
- [ ] Wildcard or empty `allowed_origins` cannot be saved
- [ ] Embed token rejected on `/api/threads` and on every `AdminOnly` route
- [ ] Dashboard access token rejected on `/api/embed/chat`
- [ ] `hmac.Equal` used, not `==` (grep the diff)

**Gate:** table-driven test over the full matrix {valid, tampered sig, bad
origin, expired, far-future exp, revoked} × {session, refresh}. Paste output.
Plus a `curl` transcript of a successful session mint and a forged one.

---

## T-20 · Widget channel + scoped embed API
**Repo:** BE · **Size:** 2d · **Deps:** T-19, T-05, T-03 · **Priority:** P0 (of this phase)
**Migration:** `029_thread_embed`

Wire the embed session into the existing chat pipeline. Follow
[`../agents/playbooks/add-channel.md`](../agents/playbooks/add-channel.md) — the
widget is a channel, and skipping a step there is how a channel ends up answering
into the void.

**Do:**
- `domain.ChannelWidget Channel = "widget"`. Then **grep every switch on
  `Channel`** and handle it: `ChatRunner.completeWith` (no outbound provider —
  delivery is the WebSocket, so this case is a deliberate no-op **with a comment
  saying so**), the usage-by-channel SQL, and the dashboard channel labels.
- Migration: `conversation_threads.embed_user_ref text`, unique index on
  `(company_id, embed_user_ref, id)`, and add `embed_user_ref` to the
  `UsageByUser` rollup as a fourth `user_key_kind` (the query already coalesces
  `user_id / phone_number / discord_user_id / lark_open_id`).
- `ThreadService.ResolveForEmbedUser(ctx, companyID, embedUserRef, msg)` — keyed
  on `(company_id, embed_user_ref)` with the **existing idle-gap + classifier
  fork logic**, matching Discord. The widget has no native threads, so the
  heuristic is the right call. Do not write a new resolver; extend the pattern.
- Route group `/api/embed` behind `middleware.EmbedAuth()`, deliberately minimal:
  | Method | Path | Purpose |
  | ------ | ---- | ------- |
  | GET  | `/api/embed/config` | Theme, greeting, suggested prompts, enabled flags |
  | POST | `/api/embed/chat` | Send a turn |
  | GET  | `/api/embed/threads/current` | Resolve or create this user's thread |
  | GET  | `/api/embed/threads/:id/messages` | History, scoped to this `embed_user_ref` |
  | GET  | `/api/embed/threads/:id/stream` | WebSocket |
  **Nothing else.** No connections, no settings, no usage, no metrics, no audit.
- Thread ownership check on every read: the thread's `embed_user_ref` must equal
  the token's. A widget user must not be able to read a colleague's thread by id.
- Budget check from T-03 applies. On `BudgetExhausted` the widget shows a plain
  message, not a 402 stack trace.
- Audit (T-05): `actor_kind=embed`, `actor_ref=embed_user_ref`, `channel=widget`.
- Config: `EMBED_ENABLED` kill switch, `EMBED_SESSION_TTL_MINUTES` (default 15),
  `EMBED_MAX_TURNS_PER_HOUR` (default 60).

**Acceptance:**
- [ ] Widget turn produces an answer streamed over the embed WebSocket
- [ ] Thread continuity: same `embed_user_ref` returns to the same thread
- [ ] Two different `embed_user_ref`s get two threads; neither can read the other's
- [ ] `/api/usage/by-channel` shows `widget`; `/api/usage/by-user` shows the refs
- [ ] Audit rows carry `actor_kind=embed`
- [ ] Kill switch off → 503 on every `/api/embed/*` route
- [ ] Zero-credit company gets a readable refusal in the widget

**Gate:** full round trip with `curl` + a WS client: mint session → send turn →
receive streamed events → confirm the thread row, the audit rows, and the
`by-channel` usage entry. Paste all four.

---

## T-21 · Widget client
**Repo:** WID (new workspace member `apps/widget/`) + PKG · **Size:** 3.5d · **Deps:** T-20 · **Priority:** P0 (of this phase)

**Do:**
- New workspace member `apps/widget/`, added to `pnpm-workspace.yaml`. No new git
  repo — it is published from the monorepo (see `T-22`).
- **Extract, do not port.** Move the reusable chat pieces out of
  `apps/dashboard/src/features/chat/` into `packages/chat-ui/`: the tool-call card,
  the markdown renderer wrapper, the streaming message list, and the shared event
  types. Dashboard and widget then both consume it. A copied component drifts
  within a month; this is the main reason the monorepo landed before the widget.
  - Watch the React↔Preact boundary: `packages/chat-ui` must compile for both.
    Keep it presentational, no hooks beyond `useState`/`useEffect`, and use
    `preact/compat` on the widget side.
- **Two build outputs from one source:**
  1. `argentum-widget.js` — IIFE loader, no framework, exposes `window.Argentum`.
     This is the script-tag path and it must work on a plain HTML page.
  2. `dist/app/` — the widget app that runs **inside** the iframe.
- Stack: **Preact + `marked` + `dompurify`**. Not React, not `react-markdown` —
  the loader has a hard budget of **≤15 KB gzipped** and the iframe app ≤80 KB
  gzipped. A widget that slows the host page gets removed by the customer's
  own frontend team.
- Loader API:
  ```js
  Argentum.init({
    clientKey: 'argw_pub_…',
    user: { ref: 'emp_812', name: 'Rina', exp: 1780000000, sig: '…' },
    apiBase: 'https://api.argentum.…',   // optional, for self-hosted
    launcher: 'bubble' | 'none',           // 'none' = you render your own trigger
    position: 'bottom-right' | 'bottom-left',
    theme: { primary: '#e11d48', radius: 12, mode: 'light' | 'dark' | 'auto' },
    locale: 'en' | 'id',
  })
  Argentum.open() / .close() / .toggle() / .destroy()
  Argentum.identify(user)   // re-sign on token expiry
  Argentum.on('ready' | 'open' | 'close' | 'message' | 'error', cb)
  ```
- `postMessage` bridge, both directions, with **strict origin checks on every
  message on both sides**. Messages: `ready`, `auth`, `resize`, `open`, `close`,
  `event`. Ignore anything from an unexpected origin — an unchecked
  `postMessage` handler is a cross-origin hole.
- Iframe app: message list, streaming deltas, tool-call cards (from
  `packages/chat-ui`, extracted above — not copied), sanitized markdown,
  composer, thread history on open, reconnect with backoff, and a visible
  degraded state when the socket drops.
- **Token refresh:** on 401, emit `token_expired` to the host page and call the
  host's `identify` handler. Never retry blindly — the host must re-sign.
- Accessibility: focus trap when open, `Esc` closes, ARIA live region for
  incoming messages, keyboard-reachable launcher.
- Responsive: full-screen sheet under 640px, panel above it.

**Acceptance:**
- [ ] Loads and works on a plain HTML page with a script tag
- [ ] Loader ≤15 KB gzipped, iframe app ≤80 KB gzipped — state actual numbers
- [ ] Streaming renders incrementally, tool calls visible
- [ ] Host page CSS cannot affect the widget, and vice versa
- [ ] `postMessage` from a wrong origin is ignored
- [ ] Session expiry triggers re-identify, not a dead widget
- [ ] Socket drop shows a degraded state and reconnects
- [ ] Works in Chrome, Safari, and Firefox
- [ ] `packages/chat-ui` builds for **both** consumers: `pnpm --filter dashboard build && pnpm --filter widget build`
- [ ] Dashboard still renders chat identically after the extraction — no visual regression

**Gate:** demo page in `apps/widget/examples/vanilla/` with a tiny signing
server. Recording of: open → ask a question → streamed answer with a tool card →
close. Plus the actual gzipped bundle sizes from the build output.

---

## T-22 · Distribution and integration docs
**Repo:** WID · **Size:** 2d · **Deps:** T-21 · **Priority:** P1 (of this phase)

The ticket that decides whether anyone actually integrates it.

**Do:**
- npm package `@argentum/widget`: ESM + CJS + types, exporting the loader API.
- npm package `@argentum/widget-react`: a thin `<ArgentumWidget {...props} />`
  wrapper — `useEffect` init, `destroy` on unmount, props mapped to `init()`
  options, `identify` on user change. **It must be a wrapper, not a reimplementation.**
- CDN build published to a versioned, immutable path
  (`/widget/v1/argentum-widget.js`) plus a `v1` alias that tracks patches. Never
  mutate a released version file.
- Example apps in `apps/widget/examples/`: `vanilla/`, `react/`, `vue/`,
  `nextjs/` — each under 50 lines, each with its own minimal signing endpoint.
  Exclude them from the pnpm workspace so their deps don't pollute the root
  lockfile; they must install standalone, exactly as a customer would.
- Publishing from a monorepo: use **changesets** for versioning the two packages.
  `apps/widget` itself is private; only `packages/widget-*` publish. Move the
  loader source under `packages/` if that keeps the publish boundary cleaner —
  decide during `T-21` and note it.
- Signing snippets in Go, Node, Python, and PHP, each showing the **whole**
  server-side flow. This is the piece integrators copy; if it is wrong or partial,
  they will pick the insecure shortcut.
- `apps/backend/docs/embed/`: quickstart, the security model, the origin
  allowlist, token lifetime, the full option reference, and a troubleshooting
  table (403 → origin mismatch, 401 → clock skew or stale `exp`, blank iframe →
  CSP `frame-src`).
- **Document the host-side CSP requirement explicitly**: the customer needs
  `frame-src` and `connect-src` entries. This is the single most common embed
  support ticket in every product that ships a widget.
- SemVer, `CHANGELOG.md`, and a stated compatibility policy against the
  `/api/embed` version.

**Acceptance:**
- [ ] `npm i @argentum/widget` then 10 lines works from scratch
- [ ] React wrapper mounts, unmounts cleanly, and re-identifies on user change
- [ ] All four example apps run
- [ ] All four signing snippets verified against the real endpoint
- [ ] Versioned CDN URL is immutable

**Gate:** integrate into a throwaway Vite React app using only the published docs,
following them literally. Time it. Anything over 10 minutes means the docs are the
bug — fix the docs, not the timing.

---

## T-23 · Widget configuration in the dashboard
**Repo:** FE, BE · **Size:** 1.5d · **Deps:** T-19, T-20 · **Priority:** P1 (of this phase)

**Do:**
- Settings → Embed tab (extending the T-19 key management):
  - Appearance: primary colour, radius, light/dark/auto, launcher position
  - Content: greeting text, 3–5 suggested prompts, locale default
  - A live preview pane running the real widget against the tenant's own data
  - Copy-paste install snippet, pre-filled with their `client_key`
- Persist to `companies.widget_config jsonb` (no migration needed if a settings
  jsonb column already exists — check `005_company_currency` and the settings
  handler first; add to the `028` migration if not).
- `GET /api/embed/config` serves it to the widget.
- Usage page: widget appears in the channels tab; `by-user` shows embed refs
  labelled as such rather than as raw ids.

**Acceptance:**
- [ ] Theme changes appear in the live preview
- [ ] Config reaches a deployed widget without a redeploy
- [ ] Install snippet is correct and copyable
- [ ] Suggested prompts render in the widget's empty state
- [ ] `pnpm build` clean

**Gate:** screenshots — config change in the dashboard, then the same change
visible in the example app's widget without touching the example's code.

---

## Dependency graph

```
T-00 ──► T-00b ─┬─► T-01 ───────────────────────► T-07 ──► T-08 ──► T-09
 (re-warm)  │   │                            ▲       ▲        │
   (monorepo)   ├─► T-02 ─┬─► T-02b          │       │        └──► T-15 (cut #1)
                │         ├─► T-03 ──────────┼───────┼────┐
                │         ├─► T-04 ──► T-13 ─┼─► T-14 │    │
                │         │                  │        │    │
                │         │             └────┴─► T-19 ─┬─► T-20 ──► T-21 ──► T-22
                │         │                            │    ▲         │
                │         │                            └────┴─► T-23  │
                │         ├─► T-05 ──► T-10 ─┬─► T-11       │         │
                │         │            │     ├─► T-12a ─────┘         │
                │         │            │     └─► T-12b (cut #4)       │
                │         │            └──────────► T-20 (audit)      │
                │         ├─► T-06 ──► T-07                           │
                │         └─► T-07b                                   │
                └─► T-17 (independent) ──► T-16 (cut #5)              │
T-18 depends on everything through week 6 ────────────────────────────┘
```

`T-00b` gates everything — it moves every file, so no other ticket may start
until it lands. `T-19` needs `T-13` for the key-management primitives and `T-04`
for admin gating. `T-20` needs `T-05` (audit) and `T-03` (budget check). Nothing in
weeks 7–8 blocks anything in weeks 1–6, so the widget phase can slip without
damaging the rest.

## Effort roll-up

| Week | Tickets                                        | Days  |
| ---- | ---------------------------------------------- | ----- |
| 1    | T-00, T-00b, T-01, T-02, T-02b, T-03, T-04, T-05 | 13.0 |
| 2    | T-06, T-07, T-07b                              | 5.0   |
| 3    | T-08, T-09                                     | 5.0   |
| 4    | T-10, T-11, T-12a, T-12b                       | 6.5   |
| 5    | T-13, T-14, T-15                               | 6.0   |
| 6    | T-16, T-17, T-18                               | 5.0   |
| 7–8  | T-19, T-20, T-21, T-22, T-23                   | 11.5  |
|      | **Total**                                      | **52.0** |

52 estimated days against 40 working days in eight weeks. **The overage is
deliberate** and is what the cut order in
[`00-sprint-overview.md`](00-sprint-overview.md) §6 exists for. Cutting T-15,
T-14, T-12b, and T-16 brings it to 42.5 — still 2.5 over, so expect week 1 to
spill into week 2. That is acceptable: week 1 is foundation work and everything
downstream compounds off it.

**Week 1 is now 13 days of work in a 5-day week.** It will take closer to two and
a half weeks, and the plan should be read that way rather than pretending
otherwise. `T-00b` and `T-02b` are the additions — both pay for themselves inside
the sprint, because every subsequent ticket that touches two apps becomes one
commit instead of two, and every API-contract change gets checked by CI instead of
by a user.

Note the trade this represents: **the widget (T-19→T-23) and the MCP server
(T-14) are the same strategic bet — make Argentum reachable from outside its own
dashboard — but the widget serves humans on the tenant's own site and MCP serves
other agents.** If the schedule forces a choice, keep the widget: it is a surface
a customer can see and adopt without writing an integration.
