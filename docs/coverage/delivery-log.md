# Delivery Log — What We've Been Doing

Reconstructed from git history across all three repos. Dates are commit dates.
Merge commits omitted; the eight `bigref`-era merges are folded into their phases.

**Total elapsed:** 2026-04-10 → 2026-05-23 (~6 weeks of active development).
**Last commit:** 2026-05-23. **Today:** 2026-07-26 — the workspace has been
quiet for roughly nine weeks. Expect environment drift (dependencies, tokens,
Metabase version, tenant DSNs) before the next sprint's first change lands.

---

## Phase 0 — Bootstrap (2026-04-10 → 2026-04-23)

`f0b4457` initial phase · `33caf2f` docker compose + module refactor · `9995e96`

First working shape: API + worker split, Docker Compose, Go module layout. Commit
messages from this phase (`faah`) are placeholders — this was exploratory.

## Phase 1 — The big refactor (2026-05-02 → 2026-05-05)

`d782129` big refactor · `5a1cc6b` checkpoint · `67dca3d` · `f2660cc` company
name in chat context · `24ff404` agent config + visualization handling ·
`9ea0007` guardrail topic enforcement

**Where the current architecture came from.** Three PRs off a `bigref` branch
established the layering that still holds: `domain` / `app` / `adapters` /
`transport`, `ChatEnqueuer` vs `ChatRunner`, the tool registry, and the YAML
guardrail engine. Dashboard was rebuilt in parallel (`0687da5` → `a19a75a`),
ending with real WebSocket lifecycle handling on thread switching.

**Landing page** shipped 2026-04-28 (`7a4f762`) with the scripted chat demo.

## Phase 2 — Ship it (2026-05-07 → 2026-05-09)

`61edb32` ci/cd · `f8daa65` image ci/cd · `9051b29` config defaults · `55749c9`
LLM client refactor · `0ea75c0` builder → golang:1.26-alpine

Backend: GitHub Actions build + GHCR push on tags.
Dashboard: Cloudflare Pages deploy, fought and won across four commits
(`6732259` → `9e9899f`) — pnpm install, SPA fallback, then Pages middleware.
Mobile chat UI polished in the same pass.

**This is when Argentum became deployable rather than runnable.**

## Phase 3 — Capability expansion (2026-05-09 → 2026-05-10)

The densest week in the project's history. Six substantial features in two days:

| Commit    | Feature                                                                 |
| --------- | ----------------------------------------------------------------------- |
| `42391d2` | `generate_document` tool — PDF / XLSX / CSV to MinIO with presigned URLs |
| `58c1d6f` | SQL Server adapter + Metabase warehouse sync + DSN builder              |
| `feb7a47` | **Multi-source DB support** with LLM-generated source descriptions      |
| `8cf653b` | **Cron-scheduled agent tasks** — asynq periodic manager, DB-backed      |
| `e340831` | Per-model LLM cost calculation                                          |
| `e70e11b` `0372ac1` `437ce57` | SQL Server reality checks: TLS 1.0, cert trust, no read-only tx option |

Dashboard kept pace: scheduled-tasks UI with cron presets and run history
(`432d6f0`), integrations tab + toasts (`6fd2217`), env-driven API/WS hosts
(`f02c954`), React Query cache clearing on logout (`aa5ec80`).

The three SQL Server fix commits are worth noting — they are what real customer
infrastructure looks like: an IP-addressed server with an old TLS stack and a
dialect that rejects the read-only transaction option other drivers accept.

## Phase 4 — Cost and context engineering (2026-05-11 → 2026-05-14)

The most technically interesting phase, and the one with the least test coverage.

| Commit    | Change                                                                                          |
| --------- | ----------------------------------------------------------------------------------------------- |
| `dcd0355` | Schema retrieval + chat runner refactor                                                          |
| `f850a88` | Cheaper/smarter LLM defaults, streaming metering, `/api/config/models`                           |
| `e02dfd4` | **Prompt caching + schema filtering** — the stated goal was cutting Anthropic input tokens        |
| `a56fd85` | Default LLM → `deepseek/deepseek-v3.2` via OpenRouter                                            |
| `94fe370` | **Per-tenant LLM credentials + embedding-based table picker** (pgvector, per-source opt-in)      |
| `74f5419` | **Bill Anthropic prompt-cache tokens** + revert to Anthropic-native defaults                      |

Note the two-day round trip on model defaults: DeepSeek on 05-12, Anthropic-native
on 05-14. With no eval harness, whether either move improved answer quality is
still unknown — which is exactly why `T-01` sits at the top of the next sprint.

Dashboard: model display in chat and settings (`917fc1e`), connection test +
reindex + RAG probe tools (`d11edef`) — the operator tooling for the new
retrieval layer. Landing rethemed to red/rose (`a1ee7c0`).

## Phase 5 — Channels and accountability (2026-05-17)

| Commit    | Change                                                                        |
| --------- | ----------------------------------------------------------------------------- |
| `17f81f5` | **Discord + Lark (Feishu) channels** — 6 migrations, new `cmd/discord` process |
| `52f2511` | **Per-thread / per-channel / per-user usage audit endpoints**                  |

Dashboard shipped both the same day: live integration settings (`135ca35`) and
the tabbed usage analytics UI (`0e51718`).

The pairing is telling: channels multiply usage, so cost attribution by channel
and end-user shipped alongside them rather than after.

## Phase 6 — Guardrail tuning (2026-05-23)

`3891579` fix: stop semantic injection guardrail blocking benign follow-ups

The last commit before the nine-week pause. A production false positive: the
semantic prompt-injection classifier was rejecting ordinary follow-up messages.
The fix rewrote the classifier prompt to default FALSE and enumerate what is
*not* injection. Same class of problem as the topic-regex narrowings, same fix
shape.

---

# Sprint 1

## Phase 0 — Re-warm and consolidate (2026-07-26)

`T-00` environment re-warm · `T-00b` monorepo consolidation

Three repos became one with history preserved through `git subtree`, zero Go
import-path changes, and a single path-filtered CI pipeline that runs tests for
the first time. Records in [`migration-notes.md`](migration-notes.md).

The re-warm smoke test found more than drift: the agent fabricated a sales
figure under budget exhaustion (`C-1`) and the primary model recorded no usage
at all (`C-2`). Both went into the plan as tickets rather than into a backlog —
see [`environment-notes.md`](environment-notes.md).

## Phase 1 — Measurement and trust (2026-07-27) ✅

`T-01` eval harness

The first regression signal this project has ever had for agent behaviour.
Thirty-one golden questions against the demo tenant, run through the real
`ChatRunner` — same agent factory, tools, guardrails and system prompt as the
worker, which is why `internal/bootstrap` exists now: the worker's wiring came
out of `cmd/worker/main.go` so a second process could reuse it instead of
copying it.

Two things worth noting about the first run, because they are the argument for
the ticket:

- It found a **demo-data landmine** nobody had noticed in three months
  (`E-5`): `dim_date.month_name` was space-padded, so correct SQL returned
  nothing. Given that empty result the agent invented a total — a second
  fabrication mechanism, distinct from `C-1`.
- It found **three defective test cases of its own**, which is the normal cost
  of a first golden set and worth stating plainly: one question was ambiguous
  only in theory, one asserted a refusal's exact wording rather than its shape,
  and one had a second defensible answer. All three were fixed by tightening
  the case, not by loosening the check.

Baseline and per-category scores: [`eval-baseline.md`](eval-baseline.md).

`T-02c` primary-model metering

The `C-2` fix, and a reminder that a plausible mechanism is not a verified one.
The ticket's hypothesis — that `stream_options.include_usage` was never
requested — was wrong: it had been requested since `74f5419`. agent-sdk-go asks
the provider for usage on every tool-calling iteration and then reads
`chunk.Usage` in only one of its two streaming methods, the one the agent never
calls. Nine weeks of turns were billed at the guardrail model's rate because a
zero-check swallowed the gap without a word.

The fix reads usage off the SSE wire (`internal/llmusage`) rather than forking
the SDK, so the numbers are the provider's own, cache reads included, across
every iteration. Anthropic's path is untouched and still takes priority.

Two consequences worth carrying forward: `T-03`'s budget check now has a real
number to gate on, and the `T-01` baseline's cost figures are retroactively
known to be a lower bound — pass rate unaffected.

`T-16` iteration budget + anti-fabrication

The `C-1` fix, and the ticket that closes phase 1. Asked the question that
started all of this — "What were our total sales last month?" — the agent now
answers **IDR 3,863,405,700**, which is the true figure, having first said
what it retrieved.

The ticket was written as "raise the cap". The cap turned out to be three
problems wearing one coat:

- **The cap the code set was not the cap that ran.** `WithMaxIterations(3)` in
  Go and `max_iterations: 3` in `config/agents.yaml` both existed, and the YAML
  won, because `WithAgentConfig` is applied last in the option list. Anyone
  who had "fixed" this in Go would have changed nothing.
- **The agent never saw the cap.** agent-sdk-go's response to exhaustion is one
  more model call carrying "provide your final response based on the
  information available". Nothing in that says what to do when the information
  available is nothing. So the fix is not a bigger number, it is a message the
  model actually receives: every tool now runs behind a guard
  (`internal/agentbudget`) that, once the budget is gone, refuses the call and
  returns the incomplete-answer instruction *as the tool's result*. The tool
  boundary is the only point inside the provider's loop this codebase owns.
- **Exhaustion was never the only route.** `E-5` had already caught a second
  mechanism — a query that succeeded and matched nothing, answered with
  "IDR 1,488,000". A zero-row `run_sql` result now says in words that there is
  no figure in it. And a reply that states one anyway, in a turn where no data
  tool returned a row, is replaced before it is sent.

That last check could not go where the ticket said. `T-16` asked for an output
rule in `config/guardrails.yaml`; **agent-sdk-go only applies output guardrails
on its blocking path**, and every chat turn streams. So every `scope: output`
rule in that file — PII redaction included — has never executed in production.
Recorded against `T-07b`, which now owns switching them on; the fabrication
check lives in `ChatRunner` instead, where it also gets the turn evidence a
regex could not have.

The eval harness earned its keep twice more. It caught that
`create_visualization` had **never worked for the eval tenant** (`E-6`: sources
seeded outside the HTTP API are never registered with Metabase), which means
the three chart cases had been scoring the agent's reaction to a broken tool —
and the gate case could not have passed at any budget. And it caught what a
bigger budget costs, in a way that reading the diff would not: with room to
work, the agent uses it, including on work nobody asked for.

The set now reads **97.0% (32/33)**, up from 96.8%, at roughly double the cost
per answer — a regression the ticket's acceptance list forbade and this log
does not hide. Turns that used to stop after three iterations now run five to
seven tool calls and finish the job. One case fails, `ambiguous-headcount`, and
it is left failing on purpose: with room to work the agent surveys both sources
instead of asking which one is meant, and whether that is wrong is a product
decision rather than a bug. Numbers and analysis:
[`eval-baseline.md`](eval-baseline.md).

## Phase 1a — Worth forwarding (2026-07-27, in progress)

`T-R1` design tokens + theme package

The first shared thing between the dashboard and the backend that is shared *by
construction*. One `tokens.json` generates `apps/dashboard/src/tokens.generated.css`
and `apps/backend/internal/report/theme/tokens_gen.go`; both outputs are
committed, and CI regenerates and diffs them.

Two findings worth keeping:

- **The dashboard's palette had drifted from its own comments.**
  `--background: 60 7% 96%` renders `#F6F6F4`, beside a comment reading
  `#F5F5F0 cream`. Same for the brand red (`#F35858` rendered, `#F25C5C`
  claimed) and the border. Nothing was broken — the values are within 4/255 —
  but for three months the design system existed twice, and the two copies had
  already begun to disagree. That is the argument for this ticket in one line.
- **A generated `:root` cannot sit in the same file as a layered `.dark`.**
  Unlayered declarations beat layered ones regardless of specificity, so the
  first working version of this change would have silently disabled dark mode.
  Caught before commit; the fix and the reason are in `index.css`.

Space Grotesk is now embedded in every PDF (three faces, six registrations, OFL
licence committed), which takes a document from 1.6 KB to 34.5 KB and takes the
renderer off Helvetica for the first time. A missing face is a compile error; a
corrupt one stops the worker at boot rather than a customer's document at render.

Record: [`design-tokens.md`](design-tokens.md).

---

## What the history says about how this project is built

**Strengths visible in the log:**

- **Vertical slices.** Every feature lands backend + frontend + migration
  together. `feb7a47` (multi-source) shipped with its own UI two commits later.
- **Cost consciousness as a first-class concern.** An entire phase dedicated to
  input-token reduction, and metering shipped alongside every capability that
  spends money.
- **Fast reaction to production reality.** SQL Server TLS, guardrail false
  positives, Cloudflare Pages quirks — all fixed within a day of discovery.
- **Comment quality is unusually high.** Non-obvious decisions carry their
  rationale in the source (why `UsageRecorder` lives in `internal/tools`, why
  `IncludeIntermediateMessages` is set, which false positive each regex narrowing
  fixed). This is what makes the codebase agent-friendly.

**Patterns worth changing:**

- **Zero tests accompany any feature commit.** All three existing test files
  predate or are incidental to the feature work.
- **Prompt and model changes ship blind.** Six commits changed agent behaviour
  with no regression signal, including one reversal.
- **No down migrations after 014.** Only the Discord/Lark batch has them.
- **`internal/app` grows unchecked** — ~2,900 lines across 18 files, the highest
  churn and highest risk in the repo, entirely untested.

## Feature velocity, measured

| Phase | Days | Features shipped | Notes                                     |
| ----- | ---- | ---------------- | ----------------------------------------- |
| 0     | 13   | 1 (skeleton)     | Exploratory                                |
| 1     | 4    | Architecture      | The refactor that made everything after possible |
| 2     | 3    | CI/CD × 2 repos   | Deployability                              |
| 3     | 2    | 6                | Peak velocity                              |
| 4     | 4    | 6                | Deepest technical work                     |
| 5     | 1    | 2 large           | Channels + audit, same day                 |
| 6     | 1    | 1 fix            | Production response                        |

Sustained rate during active weeks: roughly **1.5 substantial features per
working day**, backend and frontend together, solo. The next sprint is sized
against that observed rate — not against an optimistic one.
