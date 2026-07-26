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

## Go backend — package by package

### Packages with tests (3)

| Package                        | Test file(s)                                       | What it covers                       |
| ------------------------------ | -------------------------------------------------- | ------------------------------------ |
| `internal/llmclient`           | `factory_test.go`                                  | LLM client construction per interface |
| `internal/metabase`            | `postgres_dsn_test.go`, `sqlserver_dsn_test.go`     | DSN string building for Metabase sync |
| `internal/tools/document`      | `render_test.go`                                    | PDF / XLSX / CSV rendering            |

All three pass: `ok` in 0.45–1.0s.

### Packages with no tests (30)

Ranked by risk — how much damage a silent regression here would do.

| Risk     | Package                                | Why it matters                                                                                        |
| -------- | -------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **CRITICAL** | `internal/app`                     | ~2,900 lines. `ChatRunner`, `ThreadService` (fork heuristics), `UsageService` (pricing math), `ScheduledTaskService` (cron validation), `MeteredLLM`. Every recent bug-fix commit touched this package. |
| **CRITICAL** | `internal/guardrails`              | The security boundary. Six of the last twenty commits tuned guardrail behaviour with no regression signal — a re-narrowed regex could silently unblock prompt injection or block legitimate BI questions. |
| **CRITICAL** | `internal/crypto`                  | AES-256-GCM DSN cipher. A round-trip bug corrupts every stored connection.                              |
| **CRITICAL** | `internal/tenantctx`               | The tenant-isolation primitive. An empty-company-ID path is a cross-tenant leak.                        |
| **HIGH** | `internal/auth`                        | Argon2id hashing + JWT signing/verification, including the `?at=` query-param path.                     |
| **HIGH** | `internal/adapters/db` (+3 drivers)    | Read-only transaction enforcement, statement timeouts, row capping. The last line of defence on tenant data. |
| **HIGH** | `internal/adapters/postgres`           | 19 repository files. Every one is a place a missing `company_id` predicate could leak data.             |
| **HIGH** | `internal/tools`                       | 7 agent tools, ~1,200 lines. `run_sql` byte-cap trimming loop, `get_schema` filtering, source resolution. |
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
