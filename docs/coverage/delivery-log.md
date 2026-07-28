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

`T-R2` PDF renderer v2

The ticket the report track exists for. `generate_document` still produces the
same three formats from the same tool call; what changed is that a PDF now has a
cover, a running header, `Page N of M` in the document's own language, numbered
sections, KPI cards, callouts, and tables whose columns are measured against
their content instead of being the 12-unit grid divided evenly.

The part worth stating plainly is what moved, not what was added. **Formatting
left the model.** A v2 table cell carries a value and a type — `3863405700`,
`currency` — and the renderer decides that it reads `Rp 3.863.405.700`, with the
same separators and the same decimal count as every other cell in its column,
whatever the model felt like that turn. That is most of the difference between a
table that looks designed and one that looks dumped, and it removes a job the
model was doing inconsistently.

Compatibility is not a shim. `spec.Column` and `spec.Cell` unmarshal from both
the v1 and v2 JSON shapes, so a v1 payload *is* a v2 document whose cells are
all text; `spec_version: 2` only decides whether the renderer offers the chrome.
`internal/tools/document/render_test.go` is unchanged and passes against the new
renderer, which was the ticket's own definition of the shim working.

Three things this ticket found:

- **The 200-row export fixture paid for itself four times.** Unbreakable tokens
  drawn over the column to their right, a stride sampler that measured every
  k-th row and missed a whole category of value, a grid distribution biased
  against wide columns, and integer rounding that clipped cells by under a
  millimetre. None of the other three fixtures would have surfaced any of them,
  and the export's own hand-rolled random data was correlated in a way that had
  nearly hidden the second.
- **`internal/report/format` was wrong in the one way it is not allowed to be.**
  `Parse` could not read back `-Rp 1.234` — the minus sits in front of the
  currency symbol and the parser only stripped symbols from the front of the
  string. The eval comparator (`T-01`) and the report renderer disagreeing about
  what a number is, in the single package built to stop exactly that. A
  round-trip test over every locale × currency × compact combination now pins it.
- **Byte-stability needed two globals in a dependency, and CI found the
  second.** gofpdf writes its font catalogue in Go map order, so the same spec
  rendered twice produced identical pages with the font objects renumbered. It
  also writes `/ModDate` from the wall clock, which maroto cannot set — so the
  output was reproducible within a second and not across one. Six local runs
  said it was fixed; the first CI run said otherwise. The lesson is in the test,
  not the fix: comparing two renders catches a clock only if the pair straddles
  a tick, so it now asserts both timestamps against `generated_at` directly.

Record, gate output and known limits: [`report-rendering.md`](report-rendering.md).

`T-R4` PPTX deck renderer

The same spec, projected onto slides. No new content model, no second authoring
path: the deck's own tests read the PDF renderer's fixtures out of
`../pdf/testdata` and change one field. That is the acceptance criterion written
as a file path rather than as a promise — a copy of the fixtures in the deck's
package would let the two drift the first time somebody tuned a deck by editing
one, and the test would still pass while the claim stopped being true.

The change that makes it feel authored is where the prose goes. A paragraph the
model wrote lands whole in the speaker notes and contributes only its lead
sentence to the slide. The presenter has something to say and the audience has
something readable from the back of the room, out of the same text that would
otherwise have been a wall of 18pt.

The OOXML is hand-rolled — `archive/zip` plus string templates over the parts —
because no Go library writes PresentationML. The cost is `parts.go`. The return
is that two renders of one spec are byte-identical *by construction*, a property
`T-R2` had to fight gofpdf for across two CI rounds.

Three things this ticket found:

- **The first LibreOffice conversion found three defects the tests could not.**
  A cover subtitle the estimate put on one line came back on two with the brand
  rule drawn through it; a spec with no headings produced untitled slides with
  nowhere for the `(cont.)` marker to go; an invoice's billing address was
  truncated to one line. All three were obvious in the rendered page and
  invisible in the XML, which is the argument for the conversion being a CI job
  rather than a checklist item. The subtitle fix is the general one: the cover
  no longer chains offsets off estimated heights, because with a substituted
  font an estimate being one line out is a certainty over enough documents.
- **Three packages had to come out of the PDF renderer first.** `measure`,
  `layout` and `labels` — the text metrics, the column solver and the five words
  a renderer chooses. Two copies of any of them would have meant the same table
  proportioned differently in the report and the deck attached to it. The PDF's
  own tests pass against the extracted packages untouched, which is what makes
  the extraction reviewable rather than a rewrite.
- **`chart.maxWidthMM` was a page-shaped constant.** It capped charts at 200mm,
  sized for A4's 174mm measure, so a chart asked for across a 290.7mm slide
  silently got a 200mm image stretched to fit — a 138 DPI chart on a projector,
  which is exactly what that ticket's acceptance criterion rules out. A cap that
  guards against an absurd caller has to move with the widest caller, not stay
  at the narrowest.

**One acceptance item is not met.** The gate asks for a deck opened in
PowerPoint, Keynote, Google Slides and LibreOffice. Only LibreOffice has been
run — the other three are desktop and browser applications that cannot be driven
from this environment. It is recorded as outstanding rather than counted as
done.

Record, gate output and known limits: [`report-deck.md`](report-deck.md).

## Phase 1b — Safe to change (2026-07-28, in progress)

`T-02` test coverage + a CI gate that gates

The first ticket in this project whose deliverable is a *floor* rather than a
feature. Twenty-one of forty-nine packages now have tests, every package the
coverage doc ranked CRITICAL among them, and `golangci-lint` reports zero
issues against a five-linter config that the tree failed in fifty places when
it was first pointed at it.

The part worth stating is what the tests found, because a test suite that finds
nothing on the way in is a suite that was written to pass:

- **Scheduled tasks with a non-UTC timezone have never been able to work in
  production.** `normalizeTimezone` and `nextFire` both call
  `time.LoadLocation`, which reads `/usr/share/zoneinfo`.
  `Dockerfile.{api,worker,discord}` all run `alpine:latest` with
  `ca-certificates` and nothing else, and no file imported `time/tzdata`. So
  `Asia/Jakarta` — the timezone this product's customers actually use —
  resolves on every developer machine and fails in the deployed image. The fix
  is one blank import; the reason it went unnoticed for three months is that
  the failure is invisible everywhere except the one place it matters.
- **Two unchecked type assertions in the chat handler.** `uid.(string)` on a
  value only `middleware.Auth` sets: a route wired without that middleware
  panics rather than returning 401. Found by turning on a single errcheck
  option, which is the argument for the linter in one line.
- **`redact_nik` is dead.** A NIK is sixteen consecutive digits, and so is a
  credit-card number typed without separators; `redact_credit_cards` is
  declared first and the engine returns after the first match. The golden suite
  could not give that rule an honest must-block case, so it records the
  shadowing in a named test instead. Same treatment for a topic-gate false
  positive ("margins are collapsing" reads as a P&L question) and two phone
  redaction edges. `T-07b` owns all four.
- **The PPTX determinism test was flaky, and had been since it was written.**
  `v1_legacy.json` carries no `generated_at`, so it rendered with the wall
  clock; the test rendered twice and compared, which only fails when the pair
  straddles a second — and under `-race`, where the pair takes forty seconds,
  it does. This is precisely the lesson `T-R2` wrote down for the PDF and the
  deck's test had not learned: comparing two renders catches a clock only by
  luck. It now pins the clock, so the v1 fixture is covered rather than
  skipped.

Two things about the shape of the suite are worth carrying forward. The
guardrail golden set runs against **`config/guardrails.yaml` itself**, not a
copy, because that file is the one whose regexes keep getting narrowed —
six of the twenty commits before this sprint tuned one with no regression
signal at all. And `TestEveryRuleHasGoldenCases` fails when a rule is added
without cases, which is what makes it a gate rather than a snapshot.

On the frontend, `pnpm lint` runs eslint for the first time in the project's
history — the script had called it since the beginning, but eslint was never in
`devDependencies`, so it had failed with `command not found` in CI and locally
alike. First run: 36 problems. Most of them turned out to be one bug wearing
twenty hats: every catch clause read `e?.response?.data?.error || e.message`
with `e` typed `any`, and a thrown non-Error has no `message`, so those paths
put the literal string "undefined" in a toast. They now share one narrowing
helper. Zero errors remain; six warnings do, and they are real, so they are not
suppressed.

**One acceptance item is not met.** The ticket asks for a CI run showing red on
a deliberately broken test and green after the revert. The break was made and
reverted locally — `go test -race` failed on five subtests and then returned
exit 0 — but the CI half needs a push, which is the repo owner's call.

Record, gate output and the full lint triage:
[`test-coverage.md`](test-coverage.md).

`T-04` RBAC + team invites

Two findings, one ticket. `middleware.AdminOnly()` had existed since the first
architecture pass, was correct, was tested — and was wired to zero routes, so
any authenticated caller could rotate a database DSN, delete a data source, or
replace the Discord and Lark bot credentials. And there was no way to create a
second user at all: `users` carried a `company_id` and a `role`, and the only
writer was signup, which creates a company and exactly one admin. A company
with two people was not expressible.

The decisions worth carrying forward:

- **Access is a table, not a decoration.** The obvious fix is an `AdminOnly()`
  argument in each handler's `Register`. It was rejected because it cannot be
  verified: gin's `RouteInfo` exposes a route's final handler and nothing about
  the chain in front of it, so no test can read per-route gating back out of a
  built router. "Did we remember to gate the new one?" would be answerable only
  by reading a dozen files, forever. With the decision in
  `cmd/api/policy.go` as data, one test diffs it against `r.Routes()` in both
  directions — a route with no entry fails, and an entry with no route fails
  too. Unlisted routes are denied, so the failure mode of forgetting is closed
  rather than open.
- **The gate is wider than the ticket, on purpose.** The ticket named nine
  routes, including `PUT /connections/:id/dsn`. It did not name `POST
  /connections` — and a member who can *add* a source can point one anywhere,
  which is the same capability. Nor `POST /connections/test`, which opens an
  outbound connection to a caller-chosen host:port and writes no row. A list of
  routes is a proxy for a capability; gating the proxy and not the capability is
  how these findings get re-filed six months later.
- **Deactivation had to reach sessions that already exist.** `Refresh` re-read
  the claims it was handed and re-signed them, so removing someone left them
  seven days of working refreshes. It now loads the user, which also means a
  role change lands on the next refresh rather than the next login. Access
  tokens already issued still live out their 15 minutes; that window is
  deliberate, and a blocklist is `T-13`'s problem.
- **The last-admin rule counts who can *currently act*.** A pending admin who
  has never accepted their invite, and a deactivated one, do not count —
  otherwise inviting a second admin would unlock the door before anyone walked
  through it.
- **The migration is `021`, not the ticket's `027`.** golang-migrate only
  applies versions above the schema's current one, so landing 027 now would
  strand 021–026 permanently, and `T-05` and `T-06` are already filed against
  those numbers.

Two bugs surfaced from making the router testable at all. `NewRateLimiter`
returned a limiter that panics when Redis is absent — `newRouter` already read
`if rateLimiter != nil`, so the intent was there and the constructor never
honoured it. And `RequireRole` as first written checked the role only on admin
routes, which would have admitted a request with no identity at all to a member
route if it were ever wired without `Auth` in front of it. Both are closed.

The gate was run twice: unit tests against the real `newRouter` with nil
services, and then against a live `cmd/api` on a real Postgres — invite →
preview → accept → replay(404) → login(200), every gated route 403 for a member
and not-403 for an admin, and a removed member's login returning 403 rather
than a session.

That second run also produced the one mistake worth recording: the first
attempt sourced `apps/backend/.env`, which points `DB_HOST` at a **remote**
server rather than the local container, and `cmd/api` migrates on boot. `021`
landed there unintentionally. It is additive and forward-compatible, the
`activated_at` backfill covered all four existing accounts, and the owner chose
to leave it applied. `docs/agents/playbooks/add-migration.md` and
[`rbac.md`](rbac.md) both carry the lesson.

Record, gate output and the limits: [`rbac.md`](rbac.md).

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
