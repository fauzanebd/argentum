# Argentum — Gap Analysis and the Agent-Native Thesis

Written 2026-07-26. Findings below were verified in source; each carries the file
that proves it.

## Part I — The strategic gap

### Stated goal

> "More agent ready. I want to disrupt how company works."

### Where the product actually sits

Argentum today is an excellent **answering machine**. A person asks; the agent
queries, formats, and replies. Every capability — chat, dashboards, documents,
even scheduled tasks — is a variation on "human initiates, agent responds."
`scheduled_tasks` is the one exception, and even it is just "human initiates on a
timer."

That is a real product, and the engineering behind it is above the market
average. But it does not change how a company works. It changes how a company
*asks questions*. The person still has to know to ask.

### What "disrupting how a company works" requires

Three shifts, in dependency order. Each one is worth more than the one before it,
and each requires the one before it to be safe.

**Shift 1 — From pull to push.** The agent notices things and tells people
without being asked. Revenue dropped 18% week-over-week; the agent says so in the
WhatsApp group at 08:00 with the three most likely drivers already queried. This
is the single highest-leverage change available, because it inverts who initiates.
Nobody has to remember to log in. Prerequisite: a definition of "a thing worth
noticing," which means metrics must be *defined objects*, not free-form SQL the
LLM re-derives every time.

**Shift 2 — From answering to acting.** The agent doesn't just report that 40
invoices are overdue; it drafts the reminders, and on approval, sends them.
Today the agent is structurally read-only: `Conn.ExecuteReadOnly` is the only
data path, and guardrails block mutation keywords in user input. That is correct
for SQL against a customer's warehouse and should stay. Acting has to happen
through a *separate*, permissioned, audited action layer — never by relaxing SQL
safety.

**Shift 3 — From product to substrate.** Argentum stops being a destination and
becomes something embedded in whatever the company already uses. Two directions,
same idea:

- *For agents:* a company's own internal agent, or Claude in their IDE, asks
  Argentum for the revenue number over MCP with a scoped API key. This is what
  "agent ready" means most literally, and it is cheap to build because the tools
  already exist — they just aren't exposed outside the worker's in-process registry.
- *For humans:* an **embeddable chat widget** that drops into the internal website
  the company already runs (finding P-6). One script tag, or an npm component in
  their React app, and every person who already uses that tool has the agent
  beside the numbers they were looking at.

The widget is the higher-adoption half. MCP requires someone to write an
integration; the widget requires someone to paste ten lines. And an agent
embedded in a company's own internal tooling stops being a product they subscribe
to and becomes infrastructure they build around — which is the actual definition
of disrupting how a company works.

### Why accuracy infrastructure gates all three

You cannot push unsolicited alerts, take actions, or serve other agents on top of
a system whose answer quality is unmeasured. Right now there is no way to answer
"did that prompt change make the agent better or worse?" other than by feel.
Before shipping anything that acts autonomously, there must be a golden question
set and an offline runner that scores against it. This is not process overhead;
it is the thing that makes the other three shifts shippable.

### The metric-definition gap (most underrated)

Every question re-derives its own SQL. Ask "what was revenue last month" twice in
two threads and you can get two different queries, two different join paths, and
two different numbers — both defensible, neither authoritative. For a BI product
this is an existential accuracy problem, and it gets worse the moment alerts fire
automatically off a number nobody pinned down.

The fix is a **metric registry**: company-scoped definitions (name, description,
source, SQL template, grain, dimensions) that the agent queries through a
`query_metric` tool instead of writing ad-hoc SQL. Benefits compound:

- Same question → same number, every time.
- Watchers/alerts have something stable to watch.
- Token cost drops (no schema round-trip for known metrics).
- Corrections become durable: fix the definition once, every future answer is fixed.
- It becomes the moat. A competitor can clone the chat UI in a week; they cannot
  clone a customer's accumulated, curated metric layer.

## Part II — Concrete findings

Severity: **P0** = fix before scaling users. **P1** = fix this quarter.
**P2** = tracked, not urgent.

### Security and access control

| ID  | Sev | Finding                                                                                                                                                                                                        | Evidence                                                        |
| --- | --- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| S-1 | P0  | **`AdminOnly` middleware is defined but applied to zero routes.** Any authenticated `member` can rotate tenant DSNs, overwrite the company's LLM API keys, add allowed Discord/Lark users, and delete scheduled tasks. | `internal/transport/http/middleware/auth.go:49` — no call sites  |
| S-2 | P0  | **No team management.** `UserRepository` has `Create` and `ListByCompany`, but the HTTP surface exposes only `GET /api/users/me`. There is no invite flow, so in practice every company is a single account — which is *why* S-1 has gone unnoticed. | `internal/transport/http/handlers/user.go:25`                    |
| S-3 | P1  | **`/metrics` is unauthenticated** and returns cost and token totals as JSON. Information disclosure, and not in Prometheus exposition format so it can't be scraped properly either.                              | `cmd/api/health.go:26`                                          |
| S-4 | P2  | **WebSocket auth accepts the access token as the `?at=` query parameter.** Necessary because browsers can't set headers on WS, but tokens land in proxy and access logs. Mitigated by the 15-minute access-token TTL. | `internal/transport/http/middleware/auth.go:69`                 |
| S-5 | P2  | No audit trail of agent actions. `usage_events` records that an LLM call happened and what it cost, but not what the agent *did* — which SQL ran, against which source, on whose behalf. Mandatory before write actions. | absence: no `agent_actions` table                               |

### Billing and cost control

| ID  | Sev | Finding                                                                                                                                                                                            | Evidence                                            |
| --- | --- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| B-1 | P0  | **Credits are decremented but never enforced.** `UsageService.append` calls `credits.Decrement` and ignores the result; nothing checks the balance before a turn. A tenant on the platform's default LLM keys can spend without limit, including into negative balance. | `internal/app/usage_service.go:134`                 |
| B-2 | P1  | **Per-message cost attribution is impossible.** `MeteredLLM` passes `""` as `messageID` to `RecordLLM`, and `ChatRunner.completeWith` always passes `0, 0` for tokens — so `messages.tokens_in/out` are always zero and `usage_events.message_id` is always empty. Thread-level attribution works; message-level does not. | `internal/app/metering_llm.go:225`, `chat_runner.go:211` |
| B-3 | P1  | No plan/quota model at all: no tiers, no rate ceilings per plan, no payment integration. Rate limiting is a flat 60 req/min for every authenticated caller.                                         | `cmd/api/router.go:33`                              |
| B-4 | P2  | `DefaultPricing` is documented as "approximates GPT-4o" but the default primary model is Claude Haiku 4.5. The per-model table (`llm_pricing.go`) handles known models; unknown models silently use stale GPT-4o rates. | `internal/app/usage_service.go:25`                  |

### Correctness and quality

| ID  | Sev | Finding                                                                                                                                                                                                                                          | Evidence                                    |
| --- | --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------- |
| Q-1 | P0  | **No evaluation harness.** Answer quality is unmeasured. Six commits in a row tuned prompts, guardrails, and model defaults with no regression signal.                                                                                             | absence                                     |
| Q-2 | P0  | **3 of 35 Go packages have tests.** `internal/app` — which contains `ChatRunner`, `ThreadService`, `UsageService`, `ScheduledTaskService`, ~2,900 lines of the highest-risk logic in the system — has none. The dashboard has zero tests.           | `go test ./...`, see `coverage/test-coverage.md` |
| Q-3 | ~~P0~~ **FIXED in T-00b** | **CI never ran tests.** The workflow ran `go build` for api and worker only — no `go test`, no `go vet`, no frontend check, and `cmd/discord` was never compiled. Worse, a trigger-level `paths: ['**.go']` filter meant a frontend-only or config-only change ran no CI at all. Now: per-job path filtering, `go vet`, `go test -race`, `cmd/discord` build, and a web build+lint job. `golangci-lint` remains `T-02`. | `.github/workflows/ci.yaml`                 |
| Q-9 | P1  | **~1,100 lines of API documentation were never in git.** `apps/backend/.gitignore:62` ignored `docs/` ("Development Docs"), so `api.md`, `scheduled-tasks/api.md`, `lark-discord-integrations/api.md`, `db-regenerate-description/api.md`, and the Postman collection existed **only on one machine** — absent from the remote and from every clone. A new laptop or one `rm -rf` would have lost all of it. `T-18` also instructs agents to write docs into that directory, which would have been invisible work. **Recovered and tracked in `T-00b`.** | `apps/backend/.gitignore:62` (pre-migration) |
| Q-10 | P1 | **`.env.example` was gitignored** (`:24`), so no clone had an environment template — which is why setup was tribal knowledge and why `T-00` needed a re-warm ticket at all. **Fixed in `T-00b`.** | `apps/backend/.gitignore:24` (pre-migration) |
| Q-11 | P1 | **The dashboard's `lint` script had never worked.** It called `eslint .`, but eslint was not in `devDependencies` — `pnpm lint` failed with `command not found`. Since CI never ran it either, nobody noticed. Repointed at `tsc -b --noEmit` in `T-00b`; `T-02` installs eslint properly. | `apps/dashboard/package.json:9` (pre-migration) |
| Q-4 | P1  | **PII redaction corrupts legitimate BI output.** `redact_nik` blanks any 16-digit number — which includes order IDs, transaction references, and account numbers. `redact_emails` and `redact_phone_numbers` make "list our top customers with contact details" structurally unanswerable. The rules are scoped to both input and output because `scope` is omitted. | `config/guardrails.yaml:216-258`            |
| Q-5 | P1  | **`max_iterations: 3`** in both `agents.yaml` and `WithMaxIterations(3)`. A question needing get_schema → run_sql → refine → visualize → dashboard exceeds this. Multi-step agentic work is capped structurally.                                     | `config/agents.yaml:12`, `cmd/worker/main.go:205` |
| Q-6 | P1  | **`block_system_prompt_leak`** blocks any output containing "you are an ai" / "your instructions" / "system prompt". A user asking "what can you do?" can trip it, and the failure mode is a security-policy message instead of an answer.           | `config/guardrails.yaml:206`                |
| Q-7 | P2  | 14 of 20 control migrations have no `.down.sql`. Rollback is impossible for everything before Discord support.                                                                                                                                     | `migrations/control/`                       |
| Q-8 | ~~P2~~ **FIXED in T-00b** | `go.mod` declared `go 1.26.1` while CI pinned `GO_VERSION: '1.25'`, working only because `GOTOOLCHAIN=auto` downloaded 1.26 on every run. CI now pins `'1.26'`.                                                                | `go.mod:3`, `.github/workflows/ci.yaml`     |

### Observability

| ID  | Sev | Finding                                                                                                                                                    | Evidence                        |
| --- | --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------- |
| O-1 | P1  | No distributed tracing. A slow turn cannot be decomposed into LLM time vs. tenant-SQL time vs. embedding time without reading logs by hand.                  | absence                         |
| O-2 | P1  | `/metrics` is a custom JSON snapshot of atomic counters, not Prometheus exposition. Standard tooling can't scrape it; the Helm chart ships no ServiceMonitor. | `internal/metrics/collector.go` |
| O-3 | P2  | No persisted agent run trace. Tool calls stream to the UI live and are then lost — they aren't stored, so a failed turn can't be replayed or debugged after the fact. | `ChatRunner.runStream`          |
| O-4 | P2  | No error tracking (Sentry or equivalent) in either backend or frontend.                                                                                     | absence                         |

### Product surface

| ID  | Sev | Finding                                                                                                                                       | Evidence                              |
| --- | --- | --------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| P-1 | P1  | Landing page promises **Telegram**; it does not exist. Discord and Lark ship but are unmentioned.                                              | `apps/landing/src/components/hero.tsx:23` |
| P-2 | P1  | No public API or machine authentication. Every endpoint requires a user JWT, so no customer system — and no other agent — can integrate.        | `cmd/api/router.go`                   |
| P-6 | P1  | **No embeddable surface.** A customer with an existing internal website (React admin panel, ops dashboard, intranet) cannot put Argentum inside it. Using the product means leaving the tool where the work happens. There is no browser-safe key type, no origin allowlist, and no short-lived session token — so there is no safe way to authenticate a widget even if one existed. | absence: no `/api/embed/*`, no `embed_keys` |
| P-3 | P2  | No Slack channel. For non-Indonesian mid-market, Slack matters more than Lark.                                                                 | absence                               |
| P-4 | P2  | Dashboard has no onboarding checklist beyond "add a connection"; nothing prompts a tenant to enable table embeddings, which visibly improves answers. | `features/onboarding/`                |
| P-5 | P2  | `scratch-chat-page-plan.md` is a one-line stray artifact committed in the dashboard repo root.                                                 | `apps/dashboard/scratch-chat-page-plan.md` |

## Part III — What is genuinely strong

Worth stating plainly, because the plan should not touch these:

- **Tenant isolation design.** Three independent mechanisms (context scoping,
  per-tenant connection pool, per-tenant LLM cache), consistently applied.
- **Cost engineering.** Prompt caching, schema filtering via embeddings, tiered
  models, a small-talk short-circuit, and byte-capped SQL results. Someone has
  been watching the bill closely, and it shows in the commit history.
- **Guardrail tuning.** The regex families carry comments explaining exactly which
  false positive each narrowing fixed. That is production scar tissue, correctly
  recorded.
- **Failure-mode discipline.** Optional subsystems degrade instead of crashing:
  no MinIO → no document tool; no embeddings → plain schema path; light LLM
  resolution fails → fall back to primary; streaming unsupported → blocking run.
- **The worker/API split.** Correct call, made early, and it is what makes
  channels, retries, and horizontal scale cheap.

## Part IV — Sequencing conclusion

The dependency chain that falls out of the above:

```
   Evals + tests + CI gate          (Q-1, Q-2, Q-3)
              │  nothing agentic is safe to ship without a regression signal
              ▼
   Audit log + RBAC + credit enforcement   (S-1, S-5, B-1)
              │  the safety rails that make autonomy legible and bounded
              ▼
   Metric registry + query_metric tool
              │  gives alerts and actions something authoritative to stand on
              ▼
   Watchers → proactive push        ← Shift 1: inverts who initiates
              │
              ├──► Action framework + approval  ← Shift 2: agent acts
              │
              └──► API keys ─┬─► MCP server     ← Shift 3a: agents call it
                             │
                             └─► Embed keys ──► widget  ← Shift 3b: humans use it
                                                          inside their own tools
```

That chain is what [`plan/00-sprint-overview.md`](../plan/00-sprint-overview.md)
schedules across eight weeks — weeks 1–6 for the first three shifts, weeks 7–8 for
the embeddable widget.
