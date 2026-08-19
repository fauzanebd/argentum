# Test Coverage — Measured State

Measured 2026-07-26 by running `go test ./...` and `go vet ./...` in the backend
tree (`apps/backend/` post-monorepo; the standalone `argentum` repo at the time of
measurement). These are actual results, not estimates.

## Headline

> **Current reading, 2026-08-19: 58 of 86 Go packages have tests.** The PDF
> track added ten packages and six of them arrived with their own tests —
> `internal/numparse`, `internal/doctable`, `internal/docwarehouse`,
> `internal/evaldocs` and the two halves already there. The four without are the
> ones whose behaviour is a network call or a context value:
> `internal/dococr`, `internal/doctaint`, `internal/docchunk` (exercised through
> `internal/app`) and `cmd/evaldocs`.
>
> **The `docchunk` parenthesis above was doing real work, and Bucket B collected
> on it (2026-08-19).** "Exercised through `internal/app`" is true of the service
> that *calls* the chunker and false of the chunker: `docchunk.headingLine`
> matches `^#{1,6}\s+…` while its own comment claims it also matches an unmarked
> heading, and the parser sidecar never emits a `#` at all — so heading-boundary
> chunking has never fired on any document this product can read, every
> `heading_path` in the database is empty, and the cut points are purely the token
> budget. A package with no test file of its own is where a regex that matches
> nothing survives review, and the service-level tests could not see it because
> they feed the chunker markdown the real parser never produces
> ([`live-gate-backlog.md`](live-gate-backlog.md) §1k, finding 6). The track also added a *second* kind of
> test this file has not counted before — `make eval-docs` scores twelve
> documents end to end through the real parser, and it found four defects that
> every unit test beside it passed ([`pdf-knowledge.md`](pdf-knowledge.md) §3).
>
> Previous reading, 2026-08-17: **52 of 76 Go packages have tests** (`go list -f
> '{{if or .TestGoFiles .XTestGoFiles}}…'`). Was 46 of 73 on 2026-08-09. The
> native dashboards build is the whole of the move and it went the right way:
> `internal/dashboard`, `internal/dashboard/spec` and `internal/sqlguard` all
> arrived **with** tests — `params_test.go`, `resolve_test.go`,
> `project_test.go`, `validate_test.go`, `window_test.go`, `template_test.go`,
> `statement_test.go` — and `internal/app/dashboard_service_test.go` grew with
> the service. That is a phase where the numerator moved faster than the
> denominator, which had not happened before.
>
> Previous reading, 2026-08-09: 46 of 73. Was 43 of 69 on 2026-08-08; the
> video track added `internal/report/videoplan`, `internal/report/video`,
> `internal/report/canvas` and `internal/report/flow`, and three of the four
> arrived with their own tests. The denominator moved as much as the
> numerator — `internal/slack`, `internal/mcpserver`, `internal/webhookout`,
> `internal/tracing` and the rest are packages that did not exist when this file
> was written. The dashboard and landing apps still have zero tests; `tsc -b` and
> the lint job are what stand in for them, and the backlog's *Frontend test
> framework* entry still has its trigger.
>
> Everything below is the original measurement, kept because the risk ranking it
> produced is what `T-02` was scoped against.

**3 of 35 Go packages have any test file. The dashboard and landing apps have
zero tests.**

`go vet ./...` is clean.

> **Updated after `T-00b`:** CI previously ran no tests at all. The monorepo
> migration added `go vet`, `go test -race -count=1`, and a `cmd/discord` build to
> the pipeline, and replaced the trigger-level `paths:` filter with per-job
> filtering. `golangci-lint` and the deliberate-break proof remain `T-02`'s job.

> **Updated after `T-01`:** two more packages have tests —
> `internal/report/format` and `internal/eval` — taking it to 5 of 37. The risk
> ranking below is unchanged; `T-02` still owns every CRITICAL package.
>
> `T-01` also added a kind of coverage this document did not previously
> account for. `go test` proves the code does what it was written to do.
> `make eval` proves the **agent answers correctly**, which no unit test can
> reach: the failure it exists to catch — a confident, well-formatted, invented
> number — is a perfectly healthy code path. Baseline in
> [`eval-baseline.md`](eval-baseline.md).

> **Updated after `T-02c` and `T-16` (2026-07-27):** **10 of 41 packages have
> tests** (`go list ./...`, denominator includes the four `cmd/` packages and
> `scripts/encrypt_secret`, which earlier counts here excluded — the ratio moved
> less than it looks). `T-02c` added `internal/app` and `internal/llmusage`;
> `T-16` added `internal/agentbudget`, `internal/guardrails` and
> `internal/tools`.
>
> Two of those are CRITICAL packages, so the risk table below overstates the
> gap for `internal/app` and `internal/guardrails` — but only just. `T-02c`
> tests `MeteredLLM` streaming usage and nothing else in a 2,900-line package;
> `T-16` tests the fabrication rule and nothing else in the guardrail engine.
> Both remain `T-02`'s to finish.

> **Updated after `T-R2` (2026-07-27):** **12 of 44 packages have tests.**
> `internal/report/pdf` and `internal/report/spec` are new (the first arrives
> covered, the second is exercised through the renderer's fixtures);
> `internal/report/format` and `internal/tools/document` grew a second test file
> each. The count also picks up `internal/report/theme`, which `T-R1` added and
> did not record here — measured with `go list ./...`, not counted by hand.
>
> The new coverage is a different shape from the rest of this table and worth
> naming, because `T-R3` and `T-R4` will need the same trick. A rendered PDF
> encodes its text as subset glyph ids, so nothing downstream — not grep, not
> pdfcpu — can read a heading back out of one. The layout assertions instead
> walk maroto's component tree through an unexported `build()` that lays the
> document out without generating it, which is how "the running header is on
> every page except the cover" and "the table header repeats and is never
> orphaned" are tested at all rather than by eye.
>
> What that still cannot see is whether a page is *ugly*. `pdftoppm` on the
> written fixtures is the manual half of the gate and has no substitute.

> **Updated after `T-02` (2026-07-28):** **21 of 49 packages have tests**, and
> every package this document ranked CRITICAL now has them. The three
> HIGH/MEDIUM packages named in the ticket — `internal/auth`, `internal/config`,
> and the middleware that enforces token type — are covered too. `go test -race`
> is green and `golangci-lint` reports **0 issues** against a five-linter config
> that the tree previously failed in 50 places.
>
> The gate is now real in both directions: CI runs `go vet`, `golangci-lint`
> (including a gofmt check), `go test -race -count=1`, and builds all three
> binaries; the dashboard runs `tsc -b --noEmit && eslint .` with eslint
> actually installed for the first time.
>
> Three things this ticket found that no amount of reading would have:
>
> - **Scheduled tasks with a non-UTC timezone cannot work in production.**
>   `normalizeTimezone` and `nextFire` both call `time.LoadLocation`, which
>   reads `/usr/share/zoneinfo` — a directory `alpine:latest` does not have, and
>   the API, worker and Discord images are all bare alpine plus
>   `ca-certificates`. Nothing imported `time/tzdata`, so every non-UTC zone
>   works on a developer machine and fails in the deployed image. `internal/app`
>   now blank-imports `time/tzdata`, and `TestTimezoneDatabaseIsAvailableWithoutTheHostFilesystem`
>   points `ZONEINFO` at nothing so the test would fail if the import were
>   removed.
> - **Two unchecked type assertions in the chat handler.** `uid.(string)` on a
>   value only `middleware.Auth` sets: a route wired without it panics rather
>   than 401s. Both now go through a `userID(c)` helper alongside the existing
>   `companyID(c)`. Found by turning on errcheck's `check-type-assertions`.
> - **`redact_nik` can never fire.** A NIK is sixteen consecutive digits and so
>   is a credit-card number without separators, and `redact_credit_cards` is
>   declared first. Pinned in a named test rather than covered with a case that
>   would have quietly asserted the wrong rule. Two smaller redaction edges are
>   pinned the same way. All three belong to `T-07b`.
>
> Details, including the guardrail golden set's shape, are in the sections
> below.

> **Updated after `T-04` (2026-07-28):** **22 of 49 packages have tests.** The
> new one is `cmd/api` — the first `cmd/` package with any, and the reason is
> worth recording because it is a pattern the `/v1` tickets will want.
>
> `T-04`'s gate is "every gated route × {admin, member}". Gin's `RouteInfo`
> exposes a route's final handler and nothing about the middleware chain in
> front of it, so *no* test can read per-route gating back out of a built
> router. Putting the access decision in a table (`cmd/api/policy.go`) rather
> than in scattered `AdminOnly()` calls makes it readable — and then
> `TestEveryAuthedRouteIsClassified` can diff the table against `r.Routes()` in
> both directions: a route with no entry fails, and an entry with no route fails
> too.
>
> The tests drive the **real** `newRouter` with every service present but
> unwired. Nothing touches a database; a member's 403 is asserted directly, and
> an admin's success is asserted as "not 403", because reaching a handler backed
> by a nil service is the signal. Services must be non-nil at construction or
> the optional handler groups never register and the sweep would silently stop
> covering them — which is itself a trap this test would otherwise fall into.
>
> Two things it found:
>
> - **`NewRateLimiter` returned a limiter that panics when Redis is absent.**
>   `newRouter` already read `if rateLimiter != nil`, so the intent was there;
>   the constructor never returned nil. Production always passes a live client,
>   so it was latent rather than live — but it made the router untestable, which
>   is how it surfaced. It now returns nil for a nil client.
> - **`RequireRole` checked the role only on admin routes.** Wired without
>   `Auth` in front of it, a member-classified route would have admitted a
>   request with no identity at all. It now refuses any role it does not
>   recognise, on every route. `TestRequireRoleDeniesWhenAuthDidNotRun` covers
>   the misordered chain.
>
> Also new: `internal/app` gained `team_service_test.go` and
> `auth_service_test.go`. The in-memory user repo there deliberately reproduces
> two guards the SQL relies on — the global uniqueness of `users.email`, and
> `Activate` only firing on a still-pending row — because those are what make an
> invite single-use, and a fake that ignored them would let the suite pass on a
> service that has neither. Full record: [`rbac.md`](rbac.md).

## Go backend — package by package

### Packages with tests (24)

`T-02` added five packages and grew three that already had tests. Four of the
other new entries since this table last said twelve — `internal/report/chart`,
`layout`, `measure` and `pptx` — arrived with `T-R3` and `T-R4` and are
recorded in [`report-charts.md`](report-charts.md) and
[`report-deck.md`](report-deck.md). The last two, `internal/branding` and
`internal/report/brand`, arrived with `T-R5`
([`report-branding.md`](report-branding.md)), which also added branding tests to
`internal/report/{theme,pdf,pptx}`.

| Package | Test file(s) | What it covers |
| ------- | ------------ | -------------- |
| `internal/crypto` | `dsn_test.go` | AES-256-GCM round-trip over five DSN shapes, key-length and hex validation, a fresh nonce per seal, decryption under the wrong key, and ten malformed payloads that must error rather than panic — including the two lengths that would slice out of range without the guard (`T-02`) |
| `internal/tenantctx` | `tenant_test.go` | Every getter returns `""` unset; the three keys are distinct types so one setter cannot overwrite another; a derived context cannot write back into its parent, which is what keeps one queued task's tenant out of the next one's (`T-02`) |
| `internal/config` | `config_test.go` | All seven fallback accessors including the one that refuses to lend an Anthropic key to an OpenAI embeddings call, `WorkerQueueMap` over thirteen inputs (none of which may yield an empty map — that is a worker consuming nothing), `DatabaseURL` round-tripped through `net/url` with eight password shapes, `redisDialAddr` for URI/bare/IPv6/malformed, and the provider-scoped WhatsApp validation (`T-02`) |
| `internal/auth` | `password_test.go`, `jwt_test.go` | Argon2id round-trip and per-call salting, eleven malformed stored hashes, verification against parameters read back out of the stored string; JWT issue/verify, the secret-length floor, an expired token, a foreign signature, an `alg=none` forgery, and seven malformed tokens (`T-02`) |
| `internal/transport/http/middleware` | `auth_test.go` | A refresh token is rejected on an access route — through the header, the `?at=` query parameter the WebSocket upgrade needs, and the cookie; the fallback precedence between the three; `AdminOnly` per role, including that it denies when `Auth` never ran (`T-02`) |
| `internal/app` | `metering_llm_test.go`, `usage_pricing_test.go`, `thread_service_test.go`, `scheduled_cron_test.go` | `MeteredLLM` streaming usage (`T-02c`), plus: `RecordLLM` cost arithmetic with the 1.25×/0.10× cache multipliers and the unknown-model fallback that stops a new model string billing zero; the `continueOrFork` decision table in all eight of its states; `validateCron` / `normalizeTimezone` / `nextFire` including both DST transitions (`T-02`) |
| `internal/guardrails` | `fabrication_test.go`, `golden_test.go` | The `T-16` fabrication rule, plus a golden suite over the **shipped** `config/guardrails.yaml`: every rule with must-block and must-pass cases, a coverage test that fails when a rule is added without them, the documented false positives ("create a dashboard", "update me on sales", "integer target", benign follow-ups), scope separation, and the opposite failure directions of the two LLM patterns (`T-02`) |
| `internal/tools` | `run_sql_test.go`, `run_sql_bytecap_test.go`, `source_resolve_test.go` | The `run_sql` result notes (`T-16`), plus the byte-cap trimming loop — wide rows shrink from the tail, the count matches what was sent, a single oversized row does not become a false "matched zero rows" — and `ResolveSource` with 0/1/many sources, an explicit id, a cross-tenant id, and the empty-company rejection that happens before the repository is touched (`T-02`) |
| `internal/branding` | `service_test.go` | The contrast floor with its measured ratio in the message, the locale and length rules, normalisation before storage, logo re-encoding (JPEG→PNG, oversize scaled on both axes, SVG/HTML/truncated/empty refused, >512 KB refused), and that resolution is never fatal — broken bucket, broken row, broken company lookup and a nil service each fall back to Argentum's defaults (`T-R5`) |
| `internal/report/brand` | `brand_test.go` | Per-field fallback (a logo without a colour keeps the brand red), legal name over company name, an unparseable stored colour falling back rather than failing, the `ShowCredit`→`HideCredit` inversion surviving both projections, and the PDF and PPTX projections agreeing field for field while owning separate colour pointers (`T-R5`) |
| `internal/bootstrap` | `agent_factory_test.go` | The per-turn agent, built by the factory the worker uses and run against the **shipped** `config/guardrails.yaml`: a report turn's directive lands in the system prompt, every other turn's prompt is byte-identical to before, the guardrail classifiers are asked to judge the caller's question and nothing else, an injection in that question is still refused, and the directive itself is still the shape an input guardrail blocks — so the ticket cannot be closed by weakening the classifier (`T-A2b`) |

The twelve that came before:

| Package                        | Test file(s)                                       | What it covers                       |
| ------------------------------ | -------------------------------------------------- | ------------------------------------ |

| Package                        | Test file(s)                                       | What it covers                       |
| ------------------------------ | -------------------------------------------------- | ------------------------------------ |
| `internal/llmclient`           | `factory_test.go`                                  | LLM client construction per interface |
| `internal/metabase`            | `postgres_dsn_test.go`, `sqlserver_dsn_test.go`     | DSN string building for Metabase sync |
| `internal/tools/document`      | `render_test.go`, `convert_test.go`                 | PDF / XLSX / CSV rendering, and the two conversions between the tool's v1 types and the renderer spec — including that XLSX and CSV cells stay raw, because nobody sums a column of "Rp 1.234.567,00" (`T-R2`) |
| `internal/report/format`       | `parse_test.go`, `format_test.go`                   | Number parsing in both locales, magnitude suffixes (Juta/Miliar/Triliun, K/M/B), and the comparator that must reject the C-1 fabrication at every tolerance. `T-R2` added the writing direction — currency conventions, percent precision, compaction, dates, column type inference — and a round-trip over every locale × currency × compact combination, which is the assertion that keeps the eval comparator and the renderer agreeing about what a number is |
| `internal/report/pdf`          | `render_test.go`                                    | Four fixtures plus a v1 spec: `pdfcpu` validation, byte-identical reruns, embedded faces with no Helvetica, a clean cover with the header running from page 2, a 200-row table whose header repeats without orphans, locale-formatted cells, and content-weighted column widths (`T-R2`) |
| `internal/eval`                | `score_test.go`, `case_test.go`                     | Scoring per assertion kind, the Indonesian/English heuristic, and a validity + category-coverage check over the shipped golden set |
| `internal/app`                 | `metering_llm_test.go`                              | `MeteredLLM` streaming usage: metadata path, HTTP-tap fallback, and the unbilled-turn warning (`T-02c`) |
| `internal/llmusage`            | `transport_test.go`                                 | SSE usage parsing and cache-token normalisation (`T-02c`) |
| `internal/agentbudget`         | `budget_test.go`                                    | Budget dimensions, the refusal payload, sticky exhaustion, and what counts as evidence of retrieved data (`T-16`) |
| `internal/guardrails`          | `fabrication_test.go`                               | What counts as a stated figure — both observed fabrications must trip it, refusals and years must not — and the replacement message's cause and language (`T-16`) |
| `internal/tools`               | `run_sql_test.go`                                   | The zero-row and truncation notes on a `run_sql` payload (`T-16`) |
| `internal/report/theme`        | `theme_test.go`                                     | The generated tokens match `tokens.json`, so a hand edit to `tokens_gen.go` fails `go test` before it reaches the `tokens` CI job (`T-R1`) |

All twenty-one pass, under `-race`.

### Packages with no tests (28)

Ranked by risk — how much damage a silent regression here would do. **Every
CRITICAL row is now closed**; what is left is `T-02`'s Sprint 2 successor.

| Risk     | Package                                | Why it matters                                                                                        |
| -------- | -------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| ~~**CRITICAL**~~ ✅ | `internal/app`              | Closed by `T-02` for the three named services (pricing, fork heuristics, cron). `ChatRunner` was uncovered by unit tests until `T-A2b`, which needed to assert *how a turn is assembled* — a question the eval harness cannot answer, because it observes the answer and not the input. `ChatRunner.Run` now runs end-to-end in `chat_runner_directive_test.go` against a stub model, via an `LLMResolver` interface declared at the consumer. What the agent does with a tool result is still the eval harness's job. |
| ~~**CRITICAL**~~ ✅ | `internal/guardrails`       | Closed by `T-02`'s golden suite over the shipped YAML. The output rules still never execute in production (`T-07b` owns that), and the suite says so rather than implying otherwise. |
| ~~**CRITICAL**~~ ✅ | `internal/crypto`           | Closed by `T-02`.                                                                                      |
| ~~**CRITICAL**~~ ✅ | `internal/tenantctx`        | Closed by `T-02`.                                                                                      |
| ~~**HIGH**~~ ✅ | `internal/auth`                 | Closed by `T-02`.                                                                                      |
| **HIGH** | `internal/adapters/db` (+3 drivers)    | Read-only transaction enforcement, statement timeouts, row capping. The last line of defence on tenant data. **Needs a live database**, which is why it did not fit a unit-test ticket: the assertions worth having are "a mutation inside the read-only tx is rejected by the server", and a fake proves nothing about that. Sprint 2, with a container. |
| **HIGH** | `internal/adapters/postgres`           | 19 repository files. Every one is a place a missing `company_id` predicate could leak data. Same constraint: the bug this would catch is in the SQL, so the test needs a real server. |
| ~~**HIGH**~~ ◐ | `internal/tools`                | The byte-cap loop, source resolution and the result notes are covered by `T-02`. `get_schema` filtering and the three Metabase-backed tools are not. |
| ~~**MEDIUM**~~ ✅ | `internal/config`             | Closed by `T-02`.                                                                                      |
| **MEDIUM** | `internal/queue`                     | Task contracts, asynq option building, periodic config provider.                                        |
| ~~**MEDIUM**~~ ◐ | `internal/transport/http/middleware` | Auth and `AdminOnly` are covered by `T-02`, including the token-type check and all three token sources. CORS and the rate limiter are not. |
| **MEDIUM** | `internal/llmtenant`                 | Per-tenant client caches with TTL eviction. Concurrency-sensitive.                                      |
| **MEDIUM** | `internal/embedding`                 | Batch fan-in, dimension handling.                                                                       |
| **LOW**  | `internal/whatsapp`, `internal/discord`, `internal/lark` | External-API adapters; need HTTP-level fakes to test meaningfully.                        |
| **LOW**  | `internal/metabase` (client)            | DSN builders are tested; the REST client is not.                                                       |
| **LOW**  | `internal/cache`, `internal/metrics`, `internal/migrate`, `internal/transport/eventbus`, `internal/transport/ws`, `internal/adapters/storage`, `internal/domain`, `pkg/models` | Thin or declarative.                        |
| **N/A**  | `cmd/api`, `cmd/worker`, `cmd/discord`, `scripts/encrypt_secret` | Wiring. Cover via integration tests, not unit tests.                     |

### What `T-02` deliberately did not test, and why

- **`ChatRunner`.** ~700 lines whose interesting behaviour is "what the agent
  does with a tool result". A unit test there asserts the wiring; `make eval`
  asserts the outcome. The eval harness is the coverage for this file and
  saying otherwise would overstate what a mock-heavy test would prove.
- **Anything needing a live Postgres.** The read-only transaction enforcement
  and the `company_id` predicates are the two highest-risk untested areas left,
  and both are only meaningfully testable against a real server. That is a
  container in CI, which is a ticket, not a step.
- **The dashboard's runtime behaviour.** No test framework is installed and the
  ticket did not add one. What it added is a linter that runs — the app now
  goes through `tsc -b --noEmit && eslint .` — which is a narrower claim than
  "the dashboard is tested" and is the true one.

### The guardrail golden suite

`internal/guardrails/golden_test.go` runs against `config/guardrails.yaml`
itself, not a fixture, because the file that ships is the file whose regexes
keep getting narrowed. Its shape:

- One `goldenRule` entry per rule, carrying must-block inputs, must-pass
  inputs, and the stub classifier verdicts its cases run under. The verdicts
  matter: the topic rule's cases run with the LLM saying **FALSE** so only the
  regexes can admit a message — with the classifier admitting everything, a
  broken regex looks fine.
- `TestEveryRuleHasGoldenCases` fails when a rule exists in the YAML with no
  cases, when a rule is covered in only one direction, and when a golden entry
  names a rule that no longer exists. That is what makes it a gate rather than
  a snapshot.
- The two injection rules deliberately share a refusal message, so a block is
  attributed by running the same input under both classifier verdicts rather
  than by matching text.
- Known gaps are pinned in named tests (`TestKnownTopicGateFalsePositives`,
  `TestKnownRedactionEdges`, `TestRedactNIKIsShadowedByTheCreditCardRule`)
  rather than smoothed over. Each fails when the underlying rule is fixed,
  which is the point: the fix closes a test instead of being invisible.

## Frontend

| App                   | Test files | Test runner | Type check | Lint         |
| --------------------- | ---------- | ----------- | ---------- | ------------ |
| `apps/dashboard`      | 0          | none installed | `tsc -b` via build | **`tsc -b --noEmit && eslint .`** |
| `apps/landing`        | 0          | none installed | `tsc -b` via build | `tsc -b --noEmit` |

> **Found during `T-00b`:** the dashboard's `lint` script called `eslint .`, but
> **eslint was never in its devDependencies** — `pnpm lint` failed with
> `command not found`, so it had never run, in CI or locally. It was pointed at
> the same `tsc -b --noEmit` typecheck landing uses as a stopgap.
>
> **Closed by `T-02` (2026-07-28).** eslint 9 is installed with a flat config
> (`apps/dashboard/eslint.config.js`): the JS and TypeScript recommended sets
> plus `react-hooks` and `react-refresh`, no stylistic rules. The typecheck is
> kept in front of it rather than replaced, because the two catch different
> things.
>
> First run: **36 problems, 30 errors.** Now **0 errors, 6 warnings**. The
> triage is worth recording because most of it was one bug wearing twenty hats:
> every catch clause in the app read `e?.response?.data?.error || e.message`
> with `e` typed `any`, and a thrown non-Error has no `message`, so those paths
> rendered the literal string `undefined` in a toast. They now share
> `src/lib/api-error.ts` — `apiErrorMessage` narrows instead of asserting and
> always returns something readable, and `apiErrorStatus` exists so the one
> place that branches on 404 can tell a real 404 from a dropped connection.
>
> The six remaining warnings are two `react-hooks/exhaustive-deps` findings in
> the chat and onboarding pages and four `react-refresh` fast-refresh notes in
> shadcn components. They are warnings, so the gate passes; they are real, so
> they are not suppressed.
>
> `apps/landing` stays on the typecheck. It has no state, no hooks and no data
> fetching, so a linter would be checking a static page's import order.

Both now run in CI via the `web` job.

## CI gate — what is actually checked

`.github/workflows/ci.yaml`, after `T-00b` and `T-02`:

| Job | Fires on | Runs |
| --- | -------- | ---- |
| `backend` | `apps/backend/**`, or any tag push | `go vet ./...`, **`golangci-lint run`**, `go test -race -count=1 ./...`, build api + worker + discord |
| `tokens` | `packages/design-tokens/**`, either generated output, `Makefile` | `make tokens`, `make palette`, then `git diff --exit-code` on the generated files |
| `deck` | `apps/backend/internal/report/**` | Converts every fixture deck through headless LibreOffice |
| `web` | `apps/{dashboard,landing,widget}/**`, `packages/**` | `pnpm -r build`, `pnpm -r lint` (dashboard: `tsc` + eslint) |
| `docker` | tags `v*.*.*` | Build + push three GHCR images from the `apps/backend` context |

Now checked, and previously not:

- ✅ `go test -race -count=1 ./...` (`T-00b`)
- ✅ `go vet ./...` (`T-00b`)
- ✅ `golangci-lint run` — errcheck, govet, staticcheck, ineffassign, unused,
  plus a gofmt check (`T-02`)
- ✅ `go build ./cmd/discord` (`T-00b`)
- ✅ frontend `pnpm build` and a `pnpm lint` that runs eslint (`T-00b`, `T-02`)

Still missing:

- ❌ migration up/down round-trip check
- ❌ any answer-quality evaluation in CI — `make eval` costs real tokens and
  needs a live tenant, so it stays a local/manual gate for now
- ❌ a live-database job for `internal/adapters/*`

### The linter config

`apps/backend/.golangci.yml` enables five linters and nothing else. The tree
failed it in **50 places** on the first run; it now reports **0 issues**. What
that triage produced:

| Finding | Count | Resolution |
| ------- | ----- | ---------- |
| `defer x.Close()` and deadline setters | 38 → 0 | Most are excluded by name — teardown whose error the caller cannot act on, and none of them buffered writers. Eight that the exclusion list could not match cleanly are now written `_ = x.Close()`, which says the same thing at the call site. |
| Unchecked type assertion | 2 | **Real.** `uid.(string)` in the chat handler, fixed with a `userID(c)` helper. Found only because the config turns `check-type-assertions` on. |
| Unchecked `json.Unmarshal` | 2 | **Real.** `create_dashboard` silently accepted a wrongly-shaped `name` and then told the model the parameter was missing — advice it could not act on. It now says what was wrong. |
| `fmt.Fprintf` over `WriteString(Sprintf(…))` | 3 | Applied. |
| `strings.TrimPrefix` over an if/slice | 2 | Applied. |
| A literal U+0008 backspace in a PPTX test fixture | 1 | **Worth having found.** An invisible control character sitting in a string the deck renderer places on a slide. |
| Deprecated `reflect.Ptr` | 1 | Applied. |
| Dead `const mmPerPoint` | 1 | Left behind when `T-R4` extracted `internal/report/measure`. Deleted. |
| Missing package comments | 8 | Written. `ST1000` stays on, so the next new package needs one. |
| `ST1005` (capitalised error strings) | 2 | Disabled with a reason: the first word is "Metabase". |
| `QF1008` (drop the embedded field from a selector) | 1 | Disabled with a reason: `g.Tool.Name()` is how the budget guard says it delegates. |

Widening the linter set happens when the tree has stayed clean for a while,
not now — a gate that produces findings nobody triages is a gate everyone
learns to ignore.

**Reading the diff:** seventeen Go files were not gofmt-clean before this
ticket, so `gofmt -w` touched twenty files in total. Every one of those is
whitespace only — `git diff -w` reports `0+/0-` for all of them — so a reviewer
can skip them and read the rest. The files with real changes are the twelve new
test files, `.golangci.yml`, and the nine sources named in the table above.

Also: `GO_VERSION: '1.25'` in CI vs. `go 1.26.1` in `go.mod`. This only works
because `GOTOOLCHAIN=auto` downloads 1.26 on every run — wasted minutes and a
silent dependency on default toolchain behaviour.

## The bigger gap: no evaluation harness

Unit tests measure whether code does what it was written to do. For this product
the more important question is **whether the agent gives correct answers** — and
nothing measures that.

Recent history makes the cost concrete. These commits all changed agent behaviour
with no way to detect a quality regression:

```
3891579  fix: stop semantic injection guardrail blocking benign follow-ups
74f5419  feat: bill Anthropic prompt-cache tokens + Anthropic-native defaults
94fe370  feat: per-tenant LLM credentials and embedding-based table picker
a56fd85  chore: default LLM to deepseek/deepseek-v3.2 via OpenRouter
e02dfd4  perf: cut Anthropic input tokens via prompt caching + schema filtering
f850a88  feat: cheaper-smarter LLM defaults, streaming metering, models endpoint
9ea0007  Update agent and guardrail configurations for improved topic enforcement
```

Note `a56fd85` (default to DeepSeek) followed by `74f5419` (Anthropic-native
defaults) — a model-default reversal. Whether that was an improvement is
currently unknowable.

### The API-type drift gate

`T-02b` added a third generated-artifact gate beside the design tokens and the
OpenAPI artifacts: `make types` regenerates `packages/api-types` from the Go
structs and the `types` job diffs the result. It is a *type* gate rather than a
test gate, but it catches the same class the suite cannot — a contract the
dashboard compiles against and the backend no longer serves — and it was proven
by breaking it ([`generated-types.md`](generated-types.md)).

## Targets

Set deliberately low and reachable. Coverage percentage is a poor goal; these are
about *which* code is protected.

| Milestone | Target                                                                                          | State |
| --------- | ----------------------------------------------------------------------------------------------- | ----- |
| Sprint 1  | Every CRITICAL package has tests. CI runs `test -race` + `vet` + `golangci-lint` + frontend build. `cmd/discord` builds in CI. | ✅ `T-00b` + `T-02` |
| Sprint 1  | Guardrails golden-case suite: every rule has ≥1 must-block and ≥1 must-pass case, including the false positives the comments describe. | ✅ `T-02`, with `redact_nik` covered by a test proving it is currently unreachable rather than by a case that would have asserted the wrong rule |
| Sprint 1  | Eval harness with ≥30 golden questions on the demo tenant, scored, runnable offline, one command. | ✅ `T-01` |
| Sprint 2  | HIGH packages covered. Migration up/down round-trip in CI.                                        | Open. Both need a database container in CI. |
| Sprint 2  | Eval set ≥100 questions, with a per-commit score recorded so regressions are visible.            | Open. |

## Gate output — `T-02`, 2026-07-28

`go vet ./... && golangci-lint run ./... && go test -race -count=1 ./...` — all
three exit 0. The last 41 lines of the test run:

```
?   	github.com/fauzanebd/argentum/internal/adapters/postgres	[no test files]
?   	github.com/fauzanebd/argentum/internal/adapters/storage	[no test files]
ok  	github.com/fauzanebd/argentum/internal/agentbudget	1.387s
ok  	github.com/fauzanebd/argentum/internal/app	1.651s
ok  	github.com/fauzanebd/argentum/internal/auth	22.253s
?   	github.com/fauzanebd/argentum/internal/bootstrap	[no test files]
?   	github.com/fauzanebd/argentum/internal/cache	[no test files]
ok  	github.com/fauzanebd/argentum/internal/config	1.306s
ok  	github.com/fauzanebd/argentum/internal/crypto	1.860s
?   	github.com/fauzanebd/argentum/internal/discord	[no test files]
?   	github.com/fauzanebd/argentum/internal/domain	[no test files]
?   	github.com/fauzanebd/argentum/internal/embedding	[no test files]
ok  	github.com/fauzanebd/argentum/internal/eval	2.485s
ok  	github.com/fauzanebd/argentum/internal/guardrails	6.113s
?   	github.com/fauzanebd/argentum/internal/lark	[no test files]
ok  	github.com/fauzanebd/argentum/internal/llmclient	3.263s
?   	github.com/fauzanebd/argentum/internal/llmtenant	[no test files]
ok  	github.com/fauzanebd/argentum/internal/llmusage	3.848s
ok  	github.com/fauzanebd/argentum/internal/metabase	2.963s
?   	github.com/fauzanebd/argentum/internal/metrics	[no test files]
?   	github.com/fauzanebd/argentum/internal/migrate	[no test files]
?   	github.com/fauzanebd/argentum/internal/queue	[no test files]
ok  	github.com/fauzanebd/argentum/internal/report/chart	108.611s
ok  	github.com/fauzanebd/argentum/internal/report/format	2.684s
?   	github.com/fauzanebd/argentum/internal/report/labels	[no test files]
ok  	github.com/fauzanebd/argentum/internal/report/layout	3.062s
ok  	github.com/fauzanebd/argentum/internal/report/measure	3.116s
ok  	github.com/fauzanebd/argentum/internal/report/pdf	65.573s
ok  	github.com/fauzanebd/argentum/internal/report/pptx	161.230s
?   	github.com/fauzanebd/argentum/internal/report/spec	[no test files]
ok  	github.com/fauzanebd/argentum/internal/report/theme	3.062s
ok  	github.com/fauzanebd/argentum/internal/tenantctx	3.057s
ok  	github.com/fauzanebd/argentum/internal/tools	3.163s
ok  	github.com/fauzanebd/argentum/internal/tools/document	3.557s
?   	github.com/fauzanebd/argentum/internal/transport/eventbus	[no test files]
?   	github.com/fauzanebd/argentum/internal/transport/http/handlers	[no test files]
ok  	github.com/fauzanebd/argentum/internal/transport/http/middleware	2.529s
?   	github.com/fauzanebd/argentum/internal/transport/ws	[no test files]
?   	github.com/fauzanebd/argentum/internal/whatsapp	[no test files]
?   	github.com/fauzanebd/argentum/pkg/models	[no test files]
?   	github.com/fauzanebd/argentum/scripts/encrypt_secret	[no test files]
```

21 `ok`, 28 `[no test files]`, 0 `FAIL`, 49 packages.

**After `T-04` (2026-07-28):** 22 `ok`, 27 `[no test files]`, 0 `FAIL`, 49
packages — `cmd/api` is the addition. `go test -race ./...` exit 0.

`golangci-lint run ./...`:

```
0 issues.
```

Still 0 issues after `T-04`.

**CI fails when a test fails — proved locally.** The round-trip assertion in
`internal/crypto/dsn_test.go` was inverted (`got != tc.plain` → `got ==`) and
the suite went red on five subtests:

```
--- FAIL: TestEncryptDecryptRoundTrip (0.00s)
    --- FAIL: TestEncryptDecryptRoundTrip/empty
    --- FAIL: TestEncryptDecryptRoundTrip/postgres_dsn
    --- FAIL: TestEncryptDecryptRoundTrip/sqlserver_dsn
    --- FAIL: TestEncryptDecryptRoundTrip/unicode
    --- FAIL: TestEncryptDecryptRoundTrip/long
        dsn_test.go:103: DELIBERATE BREAK: round-trip = "…", want "…"
FAIL	github.com/fauzanebd/argentum/internal/crypto	0.545s
```

After the revert: `ok  github.com/fauzanebd/argentum/internal/crypto  1.983s`,
exit 0.

**Outstanding:** the ticket asks for a CI run URL showing the same red/green
pair on the remote. That needs a push, which is the repo owner's call, so it is
recorded here as not done rather than counted as met.

### Dashboard

```
$ pnpm --filter dashboard lint      # tsc -b --noEmit && eslint .
✖ 6 problems (0 errors, 6 warnings)

$ pnpm --filter dashboard build
✓ 2473 modules transformed.
dist/assets/index-*.css   65.36 kB │ gzip:  11.05 kB
dist/assets/index-*.js   901.96 kB │ gzip: 275.87 kB
✓ built in 3.84s
```

## Reproducing these numbers

```bash
cd apps/backend
go vet ./... && echo "vet clean"
golangci-lint run ./...                 # 0 issues as of T-02
go test -race -count=1 ./... 2>&1 | tee /tmp/test.txt
grep -c "no test files" /tmp/test.txt   # untested packages
grep -c "^ok"          /tmp/test.txt    # packages with passing tests
go list ./... | wc -l                   # denominator
```

Or, from the repo root, the whole gate in one command:

```bash
make check     # vet + lint + test -race + build (Go and every workspace app)
```
