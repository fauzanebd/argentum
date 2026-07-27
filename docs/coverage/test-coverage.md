# Test Coverage — Measured State

Measured 2026-07-26 by running `go test ./...` and `go vet ./...` in the backend
tree (`apps/backend/` post-monorepo; the standalone `argentum` repo at the time of
measurement). These are actual results, not estimates.

## Headline

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

## Go backend — package by package

### Packages with tests (12)

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

All twelve pass.

### Packages with no tests (32)

Ranked by risk — how much damage a silent regression here would do.

| Risk     | Package                                | Why it matters                                                                                        |
| -------- | -------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **CRITICAL** | `internal/app` *(partial)*         | ~3,000 lines. `MeteredLLM` is covered; `ChatRunner`, `ThreadService` (fork heuristics), `UsageService` (pricing math) and `ScheduledTaskService` (cron validation) are not. Every recent bug-fix commit touched this package. |
| **CRITICAL** | `internal/guardrails` *(partial)*  | The security boundary. The T-16 fabrication rule is covered; the YAML rule engine is not. Six of the last twenty commits tuned guardrail behaviour with no regression signal — a re-narrowed regex could silently unblock prompt injection or block legitimate BI questions. And per `T-16`'s finding, the output rules have never executed at all (see `T-07b`). |
| **CRITICAL** | `internal/crypto`                  | AES-256-GCM DSN cipher. A round-trip bug corrupts every stored connection.                              |
| **CRITICAL** | `internal/tenantctx`               | The tenant-isolation primitive. An empty-company-ID path is a cross-tenant leak.                        |
| **HIGH** | `internal/auth`                        | Argon2id hashing + JWT signing/verification, including the `?at=` query-param path.                     |
| **HIGH** | `internal/adapters/db` (+3 drivers)    | Read-only transaction enforcement, statement timeouts, row capping. The last line of defence on tenant data. |
| **HIGH** | `internal/adapters/postgres`           | 19 repository files. Every one is a place a missing `company_id` predicate could leak data.             |
| **HIGH** | `internal/tools` *(partial)*           | 7 agent tools, ~1,200 lines. The `run_sql` result notes are covered; the byte-cap trimming loop, `get_schema` filtering and source resolution are not. |
| **MEDIUM** | `internal/config`                    | 494 lines of env parsing, ~75 vars, plus 7 `Effective*()` fallback accessors and `WorkerQueueMap()` CSV parsing. Pure functions — trivially testable, currently untested. |
| **MEDIUM** | `internal/queue`                     | Task contracts, asynq option building, periodic config provider.                                        |
| **MEDIUM** | `internal/transport/http/middleware` | Auth, CORS, rate limit. Includes the unwired `AdminOnly()`.                                             |
| **MEDIUM** | `internal/llmtenant`                 | Per-tenant client caches with TTL eviction. Concurrency-sensitive.                                      |
| **MEDIUM** | `internal/embedding`                 | Batch fan-in, dimension handling.                                                                       |
| **LOW**  | `internal/whatsapp`, `internal/discord`, `internal/lark` | External-API adapters; need HTTP-level fakes to test meaningfully.                        |
| **LOW**  | `internal/metabase` (client)            | DSN builders are tested; the REST client is not.                                                       |
| **LOW**  | `internal/cache`, `internal/metrics`, `internal/migrate`, `internal/transport/eventbus`, `internal/transport/ws`, `internal/adapters/storage`, `internal/domain`, `pkg/models` | Thin or declarative.                        |
| **N/A**  | `cmd/api`, `cmd/worker`, `cmd/discord`, `scripts/encrypt_secret` | Wiring. Cover via integration tests, not unit tests.                     |

## Frontend

| App                   | Test files | Test runner | Type check | Lint         |
| --------------------- | ---------- | ----------- | ---------- | ------------ |
| `apps/dashboard`      | 0          | none installed | `tsc -b` via build | `tsc -b --noEmit` |
| `apps/landing`        | 0          | none installed | `tsc -b` via build | `tsc -b --noEmit` |

> **Found during `T-00b`:** the dashboard's `lint` script called `eslint .`, but
> **eslint was never in its devDependencies** — `pnpm lint` failed with
> `command not found`, so it had never run, in CI or locally. It now points at the
> same `tsc -b --noEmit` typecheck landing uses, which is a real (if narrow)
> signal. `T-02` installs eslint with a narrow rule set and restores proper
> linting. Expect a wall of findings on first run; budget triage time.

Both now run in CI via the `web` job.

## CI gate — what is actually checked

`.github/workflows/ci.yaml`:

```
build:  go mod download
        go build -o api    ./cmd/api
        go build -o worker ./cmd/worker
docker: (tags only) build+push argentum-api, argentum-worker to GHCR
```

Missing from CI:

- ❌ `go test ./...`
- ❌ `go test -race`
- ❌ `go vet ./...`
- ❌ any linter (`golangci-lint`)
- ❌ `go build ./cmd/discord` — the Discord gateway is **never compiled in CI**
- ❌ frontend `tsc` / `pnpm build` / `eslint`
- ❌ migration up/down round-trip check
- ❌ any answer-quality evaluation

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

## Targets

Set deliberately low and reachable. Coverage percentage is a poor goal; these are
about *which* code is protected.

| Milestone | Target                                                                                          |
| --------- | ----------------------------------------------------------------------------------------------- |
| Sprint 1  | Every CRITICAL package has tests. CI runs `test -race` + `vet` + `golangci-lint` + frontend build. `cmd/discord` builds in CI. |
| Sprint 1  | Guardrails golden-case suite: every rule has ≥1 must-block and ≥1 must-pass case, including the false positives the comments describe. |
| Sprint 1  | Eval harness with ≥30 golden questions on the demo tenant, scored, runnable offline, one command. |
| Sprint 2  | HIGH packages covered. Migration up/down round-trip in CI.                                        |
| Sprint 2  | Eval set ≥100 questions, with a per-commit score recorded so regressions are visible.            |

## Reproducing these numbers

```bash
cd apps/backend
go vet ./... && echo "vet clean"
go test ./... 2>&1 | tee /tmp/test.txt
grep -c "no test files" /tmp/test.txt   # untested packages
grep -c "^ok"          /tmp/test.txt    # packages with passing tests
```
