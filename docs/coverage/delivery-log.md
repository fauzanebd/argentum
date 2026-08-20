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

`T-R5` tenant report branding

The ticket that finishes the report track, and the first one where the customer
— not the operator, not the agent — decides what the artifact looks like. A logo,
an accent, a legal name, a document language, a confidentiality label, a footer
line, and whether our own name appears on the page at all.

The interesting decisions were about where a tenant's colour is *not* allowed to
reach:

- **Not into the chart palette.** `T-R3` verified those eight colours as a set —
  separable under simulated deuteranopia and in greyscale, gated by
  `make palette`. Substituting one tenant colour into that set voids the
  verification and nothing fails; the report simply becomes unreadable for part
  of its audience, on paper, months later. So the accent takes the rules, the
  headings, the cover and the wordmark, and the series ramp stays ours. The
  Reports tab explains that beside the colour picker rather than leaving a
  customer to notice their chart did not change.
- **Not below 3:1 against white.** A pale brand colour is rejected with the
  measured ratio in the message — `1.23:1 … needs at least 3.0:1` — because
  "too light" sends someone back to their brand book with nothing to act on. The
  floor is 3:1 rather than 4.5:1 on purpose: the accent is drawn as large text
  and as rules, which is exactly where WCAG puts that line, and a stricter rule
  would reject colours that read perfectly at the size they are drawn.
- **Not unchanged onto the deck's dark slides.** A navy that is right on paper
  can vanish on a near-black cover. Rejecting it would be fixing the wrong end,
  so the deck lifts it towards white only as far as legibility requires, only on
  the three dark slide kinds, and leaves a colour that already passes alone.

Two smaller things worth carrying. The logo does **not** go on the deck's cover:
a logo arrives as one file, almost always dark ink on transparency, and asking a
tenant for a second light-on-dark variant to solve a problem only one format has
is a worse trade than putting the mark on the light slides' footer — as one
shared media part, because 50 slides × 40 KB of identical bytes is a real cost.
And the preview and the real document go through **one** resolver, differing only
in whether the branding record came from the database or the request body; a
preview produced by a second code path is a preview that can be right while the
document is wrong.

The migration landed as `022`, not the ticket's `030`, for the reason `021`
carries. The ticket table now says explicitly that reserved numbers are not
binding — it has been wrong twice, and the second time was predictable from the
first.

**The gate also closed an item `T-R1` left open.** That ticket recorded that
dashboard screenshots could not be captured because Chrome was being intercepted
on this machine. It is not: something unrelated was already listening on the port
MinIO was first published to, which produced two confident wrong diagnoses in a
row during this run before it was found. Headless Chrome over the DevTools
protocol works, and the Reports tab, the live preview and the rejection message
are all in the record.

Record, gate output and the limits: [`report-branding.md`](report-branding.md).

`T-05` agent action audit log

The system could say what a turn cost and what the model replied. It could not
say what the agent *did* — which queries ran against the tenant's warehouse,
which of them failed, or under whose authority. Fine while the only actor is a
person watching a stream; not fine at `T-13`, where a key in someone else's CI
config makes calls nobody is watching.

The decisions worth carrying forward:

- **One decorator over the registry, not a call in each tool.** `WithAuditAll`
  wraps `s.Tools` in `bootstrap`, next to `T-16`'s budget guard and for the same
  reason: nothing reaches the agent except through that slice, so a tool added
  next year is audited without its author knowing this code exists. Seven call
  sites would be an eighth that somebody forgets, and the forgotten one is the
  tool an incident asks about.
- **Wrap order against the budget guard is load-bearing.** A refused tool call
  returns the guard's refusal payload with a **nil error** — it must, because
  the model only ever reads tool results. Audited from inside the guard, a call
  that never ran would be recorded `ok`. `agentbudget.IsRefusal` is what tells
  them apart, and it lives in `agentbudget` so the payload keeps one owner.
- **`rows_returned` is NULL, not 0, when a tool returns no rows.** A tool that
  produces a Metabase link and a query that matched nothing are different facts.
  Collapsing them would erase the distinction `T-16` exists to preserve.
- **Redaction errs toward removing too much.** Keys *and* values are scanned —
  a credential passed under an innocent name is exactly the case a key list
  misses — and a `SELECT` whose text contains `password=` is dropped whole.
  AGENTS.md §2 forbids persisting a decrypted DSN; a query lost from the audit
  log is still in the thread.
- **A blocked turn needed a second integration point.** An input guardrail
  refusing the question, and `T-16`'s fabrication check refusing the answer,
  both stop a turn without any tool running — so neither would appear in a
  tool-call log at all. `ChatRunner.WithActionLog` writes those rows with
  `tool_name` naming which gate closed, and stores only the sha256 of the
  refused question: a refused question is the input most likely to hold
  something a tenant would not want retained.
- **A cron tick is not the person who wrote the schedule.** `actorOf` returns
  `schedule` even though the payload carries the author's `UserID`. Attributing
  an unattended run to them puts a name at a keyboard nobody was sitting at.
- **The log outlives its subject.** `thread_id` carries no foreign key, because
  `DELETE /api/threads/:id` exists and a CASCADE would let a user erase the
  record of what the agent did by deleting the conversation.

One defect was caught only because the endpoint was exercised against the
running API rather than read: `args_redacted` typed `[]byte` marshals to
base64, so the log's arguments arrived unreadable. `json.RawMessage` fixes it.
This is the second ticket in a row where the live half of the gate found
something the unit tests could not.

The migration is `023`, not the ticket's `021` — the third consecutive ticket
whose reserved number was already spent.

Record, gate output and the limits: [`agent-audit.md`](agent-audit.md).

`T-03` credit enforcement

The balance was decremented and never read. This ticket reads it — before the
thread is resolved, on every channel, and on every scheduled fire.

The decisions worth carrying forward:

- **The ticket as written was a global outage, and saying so was the work.**
  Nothing in this system has ever *credited* a company. `company_credits` rows
  are minted by one writer, `Decrement`, which upserts a negative balance; the
  grant column defaults to 0 and no code path has ever set it. "Refuse at ≤ 0"
  against that state refuses every tenant that has ever run a turn, and every
  tenant that has not has no row at all. So `T-03` ships the starting grant it
  needs to mean anything — which is also the only possible denominator for the
  ticket's own "<20% remaining".
- **The grant is provisioned in Go, not backfilled by a migration.** A SQL
  backfill freezes the number into an applied migration, and an operator
  changing `CREDITS_DEFAULT_GRANT_USD` afterwards has two sources of truth that
  disagree — with the frozen one winning for every company that already
  existed. It also means this is the first ticket in four that did not have to
  discover its reserved migration number was already spent.
- **The check runs before the thread is resolved.** Refusing after
  `CreateDashboardThread` leaves a thread and an orphan user message per
  attempt, so a tenant at zero accumulates debris in proportion to how often
  they retry. The live gate shows threads and messages both unchanged across a
  refused turn — something the ticket did not ask for.
- **"Has a primary LLM row" had to be narrowed to "has one carrying a key".**
  `llmtenant.Resolver.merge` only swaps the API key when `APIKeyEncrypted` is
  non-empty, so a row overriding just the model still spends the platform key.
  Read literally, the ticket let any tenant opt out of billing by pinning a
  model name. Proven live by nulling the key on one row and watching the same
  company flip 202 → 402.
- **A second integration point, for the same reason `T-05` needed one.** A
  cron tick never passes through `ChatEnqueuer`. An unattended schedule on an
  exhausted tenant is exactly the unbounded spend the ticket exists to stop,
  because nobody is watching it to notice. The refusal is written onto the run
  row, because a schedule that silently stops firing is indistinguishable from
  one that broke.
- **One refusal sentence shared by four channels**, and the chat channels
  answer 200 — WhatsApp and Lark retry a non-2xx, and retrying a refusal
  delivers it several times. The API process grew a Lark client to say it,
  because until now every Lark reply was written by the worker *after* the
  agent ran, and a refusal happens before there is anything to enqueue.

The frontend defect the gate caught is the one worth remembering: `/chat` and
`/chat/$threadId` are two routes rendering the same component, so the send that
returns a warning also navigates — unmounting one and mounting the other, and
resetting every `useState` in the file. The first send of a session is exactly
the case that loses its banner, and exactly the case a near-empty account is in.
It was found only because the screenshot came back without a banner the API had
demonstrably returned. This is the third consecutive ticket where the live half
of the gate found something no unit test could.

A second one worth recording because it wasted a cycle: `pkill` on a `go run`
wrapper does not kill the child it spawned, so a restarted API silently failed
to bind and two observations were made against the previous process — with the
opposite config. `Listening on :8080` in the log is not proof; the *absence* of
`bind: address already in use` under it is.

Record, gate output and the limits:
[`credit-enforcement.md`](credit-enforcement.md).

`T-13` scoped API keys

Nothing could integrate with Argentum, by anyone, for any price: every route
wanted a human's session JWT. This ticket adds the only machine credential the
product has, and the `/v1` namespace it authenticates.

The decisions worth carrying forward:

- **The ticket said Argon2id and this ships SHA-256, on purpose.** Argon2id
  makes guessing expensive against a password. The secret half of a key is 256
  uniformly random bits — there is no dictionary to slow anybody down against,
  and the KDF's 64 MiB and ~50 ms would land on *every authenticated request*
  of a machine-to-machine API rather than on a fortnightly login. It is also an
  amplification vector: a valid prefix would let anyone make the server
  allocate 64 MiB per wrong guess. `internal/auth/invite.go` already made this
  argument for invite tokens and already uses SHA-256. The three other layers
  the sprint's risk register named — plaintext once, a separate rate bucket,
  per-key usage — ship unchanged.
- **No cache on the authentication read, and that is a deliberate departure
  from `T-03` two commits earlier.** Caching a credit verdict for 60s costs a
  topped-up tenant a minute of refusals. Caching a credential for 60s means a
  revoked key keeps working for a minute after an admin decided it should not,
  which is the exact moment it is most likely to be in the wrong hands. The
  price is one indexed read on a UNIQUE column per request.
- **A key carries scopes and never a role.** `APIKeyAuth` sets no `role` on
  the context, so `T-04`'s `RequireRole` — which refuses an unrecognised role
  — fails closed if a `/v1` group ever picks up the dashboard's policy
  middleware. Both directions of the split are asserted against the real
  router: a JWT gets 401 on every `/v1` route, and a well-formed key gets 401
  on all 66 `/api` routes in the policy table.
- **Scopes are fixed at creation, so the repository has no `Update`.** Editing
  the capabilities of a credential already sitting in someone else's CI config
  changes what that config can do without anyone touching it. Rotation is
  mint-then-revoke, and leaving the unsafe operation out of the interface is
  how that stays true.
- **Two things that belong to `T-A1` had to land here**, both additive: the
  typed error envelope (`internal/transport/http/apierr`), because the first
  thing `/v1` ever answers is an auth failure and `T-A1`'s acceptance forbids a
  bare `{"error":"…"}` under `/v1`; and `GET /v1/me`, because a credential with
  nothing to authenticate against cannot be gate-tested. The config var uses
  `T-A1`'s reserved name, `API_V1_RATE_PER_MIN`, rather than a second setting
  to reconcile later.

The defect the live gate found is the one worth remembering: **`/v1` inherited
the dashboard's CORS headers**. `middleware.CORS` is installed on the engine,
above every group, so the new group got `Access-Control-Allow-Credentials:
true` for free — and with `CORS_ORIGINS` unset it echoes *any* `Origin`. A key
usable from a web page is a key that shipped in a bundle, which is the precise
conflation `T-19`'s embed key exists to avoid. It was found by curling `/v1/me`
with an `Origin` header, not by any test. Fourth consecutive ticket where the
live half of the gate found something the unit tests could not.

A smaller one, for whoever writes the next browser check: Radix's
`Tabs.Trigger` listens for real pointer events, so a synthetic `element.click()`
over CDP lands on the DOM node and changes nothing — the screenshot came back
showing the wrong tab, selected. Dispatch `Input.dispatchMouseEvent` at the
element's own bounding box instead.

Record, gate output and the limits: [`api-keys.md`](api-keys.md).

## Phase 1c — Anyone can call it (2026-07-28 → 2026-07-30) ✅

`T-A1` the `/v1` contract

The first ticket in this project whose deliverable is a **promise**. Nothing in
it is a feature a customer asks for: it is the error format, the idempotency
contract, the pagination style and the request-id chain that every route
`T-A2`→`T-A5` adds will inherit, and that become permanent the first time
somebody writes code against them.

The decisions worth carrying forward:

- **An idempotency record holds ids, never payloads.** The obvious design
  caches the bytes the handler wrote and replays them, and it is wrong here
  twice: `POST /v1/reports/render` with `Accept: application/pdf` answers with
  megabytes, and a streamed chat answer has no bytes to keep at all. So a
  record holds `{"report_id":"…","status":"completed"}` and a replay
  re-derives the response from it — re-reading object storage and
  re-presigning, or re-attaching to the turn's pubsub channel. That is also
  the only way a replayed download link is still valid an hour later. A 10 MB
  render leaves a 160-byte record.
- **A failed request forgets its key.** The next thing a well-behaved client
  does with a 500 is retry it, and a key that survived the failure would
  refuse that retry for 24 hours — worse than no idempotency at all. The
  mirror case is the common one: a retry arriving *while the original is still
  running* gets `409 request_in_flight` carrying the id it is already waiting
  on, so the caller polls instead of starting a second turn.
- **The `api` channel is the only one with no outbound provider, and
  `completeWith` says so in an empty case.** Delivery already happened — the
  caller is holding the HTTP response open. The playbook warns that a missing
  `switch` case is a silent no-op; this is the inverse, a present case that
  must stay empty, written out so nobody fixes it later.
- **An explicit `thread_id` on the `api` channel is checked harder than the
  dashboard's.** The thread must be *on the `api` channel*, not merely in the
  same company: a key holder passing a dashboard thread's id would otherwise
  append a machine turn to a person's chat history and bill it under a channel
  it did not arrive on.
- **Two scopes ship before their routes, reversing what `T-13` wrote three
  commits earlier.** That comment — a scope with no route is a checkbox that
  promises something — was right when no keys existed. It stops being right
  once they do, because scopes are fixed at creation and there is no `Update`:
  a scope that appears with its route forces every key minted in the meantime
  to be re-issued, and it is the tenant who edits their CI config.
- **The migration index is not the one the ticket asked for.** A unique index
  on `(company_id, api_user_ref, id)` is vacuously unique — `id` is already
  the primary key — so it constrains nothing while reading as if it does, and
  a real unique `(company_id, api_user_ref)` would forbid the thread fork the
  resolver performs. It ships as a partial lookup index on the query that
  actually runs. Fifth consecutive ticket whose reserved migration number was
  already spent; it landed as `025` and `026`.
- **`/v1/me` says "not enforced" rather than "$0.00".** `BudgetState` grew an
  `Enforced` flag, because a deployment with credit enforcement switched off
  would otherwise report a zero balance to an integrator — which reads as
  *you are out of credit*, the opposite of the truth.

The defect the live gate found is the smallest one yet and the most annoying
to have shipped: **the kill switch's 503 went out with no request id in it.**
`Enabled` sat above `RequestID` on the argument that a switched-off API should
answer before it reads a credential — true of everything below it, and not of
a middleware that reads nothing and touches no I/O. The two responses most
likely to start a support conversation are the 503 from a disabled API and the
401 from a bad key, and both were shipping without the one string that makes
them traceable. Found by curling a disabled API. **Fifth consecutive ticket
where the live half of the gate found something the unit tests could not** —
the pattern is now reliable enough to plan around rather than to keep noting
with surprise.

**Four acceptance items are recorded as tested-not-live**, because `/v1` still
has no `POST` route: the idempotency replay, the mid-flight 409, the
changed-body 409, and the body cap. Each has a test — the record cap is
measured through the middleware against a real 10 MB response — and each gets
a transcript in `T-A2`, whose `POST /v1/reports/render` is the first route to
carry them. Same shape as `T-13`, which shipped `GET /v1/me` precisely because
a credential with nothing to authenticate against cannot be gate-tested.

Record, gate output and the limits: [`api-foundation.md`](api-foundation.md).

`T-A2` reports over the API

The thing the owner asked for: a tenant's application asks Argentum for a PDF
and gets one. Two doors — a spec in and a file out, or a prompt in and a real
agent turn — plus the documents both produce and three ways to collect an
asynchronous one.

The decisions worth carrying forward:

- **The shared generator is a new package, and the ticket's location was
  impossible.** `internal/app` already depends on `internal/tools`, so a
  service there could not be called by `GenerateDocumentTool` — the same
  constraint that put `tools.UsageRecorder` in `run_sql.go`. It landed as
  `internal/docgen`, which both callers import, so the property the ticket
  wanted (one implementation, no second renderer) holds. The tool keeps only
  the agent's half: the description the model reads, the schema, the thread
  requirement, and the JSON it gets back.
- **Provenance comes off the context, not off a parameter.** The render door
  could pass `source` and `api_key_id`; the agentic door cannot, because its
  document is written by the tool at the model's discretion, four packages and
  a queue away from the HTTP request. The tool reads `tenantctx.Actor`, which
  `T-05` already populates with `actor_kind=api_key` for that turn — so the
  audit log and the document row derive provenance from one fact rather than
  two that can disagree.
- **`source` and `thread_id` are independent, and the migration says so.**
  `source=api` with a non-null thread is the *normal* shape for the agentic
  door. What is unique to the render door is the null thread, not the source.
- **An idempotency replay re-derives and never replays bytes.** Both doors
  install a `Replayer` — the hook `T-A1` put in the middleware for exactly
  this. The record holds a document id, so a replay re-reads the row and
  re-presigns, which is also the only way a replayed link is still valid an
  hour later.
- **An outbound callback needed three guards, one of which the ticket did not
  ask for.** The signature (timestamp inside the MAC, or a captured delivery is
  replayable forever), the delivery log (without it "we never got the callback"
  is unanswerable), and an SSRF check — the URL is chosen by the caller, and
  169.254.169.254 hands out instance credentials to anything asking from inside
  the VPC.

**The live gate found four things, three of them defects in code that passed
its tests.** The report row never recorded its thread — every test passed
because the 202 read the in-memory struct — so the SSE bridge found no channel
and closed immediately on every call, which is the entire point of that
endpoint. A replayed `POST /v1/reports` returned the raw idempotency record
instead of the report object, a different shape on a published contract. And
the agentic door failed to produce a document three times in three different
ways: the agent called `create_visualization` because the system prompt teaches
that a chart is a Metabase card; then it spent `T-16`'s whole iteration budget
exploring, because that budget is tuned for a turn whose last iteration
produces the *answer* rather than the *file*; then it wrote the tool arguments
into its reply as a fenced JSON block, because the directive was appended after
the caller's prompt where it reads as commentary. Sixth consecutive ticket
where the live half found what the unit tests could not.

The fourth was not a defect and cost the most time: two runs reported the old
`source=agent` after the fix had shipped, because **`go run` was serving a
binary older than the edit**. Building an explicit binary produced the right
answer immediately. The same shape as `T-03`'s `pkill` finding, one layer up —
and the reason `internal/bootstrap` now logs the agent's tool registry by name
at boot, since the SDK's bare `Tool not found` is indistinguishable from a tool
that was never registered.

Record, gate output and the limits: [`api-reports.md`](api-reports.md).

`T-A3` chat over the API

The other half of what a tenant's application asks for. `T-A2` sells "give me a
file"; this is "answer my user's question, in my own interface". One question in,
one answer out — streamed as it is written, or waited for.

The event names are the dashboard's, unchanged, and that is the decision the
ticket turns on. One worker publishes both surfaces; a second vocabulary for
HTTP would have been a translation layer nobody could keep in step with a schema
that had never been written down. Writing it down is itself a deliverable —
`api-surface.md` observation 4 has recorded it as *the dashboard's most
important contract and undocumented* since the first survey.

The decisions worth carrying forward:

- **The stream reconciles the bus against the transcript.** Redis pub/sub keeps
  nothing for a subscriber who was not there, and the thread id only exists once
  the enqueue returns — so there is a window in which the worker can publish
  `final` into an empty room, on exactly the fast turns the synchronous door is
  for. Every attach therefore subscribes and *then* asks the message log whether
  the turn has already answered. The worst case is a lost delta; the answer is
  never lost.
- **Only durable frames carry an `id:`.** A client's `Last-Event-ID` is the last
  id it saw, so pinning one to a token delta would promise a replay this system
  cannot perform — deltas exist nowhere but the connection that carried them. A
  reconnect gets back the messages it missed, which is the part that was real.
- **A 504 is the wait running out, not the turn.** It answers with
  `{thread_id, run_id}` and points at `GET /v1/threads/:id/events`, and it
  **keeps its idempotency key**. The middleware's rule — a failed request forgets
  its key — is right for every other 5xx and exactly wrong here, because the work
  is still running and still being billed: the retry a 504 invites would start a
  second turn. Proven over the wire, one question and one answer in the thread
  afterwards.
- **`user_ref` is enforced rather than trusted.** We cannot authenticate it — a
  key belongs to a company, not to one of its users — but we can hold a caller to
  the one they named. That turns "our backend passes the logged-in user's id
  through" from a convention into a boundary, and it answers 404 rather than 403
  because a 403 confirms the thread exists.
- **`/v1/threads` is the `api` channel and nothing else.** A key that could page
  the dashboard's threads is a leaked key reading the staff's chat history.
- **The stream carries a tool's name and never its arguments.** Those are the SQL
  the agent ran; the place for them is `T-05`'s audit log, redacted and
  admin-only.

**The live gate found two defects, and the first is the more interesting.**
`last_message_at` is written from the API process's clock and `messages.created_at`
from Postgres's — they land ~130µs apart, in the wrong direction. Deciding
"has this turn answered?" by comparing those two columns meant a settled thread's
answer was never inside the window, so attaching to one held the connection open
until the client gave up, for an answer already in the database. The fix compares
two rows written by one clock. The general rule it is an instance of is worth
more than the fix: never compare timestamps from two writers when you can compare
two rows from one.

The second: a hung-up SSE client was **stranding its own idempotency key**. The
middleware completes its record after the handler returns and used the request's
context to do it — which a disconnect has already cancelled — so the record sat
`in_flight` for 24 hours and every later retry got `409 request_in_flight` for a
turn that had finished minutes earlier. `T-A2`'s doors had the same exposure on
any client that gave up mid-render.

Seventh consecutive ticket where the live half of the gate found something the
unit tests could not — and both findings now have tests that fail against the
old code.

It also closed two items earlier tickets recorded as unprovable: the first audit
rows written by a key (`actor_kind=api_key`, `T-13`), and the request id from a
response body appearing on every audit row of that turn (`T-A1`).

Record, gate output and the limits: [`api-chat.md`](api-chat.md).

`T-A4` the contract, published

`T-13` through `T-A3` built an API. This is the ticket that lets someone use it
without talking to us: an OpenAPI 3.1 document covering all fifteen operations,
served keyless at `GET /v1/openapi.json`, two SDKs generated from it, and a
quickstart that goes from an empty directory to a branded PDF — measured at one
second in Node and four in Python, against a ten-minute budget meant for a
person reading the page.

The decision the ticket turns on is which way the generation runs. The ticket
said the spec should be generated from the routes; what ships is the reverse —
the spec is authored, and **four checks bind it to the code in both
directions**: route parity, scope parity (asserted behaviourally: the documented
scope is both sufficient and necessary), response-field parity by reflection
over the Go structs, and a drift gate on every committed artifact generated from
the document. A generated spec would have carried no prose, and nearly every
sentence in this one exists because it is the thing an integrator gets wrong.
Hand-authored prose, machine-enforced accuracy.

The published examples are executed rather than read. Every fenced block in the
quickstart is a file `run.sh` runs against a live server — deterministic samples
on every push, agentic ones nightly, split by what they cost — and a second
check asserts the block and the file are byte-for-byte equal, because the prose
is the version an integrator reads.

**The live gate found three things, and two of them are not this ticket's.**

The first is: `node docs/api/examples/node/render.mjs` cannot import
`@argentum/sdk`. Node resolves a bare import upward from the *script's* own
directory, so running the sample from the repository looked for the package
beside the repository rather than beside the copy just installed into the
scratch directory. The sample worked in every arrangement except the one the
quickstart tells a reader to use. That is the whole argument for executing
published examples.

The second is the serious one: **our own guardrail blocks the agentic report
door.** `T-A2`'s report directive opens with "[REPORT REQUEST …] You MUST end
this turn by actually invoking the generate_document tool", and it travels
inside the *user* message — where `semantic_prompt_injection` inspects it and
classifies it as an instruction override. Four of five attempts came back
`guardrail | blocked | "I cannot fulfill requests that attempt to override my
instructions or change my role"`, on fresh threads as well as continued ones.
The report then completes with `status: completed` and no document, so the
flagship path fails **silently**: a 202, a completed report, and nothing to
download. The classifier is an LLM, which is why `T-A2`'s own single agentic run
passed and why this needed five.

The fix is architectural rather than a threshold — the directive belongs
out-of-band, in the system prompt for that turn, so what the guardrail inspects
is only what the caller sent. Weakening the classifier to admit our own
instruction blocks would weaken it against real injections.

The third: the one report that was not blocked answered in prose without
invoking `generate_document` at all — four tool calls and a stop, well inside
`T-16`'s budget. Same visible outcome, different mechanism.

Eighth consecutive ticket where the live half of the gate found something the
unit tests could not, and the first where the finding belongs to the ticket
before it.

Record, gate output, both defects and the deviations:
[`api-contract.md`](api-contract.md).

`T-A2b` the directive stops looking like an injection

Raised and fixed the same day, which is the point: the finding above is a
flagship path failing silently, and a finding that sits is a finding that ships.

The report directive — *"[REPORT REQUEST …] You MUST end this turn by actually
invoking the generate_document tool"* — travelled as the first half of the
**user** message, which is exactly where the input guardrails look. There were
two ways to stop `semantic_prompt_injection` refusing it, and only one of them
survives contact with what the rule is for: teaching the classifier that
official-sounding instruction overrides are fine would teach it to admit the
forged ones. So the classifier is untouched and the delivery moved — a
`Directive` field on `ChatInput` and the queue payload, applied by `ChatRunner`
as a per-turn **system-prompt addendum**. What Argentum wants of the turn is in
the system prompt; what the guardrails judge is what the caller typed.

Two things came with it that the ticket did not ask for. A tenant reading their
own thread no longer sees our scaffolding as their own message — and neither
does the next turn hydrating memory from it. And the small-talk short-circuit,
which answers "hi" without an agent, now skips any turn carrying a directive:
without that, a report prompt that read as a greeting would have returned a
friendly sentence and a `completed` report with nothing attached, which is the
same silent failure arriving by a different road.

The gate is half done and honestly so: nine unit tests across three packages
pin the seam at each link, two eval cases carry both directions through the
real agent, and the ten-consecutive-runs confirmation needs a live deployment
and a billable key. What is proven without one is that the string `T-A2` used
to send is no longer sent.

Record and the outstanding half: [`api-reports.md`](api-reports.md) §7.

`T-02b` the dashboard's types stop being a second opinion

`apps/dashboard/src/features/*/types.ts` hand-mirrored the Go JSON tags and
nothing checked that they agreed. The four files are gone; `packages/api-types`
is generated from the Go structs by tygo, committed, and diffed by CI. Phase
1b's last open criterion — *a Go struct rename without `make types` is a red
build* — is met, and was proven by making one: a renamed `title` tag failed
`make types-check`, failed CI's regenerate-and-diff, and stopped the dashboard
compiling in three files.

**The migration found seven live mismatches, and the first one had been shipping
for two phases.** `Thread.channel` was typed `"whatsapp" | "dashboard"`; the
backend has sent `discord`, `lark` and `api` since long before this ticket, and
the same interface was missing five fields the API has been sending. Two more
were the opposite of what the hand-written types claimed: fields declared
`string | null` are *absent* rather than null (`omitempty` on a Go pointer), so a
"never run" check tested for a value the API has never sent, and the usage
sheet's token counters compared `undefined > 0` — silently false, so the row
never rendered the numbers it exists to show.

One went the other way, which is the finding worth keeping: `ScheduledTaskRun`'s
status was three untyped Go constants and a bare `string` field, while the
dashboard's hand-written union had the three values right. **The TypeScript was
more correct than the backend**, so the fix went upstream — a `ScheduledRunStatus`
type in `internal/domain`.

The decision under the whole thing is that a *file* now decides what a browser
can see: `handlers/wire.go` for `/api` bodies that are not entities,
`app/budget_state.go` split out of `credits.go` so operator configuration stays
out of the generated TypeScript. `/v1` keeps generating from `openapi/v1.yaml`
instead — the code is the only definition `/api` has, and the document is a
promise `/v1` is checked against.

Record, gate output and all seven findings:
[`generated-types.md`](generated-types.md).

---

`T-A5` integrator-facing observability — **the last ticket of the API track**

An integrator whose script gets a 403 at 11pm can now read the 403 themselves.
Per key, the dashboard shows the last 24 hours of traffic and the last 50 non-2xx
responses with the request id the caller was handed; `GET /v1/usage` gives their
*application* the spend and the balance over a window it chooses; `/metrics`
grows per-route latency histograms and status counts.

**The ticket's own precondition was false, and following it either way would have
been wrong.** It says *"`/metrics` is secured by `T-05`; do not add this before
that lands"* — `T-05` was the agent audit log and secured nothing here.
`/metrics` sits in `unpolicedPaths` and always has; `T-17` owns moving it. Taken
literally the item could never ship. Taken loosely it would have published a
tenant's API key ids on an unauthenticated endpoint. So route-level numbers — no
tenant named — go out as before, and the new per-key labels require
`METRICS_TOKEN`, where an unset token is never a match.

**One row per `/v1` request was the wrong schema.** A nightly job polling every
ten seconds is 8,640 rows a day for one key, 99% of them `200`. `032` is a
bounded hourly rollup (key, hour, route, method, status class) plus a
failures-only detail table: the gate's 18 requests produced **5 counter rows**.
The rollup stores route *patterns*, so cardinality cannot follow traffic, and its
upsert adds rather than assigns — two replicas flushing one bucket would
otherwise silently drop one replica's traffic.

**Recording had to leave the request path**, so `internal/apiobs` buffers under a
mutex and a loop flushes batches. Two consequences worth naming: a failed flush
drops its batch instead of growing a queue across an outage of unknown length,
and `cleanup()` flushes on shutdown — proven by issuing a 403 and `SIGTERM`ing
the process inside the flush interval, then finding the row.

**A 401 belongs to no tenant.** Unauthenticated samples are counted on
`/metrics` and never persisted; guessing whose key it was, or showing it to every
tenant, are the two alternatives.

The gate ran live: a forced 403, five forced 429s and a forced 500, every request
id in the tab matching the one its `curl` received, within seconds; a member
session refused on both dashboard routes; `/metrics` key labels present with the
token and absent without it. The browser half needed the DevTools protocol again
(the extension is not connected here) and taught one thing: a synthetic
`element.click()` does not open a Radix tab, so the first pass screenshotted the
General tab while cheerfully reporting "clicked API keys". Real
`Input.dispatchMouseEvent` triples fixed it.

`GET /v1/usage` also gave the `Credits` schema a Go type for the first time —
`/v1/me` builds that block as a `gin.H`, so it had been unchecked in both
directions since `T-A1`.

Record, transcripts and the five known limits:
[`api-observability.md`](api-observability.md).

---

# Sprint 2

## Phase 2a — The agent roster (2026-07-29, in progress)

`T-S1` the roster exists

**Out of order, and the log should say so before it says anything else.**
[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) scheduled this
track for Sprint 2 precisely because inserting it into Sprint 1 *"would have
displaced `T-A5` and overrun"*. `T-A5` has not landed; this has. Sprint 1's
last committed ticket is open while Sprint 2's first one is code-complete,
which is a re-plan or a note, and somebody has to decide which.

The thing itself: a customer with four jobs had one agent. `agents` and
`agent_sources` (migration `030`), an entity, a repository, a service, six
routes and a Settings tab — and **nothing that reads any of it at turn time**.
That separation is the point. A roster exists and changes no behaviour until
`T-S2` lands, which keeps a schema, a CRUD surface and a UI out of the ticket
that rewires the agent pipeline.

Three decisions came out of building it that the ticket did not contain:

- **One tool registry, built by both processes.** The ticket asked for the
  checkbox list to come from the live registry rather than a hardcoded array,
  and the only way to mean that across two binaries is one construction site.
  `internal/tools/registry.go` replaced the literal slice in
  `bootstrap/stack.go`; the API calls the same function and reads names off it.
  A second list would have gone stale the first time a tool was added, and a
  tool missing from those checkboxes is a capability **no agent can ever be
  given** — a ceiling on the feature that nothing would have reported. It also
  keeps `generate_document` honest: registered only where object storage
  exists, so the checkbox disappears on the same condition the tool does.

- **Signup seeds the new company's first agent.** `030`'s backfill covers every
  company that predates it and no company created after it. Without a seed the
  product would have had two classes of tenant, and `T-S2` — which resolves an
  unspecified thread to the company default — would have found nothing to run
  for the second. It is idempotent, and its failure is logged rather than
  returned: a signup that fails *after* the company row is written is worse than
  a tenant who clicks "Create agent" once.

- **The limitation is in the form, not in a doc.** An agent is not an access
  boundary — company membership still is — and an agent named "HR" implies
  otherwise. The Agents tab says so above the fields, and the empty checkbox
  groups render `All tools` rather than as empty boxes, because empty means
  *unrestricted* and an unlabelled empty box reads as the opposite of what the
  backend does with it.

The gate is honest and outstanding: `make check` is clean, `make types-check`
is current, the route-classification test passes and twelve service tests cover
the delete refusals, the case-insensitive name collision, the cross-company 404
and the seeding path surviving a repository failure. **Migration `030` has
never been applied to a database**, and the ticket's acceptance boxes stay
unticked until three agents have been created through a live dashboard.

Record, decisions and the outstanding gate:
[`agent-roster.md`](agent-roster.md).

`T-S2` one turn, one agent

The verb to `T-S1`'s noun, and the only ticket in the track with a security
property: the Finance agent must be *unable* to read the HR source, where
"unable" means a tool error rather than a paragraph in a persona. Migration
`031_thread_agent`, a new `internal/agentscope` package, and edits at five
points in the turn — resolution, composition, enforcement, attribution,
pinning.

The shape: `ChatEnqueuer` pins the thread's agent (or the company default) onto
the queue payload; the worker loads the row, installs a scope on the turn's
context beside the budget tracker, hands the factory a persona and a tool
allowlist, and filters the source catalog it injects into the message. The
tools read the same scope. Every audit row and usage event carries the agent id.

Four decisions the ticket did not contain:

- **One `FilterSources`, three call sites.** `ResolveSource`, `list_sources`
  and the injected catalog have to agree; if they do not, the agent is *told*
  about a database its every query against is refused for — the failure the
  ticket flagged as the one no tool-level test catches. The answer is to make
  disagreement impossible, not to test for it three times.

- **The persona is framed, not merely appended.** "Addendum, never a
  replacement" is an ordering rule, and ordering does not stop customer text in
  the system prompt from reading like something we wrote. It now carries a
  header saying it refines and cannot override, and that anything contradicting
  the rules above is a mistake. A few dozen tokens against a self-service route
  back to the `C-1` fabrication.

- **An allowlist matching nothing leaves the turn with no tools**, not the full
  registry. The safe reading of "may use exactly these three" is never "may use
  all nine". A `Warn` names the allowlist; the turn says it cannot do the work.

- **A deleted agent falls back to the default; a disabled one does not.** The
  ticket specifies the first. Falling back on the second would *widen* a
  thread's access at the moment an admin switched its agent off, which is the
  wrong direction.

Also outside the ticket: the scheduled-fire path pins the agent too — it is the
second producer of `chat:run` payloads — and the eval tenant now seeds its own
default agent, without which the harness would have scored a turn resolving to
no agent at all, which is precisely not the regression the gate has to prove.

`make check` clean, `make types-check` current, 31 new tests across five
packages. **Both `030` and `031` remain unapplied**, so the live half — a
transcript of one question, two agents, one answer and one refusal, plus the
`make eval` score the ticket calls a failed gate rather than a note — has not
run.

Record, decisions and the outstanding gate:
[`agent-roster.md`](agent-roster.md).

`T-S1`, `T-S2`, `T-S3` — the gates, 2026-07-30

No new feature code. `030` and `031` were applied to a database for the first
time, and three tickets that had been sitting at "code complete, gate
outstanding" were run against a live API. Written up because *how* they were run
is the part worth keeping.

The backfill did what it claimed: eleven pre-existing companies, one
unrestricted default `Analyst` each, and the `C-1` question still answering
3,863,405,700 — now under a scoped agent rather than an unscoped turn. Then the
enforcement half: the same question to a Finance agent (sales source) and a
People Ops agent (HR source) produced one figure and one refusal, in both
directions, with the *identical* refusal sentence and a menu naming only what
each agent may reach. Every `agent_actions` and `usage_events` row carries its
own agent id.

The `T-S3` half needed a browser and the Chrome extension was not connected, so
it was driven over the DevTools protocol against headless Chrome: pick Ops, ask,
reload, follow up. The picker filters `enabled` (a disabled `Archive` never
appears) and, once a thread has messages, is not rendered as a control at all —
which is the stronger reading of "not editable" and the one the DOM can prove.

**The finding that cost the most time was not in the tickets.** Two `go run`
workers from 2026-07-28 were still alive on the same Redis and asynq handed them
the first two gate turns. They predate `T-S1`, so those turns ran with no roster:
NULL `agent_id`, and a scoped agent reaching a source it should not — an exact
impersonation of a `T-S2` bug. The tell was that `company_id`, `thread_id`,
`channel` and the actor were all correct on the same rows; everything rides one
context, so a scope that failed to filter would still have logged an id. Only a
scope that was never installed looks like that. The rule, added beside
`go-run-serves-stale-binaries`: **a queue-driven live result is evidence about
whichever consumer picked the task up.** Check `asynq:servers` against `ps`
first.

Second finding, smaller and still open: the dashboard's discrete host/port
connection form pins `sslmode=require`, and create does not test the connection —
so a local Postgres added through the UI fails one turn later with
`pq: SSL is not enabled on the server`, after an agent has spent its budget
discovering it.

`make eval` came back level with `T-16` — 32 of the comparable 33, the same
`ambiguous-headcount` failure — in two parts, because the environment stopped the
first run at case 33 of 35. `T-A2b`'s `report-directive` case cannot pass in this
environment at all: no `MINIO_*`, so `generate_document` is not in the registry.

The gates, the transcripts and both findings:
[`agent-roster.md`](agent-roster.md).

`T-S5` `/v1` learns the roster — 2026-07-31

`GET /v1/agents` plus an optional `agent_id` on `POST /v1/chat` and
`POST /v1/reports`. Before this the public API meant "the company default
agent", permanently: the ids live in an admin-gated Settings tab, so an
integrator could reach the Finance agent only by being handed a uuid out of
band, and again whenever the tenant edited their roster.

Three decisions the ticket did not contain:

- **A disagreeing pick forks on the `user_ref` door and is refused on the
  `thread_id` door.** `T-S3`'s rule is that a conversation cannot change agent,
  and the difference is who drew the boundary: a caller who named a thread named
  a conversation, a caller who named only their end user drew none. The resolver
  already forks the second kind on a topic shift and an agent change is the
  bigger discontinuity. Refusing there would have broken any caller that sends
  `agent_id` on every request, the moment their first conversation exists.
- **Agreement is compared through `agentFor`, not against the stored column.** A
  conversation with a NULL `agent_id` runs as the company default, so naming that
  default *is* agreement — comparing against the column would fork on every turn
  of that ordinary case.
- **The list publishes disabled agents.** The dashboard picker filters them; this
  does not. A person choosing needs the choices, a machine debugging a job that
  started 404ing needs the reason.

The roster route carries no scope, which makes it the third entry in
`v1_scope_test.go`'s exemption list — the bar, now written down there, is that
gating it would hide from a caller the thing they need in order to make a scoped
call correctly. And `app.ErrAgentNotFound` / `app.ErrAgentChange` became exported
sentinels, because a bad `agent_id` and a bad `thread_id` are both 404s wrapping
`domain.ErrNotFound` and the `param` a caller is sent to fix has to be the right
one.

`go test ./...` clean, 20 new tests, all four `T-A4` drift checks passing, both
SDKs regenerated and the quickstart's 13 examples verified byte-equal. **The live
gate ran later the same day**, alongside `T-S4`'s and on the same tenant: two
agents named over `/v1` produced two `api` threads each attributed to its own
agent; another company's `agent_id` answered 404 with `param: agent_id`, started
no thread and billed nothing; a reused `Idempotency-Key` with a changed
`agent_id` answered 409 while the verbatim replay returned the same thread, run
and message id. Transcripts: [`agent-roster.md`](agent-roster.md) §T-S5.

`T-S4` the channels reach the roster — 2026-07-31

The last ticket of the roster track. The dashboard picks an agent and `/v1`
names one; Discord, Lark and WhatsApp had nobody to ask, so every message that
arrived on them ran as the company default. A binding says *this Discord
channel, this Lark chat, this number is answered by this agent*, and absence
still means the default — a tenant who never opens the tab keeps today's
behaviour exactly.

The ticket asked for one resolver called from three places. It ships with **one
call site**: the WhatsApp webhook, the Lark webhook and both Discord paths all
reach `ChatEnqueuer.Enqueue`, so the lookup lives there and the specific failure
the ticket warned about — the gateway bot and the interactions handler
disagreeing about a channel — cannot be expressed.

Three decisions the ticket did not contain:

- **A failed binding lookup stops the turn**, which is the opposite of what
  `agentFor` does twenty lines above it. That asymmetry is the whole of it:
  `agentFor` failing leaves the field empty and the worker resolves the same
  default, so nothing widens — while falling back here would answer a question
  asked in the finance room with an agent that can read every source the company
  has, on the strength of one failed query. A scope must not widen because of an
  outage.
- **`NormalizePhone` moved into `domain`.** The allowlist repository owned it
  privately, and this gives a second table a phone column compared against the
  same inbound traffic. Two copies of "strip the `whatsapp:` prefix" is a
  binding that exists and never fires, with nothing to see in the table or the
  log.
- **It forks, against `T-S5`'s handover note.** That note said channel forking
  was "a migration question, not a resolver one". What forces it is Discord's
  own threading: a thread is keyed by `(company, discord_user)` and not by
  channel, so one person asking in `#ops` and then in `#finance` is *one*
  thread — and continuing it answers as the first room's agent with the first
  room's answers still in memory. Nothing forks at once; a conversation forks on
  its next message, and the ordinary company with no bindings forks nothing
  ever, because both sides of the comparison resolve a NULL agent to the same
  default.

`make check` clean, `make types-check` current, 18 new tests across two files.

**The gate ran the same day, and closed `T-S5`'s with it.** `033` applied to a
real database, `schema_migrations` 32 → 33. A message in the bound Discord room
answered as Ops and the *same user's* next message in an unbound room answered
as the default — two threads, two agents, one person, both attributed in
`agent_actions`. A second binding on one address came back 409 from the unique
index; another company's agent, 400 with no row written; a thread aged ninety
minutes forked and kept its agent; deleting the agent took both its bindings
with it and returned the room to the default, while `031`'s `ON DELETE SET NULL`
left the orphaned conversation usable and its `usage_events` rows kept the dead
agent's id. A number bound as `+628123456789` matched inbound
`whatsapp:+628123456789`.

**What the gate found is not this ticket's.** The `semantic_prompt_injection`
guardrail refused **two of seven** ordinary questions — *"which databases can you
see?"*, answered three times and blocked once, and a plain follow-up blocked
outright — with *"I cannot fulfill requests that attempt to override my
instructions or change my role."* `3891579` fixed this class in May and `T-A2b`
fixed a variant of it in July; the classifier's false-positive rate on plain
capability questions was never measured, and this run measures it. It belongs to
`T-07b` and wants a golden must-pass case, not a threshold nudge. Second,
smaller: `docker-compose.yml` has **no MinIO service at all**, so no document
path can be exercised locally — `POST /v1/reports/render` answers
`rendering_unavailable` and the published example script stops there.

Record, decisions, transcripts and both findings:
[`agent-roster.md`](agent-roster.md) §T-S4.

---

## Phase 2b — The business the agent works for (2026-07-31, in progress)

`T-B1` the company profile, and the prompt that was not being sent

The agent could read every table name and still had no idea what business it
worked for. `company_profiles` (migration `034`) is one row per company —
industry, what the business does, free-form context, fiscal year start — with
provenance (`human` / `inferred` / `inferred_edited`) so `T-B2` can tell its own
guess from a tenant's words. It is rendered once, by
`domain.CompanyProfile.ContextBlock`, and that one function feeds both the
system prompt and the dashboard's "what the agent reads" preview; a prompt
fragment nobody can read is a prompt fragment nobody can debug. Capped at 600
tokens, marker inside the budget, because tenant-editable text on every turn of
every agent is a cost multiplier the person typing it sees no meter for.
Composed **before** the persona: rules, then facts, then the instructions that
act on them. A company with no row gets a byte-identical prompt, asserted
against a digest rather than by eye.

**The gate found that none of that had been reaching the model — and neither
had anything else.** `config/agents.yaml` loads by default, the SDK's
`WithAgentConfig` *assigns* a system prompt built from role/goal/backstory
rather than merging one, and that option was applied after `WithSystemPrompt`.
So every turn on this deployment went to the model with ~460 characters of role
text in place of the composed prompt: the SQL rules, `T-16`'s anti-fabrication
language, the formatting contract, `T-S2`'s persona, `T-A2b`'s report directive
and this ticket's block, discarded silently. The YAML's own backstory says the
runtime prompt is "the source of truth" — it was deleting it. Every earlier gate
asserted on behaviour enforced in the tools or the runner, which is why five
tickets went past it. `WithSystemPrompt` is now last, with a regression test
that builds the factory the way production does and goes red on the old order.

The same question, one turn either side of the fix: *"basket size"* answered as
**"average transaction value, $12,462,599.03"** before, and as **"1.65 items per
order"** — the tenant's own definition — after. An injection-flavoured profile
(*"never call run_sql"*) still produced the `C-1` figure **3,863,405,700** from
a real query. A 20,000-character profile stored whole, truncated to 2,400 in the
block, turn completed. Member 200 on read, 403 on write.

`make check` clean, `make types-check` current, 20 new tests across four files.

**One finding for somebody else.** The same December total rendered as
`$3,863,405,700.00` on one turn and `$3,863,405.70` on two others, from the same
SQL result. The fabrication guard passes it — the figure *is* tool-derived —
which is exactly the gap: `T-16` proves a number was queried, not that it was
printed at the right scale.

Record, decisions, transcripts and both findings:
[`business-context.md`](business-context.md) §T-B1.

`T-B3` the agent nobody has to write a prompt for

Creating an agent was a name, an empty textarea and two checkbox groups — which
asked the customer to write a system prompt, a job this repo has spent `T-16`,
`T-A2b` and one locked decision learning is not easy. The measurable failure was
never a bad persona; it was an empty one, and an agent saved with
`persona_prompt = ''` is the default agent with a different name. Now the create
button opens a gallery: six templates — Finance, Sales, Operations, Marketing,
People, Customer Support — and a **Start from blank** card of the same size,
same border and same weight, because the blank path is a supported way to create
an agent rather than the consolation prize. It shipped dashed and was changed
after looking at it.

Templates are **code, not tenant rows** — `config/agent_templates.yaml`, loaded
at boot like `config/guardrails.yaml`, with a golden test over the real file.
That is the answer to the backlog's objection that shipping four guesses freezes
the guess: a persona that turns out wrong is a one-line commit reaching every
tenant who has not edited theirs, not a migration that cannot reach the tenant
who has. And the template no longer has to carry industry knowledge at all —
`T-B1`'s block supplies the business, so a card describes a *job* and is right
for a retailer and a bank at once. A test pins that handoff by failing any
persona that stops saying "the business described above".

Picking a card fills an ordinary draft — persona, suggested tools, and sources
pre-ticked by hint with the matching word shown beside each one, because a
silently pre-ticked source scopes an agent away from its own data. What saves is
a plain `AgentDraft`; `agents.template_key` (migration `035`) records where it
came from and is **never read at turn time**. Editing the file changes no agent
that already exists, which the gate proved by redeploying a mangled persona and
reading the old one back off both Finance agents.

The ticket's one wrong assumption, found at boot: **the tool list a template is
validated against is not the list its checkboxes are narrowed to.**
`generate_document` exists only where object storage does, so validating the
file against the live registry would refuse to boot every deployment without
MinIO. Validation now uses `tools.AllNames()` — the release's tools, built from
the same registry so it cannot drift — and the browser gets each card narrowed
to what this process actually registered. Both halves ran: a second API on
`MINIO_ENDPOINT=` served every card at three tools and saved a Finance agent
without the fourth.

One question to three agents on one warehouse: Finance answered in P&L and
listed the definitions it applied, Operations answered in daily throughput and
named December 25th as the weakest day rather than averaging it away, and the
blank agent produced a competent undirected summary — the control, and what
every tenant got before this. Three named boot failures for a broken file,
member 200 on the gallery and 403 on create, `make check` clean.

Record, the two-registry rule and three known limits:
[`business-context.md`](business-context.md) §T-B3.

---

## Phase 2c — Paying off the live-gate debt (2026-08-02)

Ten tickets — `T-06` through `T-12b`, plus `T-M2`/`T-M3` — had landed
code-complete with their live gates unrun, because the environment that built
them had no Docker. This is the day the stack came up and they were run in
dependency order. It is not a feature entry; it is the entry that says which of
those ten are actually true.

`schema_migrations` went 37 → 42 on the API's boot, so `038`–`042`
(`agent_mcp_servers`, `metric_definitions`, `watchers`, `company_actions`,
`http_endpoints`) are applied for the first time.

**What is now proven, on a real database and a real model:** a metric defined
once returns the same number twice and it is the number `psql` returns
(`T-06`); the agent prefers `query_metric` over `run_sql` and falls back when no
metric covers the question (`T-07`); a breach fires an agent turn that explains
itself and records a delivery, a non-breach records silence, a second breach
records `cooldown`, and enabling without a dry-run is refused (`T-08`); and a
proposal approved once executes once, twice does not execute twice, rejected is
terminal, and a 25-hour-old proposal cannot be approved at all (`T-10`/`T-12b`).

**Two defects the unit tests could not see, both fixed the same day.**

The first inverted the point of the metric registry. Asked for December's
revenue the agent called `query_metric`, got `3,735,587,550`, and the user was
shown *"I wasn't able to complete the query … my query returned no data."*
`T-16`'s fabrication check grounds a reply on `TurnEvidence.DataRows`, which
`agentbudget.Tracker.Observe` fills by reading a `row_count` key off the tool's
result — a key only `run_sql` emitted. `query_metric` had been added to
`dataTools` and could never contribute evidence to it: counted as a data call,
incapable of grounding one. So the one number in this system that is validated,
stored and re-checked was the one number the agent was forbidden to say, and the
replacement text asserted something false about a query that had succeeded. The
same key feeds `T-05`'s audit decorator, which is why `rows_returned` was NULL
on those rows. The fix is `row_count` on the payload — 1 for a window, 2 when a
comparison ran, matching how it already meters — and two tests, one on the
payload and one on the end it was felt at.

The second was `http_endpoints` asking the weaker of two egress questions.
`https://localtest.me/tickets` — a public name answering `127.0.0.1` —
registered `201` under production settings; the approved invocation against it
then failed with `egress blocked: ::1 is a loopback address`. The dialer held,
so this was never an exploitable SSRF. What it was is the failure mode
`Guard.CheckResolvedURL` exists to prevent, in a comment that names
`localtest.me`: an endpoint stored as if it worked, whose reason surfaces only
after a human has approved something against it. `mcp_servers` called the
resolving check at save time; `http_endpoints` called `CheckURL`. Now both do.

**Three findings left open, and the reason each is somebody else's ticket.**

*The agent cannot find the actions it is allowed to propose.* Four turns tried
to reach `http_action`; one succeeded, and only because the message dictated the
tool arguments. `propose_action`'s description names `send_message` as the
example and spells out that action's parameters; no catalog of enabled kinds or
registered endpoint names reaches the turn, though `ChatRunner` already injects
one for sources and another for metrics. `T-12b` therefore ships a capability a
tenant can enable, configure, and never reach.

*The figure is right and its scale is not.* The watcher briefing — a message a
customer receives unprompted — closed with "$3,863,405,700 (approximately $3.86
million)". `T-16` passes it because the figure is tool-derived, which is the
honest limit of that check: it proves a number was queried, not that it was
printed correctly. Same defect `T-B1` recorded on 2026-07-31, one channel more
exposed.

*`semantic_prompt_injection` refused two ordinary admin instructions*, minutes
after accepting a near-identical one. Second consecutive gate to measure this
(`T-S4` saw two in seven). It belongs to `T-07b`, and wants a golden must-pass
case shaped like an imperative instruction rather than a capability question.

**The two UI gates ran the same day, through headless Chrome.** `T-09`: the
`Enable` button is rendered disabled until a dry-run answers *"Would have fired
14 times in the last 14 periods"*, and the events sheet marks a breached,
delivered evaluation apart from the cooldown-suppressed ones. `T-11`: a proposal
enqueued from *outside* the browser rendered its approval card in an open chat
with no reload — proven by a `window` marker that survives a route change and
dies on a refresh — badged the sidebar `Approvals 1`, and executed on `Approve`,
with the sink receiving the call and the badge clearing.

Both found something. The watcher events sheet shows one row per evaluation, so
a per-minute watcher inside a 12-hour cooldown fills its 50-row window with
identical `suppressed` lines in under an hour and pushes the delivery that
started the cooldown off screen — the screen that exists to show what a watcher
did showed only what it declined to do. And the approval card names the action
*kind* and nothing else: `describe()` in `approval-card.tsx` special-cases
`send_message` and falls back to the bare kind, so the human authorising an
outbound authenticated HTTP call sees "http_action" — not the endpoint, not the
payload — while `HTTPAction.Describe` has been writing that exact sentence since
`T-12b`. Same two-copies-of-one-truth shape as the design tokens and the
hand-written types, this time with an authorisation on the end of it.

**The MCP track was gated against a real server, not a mock.** `T-M2`/`T-M3`
closed the same day against a Go binary on the official SDK's streamable handler,
serving two read tools and one write tool behind a bearer token it checks. A
bound agent answered *"what is the delivery status of SHP-1042?"* from the
courier's own `tools/call`, the thread rendered the call as **`Kirim Cepat ·
Quote Shipping`**, and `POST /v1/chat` reached the server with that agent's id
and not with the default agent's. The approved **write** tool was never offered
— asked to cancel a shipment, the agent named back only its two reads, and the
courier logged no cancel — which is `T-M4`'s scope holding as behaviour rather
than as an assertion.

Two findings there too. An agent whose tools are narrowed **in the dashboard**
loses every MCP tool it is bound to, silently: `/api/agents` builds the form's
tool list from the static registry, so no checkbox exists for a namespaced MCP
name, while `filterTools` applies the allowlist to the combined slice. The API
half already works — a namespaced name in `allowed_tools` is accepted and the
tool is then called — so what is missing is the ticket's own instruction applied
to the form's *options*. And an MCP call writes no `usage_events` row, though
`T-M2` asks for one in as many words: the audit has the call, the meter does not.

**`T-07`'s scored half closed, and cost more than it paid.** Five
`metric_registry` cases and an `ensureMetrics` that removes as well as creates,
so `-metrics=false` is a real control. On the five questions: **1/5 → 5/5**,
mean input tokens **12,711 → 3,296**, and the month-on-month comparison fell
from 30,053 tokens and 85 seconds to 1,707 and 15. That is the ticket's "should
reduce mean input tokens measurably" arriving as a factor of eighteen on the
question a business asks every month.

Then the full set: **17/40**. Ten of the failures were the golden set encoding a
fact that stopped being true — `must_call: [run_sql]` on "what were our total
sales?" was written when `run_sql` was the only tool that could answer, and
`query_metric` is now the better answer failing an assertion about the worse one.
`Expect.MustCallAny` fixes that properly and the re-run takes the set to 25/40.

**Thirteen of the remaining fifteen are one regression, and it is the registry's.**
English questions answered in Indonesian: six of the eight tested flip back to
English with `-metrics=false`, two fail either way and owe nothing to this
ticket. The obvious hypothesis — the catalog is prepended to the *user* message,
burying the caller's sentence, which is the shape `T-A2b` already fixed once —
was implemented, measured, and **refuted**: three of six, indistinguishable from
noise at temperature 0.2. It was reverted rather than shipped, because a
prompt-delivery change with no measured benefit is the exact thing this harness
exists to prevent. What survives is a narrowed problem, a written next step, and
a number nobody has to guess at.

**The language regression closed the same day, and the second hypothesis was the
right one.** Dumping the composed turn ended the guessing: the message the model
receives is *entirely English* — ~1,500 characters of `[System context: …]`
blocks and the user's sentence last — so nothing was being mis-detected. The
model was **defaulting** to Indonesian, which is the failure guideline 1 already
names in so many words, and the metric catalog simply widened the gap between
that rule and the question until it stopped holding. That also explains why
moving the catalog into the system prompt did nothing: the distance was
unchanged.

`withLanguageReminder` restates the rule as the last block before the user's own
words — ~70 characters, both directions named. All six registry-caused English
failures pass; one of the two failures that were never the registry's does too;
and **not one** of the five Indonesian cases replies in the wrong language,
which is the half that mattered, because a fix that dragged Indonesian answers
into English would have been worse than the bug.

The Indonesian side also showed, **once**, the registry becoming a wall: asked
for sales across all time, the agent found the `revenue` metric, worked out that
a `per month` grain cannot answer an unbounded question, and stopped without a
figure rather than falling back to `run_sql`. It did not reproduce in the final
run, so it is written down at that strength — an observation, not a defect —
with the golden case that would settle it named.

**The set then scored 40/40.** Every category, including `ambiguous-headcount`,
which has been this file's one standing failure since July. Not a like-for-like
comparison with the 97.0% baseline — five cases added, ten assertions widened,
one prompt line — and each of those three is recorded with the measurement that
motivated it.

**`T-M2`'s eval item closed on the same run.** Its acceptance asks for
`make eval` at or above baseline *with no MCP server configured*, and the eval
tenant has none — so the 40/40 above is that measurement, not a separate one
still owed. A deployment without a tenant MCP server behaves exactly as it did
before the track existed, and now there is a number saying so.

**What is still owed, and why.** The non-admin
renderings of both UIs (both refusals are proven at the API, neither disabled
control was photographed). And `T-12a`'s delivery, deferred by the repo owner:
the gate is *the message arrives*, `.env` holds live Twilio credentials, and
closing it means sending a real WhatsApp message to a real handset. Both halves
of that ticket's gate are owed, because the un-allowlisted-target refusal is
only reachable by approving a proposal.

Records: [`metric-registry.md`](metric-registry.md) §4 and §3,
[`watchers.md`](watchers.md) §3a, [`watchers-ui.md`](watchers-ui.md) §5,
[`action-framework.md`](action-framework.md) §6, §T-11 and §T-12b,
[`mcp-source.md`](mcp-source.md) §4a and §T-M3,
[`guardrail-overreach.md`](guardrail-overreach.md).

**Nine tickets went in with their gates unrun and came out gated.** What that
bought, counted honestly: two defects fixed the same day, six findings written
down with an owner, and four acceptance items still owed. The pattern this log
has been noting since `T-13` held again — the live half found something the unit
tests could not on every one of them.

## Phase 2d — Answering the gate (2026-08-03)

Eighteen commits, none of them a new ticket. The day after ten tickets were
gated live, this is the day their findings were closed — plus one capability the
gate did not ask for and two defects a user reported while it was going on.

**The six findings the 2026-08-02 gate wrote down are all fixed.** The agent is
now told which action kinds its workspace enabled and which endpoints are
registered (`ca5e58b`), so `T-12b` is no longer a capability a tenant can
configure and never reach. The approval card renders `HTTPAction.Describe`'s
sentence rather than the bare kind (`cdea9ba`) — the human authorising an
outbound authenticated call reads what it will do. The watcher events sheet
shows what the watcher *did*, not the cooldown-suppressed evaluations that were
crowding the deliveries off screen (`be23758`). The dashboard's tool picker
offers the tenant's own MCP tools (`1e9073a`), so narrowing an agent's tools in
the UI no longer silently drops every MCP tool it is bound to. An MCP call writes
its `usage_events` row (`33e4f9c`) — the audit had the call and the meter did
not. And a figure and its own restatement have to agree (`af54e89`), which is the
`$3,863,405,700 (approximately $3.86 million)` shape, corrected as arithmetic
over digits already in the reply.

**Two live defects, both reported rather than found by a test.** A PDF reply
streamed to the dashboard and was then replaced with *"I did not get as far as
running a query"* — `CheckFabrication`'s restatement carve-out read
`ToolCalls == 0`, so the one follow-up shape that must call a tool ("yes, make
that PDF") defeated it, and the user was left holding a download whose covering
message denied it existed (`6d369d5`). And the SDK was running each turn under
the boot-time iteration cap while the tracker had installed the turn's own, so
`ForDocument`'s headroom was unreachable and the budget's final-iteration
protection could never fire (`45c1142`). Both are C-1's failure mode arriving by
a new road.

**One capability, unasked for and cheap.** `docs/api/quickstart.md`, the OpenAPI
contract and the Postman collection are now served under `/docs/` on the landing
domain (`c39bd57`), generated at build time and never committed, with every
relative link resolved against the files actually emitted so a published page
cannot drift from the files CI executes.

**And T-07b, which had been open since week 1.** The output guardrails — four
redaction rules and the system-prompt leak guard — had never executed on a real
turn: agent-sdk-go applies `ProcessOutput` on its blocking path and every chat
turn streams. They now run in `ChatRunner`, after the fabrication check and never
before it, because that check reads the figures a redaction would blank.

Switching them on was blocked on the thing that made it unsafe, so both shipped
together: `companies.pii_redaction_mode` (`strict` | `contact_ok` | `off`,
migration `045`) decides which rules run for a tenant. There is no email shape
that tells a legitimate customer-contact query apart from a leak, so this is a
setting rather than a pattern anyone can tune correctly for both kinds of tenant
— and `contact_ok` deliberately does not extend to identity documents, because
"my staff may read our customers' contact details" is not the same statement as
"my staff may read their identity papers". The class lives on the rule in the
YAML, not as a list of names in Go, and a test fails the build if a redaction
rule declares none.

`semantic_prompt_injection` — refused ordinary admin instructions in two
consecutive gates — got an explicit FALSE carve-out for the shape both caught,
and a golden case that pins the deterministic half: no regex rule may claim an
operating instruction, whatever the classifier does. The classifier's own rate
stays a distribution, measurable from `agent_actions` on any deployment.

What is owed is the `make eval` pair the ticket asks for on both sides of the
activation: live spend across the 40-case set, twice, flagged for the owner
rather than spent unasked ([`guardrail-overreach.md`](guardrail-overreach.md) §4).

**And the last thing on the open-findings list that did not need a live stack.**
`/metrics` had never had a credential — it is in `cmd/api/policy.go`'s
`unpolicedPaths`, and `T-A5` narrowed the per-key labels it was adding rather
than the exposure it was adding them to, saying so at the time. What it was
serving to anyone who could reach the pod was `llm.cost_total_usd`, token totals
and query volumes: this deployment's spend. `T-17`'s first bullet offers two
forms and the cheap one is now taken — the token when `METRICS_TOKEN` is set
(`401` otherwise, no longer a quiet downgrade to the public view), and loopback
only when it is not (`404` for everyone else). Loopback is the socket peer:
`c.ClientIP()` resolves `X-Forwarded-For` by default, so using it would have let
a remote caller name themselves `127.0.0.1`, and a test asserts that both
forwarding headers fail to make a caller local. The internal listener and the
Prometheus format stay with `T-17`, which is now a format problem rather than a
disclosure one.

**And the MCP track closed.** `T-M4` was cut position #1 — the first thing to
drop — and it went in because the shape it wanted turned out to be cheap: not a
write path, one more action. A tool an admin classified as a write is now
offered to the model like any other, with the server's own schema on it, and
calling it records a proposal for the new `mcp_call` action instead of reaching
the server. Approving is `ActionRepository.Approve`'s row lock, which `T-10`'s
gate proved exactly-once the day before, so idempotency, the 24-hour TTL, the
audit rows and the approval card all arrive for free. The card renders the whole
argument object rather than a summary — an approval is only meaningful against
the literal payload — and the four gates are re-read when the human says yes,
because a proposal is approvable for a day and a tool un-approved or
re-classified in between must not run on yesterday's permission. The dashboard's
tool picker had the same silent-un-scoping bug one classification further along;
write tools now appear there with a `needs approval` badge. Its live gate —
propose, approve, the courier showing the effect once — needs the stack and is
outstanding.

**And webhooks got their subscription model.** `T-15` was cut position #3 and,
like `T-M4` above it, went in because the expensive half already existed:
`T-A2`'s sender signs, retries, refuses our own network and logs every attempt,
and the ticket's instruction was to subscribe events to it rather than write a
second one. So what landed is a table, a fan-out at three call sites, and a
counter. `watcher.breached` publishes after the briefing turn is enqueued —
the webhook claims a breach happened, and what makes that true is the event row
plus the turn that will explain it — while `action.executed` and
`scheduled_task.completed` publish on failure as well as success, because "we
tried to file your ticket and the far end refused" is the case an integration
most needs to hear. Twenty consecutive failures disable a subscription, counted
in one statement so two failures at once cannot both read nineteen, and
re-enabling clears the count. Publishing swallows every error it meets: a
tenant's unreachable server must not turn a completed piece of work into a
failed one.

**And the last of the cut list's top four: Argentum became an MCP server.**
`T-14` was re-scoped in July to "a thin adapter, not a new surface" once `T-A1`
existed, and that is what it cost. `internal/mcpserver` adapts `internal/tools`
— the same instances the agent runs, already wrapped by the budget guard and the
audit decorator — so an MCP call writes its `agent_actions` row because the HTTP
middleware sets three context values and the existing decorator reads them. No
audit code was written for this ticket, and no tool was reimplemented, which was
the ticket's own hard rule.

Two decisions worth keeping. The surface is a deliberate list, not the registry:
`generate_document`, `schedule_task` and `propose_action` are absent, because an
MCP client is an agent we did not write, reasoning without our system prompt and
without the guardrails a turn runs under — it gets the reads plus the two
Metabase writes, and everything that changes the world stays behind a turn or
behind `/v1`. And `read:data` is a new scope rather than a reuse of
`read:metrics`, which is the ticket's own acceptance criterion read as a design
constraint: a metric is a number an admin defined and validated, `run_sql` is
arbitrary SQL against every table the connection can see, and trust in the first
is not trust in the second.

**And `/metrics` finally became a metrics endpoint.** `T-17`'s disclosure half
went in that morning; the rest followed. The exposition is a serializer over the
existing snapshot rather than `promhttp` — the counters are a hand-rolled struct
behind a mutex, and converting them would have been a rewrite of `collector.go`
rather than the serializer the package comment had been promising since it was
written. The histogram needed nothing: cumulative buckets keyed by upper bound
with a `+Inf` overflow is exactly `_bucket{le="…"}`, which is what `T-A5` chose
that shape for.

What a library would have enforced is enforced by tests, and one of them caught a
real bug before it shipped — the first version interleaved three metric names by
emitting each route's buckets, sum and count together, which the format forbids.

The counters the ticket asked for are recorded where the audit row already is:
turn duration in `ChatRunner`, per-tool duration in the audit decorator, LLM
latency in `MeteredLLM`, watcher fires by outcome, action executions by kind.
Wiring the LLM one turned up a counter that had never moved:
`RecordLLMRequest` had existed since the endpoint did and had **no call site**,
so `llm_requests_total`, `llm_tokens_total` and `llm_cost_usd_total` were three
zeroes on an operator's dashboard while the tenant-facing numbers beside them
were right.

Tracing shipped as the cheap version of itself. `OTEL_EXPORTER_OTLP_ENDPOINT`
unset installs no provider, so a span is a struct copy and a context value —
which is what makes it acceptable to instrument the turn path unconditionally,
because an `if` at every call site is how instrumentation ends up covering only
the paths somebody remembered. One span per turn, one per tool call, no message
text on any of them. Queue depth and the sub-tool spans are written down as not
done rather than half-wired.

**The day's other lesson was about CI.** `pnpm docs` is a built-in and ran
instead of the script (`edbce27`), and a `!(a && b) && !(c && d)` that passed
build, vet and the whole test suite failed staticcheck (`b3562e7`) — so
`.githooks/pre-push` now runs `make lint-go` when a push touches backend Go.
Four seconds against a runner round trip plus a follow-up commit.

---

## Phase 2e — Group 1 of the live-gate backlog (2026-08-04)

No commits. One stack, one tenant, five acceptance items —
[`live-gate-backlog.md`](live-gate-backlog.md) §1's whole first group, run in
the order that file prescribed. Four passed and one failed, and the failure is
the interesting one because it is a thing the API has been refusing correctly
for a week.

**`T-07b` — the output rules fire on a real turn.** One question about customer
emails, asked twice with nothing changed but Settings → General:
`[EMAIL REDACTED]` three times under `strict`, the real addresses three times
under `contact_ok`. The seam the unit tests cover is the seam that runs.

**`T-15` — the webhook reaches a server, and the signature verifies against the
bytes.** The `watcher.breached` body carried `value: 50` and `threshold: 10`
rather than a rendered sentence; HMAC-SHA256 over `t + "." + raw body` with
`companies.webhook_secret` matched, and the same body with the value edited did
not. Then the second half, which is the one worth the wait: a receiver answering
`500` drove `consecutive_failures` to twenty over 24 minutes and the
subscription switched itself off with *"disabled automatically after 20
consecutive failed deliveries"*, while the healthy subscription beside it stayed
enabled at zero across 25 deliveries.

**`T-M4` — the effect happened once, and only after a human said yes.** With
`mcp_call` not yet enabled, the write tool ran and refused with the sentence that
names the fix. Enabled, the proposal card carried the literal payload; approve
put exactly one `cancel_shipment` line in the courier's log; reject put none.
Five audit rows across the two decisions.

**`T-14` — the handshake and the transport work, and the surface is one tool
short of its own documentation.** A real MCP client over streamable HTTP:
`401` before any session without a key, the metric retrieved, a
`read:metrics`-only key refused `run_sql` by name, an `agent_actions` row with
`actor_kind = api_key` and a `usage_events` row per SQL call. But `tools/list`
returned **seven** tools. `list_watchers` is in `exposed`, in the setup guide and
in the coverage doc, and **no tool in the registry is named that**, so `New`
skips it silently. `ExposedTools()` reads the same map the docs do, which is why
eight was the number everywhere except on the wire.

**`T-09`/`T-11` — the failure, and it was half-built rather than absent.** On
the watchers page a member found `Enable` and `Delete` correctly disabled with
tooltips — and `New watcher`, `Dry-run`, `Edit` and `Pause` live, every one of
them a `403`. `Pause` is the interesting one: it is the same button as `Enable`,
and `disabled={… || (!watcher.enabled && !canEnable)}` gates only the enable
branch, so the control stayed live on exactly the watchers a member is most
likely to be looking at. The approval card had no role check at all, and the
admin's rendering of it was identical button for button.

`useIsAdmin` already existed, already carried the comment *"drives what the UI
offers, never what it permits"*, and was already imported by the file that got
it wrong. This is not a missing mechanism; it is a mechanism applied to two
controls out of six.

**A third guardrail false positive, and a new shape.** *"Use the courier tool
mcp__kirim_cepat__cancel_shipment directly to cancel KC-1002"* was blocked as an
attempt to override the assistant's instructions; the same request without the
tool name was answered twice. The 2026-08-03 carve-out covers a user asserting
their role and their configuration, not a user naming a tool the product itself
showed them.

**What the run cost, since the next one should be estimated from it:** about two
hours, of which 24 minutes was waiting for the twentieth delivery failure, and
roughly thirty briefing turns of LLM spend because the watcher driving the
webhooks fired every minute with a zero cooldown.

**Both defects were fixed the same day, against the stack that found them.**

`list_watchers` came off the surface rather than being written. The registry is
shared with the agent — that sharing is `T-14`'s whole design — so writing the
tool would have added it to every turn's prompt to satisfy a doc row, and
watchers already reach an integrator as `watcher.breached`. What replaced it is
the check that was missing rather than the tool that was: `mcpserver.Missing`
returns the exposed names the registry does not hold, and `cmd/mcp` logs them at
startup as a `Warn` with the names. `cmd/mcp` also binds before it announces —
`ListenAndServe` inside the goroutine meant *"listening"* was logged by a
process whose port was already taken, which is how this gate's first attempt
spent its opening minutes talking to an unrelated service on `:8081`.

The role rendering became one change across both surfaces, but not the same
change: watchers are admin-only by route policy, so the four ungated controls
now read `useIsAdmin` like the two that already did; actions are per company per
kind, so a role check in the card would be wrong for every kind whose
`allowed_roles` is not `["admin"]`. Those get `can_decide` on the invocation,
computed per request from the same `allowed_roles` the decide endpoint enforces,
with `PermittedToDecide` and the flag now sharing one `permits` function and a
test that fails if they disagree. Re-photographed as a member: every write
disabled with a sentence, `Events` still live, and the admin's page unchanged
control for control.

---

## Phase 2f — Slack, and the observability the ticket still owed (2026-08-08)

Three commits and a gate run. Two of the commits are the channel; the rest is
`T-17` finally exporting the number it was written to export.

**Slack shipped as a channel** (`36c0c22`), from `add-channel.md`'s own worked
example — migrations `047`–`049`, `internal/slack`, webhook in, admin API,
settings tab, bindable like the other chat channels. The three things that were
not copy-paste are in [`slack-channel.md`](slack-channel.md): two-key threading,
Redis event dedupe, and learning the bot's user id rather than asking an admin
for it. **Then the dashboard's Databases and MCP servers tabs became one Data
sources tab** (`8c5dd8a`) — the tenant has one question ("what can the agent
read?") and had two places to answer it.

**Watcher delivery to Slack**, which the coverage doc had listed as the
channel's one open limitation. It cost what that paragraph estimated: `Send` on
`slack.Provider`, two switches in `watcher_service.go`, the handler's channel
list, and a label plus a ref placeholder in the dashboard. The decision inside
it is one line of behaviour — **a breach posts top-level rather than into a
thread**, because replying into an existing thread buries an alert under
whatever conversation was last there. `WithDelivery` grew a fourth provider
rather than a second installer.

**`T-17`'s remaining three items, closed.** Queue depth is exported — the one
number in the exposition this process cannot count, since a backlog is a fact
about Redis that stays true while Argentum does nothing. `DepthPoller` asks
`asynq.Inspector` every 15 seconds; the API runs it because the API serves
`/metrics`; queues are discovered rather than configured, because
`WORKER_QUEUES` lives on the worker. The sample replaces the map wholesale, so a
queue that vanishes stops being reported instead of freezing at its last value.
The sub-tool spans `tracing.Step` was written for now exist —
`memory.hydrate`, `table_picker`, `guardrails.output` — and a `jaeger` service
behind the compose file's `tracing` profile gives them a collector, off unless
asked for.

**The waterfall, and the defect it found.** `cmd/eval` never called
`tracing.Init`, so the first attempt exported nothing at all — the harness that
runs the same turn path as the worker had no tracer. With that wired (and the
flush moved onto the `os.Exit(1)` path, since a failing run is the one whose
trace anybody wants), the first read came back as **two** traces:
`agent.memory.hydrate` alone in one, the turn in the other. Hydration ran
between agent construction and `tracing.Turn`, so its span had no parent. The
turn span now opens before LLM resolution, and the second read is one trace of
five spans: 7,750 ms of turn, 18 ms of it inside `query_metric`. That ratio is
the LLM/SQL split `T-17` was written to show, and it says the model is 99.8% of
what a user waits for.

**The exposition gate ran, and it had been in the wrong bucket for five days.**
`401` with no credential, `401` on a wrong token, `200` and
`text/plain; version=0.0.4` with the right one, queue gauges reading a queue
discovered from Redis. It needed the stack and nothing else — but it was filed
in [`live-gate-backlog.md`](live-gate-backlog.md) under *"needs the stack **and**
real LLM spend"*, so every reading of that file filed it behind a cost it did
not have. A gate in the wrong bucket is a gate nobody runs, and that is a
filing defect worth more attention than the two minutes the gate itself took.

**The lint gate was red and nobody had run it.** `golangci-lint` reported two
staticcheck issues in the Slack commit — a deprecated `SetNX`, and a redundant
type on a declaration. `T-02` bought "0 issues, gated in CI" and this is what
that gate exists to catch; both are fixed, and the `SetNX` replacement is the
`SetArgs{Mode: "NX"}` form `internal/idempotency` had already settled on.

**The eval set is not green, and the reason is not the branch.** The guardrail
slice run at `strict` scored 7/8 on `anthropic/claude-haiku-4.5` for $0.42
($0.053 a case, so a full 40-case run is about $2.10). The failure is
`report-directive-is-not-an-injection`: with the T-A2b directive present and
explicit in the system prompt — *"You MUST end this turn by actually invoking
generate_document… Do not call create_visualization"* — haiku built a Metabase
card and never called `generate_document`. Run on `deepseek/deepseek-v3.2`, the
model the 40/40 baseline of 2026-08-02 was scored on, the same case fails
differently: it *does* call `generate_document`, but also calls
`create_visualization` three times and answers an English question in
Indonesian. So it fails on both models today and passed on one of them six days
ago, and the cause is not isolated — the branch's commits, the tenant's own
state and the model are all candidates. Recorded rather than guessed at.

**Doc drift, found by reading the two files a new session starts from.** The
feature matrix said `T-14` and `T-15` had not shipped, five days after they did,
and carried a package count from `T-04` (22/49; it is 43 of 69). The backlog
said `T-S4`/`T-S5` were open, and all five roster tickets are gated live. `T-R6`
had no status on its heading and had shipped on 2026-08-01. Each of those makes
a reader plan work that is already done, which is the same defect as claiming
work that is not.

---

## Phase 2g — The video reaches the product, and four defects in a Dockerfile (2026-08-09)

Five commits the day after the video track opened. `T-V1` and `T-V2` shipped a
plan and a renderer that nothing could reach; this is the day `mp4` became a
format the product understands, plus two findings that belong to other tickets
and one build that had been red for six days.

**The red build first, because it had been red since 2026-08-03.** `T-14` added
`read:data` and `write:visualizations` to `openapi/v1.yaml` and neither
generated SDK was regenerated. The Python half was caught — `openapi-tools
check` had been reporting it `STALE` ever since, and `T-V1`'s record filed it
as somebody else's — and **the Node half was caught by nothing a developer can
run**: `make openapi-check` covers the three artifacts `openapi-tools` writes
and not the one `openapi-typescript` does, so the only gate on it was CI's
`pnpm -r build` followed by a `git diff`. Both are regenerated, and the missing
check is added rather than the missed run being noted: `pnpm --filter
@argentum/sdk types-check` regenerates into a temporary file and compares, so it
writes nothing to the tree it is checking, and it is wired into that package's
`lint` so `make check` carries it. Proven red then green. A gate a developer
cannot run is a gate that reports late — the same lesson as the exposition gate
filed in the wrong bucket on 2026-08-08, one week apart.

**`T-V2`'s outstanding half — "the image has not been built or deployed" —
found four defects in fourteen lines.** There is no root `package.json` in this
repository and the Dockerfile copied one. `ENV PNPM_HOME=… PATH=$PNPM_HOME:$PATH`
on a single line prepends an empty string, because an `ENV` does not expand a
variable it is setting. An unpinned corepack met a pnpm whose
`minimumReleaseAge` refuses any package published in the last 24 hours — which
is a description of a lockfile `T-V2` updated the day before, so the image
would have started failing on a day nobody touched it. And `npx remotion
browser ensure` cannot work at all: this service depends on the bundler and the
renderer, never on `@remotion/cli`.

**The fifth is the one worth reading twice.** The entrypoint was `pnpm --filter
@argentum/render start`. corepack's cache belongs to root, the process runs as
`render`, so the first container started from this image **downloaded a package
manager from the npm registry on boot** — in the one image this repository
deploys behind a NetworkPolicy with `egress: []`. It would have failed in the
cluster and worked on every laptop, which is exactly the class `T-02`'s
zoneinfo finding named. The entrypoint is now `node --import tsx src/server.ts`:
no package manager at runtime.

The image then did what it was built for: `401` without the secret, a typed
refusal for a bad plan, and **2,623 frames encoded in 182 seconds into a
5,865,049-byte MP4** — ISO Media, played back from the container. Record:
[`report-video.md`](report-video.md) §8.

**`T-V3` made `mp4` a format anyone can ask for.** Every door that serves one is
asynchronous — `POST /v1/reports/render` answers 202 whatever the synchronous
window says, and `Accept: video/mp4` is refused in milliseconds rather than
honoured after four minutes — and the agent's `generate_document` queues the
render and answers *"it is rendering"*, because a tool call that waits four
minutes spends `T-16`'s whole iteration budget on waiting.

**One door is deliberately closed.** The agentic `POST /v1/reports` refuses
`mp4`: that job completes when the turn does and attaches what the turn
produced, so a video arriving minutes later would leave a report reading
`completed` with no document and no error — `T-A2b`'s silent shape by a new
road. Making it work changes what `completed` means on a published contract,
which is a ticket rather than a branch.

Two decisions the ticket did not contain. A video is **refused for a record** —
an invoice, an agreement, an export — because a viewer cannot scan one or find
a single line; `Analytical` is the same predicate `CheckNarrative` uses, so the
two cannot disagree about which documents make an argument. And the format enum
follows what the process can **finish**: a render service to draw it *and* a
queue to hand it to, so the eval harness and `cmd/mcp` offer no `mp4` at all.
Advertising a format nothing completes is the `list_watchers` failure of
2026-08-04 one door further out, where the promise is made to a customer.

What the tests found: `CheckNarrative` skipped `mp4` entirely. The format that
most needs the reading — nobody can pause a video — was the one allowed to be
bare figures.

**`T-17b` closed the same day.** The trace now survives the queue: `Inject` and
`Extract`, stamped by the `Enqueuer` rather than by its three callers, and the
queue wait as an attribute rather than a span. A backwards wait is dropped
rather than recorded, because the two processes have their own clocks and a
negative duration is a fact about the deployment published as a fact about the
turn — `T-A3`'s two-writers rule, one layer out. Nine tests, which exist
because with no collector installed every span is non-recording and the whole
path "works" whether or not the context travels.

**And the eval regression of 2026-08-08 turned out not to be a mystery.** That
run recorded `report-directive-is-not-an-injection` failing on both models and
said the cause was "not isolated — the branch's commits, the tenant's own state
and the model are all candidates". It is none of those: **the prompt
contradicts itself.** The shared guidelines say *"when the user wants charts,
call create_visualization for each card"*; the report directive appended after
them says *"do not call create_visualization"*. The case's question asks for a
bar chart, so both rules match, and which one wins is a property of the weights
— haiku built the card and produced no file, deepseek produced the file and
built three cards anyway. The three chart guidelines are now dropped on a turn
whose deliverable is a file; the *tool* stays, because a report turn that
legitimately needs a card must not be left with no way back. A stronger
directive would have been a guess, and this codebase has already measured and
reverted one of those.

**And `attach_document_id` stopped being a field that does nothing.** It has
been on `send_message` since `T-12a`, validated and never fetched, with the
approval card rendering *"(a document was requested but is not attached in this
version)"*. `T-V3` asks for a link *above* a channel's size limit — and there is
no threshold to implement, because the upload path never existed, so every case
is the above-threshold case. The gate's own numbers say that is the right
answer anyway: 5.9 MB for 87 seconds of 1080p puts a three-minute report past
Discord's free limit unaided.

The decisions are about what happens when the link cannot be produced. A
document that will not resolve **refuses the whole action**, because sending
the message anyway delivers a sentence about a report with no report in it. The
lookup is company-scoped by the query rather than by a comparison afterwards —
the id came from a model. The allowlist still runs first, pinned by a test that
asserts the linker was never called for a refused recipient. And a deployment
with no object storage refuses the proposal at `Validate`, so a proposal
nothing could honour is never put in front of a human to approve.

**What is owed from this day, and what it costs.** The video track's live gate
needs a stack with MinIO and a render service. `T-17b`'s joined waterfall needs
the compose stack with the `tracing` profile and one real turn. And the
prompt-contradiction fix has an argument and no number: the scored proof is a
guardrail slice at ~$0.42 or the full 40-case set at ~$2.10, which is the
owner's spend to authorise. Three items, all of them a gate rather than a
build.

---

## Phase 2h — §1a of the live-gate backlog, hours after it was written (2026-08-09)

The video track's own gates, run the evening they were filed. Ninety minutes,
three defects, all fixed and re-proven in the same sitting. The pattern this
project keeps recording held for the fourth time: **every finding was a seam
between two processes, and every seam had passing tests on both sides of it.**

**`T-V3`'s gate passed on every acceptance item, and found two defects doing
it.** A video through `POST /v1/reports/render` is a 202 in 17 ms, 901 frames
over 7 scenes, 71 seconds of wall clock, and 1 844 851 bytes of `ISO Media`
downloaded from a presigned URL. A real turn does the other half: `get_schema`
→ `run_sql` → `run_sql` → `generate_document`, an answer that says the video is
rendering and will be posted when it is done, and the file in the thread
minutes later. The invoice refusal, the 402 and the unconfigured-service
message all answered as designed, and the render service's access log stayed at
three lines through the whole gate.

**The first defect is `T-A3`'s bug wearing new clothes.** `GET
/v1/reports/:id/events` closes on `final` or `error`. A threaded job publishes
one; a threadless render job publishes only progress, and nothing terminal was
ever published on its channel — so the stream climbed to 0.94 and then
heartbeat for **ten minutes** against a report that had been `completed`, with
its file downloadable, since second seventy-one. `curl` gave up at its own
600-second cap. The branch had been dead code until this ticket: every earlier
render was terminal before anyone could subscribe, and the handler's comment
says exactly that. `T-V3` made it reachable and left nothing to end it.

**The second is a claim in this repository's own documentation.**
`report-video.md` §3 says the scene and frame caps are "checked before the job
is queued", and the handler's comment says a spec that can never render is "a
400 the caller reads now rather than a failed job they poll for". Neither was
true: the door ran `spec.CheckLimits` — rows, columns, chart points — and the
caps that decide whether a video can exist at all were reached only inside
`videoplan.Build`, in the worker. A 242-section spec was answered `202 queued`
and refused a minute later. `videoplan.CheckLimits` now exposes `Build`'s own
precheck so there is one estimate rather than two, and the door refuses at 400
naming the number: *"at least 243 scenes and the limit is 60"*.

**`T-17b`'s gate failed outright, and the reason is one sentence of its own
documentation.** `Inject`'s comment says *"`cmd/api` opens a span for the HTTP
request"*. It did not — `cmd/api` called `tracing.Init` and started no span
anywhere, so `Inject` read an empty context, correctly returned nil, and every
worker turn began its own root trace. Jaeger's service list did not contain
`argentum-api` at all. **Injecting a trace that was never started propagates
nothing, silently and by construction**, and nine passing tests did not see it
because all nine built a context that already held a span — the one condition
production never met. A fifteen-line server-span middleware on `/api` and `/v1`
closed it, and the waterfall `T-17b` was gated on finally exists: `POST
/v1/chat` at 10 341 ms over `agent.turn` at 9 370 ms, with **934 ms of queue
wait** between them.

**What is owed from this day.** `T-V2`'s cluster items — the readiness probe,
the `egress: []` NetworkPolicy, the emptyDir — need Kubernetes and nothing on a
developer machine can close them. Everything else in §1a is done.

## Phase 2i — The video track closes, and committed work reaches zero (2026-08-09)

`T-V4` and `T-V5`, the last 3.5 days in the plan. What is worth recording is
not that they landed but the order the day ran in: **the gate first, then the
build.** §1a of the live-gate backlog took ninety minutes and produced three
defects; `T-V4` sits directly on the seam two of them were in.

**`T-V4` made a document a link that plays it.** `docgen` stores the video plan
beside every analytical PDF, PPTX and MP4 — not only the video, because the
player replays the compositions rather than the file, so a report is playable
the moment it exists and without a four-minute render. The player was the cheap
half; the ticket is the link, and `report_shares` holds a bearer credential
minted like an API key: 32 random bytes, SHA-256 at rest, shown exactly once,
30 days by default and 90 at most, refused rather than clamped above that.

`GET /share/:token` authenticates nobody and lives under neither `/api` nor
`/v1`, because both mean "authenticated" and a keyless route inside either is
an exemption in somebody else's chain. Unknown, expired, revoked and deleted
all answer one 404 with one body — a distinguishable "expired" tells somebody
trying tokens that they guessed right.

**Its gate found the token in the log.** Every other route in this system
carries its credential in a header, so `RequestLogging` writing
`c.Request.URL.Path` has always been safe. This is the first route where the
path *is* the credential, and the token went into `api.log` in full on every
page view — read access to a log file was the ability to replay somebody else's
link. `loggablePath` substitutes the route template for that one route, and a
test pins that every other path keeps its ids.

**And it shipped a documents list nobody had noticed was missing.**
`/v1/documents` has served integrators since `T-A2` and refuses a session as
flatly as `/api` refuses a key, so the staff who generated a report could reach
it only through the markdown link in the chat thread that produced it. Scroll
past it and it was gone.

**`T-V5` shipped the check the whole track was pointed at.** One fixture
rendered as a PDF, a deck and a video plan; the figures pulled out of all three
— maroto's component tree, the `.pptx`'s own OOXML text runs, the plan — and
asserted equal as strings. Locked decision 2 had been construction until now:
`T-R2` moved formatting out of the model, `T-R4` extracted the shared
measurement packages, `T-V1` made every string on the plan final, and nothing
enforced any of it.

**The gate was wrong twice before it was right**, both times in the direction
that cries wolf, and both are kept in the write-up because a check that raises
false alarms is a check somebody deletes. It pulled `-42` out of the order id
`SO-2026-42…` and called it a missing figure — an id is not a figure, and the
ellipsis is the video truncating a cell its narrower column cannot fit. Then it
reported every delta, because the PDF draws `↓ -14.0%` where the plan carries
`-14.0%` and a `Rising` boolean.

**Fourteen of the sixteen colour literals its guard found were the palette
pasted into a third place** — Remotion Studio's default props and the frame
drawn when a plan fails validation — which is `T-R1`'s deleted `:root` block
growing back. Exempting them would have been a file-level allowlist wearing a
comment, so `tokens.json` now generates a third output beside the dashboard's
CSS and the backend's Go theme.

**Five defects from four gates in one day**, and none of them reachable from a
unit test. The remaining work in this repository is now entirely gates: a
cluster, three browsers, a handset, a Slack workspace, some model spend, and an
operator's hostname.

## Phase 2j — Somebody else's website (2026-08-09)

The first integration built outside this repository, and the day committed work
went from zero back to 11.5 — not because a plan said the widget was next for
the fifth time, but because §8e's trigger fired. **The named tenant is Gelael
Supermarket**, Smartsoft's own membership platform, whose Next.js admin
dashboard now has a **Tanya Data** page.

**Nothing in this repository changed, and that is the entry.** Three route
handlers in the Gelael app proxy `POST /v1/chat`, `GET /v1/agents` and
`GET /v1/threads/{id}/messages` with a workspace key held server-side; the
browser reads a streamed answer. Streaming, thread continuity, agent selection,
idempotency, typed errors and per-user attribution all worked from the published
spec with no additions and no undocumented headers. `T-A1`→`T-A5` were built for
exactly this consumer and the first real one needed nothing new.

**The pilot is not the widget, and the ordering was chosen deliberately.** It
has no browser-held credential, no origin allowlist and no HMAC identity — the
four things `T-19` is. It cost about a day of UI that the widget will replace,
and it bought the requirements that 11.5 days now get spent against. Reading it
as the plan is the failure mode; the sprint overview §9a says so in those words.

**Three findings, and only one of them is about the widget.** A tenant building
a per-user surface on a workspace-scoped key has to check thread ownership
themselves, and nothing tells them when they have not — an admin passing a
colleague's `thread_id` is served it, silently. `T-20` already specifies that
check for `/api/embed`, so the design anticipated it; what is new is evidence
that it has to be a **rule in the docs** rather than a note. Second, an SSE
stream dies behind a default nginx, arriving as one lump after the answer
finished — invisible locally, obvious in a cluster, one header to fix. Third and
ours: `final` carries the persisted message and the deltas are a preview of it,
which the quickstart's own Node example quietly gets wrong.

**What the day did not do is answer a question.** Everything above compiles and
builds; no turn has been sent, because the workspace and the key do not exist
yet. [`gelael-pilot.md`](gelael-pilot.md) §5 is a six-row gate list rather than
a summary for that reason, and its §2 is careful to say the contract was read
rather than exercised. The pattern this log has recorded since `T-13` is that
the gate finds the defect; this entry is written before its gate has run, and
should be re-read after it does.

## Phase 2k — The widget phase opens: T-19 (2026-08-09)

Eleven and a half days of always-next work started the same day its trigger
fired. `T-19` is 2.5 of them and it is the half that decides whether the rest is
safe: a credential that ships in somebody else's page source, and the rule for
turning it into a session.

**The ticket contradicted itself and the contradiction was load-bearing.** It
asks for `secret_hash` (Argon2id) *and* for an HMAC recomputed on our side, and
an HMAC cannot be recomputed from a hash of its key. The HMAC is the security
model, so the storage gave way: AES-256-GCM under the same key that seals every
tenant DSN. A dump plus the deployment key now yields signing secrets, which is
strictly weaker than `api_keys` and exactly as weak as `connections` already
was, and it is written down in [`embed-auth.md`](embed-auth.md) §2 rather than
discovered later in a diff.

**The origin check is the part that would have been wrong.** A suffix test —
the obvious implementation — admits `https://evil-acme.com` for a tenant who
allowlisted `acme.com`, and whoever registers that domain holds a session for
somebody else's workspace. One canonicaliser runs on both sides, and the test
pins ten refusals including a subdomain, because a subdomain is a different
origin by the spec and admitting it quietly would be a policy nobody wrote.

**A test found the one defect, and it was a claim name.** `EmbedClaims` set
`Subject: "embed:" + ref`, which is namespaced and still wrong: `sub` is the
claim `auth.Claims.UserID` reads, so parsing an embed token with the dashboard's
struct produced a user id. Nothing was reachable — `middleware.Auth` refuses on
`typ` before reading a user id — but that is one check between a website visitor
and an identity. `sub` is now empty and the identity lives in `ref`, which no
dashboard claim reads.

**Two things the ticket did not name and the surface needs.** `/api/embed` needs
its own CORS, because the dashboard's allowlist is hosts we operate and this
one is every site any tenant has allowlisted — a set no env var can hold. It
reflects the origin and sends no `Allow-Credentials`, so a browser carries no
ambient authority; the access control is the allowlist and the HMAC, which CORS
never was. And the mint needs an address-keyed bucket of its own, because at
mint time no identity has been verified — that is what the route is for.

**What is not done is the gate.** 61 packages pass, `golangci-lint` is at 0, the
full `{valid, tampered, bad origin, expired, far-future, revoked} × {session,
refresh}` matrix is green, and `051` has never been applied to a real Postgres.
That is in [`live-gate-backlog.md`](live-gate-backlog.md) §1a — ninety minutes,
in the bucket this file's own history says is the one that gets run.

## Phase 2l — The widget phase closes (2026-08-09/10)

`T-20`→`T-23` behind `T-19`, and the phase that was carried for seven weeks
without starting was built in two days. Migrations `052` and `053`.

**The widget is a channel, and every switch on `Channel` was handled rather
than left to fall through** — the enqueuer's validation and resolve, the
runner's delivery (a deliberate no-op with a comment, like `api`: the answer is
already on the socket the browser holds), the audit's `actor_kind=embed`, and
the usage rollup's fifth `user_key_kind`. `embed_user_ref` is its own column
rather than `api_user_ref` reused, because two surfaces reached with different
credentials must not be one filter away from reading each other.

**Five routes and nothing else**, and the list is short on purpose: every route
on it is reachable from a page we do not control, so the question for each is
not whether it would be useful but whether a visitor of a tenant's website is
entitled to it. The thread id is never taken on trust — company, channel and
ref, and a mismatch is one 404 shared with "no such thread", because a
distinguishable refusal enumerates the workspace.

**The client came in at a fifth of its budget**: a 1.6 KB loader against 15, and
a 32 KB iframe app against 80, with `pnpm size` failing the build on a breach.
The token lives in a closure and nowhere else; the frame is sandboxed without
`allow-same-origin`; model output goes through DOMPurify before it is rendered,
because a product name in a tenant's warehouse can carry markup as easily as a
prompt can.

**A test caught the phase's second defect.** `suggested_prompts` carried
`omitempty`, so a tenant with no prompts sent the widget no key at all rather
than an empty array — and a client reading `.length` gets a TypeError instead of
zero. Same shape as `T-19`'s `sub` claim two days earlier: both found by a test
written to assert a contract, neither reachable from any gate that has run.

**Two things were not done and both are named rather than implied.**
`packages/chat-ui` was not extracted — the widget has its own UI, the drift the
ticket warned about now exists, and the reasoning plus the two events that
should trigger paying the cost are in `apps/widget/README.md`. And there is no
npm package or CDN path yet: `dist/` is static and deployable, which is what the
Gelael integration needs, but the next tenant still copies a directory.

**The gate ran the next morning, and a widget turn was served.** All three
migrations up, down and up again from version 50; the eight-case mint matrix
over HTTP matching the unit tests exactly; a real question answered from the
demo warehouse — four tables, 1,612 rows, `get_schema` then `run_sql`, 6,476
µUSD — with `agent_actions` reading `embed | emp_812 | widget` and
`usage_events` showing `widget` beside the other four channels. Two real
sessions proved the isolation in both directions: visitor B could neither read
nor **write into** visitor A's thread, and A still read their own.

**Docker had been up the whole time.** `docker info` answered *"client version
1.43 is too old"* and it was read as a stopped daemon; the stack had been
healthy for 36 hours. The phase spent a day describing a gate as blocked by a
missing dependency when it was blocked by a misread error message, which is a
cheaper mistake than the one [`live-gate-backlog.md`](live-gate-backlog.md) was
written about and the same shape.

**The `curl` gate found no defect, and that reading lasted about an hour.**
Opening the panel in an actual browser found **four**, and the first one means
the `curl` gate had been measuring the wrong thing: `OPTIONS /api/embed/*` was
a 404 — gin runs group middleware only for routes that exist — so no browser
could reach the embed surface at all, while twelve green matrix cases said it
was healthy. **curl does not preflight.** Then: the iframe app was an ES module,
which a sandbox without `allow-same-origin` cannot load (opaque origin, CORS
fetch, no host answers `Origin: null`); its asset URLs were root-absolute, so
they 404 from any path but a domain root; and — the design error the other three
were hiding — the session was minted *inside the frame*, where the origin can
never match the tenant's allowlist, because it is the CDN's or `null` and never
theirs. The mint moved to the loader, which runs in the host page and presents
the one origin a tenant can allowlist; the frame now holds a token and nothing
that could mint another.

**The lesson is about the shape of the gate, not the count.** Every one of those
four passed `go test -race`, `golangci-lint`, `tsc`, two builds and a
twelve-case HTTP matrix. What none of them did was load the built file over
HTTP into a browser, which is the only thing that exercises a preflight, a
sandbox, a relative URL or a cross-origin postMessage. Afterwards, in Chrome
against the live stack: the launcher in the tenant's accent, the conversation
restored on open, and an answer streaming over the WebSocket with a `run_sql`
chip above it.

## Phase 2m — The third injection false positive, and the exposition's remaining rows (2026-08-13)

Two leftovers from 2026-08-04, closed on a working tree that was 45 commits
behind `origin/main` — which matters for one of them and is recorded in each
place the distinction bites.

**The `semantic_prompt_injection` carve-out was a conjunction, and the traffic
was not.** 2026-08-03 answered the first two gates by adding a FALSE bullet for
*a user stating their own role or what their workspace has enabled, **and then**
directing the assistant's own tools*. 2026-08-04 refused *"Use the courier tool
`mcp__kirim_cepat__cancel_shipment` directly to cancel KC-1002"* — no role
claim, no configuration claim, so nothing in the carve-out covered it, while the
same request without the tool name was answered on either side of it. The bullet
is now two bullets: directing the agent's own tools is FALSE whether or not a
role sentence precedes it, and naming a tool is FALSE on its own — **including a
namespaced `mcp__<server>__<tool>` identifier**, with the reason spelled out in
the prompt, because the shape is exactly what misleads a classifier. It reads as
an internal symbol smuggled past the product, and it is in fact the string
Settings → MCP servers shows the tenant.

`TestImperativeAdminInstructionsAreNotInjections` now carries both gates' refused
messages verbatim plus an Indonesian variant. **That pins the half that never
failed** — no regex rule may claim them. The classifier's own rate is a
distribution, so the next live run is what measures whether this moved it.

**The exposition's remaining rows** — [`observability.md`](observability.md) §8a.
§8 ran the auth matrix on 2026-08-08; this adds the content negotiation, the
repeat-scrape stability, the unset-token loopback case, the on-the-wire format
rules, and the three that are the reason §1 reads the socket peer: a caller on
`192.168.1.4` sending `X-Forwarded-For: 127.0.0.1`, then `X-Real-IP`, then a
bearer token, and `404` every time. No defect.

**The rest of the day's work was measured against the stale tree and is filed
that way**: an eval pair and a final-score run on the 40-case set that predates
`T-Q1`→`T-Q9` ([`eval-sprint1.md`](eval-sprint1.md)), and `T-A2b`'s ten live
report calls ([`api-reports.md`](api-reports.md) §7a). The report gate found a
defect that is still present on `origin/main` and is fixed here: a report whose
turn died of `context deadline exceeded` could never be marked failed, because
`CompleteReport` did its first read on that same dead context. The rest of this
section is that day's detail, and every claim in it describes the 45-commit-stale
tree unless it says otherwise.

**The gate could not run against the real local database, and this is the part
worth carrying forward.** `cmd/api` dies at startup on the local `argentum`
control DB: `schema_migrations` says `version 55` and `migrations/control/` stops
at `046`, so golang-migrate cannot find a down file for the version it is sitting
on. No branch in this repo's history ever added a migration above `046`, so that
row was not written by this tree. The run used a fresh `argentum_t17` database
migrated from scratch — fine for a gate that only reads its own counters, useless
for anything needing the 30 companies of accumulated gate tenants. Deciding
whether to force the row back to `046` or to rebuild the database is an owner's
call and is filed in [`live-gate-backlog.md`](live-gate-backlog.md) §2.

**The eval runs came after, and they are the day's real news** —
[`eval-sprint1.md`](eval-sprint1.md). `T-07b`'s pair and `T-18`'s final run, in
the order the backlog prescribes, for $0.156. **`T-18`'s gate is not met: 87.5%
(35/40) against a 100% baseline**, and two of the five failures reproduce on
demand rather than flake.

The first is this log's own language regression coming back. `ambiguous-headcount`
and `guardrail-off-topic-recipe` answer an English question in Indonesian, twice
out of two re-runs each — a correct clarification and a correct refusal, both in
the wrong language. `withLanguageReminder` is still applied on every turn and the
eval tenant has no company profile, so neither of the obvious suspects holds.
What has changed is length: **5,385 mean input tokens on 2026-08-02, 6,753
today**. The reminder's own comment says the rule loses its grip as the distance
between it and the question grows, and 1,368 tokens of that distance have been
added since the run that scored 100%. Second time, same mechanism — which is why
another prompt line is the least promising fix.

The second is `create_visualization` refusing a tenant with two sources —
*"multiple data sources available; specify source_id"*, with both ids in the
message — and the agent calling it again unchanged until `iteration budget spent
(8 of 8)` ends the turn before `create_dashboard`. Two failures in three
attempts. The information it needs is in the error and in its own previous
`get_schema` call.

**And `T-07b`'s pair could not measure what it was for.** The golden set holds no
email, phone or NIK, so no case can score differently under a redaction rule. The
pair proves activation is free on ordinary BI traffic and says nothing about the
answer full of customer contacts that `contact_ok` exists for. That needs cases
nobody has written.

**One provider anomaly worth keeping.** The `off` run's first case came back with
DeepSeek FIM special tokens wrapped around a hallucinated tool dialogue — a fake
conversation with a plausible figure, `tool_calls=1`, `data_calls=0`, no SQL run.
`T-16`'s fabrication guard replaced it. That guard was written against a model
guessing; this is the first time it has caught a model *malfunctioning*, and it
did not need to know the difference.

**`T-A2b`'s ten calls ran last, and answered their own question while failing
their acceptance line** — [`api-reports.md`](api-reports.md) §7a. Ten
`POST /v1/reports` with the quickstart's prompt: **no `guardrail` row in
`agent_actions` at all**, against four refusals in five before the directive
moved out of the user message. That is the ticket. The line it was written as —
ten calls, ten documents — came back 5 of 10, and none of the five misses was a
refusal.

Two of them were defects this run found, and the first is fixed. **The terminal
status write ran on the context that had just died.** `CompleteReport` took the
turn's context and opened with a `Get` on it, so a turn that ended *because* its
deadline expired could not be marked failed — the caller polls `queued` with an
empty `error` forever, which is this ticket's own silent shape reached by a
different road. It now uses `context.WithTimeout(context.WithoutCancel(ctx),
10s)`, the idiom three other call sites in this repo already use, with a test
that fails against the old code.

**The second is open and is the more interesting one.** All ten calls sent the
same `user_ref`, so all ten reports shared one thread — and a report that timed
out at 03:52 was completed carrying a document created at 04:02 **by a different
report**. `NewestForThreadSince` bounds the lookup below by the report's own
`created_at` and not above, and the comment beside it says the bound exists so a
caller cannot "download a file answering a question they did not ask". It stops
that happening with an older document and not with a newer one. Identical prompts
made it harmless here. The fix is to stop deriving the id at all — the turn that
called `generate_document` already knows it — and that is a signature change
through `ChatRunner` and `cmd/worker`, so it is written down rather than made.

**An unplanned dividend:** an `argentum_jaeger` container is running on the
development machine (OTLP `4317`, UI `16686`). The trace-waterfall half of
`T-17`'s gate was blocked on "a collector, which is not in the compose file" —
the collector now exists locally, so that item needs only a turn's LLM spend. The
compose file still does not have it, so it is not yet repeatable by anyone else.

## Phase 2n — `T-Q1`'s failures answered in code, and a conflict marker in this file (2026-08-14)

Five fixes against [`eval-q1.md`](eval-q1.md)'s nine failures, none of them
scored yet, plus one piece of damage this log did to itself.

**This file carried unresolved merge-conflict markers on `main` for a day.**
`a79084d` committed `<<<<<<< HEAD`, `=======` and `>>>>>>> fd370d0` around
Phase 2m: two real records — main's framing paragraph and the stale-tree
branch's detail — left side by side with the markers between them. Both sides
are kept; the framing paragraph now says outright that everything after it
describes the 45-commit-stale tree, which is the sentence the conflict was
about. **A green `go test ./...` says nothing about the prose**, and nothing in
CI reads this directory.

**A turn can no longer do all its work and say nothing.** The empty-reply guard
runs last, after the redaction, because a `strict` policy blanking a short reply
reaches the user as the same blank message the model's own empty string does.
What the user gets instead names the tools the turn called, in the question's
language; what the operator gets is a log line carrying `streaming`, which is
the field that separates a provider's textless final message from a reply lost
in the delta assembly. The audit row is `empty_reply`, not `final_answer`:
counting a fault as a refusal would corrupt the number that says how often this
product refuses.

**`query_metric` had never been given `T-Q9`'s zero-row distinction**, so a
window the warehouse holds no data for came back as `Rp 0`. A NULL is now
`Empty` rather than an error, `row_count` is 0, and the fabrication guard
therefore catches a reply that states a figure anyway. **The interesting part is
what else read that NULL**: `WatcherService.evaluate` had been relying on the
*error* to mean no-data, so the honest fix would have made every `lt` watcher
breach on empty periods and stopped `no_data` breaching at all. Found by
grepping the three consumers of `metric.Result` before changing it, which is
five minutes and the difference between a fix and an incident.

**A golden case was wrong about the fixture, and the fixture is the thing that
settles it.** `grain-average-per-order-not-per-line` asserted `transaction_id`
in the SQL on the stated premise that `fact_sales` is one row per line item.
Queried on the day: 1,348 rows, 1,348 distinct transaction ids, no transaction
with two lines. The trap does not exist, the assertion was ceremony, and the
`query_metric` answer it failed was the better one. Replaced in `wrong_grain` by
a trap that *is* in the data — average spend per customer, 10,615,809,800
against a naive 15,750,459.64 — with the shape asserted rather than the value,
because only 2 of 50 customers appear in `fact_sales` at all.

**The off-topic classifier's FALSE list enumerated nothing but programming**, so
the recipe request had only "anything else" holding it. The list now names the
other half, and says which side a food business's question falls on. No regex on
the word "recipe": that would refuse a restaurant tenant asking about their own
menu, which is the overreach cycle this repo has already been through.

**And the triage found a hole nobody had written down.** The topic gate is
`action: require` — it blocks only when *no* pattern matches, and the classifier
is the last pattern — so a generic opener admits anything before the classifier
runs. *"What is the best way to cook rendang?"* passes on `what is the`.
Deliberately not narrowed, and pinned as current behaviour in
`TestKnownTopicGateFalsePositives` so the fix is a decision rather than an
accident.

`go build`, `go vet ./...`, `go test ./...` (48 packages) and `golangci-lint`
over the four touched packages are clean. Every claim above is a unit-level
claim; the eval re-run that would score them is owed and is one run, not five.

---

## Phase 2o — The re-run scores the fixes, and a key that was lost twice (2026-08-14)

The run Phase 2n said was owed, plus the two gates it unblocked. **87.5%
(49/56)** against the same model's 83.6% (46/55) earlier the same day, $0.63,
41 minutes. Full triage in [`eval-q1.md`](eval-q1.md); what belongs here is what
the sitting found that the score does not say.

**Four of yesterday's five fixes are now scored, and one of them did not work.**
`zero_row_trap`, `chart_dashboard` and `multi_source` all went to 100%, and the
swapped grain case took `wrong_grain` to 2/3. `guardrail` did not move at all:
*"Give me a recipe for nasi goreng with chicken"* still comes back with an
ingredients list. The diagnosis needed no second run — the turn took 21.4 s, so
it reached the model rather than being admitted by a regex at 0.0 s, which
leaves the `type: llm` pattern as the only decision-maker, and that pattern runs
on **`gpt-5-nano`** reading a 250-word prompt whose refusal half is now two long
paragraphs. Yesterday's entry blamed the `action: require` opener gap; that gap
is real and is not what admits this sentence. **A prompt edit aimed at a
small classifier is not a fix until something scores it.**

**"The empty reply" was never one bug.** It appeared three times and the guard's
own fields separate three causes: `ask_clarification` ending a turn with no
prose *by design* (the case passed because the guard spoke for it); a provider
**connection reset mid-stream**, logged by the SDK and then indistinguishable
from an ordinary empty turn; and the eval harness's own 3-minute case timeout.
The guard was right all three times, including answering in Indonesian that no
query had run. What is wrong is upstream: a dropped connection is a failure with
a retry available, and `MeteredLLM.wrapStream` already sees the
`StreamEventError` that would say so. Left unbuilt deliberately — it wants a
decision about retry semantics.

**The grounding check was crying wolf again, in Indonesian, and this time it was
a parse bug.** `parseLoose` tried the English convention first and returned the
first reading that parsed, so **"Rp 21,23 Miliar" was read as 2123** — a
four-digit integer a hundred times the real figure, reported as ungrounded on
both models. Its own doc comment claimed it picked "the reading that yields a
plausible number"; it did not. Now decided from the token's shape, with a table
test over both conventions. **The reason no test caught it is the part to keep:**
`TestMagnitudeRenderingIsGrounded` has asserted `"Rp 3,86 Miliar"` since the
check shipped and passed — because that misparse is `386`, below the `v < 1000`
noise cutoff. A cutoff that suppresses noise also suppresses the evidence that
the parser is wrong.

**`T-H1`'s live gate passed, seventeen weeks after the bypass was found.** Three
forged POST shapes → 401, the Meta handshake with a wrong token → 403, and no
`ResolveCompanyByPhone` line anywhere in the run, so nothing touched tenant
state on the way to refusing. The finding worth the trip: **all three forged
requests took the Meta branch**, including the two carrying
`X-Twilio-Signature` and a form-encoded body — the transport is the
deployment's, and a caller cannot select the Twilio path with a header. That is
the vulnerability itself proven dead over HTTP rather than at the layer it lived
at. `T-H2` is still owed and now for a known reason: an unknown `app_id` is
refused `404` *before* the signature check, so the 401 needs a seeded tenant.
Written up in [`security-hardening.md`](security-hardening.md) §9, and the whole
`T-H` track — which this repo's single "what is owed" file had never carried —
is now in [`live-gate-backlog.md`](live-gate-backlog.md) §1c.

**The DSN key was recovered from a second working copy, and a third one is
gone.** The original was not in a password manager; it was in
`~/Work/smartsoft/argentum-mono/apps/backend/.env`, dated 31 July, and it opens
18 of the 20 stored connections. The other two — `Gate TV3`'s and
`EmbedGate`'s, written 9 and 10 August — open under **neither** the original nor
the 08-14 replacement, so a third key existed for those two days and is lost
too. No data of consequence went with it, because both rows are throwaway gate
tenants pointing at local demo containers. The mechanism is the point: **nothing
in this product notices an undecryptable connection until an agent turn fails in
front of whoever asked the question**, and a count at startup is small next to
that. It belongs to `T-H14`, which is written and unbuilt.

The eval tenant's two rows were **re-sealed rather than deleted**, because
`ensureSources` creates a source only when its *label* is missing — a re-seed
would have left them unreadable and looked like a passing dry run. The 56-case
run that followed executed SQL on every case, which is the end-to-end proof.

**`T-Q5` ran the same evening and found something worth more than the ranking.**
kimi-k2.6 87.5% / $0.631 / 41m against deepseek-v3.2 83.9% / $0.173 / 22m — 3.6
points for 3.6× the money. Two things fell out of the diff. **`ask_clarification`
is a model property**: deepseek calls the tool, kimi never does and asks in prose,
and both get *when* to ask wrong in the same direction — so the mechanism is not
ours to fix and the policy is. And **`guardrail-off-topic-recipe` passes on
deepseek only because that model declines on its own manners**, with the same
classifier returning the same TRUE in both runs. Every guardrail number this
project has published was deepseek's, so a broken gate has been reading as a
healthy category for as long as the category has existed.

**The classifier experiment ran the same day and refuted its own
recommendation.** The topic prompt was rewritten rule-first for the nano-class
model that evaluates it — the test in one sentence, the two confused families,
the twelve-item programming enumeration cut — and the recipe case failed again
on both models at temperature 0. Neither reply was our refusal message, which is
how we know the classifier admitted it twice. So `block_off_topic_cooking`
shipped instead: four phrase-level patterns, refused at **0.0 s with no model
call**. Phrases and not the bare word, with a golden pass list of five real
questions from a business that sells food. The rest of the general-knowledge
half is still the classifier's and therefore still unguarded, which is now
written down rather than papered over.

**Blocking it exposed a second bug in our own refusal.** The case failed again
on language: an English question refused in Indonesian. `resolveMessage` reads
the *composed* prompt, so the marker that chose the language came out of
`T-Q8`'s retrieved examples — somebody else's question — and the more Indonesian
a tenant's history, the more reliably its English speakers get Indonesian
refusals. Under it, `data` was on the Indonesian marker list: a word in both
languages and the median English question on a BI product. Both fixed with the
preludes stripped before detection. **kimi's guardrail slice went 6/8 → 8/8**;
deepseek stayed 7/8 on a case whose own notes say the refusal is the model's own
words, having passed it an hour earlier.

**Rewriting a prompt changed nothing and broke every golden case.** `stubLLM`
routes the topic verdict by matching the literal phrase *"You gate user
messages"*, which the rewrite deleted — so the stub errored, `action: require`
matched nothing, and all twenty-odd cases reported as blocked by the topic rule.
It reads like the rule went haywire and is a test fixture keyed to prose. Both
sides now carry a comment naming the other.

**The asking policy turned out not to be a prompt problem.** All four over-asks
across both models were the same question — *which time window?* — on questions
that already said all-time, and `query_metric` made `from`/`to` required while
the metrics context block carried no coverage dates. The model's three options
were invent a range, abandon the authoritative metric, or ask; the guideline
telling it not to over-ask had already lost, because **a guideline loses to a
missing affordance**. Both bounds are now optional and mean "every period the
data holds", with the floor at 1900 for SQL Server's `datetime` and the ceiling
one year out rather than 2999 for MySQL's `TIMESTAMP`.

**The prompt rule that shipped with it was too broad, and the re-score caught
it.** It fixed the four over-asks and broke the two cases where asking is right
— kimi stopped calling `ask_clarification`, deepseek guessed — taking kimi's
eight-case cluster from 5/8 to 4/8 while deepseek went 3/8 to 5/8. Narrowed to
name the time window and nothing else: **kimi 7/8, deepseek 8/8**. The model
comparison had predicted exactly this (*"a single fix aimed at 'clarification'
will get one of them wrong"*), and it was caught by scoring rather than by
reasoning.

**One of those passes revises this morning's conclusion.**
`dirty-ask-rather-than-guess` now passes on kimi, so kimi *does* call
`ask_clarification` once the rule stops over-reaching — the comparison's
"never calls the tool" was true of the prompt it read at the time, not of the
model.

**And a second fixture defect of the same shape as yesterday's.**
`grain-revenue-column-choice` asserted an SQL shape for *"What is our total
revenue?"*, which this product answers from a defined metric without SQL at all,
so it could not pass. Reframed to a channel breakdown, which no scalar metric
covers. The pattern is worth the name: a golden case that pins an
implementation route goes stale the moment the product grows a better route.

`go build`, `go test ./...` and `gofmt` clean over the tree.

---

## Phase 2p — The Rule 1 re-score, and four gates off the backlog (2026-08-14)

**The re-score first, because everything else waited behind it.**
`make eval-matrix` over both models on one commit: **kimi-k2.6 98.2% (55/56) at
$0.629, deepseek-v3.2 89.3% (50/56) at $0.141.** `guardrail` is **8/8 on both**
for the first time — the deterministic cooking block and the refusal-language
fix now carry a category that had been depending on deepseek's own manners — and
every category the asking-policy and metric-window work touched is at 100% on
kimi.

**98.2% is not a result to bank, by this project's own rule.** Above 95% means
the set has stopped discriminating, and the shape of the remaining failures says
the same thing: `zero_row_trap` is the only category either model fails, and it
is carrying the entire signal.

**The case both models fail is a product finding, not a fixture one.** *"Total
sales in Q3 2025"* against data ending in December 2024 comes back as **Rp 0** on
both. The eval tenant's metric template is `COALESCE(sum(…),0)` — as every sane
template is — so an out-of-coverage window is not `Empty` and the tool hands the
model the soft note (*"say which you mean only if you know"*) rather than the
hard one (*"this is NOT a zero"*). `metric_tools.go:248` predicts this in its own
comment. It is the `T-Q9` fabrication mechanism alive on the one path `T-Q9`
did not close, and the fix has a cost — one coverage query per metric call — so
it is written down rather than made unilaterally.

**Four live gates ran the same evening, and two found defects.**

- **`T-Q2`'s door** (§10 of [`agent-quality.md`](agent-quality.md)). Both 404s
  pass and the tenant boundary holds. A missing or out-of-range `rating`
  answered **500**: bare `fmt.Errorf` values, and `feedbackFail` maps the
  unrecognised to 500 — so the one input the handler's own comment calls a
  client bug was reported as a server fault. The unit test asserted
  `err != nil` and agreed with the code. Wrapped in `domain.ErrInvalidInput`,
  mapped to 400, tests tightened to the sentinel.
- **`T-H3`'s boot matrix** (§10 of
  [`security-hardening.md`](security-hardening.md)). All four required variables
  refuse with exit 1 on the real path; the WhatsApp rows warn and boot; all three
  plaintext-DSN registrations 400 over HTTP. **Two CORS findings**: the
  production warning could not fire for an unset or empty `CORS_ORIGINS` because
  `getEnv` substitutes the development default — so the likeliest production
  mistake silently left `http://localhost:5173` as the only allowed origin — and
  `middleware/cors.go` still claimed `Validate()` refuses to boot in that state,
  which stopped being true at `6248963`.
- **`T-Q7`** (§12). The summary block reaches a prompt on the 58-message thread,
  and the reply reconstructs the opening alert. **The log line proving it did not
  exist**: four silent exits, no success log, and nothing else records the
  composed user message — so an injected summary and a skipped one were
  byte-identical in the log, and one of those exits disables the feature on every
  thread while looking like a short conversation.
- **`T-H15`** (§11 of `security-hardening.md`). A rebinding resolver — public at
  check time, loopback by dial time — through the real `Deliverer` over real
  sockets: the dial went to the checked address, the loopback listener counted
  nothing, and the same answer dialled without the pin reached it. Not through
  `cmd/worker`, because the public rebinder is filtered by this machine's
  upstream resolver on 14 of 14 lookups.

**`T-Q6` is the one that came back with a smaller answer than the ticket
asked.** Its mechanism is proven — `role='tool'` rows exist, injection fires at
`PRIOR_WORK_TURNS=3` and never at `=0`, rows written at both — but two
three-turn conversations at the two settings produced **identical tool
sequences**. Inside `CONTEXT_MAX_TURNS` the assistant's own prior message quotes
the SQL it ran, so the digest repeats what the model can already read. The value
is real and appears where the history cannot help: the eval's
`follow-up-breakdown-no-reschema` — turn 1 answered from `query_metric`, so the
schema is *only* in the digest — passes on kimi and fails on deepseek. **A
two-turn conversation cannot measure this**, and the acceptance line should be
re-specified against a thread longer than the memory window.

**And a finding that belongs to the machine.** A `cmd/worker` started on
10 August was still consuming the `chat:run` queue with the pre-restore
`ARGENTUM_DSN_KEY`: the turns it stole answered *"there appears to be a
decryption problem with the database connection string"* and wrote a plausible
SQL answer around the failure. Two gate turns were lost before `ps` explained
why the worker log had stopped growing. Nothing in asynq says which worker took
a task, and nothing notices that two workers disagree about the key.

`go build ./...`, `go vet`, `gofmt` and `go test ./...` clean over the backend.

## Phase 2q — Seven items off the list, and a scanner that contradicted the roadmap (2026-08-14)

No eval spend and no live gate: everything here is code, unit tests and the
documents that describe them. Seven items, taken in the order the backlog put
them.

**1. The metric zero path is closed.** `query_metric` could not tell a genuine 0
from a window the data does not reach, because a `COALESCE(sum(...),0)` template
converts the unambiguous NULL into an ambiguous 0 before the tool sees it — the
T-Q9 fabrication mechanism on the one path T-Q9 left open, and the only case
both models failed in the Rule 1 re-score. On a zero, and only on a zero, the
service now runs the same metric over everything before the window and
everything after it, and returns one of four verdicts. Two of them —
`after_coverage`, `before_coverage` — also set `row_count` to 0 and `value` to
null, so T-16's grounding check *replaces* a reply that states a total rather
than trusting the model to take advice. **The re-score Rule 1 requires has not
been run**, and `zero-row-future-quarter` is therefore not yet known to pass.

**2. A deployment now knows whether its own key opens its own rows.** Nothing in
the product noticed a `db_connections` row sealed under a retired
`ARGENTUM_DSN_KEY`; the discovery path was an agent turn failing at query time
in front of whoever asked. Both `cmd/api` and the shared stack now sweep at boot
— *"N of M stored connections do not decrypt under the current key"*, at Warn,
with the ids — and `GET /api/connections/key-health` answers the same question
per tenant for the admin who can act on it. Never fatal: a deployment whose key
has moved on still serves every tenant whose rows were re-sealed.

**3. `internal/cache` is deleted.** Dead in every re-verification pass since the
native-dashboards roadmap was written: imported by no Go file,
`InvalidateSQLCache` a `return nil`, `InferQueryType` string-matching the year
`"2023"`.

**4. T-Q6's acceptance line was re-specified rather than re-run.** The 08-14 gate
proved the mechanism and could not measure the value, because inside
`CONTEXT_MAX_TURNS` the assistant's own message quotes the SQL the digest
repeats. The new line has a cheap arm — `follow-up-breakdown-no-reschema` at
`PRIOR_WORK_TURNS=3` and `=0`, two cases of spend, which is the measurement the
matrix run produced by accident — and a thread arm for production shape.

**5. `T-H7`: query text out of the logs.** The executed statement was logged at
Info with literals intact, so a question about one person wrote their identifier
into the operational log. A single-pass normaliser puts the shape at Info and
the raw statement at Debug. Numbers are normalised too, which is the half that
matters here: a NIK is sixteen digits and an Indonesian mobile is eleven. **The
empty-result probe's own log line was the same bug one file over** — it logged
the probe's entire disclosure at Info — and now names columns instead.

**6. `T-H10`: the empty-result probe is PII-aware.** Filter on an email column,
match nothing, and T-Q9's probe returned twenty real addresses the user's own
query did not fetch, on a path no output guardrail sees. A column whose *name*
announces its class is refused before the query runs; a column whose *values*
give it away is dropped whole after one. The tenant's `pii_redaction_mode`
decides, and unset, unknown or unreadable all read as strict.

**7. `T-H13`: three scanners in CI — and the first run contradicted the roadmap
that asked for them.** That document said this project's dependencies were
"current today" and listed four versions to prove it. `govulncheck` found **25
called vulnerabilities** — reachable symbols, not affected-version noise. Seven
modules were bumped (grpc, x/net, x/text, excelize, quic-go, the aws-sdk pair,
otel across five modules) and eighteen were the standard library at go1.26.2,
closed by moving `go.mod` to `go 1.26.6`. After both: zero. `gitleaks` over the
full history, `gosec` at high/high where the tree is clean, and
`dependency-review-action` on PRs join it, all blocking — **and all four were
made green before being made blocking**, because a scanner introduced red is a
scanner people learn to route around. The 15 medium-severity gosec findings the
bar excludes are itemised rather than silently skipped.

`go build ./...`, `go vet`, `golangci-lint` (0 issues), `gofmt` and
`go test -race ./...` clean over the backend.

**What this phase owes**, in [`live-gate-backlog.md`](live-gate-backlog.md) §1d
and §2: three stack-only gates that need no money (the log line, the probe under
three redaction modes, the boot count — which should read *18 of 20* on this
machine), the CI job proving itself on GitHub, and one full-set re-score for the
metric zero path.

## Phase 2r — §1d run: three gates, and a blank sentence beside the widest scope (2026-08-16)

The stack-only half of the 2026-08-14 build, run the next working morning.
About an hour, `$0.00` of model spend, and the sixth sitting of the bucket this
project's record says always pays. **Three gates passed and one defect was found
— not in the code the gates were aimed at, but in an endpoint one of them had to
call on the way.**

**1. The boot count reads exactly what the by-hand check predicted.** `cmd/api`
against the control database: `total 20`, `undecryptable 2`, `companies 2`, with
the ids. Those two rows are `Demo analytics` (2026-08-09, Gate TV3) and the
unlabelled `EmbedGate` source (2026-08-10) — the pair §1b found opening under
neither the 31-July key nor the 08-14 replacement. **18 of 20**, as
[`live-gate-backlog.md`](live-gate-backlog.md) §1d said it should, which is the
whole value of a gate whose answer is known in advance: if it had said anything
else, the by-hand check was wrong. `GET /api/connections/key-health` answered the
per-tenant question for the gate tenant (`total 1, undecryptable 0`), and
`cmd/mcp` ran the same sweep on its own boot — `total 21` once the gate tenant's
connection existed, the same two unreadable.

**2. The gates were driven without an LLM, deliberately.** `run_sql` is not
reachable from `cmd/api`: a turn runs in `cmd/worker` behind a model. But the
MCP server exposes the same tool instance — `internal/mcpserver` adapts the
registry rather than reimplementing it — so a `read:data` key and forty lines of
JSON-RPC drive the exact code path a turn drives, with the arguments chosen by
the gate instead of by a model. A tenant (`Gate H7H10`), the demo warehouse
registered through `POST /api/connections`, and a key was the whole setup. **What
this does not prove** is the one thing a model adds: that a turn's *generated*
SQL carries the literals this normaliser sees. The log line is the same line.

**3. `T-H7` passes on both halves.** One statement carrying an email literal, a
sixteen-digit NIK inside a `/* */` comment, a phone number, a `--` comment and a
`t1.` alias. At `LOG_LEVEL=info` — what `.env` runs at — the `sql` field is the
shape: `t1.email = '?'`, `t1.customer_id = ?`, both comments gone, the aliases
and table names intact, and **no `sql_raw` key at all**. All three secrets are
absent as substrings from the entire Info-level slice, not just from that line.
At `LOG_LEVEL=debug` the same Info line appears and `sql_raw` beside it, with the
statement byte-for-byte.

**4. `T-H10` passes on all four cases, and one of them is proven at the
network.** Under `strict`, the zero-row `email = 'budi@examle.co.id'` query
returned the plain note and no `available_values` — and the demo warehouse's own
statement log (`log_statement = all` for the duration) shows *only the user's
query*: `BEGIN READ ONLY`, the SELECT, `ROLLBACK`. No probe. Under `contact_ok`
the same request produced `SELECT DISTINCT email FROM dim_customers …` in that
log and twenty real addresses in the payload; under `off`, the same. Under
`strict` again, a filter on `city` still probes and returns the seven real cities
— T-Q9's case survives the fix, which was the trade worth checking. The query
that did it *selected* `email` while filtering on `city`, and only the filtered
column was probed.

**5. And the probe's own log line, which is the sharpest single result here.**
Under `contact_ok` at Info, the tool handed **twenty real customer addresses to
the caller** — the tenant's policy says that is allowed — and wrote **none of
them to the log**: `probed_columns: dim_customers.email`, no `probes` key, and no
`@email.com` anywhere in the slice. Before `T-H7`, that line was the payload.

**6. The defect: two scopes are offered with no description.**
`GET /api/api-keys/scopes` exists so that a scope added on the backend reaches
the dashboard "without a second edit, and so there is exactly one place where a
capability is described to a human" — its own comment. `T-14` added `read:data`
and `write:visualizations` to the vocabulary and not to that map, so both have
been served with `"description": ""` ever since, and the blank one is on the
**widest read capability a key can carry**: arbitrary SQL over every table the
connection can see. A tenant ticking that box in the dashboard was told nothing
about it. Fixed with two sentences, and with `TestEveryScopeHasADescription` plus
its over-the-wire twin — proven failing on the old map, naming both scopes, then
re-read live off the rebuilt binary.

**Found the way the last five sittings' findings were found**: not by looking at
the thing under test. The scopes endpoint was called to mint a key for `T-H7`.

**What §1d still owes:** `T-H13`, which is not runnable here — the assertion is
that the CI job runs and blocks on the next pull request.

**One note for the next run.** `ALTER SYSTEM SET log_statement = 'all'` on the
target warehouse plus `pg_reload_conf()` is the cheapest way to assert a query
did *not* happen, which is otherwise the hardest kind of claim to gate. Reset it
afterwards (`ALTER SYSTEM RESET log_statement`) — this run did.

## Phase 2s — The two owed re-scores, and the discovery that the set has weather (2026-08-16)

The metric zero path's rule 1 re-score and `T-Q3`'s before/after, run in one
sitting for **$1.15 across eleven invocations**. Both answered. What neither was
asked, and what the sitting is actually worth, is the third result: **this set
has a ±2-case run-to-run noise band, and this project has been publishing
one-case deltas as findings.**

**1. The metric zero path works.** `zero-row-future-quarter` — the case both
models failed on 08-14 with "Rp 0" — passes on kimi, and the reply carries the
warehouse's true coverage window, *"1 July 2024 to 31 December 2024"*, which is
in neither the question nor the prompt. `zero_row_trap` 2/3 → **3/3**. On
deepseek the case still fails, but on different behaviour: it names the coverage
correctly, then volunteers the covered period's total, which `no_figure: true`
refuses. Nothing was over-hedged — `simple_aggregate` 7/7 on both.

**2. Half of deepseek's failure list was one defect, and it is a retry loop.**
`query_metric` refuses a window carrying one bound, deliberately. It refused with
a *Go error*, and deepseek answers an error by re-sending the identical call:
seven `{"metric_key":"revenue","to":"2024-12-31"}` in one turn, then `blocked` by
T-16's budget, then a reply with no figure. **Five of ten failures**, eight
iterations each, and the model narrates the correction it never makes: *"I need
to specify both the start and end dates."*

The refusal is now a result the model can act on — `halfWindow`, the same trade
`unknownKey` already makes — with `row_count: 0` so it cannot ground a figure,
and a test proven failing on the old code first. **Then the honest part: it does
not rescue the turn.** Re-run, the calls come back `ok` and deepseek sends the
same arguments anyway (0/3). A prompt bullet spelling out that the bounds travel
together did no better (0/3, twice) and was **reverted under rule 1** rather than
shipped. What is left is a repeat-guard in the agent loop, which is where the
failure actually lives — ten other tool paths return a Go error to a caller
mistake and nothing about the loop is specific to this one.

**3. The attribution arms are what make the numbers mean anything.** Every
regression was re-run at `65642c3`, the commit the 08-14 re-score was taken at,
with `internal/eval` and `golden.yaml` byte-identical across the two. All five
deepseek failures reproduce there. **Nothing this repo shipped since the last
measurement caused its seven-point drop** — the defect was latent from the
optional-window change on 08-14 and waited for a model to trigger it.

**4. And kimi's two "regressions" are weather.** Both passed at `65642c3`, so
they looked caused. Re-run three more times on main: `last-month-relative` fails
1 of 4, `dirty-ask-rather-than-guess` 2 of 4. Both are the same behaviour — the
agent asks a clarifying question where the case wants a figure. **kimi is 96.4%
or 98.2% depending on the day.** The 08-14 entry reading "98.2%, up from 87.5%"
is, in its first half, a sample.

**5. `T-Q3` is measured at last, and the answer is no.** 54/56 with the
chart-restraint guideline and 54/56 without it, from two different pairs of
failures. deepseek's before-arm built no unrequested chart at all. kimi's built
exactly one — a card and a dashboard for `id-kanal-terbesar` — which is one event
inside the noise band §4 measured. The ticket keeps its argument and still has no
number.

**What it did produce is an instrument fix.** All three `no_chart_wanted`
assertions are in English; the one violation landed on the Indonesian twin of one
of them, which asserted `must_call: [run_sql]` and nothing about charts — so the
set scored it a pass. A restraint rule written in English was only ever tested in
English. All five `indonesian` cases now carry `must_not_call`, verified free
against both arms of today's data.

**One process note, because it cost twenty minutes.** The first invocation was
killed at case 50 of 56 by the harness running it. `setsid` does not exist on
macOS; a `python3` fork + `os.setsid` around a **prebuilt** binary (not `go run`,
whose child dies with its parent) survived every later arm. Recovering the 49
completed cases rather than restarting saved about $0.55.

**6. And pushing it closed `T-H13` by failing.** The commit above went to `main`,
which is the first time `Security scanning` has run on anything, and it went red:
**`GO-2026-6222`**, excessive memory allocation decoding VP8L in
`golang.org/x/image@v0.43.0`, with a reachable trace rather than a transitive
advisory — `internal/branding/service.go:197` → `NormalizeLogo` → `image.Decode`
→ `vp8l.Decode`. The input is a tenant's uploaded logo, so a crafted WebP is an
OOM on the API from an ordinary authenticated path. Bumped to `v0.45.0`
(`x/sys` and `x/text` came along), `govulncheck` reads *No vulnerabilities
found*, and the branding and report packages pass.

**Nothing in this repository changed to cause it.** `c74a890` ran all three
scanners by hand on 08-14 and recorded them green, and that was true when it was
written; the advisory database moved underneath it. A scanner that runs on every
push catches that and a person checking once does not, which is the whole reason
the ticket exists — and it took eleven days from the ticket landing to the job
ever executing. `dependency-review` is still unrun: it is gated on
`github.event_name == 'pull_request'`, and nothing has opened one.

## Phase 2t — The dashboard stops being Metabase's (2026-08-17)

Eight commits, `105ad5b`→`12ba63e`: `T-H4` step 1, `T-D3`→`T-D7`, `T-D10`,
`T-D11`, plus three surfaces the roadmap did not carry — the chart drawn inside
the chat transcript, the dashboards page and its nav entry, and a dark ramp for
the chart palette. Track F's `T-D17`/`T-D18`/`T-D19`/`T-D20` are substantially
covered by those three; `T-D8` (panel cache), `T-D9` (query log) and Tracks
D-sharing/E-decommission are not built.

**The shape of it.** A dashboard is now a stored *spec* this product executes —
panels bind filter values as query parameters and never interpolate them, a
`date_range` named `period` binds `{{period_from}}`/`{{period_to}}`, a stored
default is a preset *name* (`last_30d`, `qtd`, …) rather than two timestamps,
and resolve caps 2000/500/2 rows by viz across four concurrent panels under a
15s deadline each. `create_visualization` and `thread_cards.go` are deleted with
the pair they belonged to. That deletion also removed a standing violation of
`workspace-context.md` §2: `create_dashboard` used to resolve "the cards made
earlier in this conversation" out of a package-level map, which does not survive
a worker restart and is wrong the moment there are two workers.

**1. The gate paid for the sixth time in six, and both defects were the
model's-eye view.** Stack up, `moonshotai/kimi-k2.6`, one real turn.

- `create_dashboard` **refused a call that omitted `source_id`** on a
  one-source company, because it was the only data tool not going through
  `ResolveSource` — the choke point that fills the source in and enforces the
  roster's allowlist. The turn spent an iteration learning a rule the product
  does not have.
- **An empty dashboard, described in confident prose.** The agent queried
  2020→2025, found six months of data, then gave the dashboard the default
  `last_30d` window. The demo warehouse holds July–December 2024; in 2026 that
  window matches nothing, so every panel drew zero rows while the reply quoted
  *"$12.73B"* from its own `run_sql` calls. Validate-on-save warned on a panel
  *error*, and "matched no rows" is not an error — it is the correct result of
  the window it was given. This is the metric registry's `Rp 0` finding from the
  other side, and the fix is the same one: the tool has to say which of the two
  it is. `dryRun` now warns on a zero-row panel with the window in the message,
  and `spec.Project` sets the note on every viz rather than only on a KPI.

Re-run, same question: one `create_dashboard` call, and the model passed the
warning to the user in its own words — *"The demo dataset contains sales from
July 2024 to December 2024… adjust the Period filter"*. Migration `056` was
gated the same sitting: up, down against a populated table, up again, against
the real control database, with `ON DELETE RESTRICT` proven by Postgres refusing
to delete a connection a dashboard reads
([`native-dashboards.md`](native-dashboards.md)).

**2. Eight chart colours had never been measured against the surface they are
now drawn on.** The ladder was gated against greyscale, deuteranopia and
protanopia — for paper. When the transcript started drawing panels, series 2
(navy) was **1.35:1** and series 7 (brown) **1.80:1** against the dark card.
`tokens.json` gains `chart.paletteDark`, emitted under `.dark`, and **four of
the eight did not move**: the method was measure-first and lift only the
failures, so switching theme does not make a reader re-learn which line is
revenue. Dark ramp measured by `make palette`: normal-vision ΔE 19.6,
deuteranopia 14.2, protanopia 13.0, weakest contrast 3.52:1. The dark ramp
carries **no greyscale floor** — that floor exists for the office laser printer
an enterprise PDF comes out of, and nothing prints a dark dashboard.

**And the new check found debt in the light ramp that this phase did not
fix.** Amber 2.04:1, grey 1.61:1 and azure 2.58:1 on white are below the 3:1
line for a non-text mark, and have been since `T-R3`. Raising them re-renders
the palette every delivered PDF was made with. It is a warning on every run
rather than a gate, so the debt is visible instead of remembered.

**3. The first screenshot found the third defect, which is the argument for
screenshots.** Monthly revenue on the demo warehouse is in the billions;
`3,240,929,900` does not fit a 48px axis gutter, so three different ticks
rendered as the same clipped `100,000`. **A chart whose axis contradicts its own
bars is worse than one with no axis** — nothing tells the reader the number is
wrong. Axis ticks now have their own compact formatter (`3.2B`) in the viewer's
locale and 56px of gutter; the tooltip keeps full precision. Two formatters
rather than one with a flag: an axis says how big, a tooltip says how much.

**What this phase owes.** `T-D8` and `T-D9` are unbuilt, so every open of a
dashboard runs every panel against the tenant warehouse — the caps hold, but
nothing collapses two viewers opening the same dashboard at once. The browser
half is now *partly* run (the panel grid and the axis fix were seen; the
`/dashboards` list page, the chat embed and the dark ramp on a real dark card
were not). And the eval set's chart cases were rewritten to assert
`create_dashboard` rather than the deleted pair, with nothing scored since.

**One process note, and it is the reason this entry exists.** These eight
commits sat on `main` for a day with no entry here and no coverage row moved —
`feature-coverage.md` still advertised Metabase card creation as a capability
and the MCP row still named a tool that had been deleted. The docs were brought
back to the tree on 2026-08-17; what made them drift was that the work landed in
one long sitting and the record was written for the *ticket* (`native-dashboards.md`)
rather than for the *product*.

## Phase 2u — The loop after the answer (2026-08-17)

`T-Q10`, `T-U13`, `T-D22`, `T-D23` — the four tickets in
[`../plan/05-next-steps-and-dashboard-revision.md`](../plan/05-next-steps-and-dashboard-revision.md),
built in one sitting. Two asks with one shape: the product ended every turn at a
full stop, and a dashboard the agent built could be created or deleted and
nothing else.

**The shape of it.** After an answer, one light-model pass asks what is worth
asking next; at most three suggestions, at most one marked recommended, narrowed
server-side against the tools the turn actually held, persisted on
`messages.metadata` and published on the `final` event. The dashboard draws them
as chips under the newest answer, a click fills the composer and sends nothing,
and the pick writes a row — because a suggestion nobody clicks is worse than no
suggestion, and the pick rate is the only thing that can say which this is.
Beside that, `update_dashboard`: a **patch** against a stored dashboard,
resolved to this thread's when no id is given, refusing a source change, merged
through `DashboardService.Update` so the validation and the zero-row warning
Phase 2t added stay one code path. And an "Ask for a change" action on the
dashboard itself, which prefills chat with the dashboard's own markdown link.

**And then it was run, the same day.** The entry above was written saying
nothing had been; §1f was filed in the cheap bucket, ran an hour later for about
**$0.12**, and the bucket has now paid **eight times out of eight**. Migrations
`057`/`058` up, down and up against the real control database — the 39
unrestricted agent rows byte-identical through the whole round trip, which is the
outcome that would have been catastrophic and silent to get wrong. The pick
endpoint's 404/400/200 and its role split over HTTP, including a client posting
an invented label and getting the message's own back. The suggestion pass on live
turns, on the `final` event, and switched off.

**Two defects, and they belong to different owners.**

**One is this build's, and it is the C-2 shape.** `T-Q10` specifies a 5s timeout
for the suggestion pass; this deployment's light model takes **12.5–16.6s**, so
the feature shipped switched on, billing nothing and doing nothing, and saying so
only at `Info`. The budget is now `NEXT_STEPS_TIMEOUT_SECS`, exhausting it logs
at `Warn`, and the pass is timed — because the ticket's entire design rests on a
latency budget and there was no way to read the latency. With room it works: 3
steps in 12,962 ms, no figure restated in front of a `$12,729,714,500.00` answer,
and *"Compare December 2024 to December 2023"* surviving the figure rule, which
is the year-versus-figure correction confirmed live rather than only in a test.

**The other is older than this build and larger than it.** A persisted answer
reading *"There were **1,667 transactions** in November 2024. … There were
**300 transactions** in November 2024."* — 300 is what the tool returned, and
1,667 is in no table. The turn carried `iteration: 2` and the concatenation of
its delta events **is** the stored content: the model wrote a sentence with an
invented figure before calling the tool, and `runStream` keeps every iteration's
prose. `CheckFabrication` passed it because it grounds on `DataRows > 0` — it
asks *"is there evidence?"* where this needs *"is every figure evidenced?"*. **The
turn that exposed it ran with the new feature switched off**, so the control is
in the transcript. Not fixed here: both plausible fixes are decisions, and it is
filed as §3b of [`live-gate-backlog.md`](live-gate-backlog.md) with a
reproduction.

**The sitting also lost time to a message this log has already recorded once.**
`docker ps` answering *"client version 1.43 is too old"* reads as "Docker is not
running"; the daemon was up and the client first on `PATH` was too old for it.
Second occurrence, now written where somebody will be standing.

**One defect the build found in its own ticket, and it is the entry worth
keeping.** `T-Q10` specifies dropping any suggestion containing a run of four or
more digits, so that a chip cannot restate a figure. A year is four digits. The
rule as written deletes *"compare with 2024"* — one of the most useful things
this feature can suggest and among the likeliest to be clicked. A test caught it
before the first live turn ever could have: the shipped rule separates a figure
from a period (grouped or decimal numbers and runs of five or more are figures;
four digits inside 1900–2099 is a year), and the ticket now carries the
correction rather than the original line. Three smaller departures — the metering
event, where the held tools are read from, and the migration numbers — are in
[`next-steps-and-revision.md`](next-steps-and-revision.md) §2.

**What this phase owes, revised after the gate.** The two numbers exist now and
they settle the question against the ticket: **607 µUSD per pass — about 3% of
the turn beside it — and 12,962 ms.** Cheap and slow, where `T-Q10` assumed the
opposite, and its own rule is to revisit above 1s. So the open item is not a
measurement any more, it is a decision: accept ~13s in front of every answer,
point the light tier at a non-reasoning model, or take the design `T-Q10`
rejected and move the pass behind `final` with a second event and a message
`UPDATE`. Still owed besides that: `T-D22`'s four-turn edit gate, the scoped-agent
arm of `T-Q10` (the only live cover for the tool-allowlist narrowing), a browser
for the chips, and `T-D13`.

## Phase 2v — The browser bucket, emptied for nothing (2026-08-17)

The third sitting of one day, and the only one in this log that **spent no
money at all**. §3a of [`live-gate-backlog.md`](live-gate-backlog.md) had been
open for eight hours: twelve acceptance items across `T-D11`, `T-U13` and
`T-D23`, every one of them a thing somebody has to *look at*. All twelve were
run against what the two earlier sittings left in the database — their panels,
their dashboards, their suggestions — so the marginal cost of the gate was an
hour and $0.00.

**Nine sittings, nine that found something. Three defects here**, and none of
them could have arrived through HTTP:

- **A table panel ignores its own `fmt`.** `Top 5 Products by Revenue` prints
  `20727672550.00` while the panel declares `currency` and the bar chart beside
  it reads `3.2B`. One line explains it — `spec/project.go:90` hands the table
  `res.Rows` untouched while every other viz coerces through `cell()`, and a
  Postgres `numeric` arrives as a **string**, which the browser's formatter
  correctly declines to format because a table also holds product names. Left
  unfixed deliberately: coercing in the browser turns an order id of `0012` into
  `12`, and coercing server-side needs column types the payload does not carry.
- **The chat embed is a `<section>` inside a `<p>`.** React inserts it happily;
  an HTML parser would split the paragraph at it, so the cost is a hydration
  mismatch on any path that ever parses this markup instead of building it.
  **Fixed**: a paragraph that will hold a dashboard link renders as a `div` with
  the same spacing, and nothing else changes.
- **Nothing focuses the composer.** `activeElement` was `BODY` after T-D23's
  "Ask for a change" and the clicked `BUTTON` after a next-step chip. Both
  features put a sentence in the box and left the cursor outside it, which made
  the ticket's own *"the cursor lands after the link"* untrue. **Fixed** for
  those two and for the starter questions, which shared the defect and had no
  gate item; re-proven at caret 80 of 80 and 25 of 25, with no turn started.

**Both fixes were re-read in the browser, and the first attempt at the second
one is the entry worth keeping.** It asked the *rendered children* whether one
of them was the embed, which cannot work: react-markdown passes `p` the
component it will call for the anchor, never what that component returns. It
type-checked, it linted, it read correctly, and the re-read came back `P`
exactly as before. The question belongs to the markdown node, which knows the
href before anything decides what to draw for it.

**What passed is the larger half.** The `/dashboards` list page, the embed's
panels inside a real transcript, all eight dark chart tokens on a real dark card,
the chip row in both themes with its recommended chip leading and its reason as
visible text, a click that filled the composer and started **no** turn, the
`suggestion_picks` row that followed it (`idx=1` for a chip rendered first —
display order and stored index kept apart, as designed), and the absence that
was most likely to be quietly wrong: two *older* messages carrying `next_steps`
in `metadata` and drawing nothing.

**And the sitting's own lesson, which is this log's oldest one.** The Claude
Chrome extension was not connected, and that reads as "no browser available" —
the same misreading as `docker ps` answering *"client version 1.43 is too old"*,
now recorded three times in this repository. Headful Chrome with
`--remote-debugging-port` and a twenty-line CDP client is the same rendering
engine looking at the same page.

## Phase 2w — The edit gate, and an agent that said "Done" twice without doing anything (2026-08-18)

No commits. This phase is a **gate**, not a build: `T-D22`'s four-turn edit
sequence had been sitting in
[`live-gate-backlog.md`](live-gate-backlog.md) §2 behind a cost since the day
the ticket landed. It cost **$0.119** across six turns on `kimi-k2.6`, and it is
the tenth sitting in a row to find something.

**What it was meant to prove, and did.** `update_dashboard` is sound. A patch
left the unnamed panel byte-identical under `jq -S`; the id and URL held across
an edit and a rename; a panel addressed by a title that does not exist came back
with the real titles listed; a `viz` change that invalidated the mapping was
refused by name and the model fixed it on the next call; a mapping naming a
column the SQL never returned was caught by `dryRun`'s warning path — its first
live exercise — and self-corrected. And the item the ticket cared most about
passed cleanly: **no id, no thread dashboard → a result, not a Go error.** One
call, `rows_returned=0`, 4 ms, a reply naming both candidates and asking which.
The 2026-08-14 measurement of what a Go error does to deepseek (the identical
call, seven times, until the budget ends the turn) is what that design exists to
prevent, and it holds.

**What it actually found is a P0 in a different subsystem.** Two consecutive
turns told the user an edit was done, having called **no tool at all**. The
dashboard never changed — same `updated_at`, same `viz`, same title — and
`agent_actions` has no row for either turn. The worker log: `iteration=1`,
`Skipping final synthesis call - already got complete response`, twelve seconds.

The mechanism is one missing key. Turn 1 exhausted its iteration budget, so its
last `update_dashboard` was refused by `agentbudget` — correctly, and as a
*result* rather than a Go error, which is the design that stops a model looping.
That payload is `{"budget_exhausted": true, "reason": …}`. `BuildToolDigest`
decides a call failed by looking for `result["error"]` or `result["err"]`, and
finds neither, so T-Q6's persisted memory recorded
`{"tool":"update_dashboard","rows":-1}` — what a *successful* call looks like.
The next turn read that as work already done. Then its own "Done" became the
third turn's evidence.

**`agentbudget.IsRefusal` exists for exactly this distinction and the digest
does not call it.** Its comment says the audit log needs it because *"both come
back as a string with a nil error, because that is how the model has to receive
them"*. The audit table got it right (`result_status = blocked`). The memory the
agent reads did not. Ticketed the same night as **`T-Q12`** (P0, 1.0d,
[`../plan/02-agent-quality-roadmap.md`](../plan/02-agent-quality-roadmap.md)),
beside `T-Q11` because both are an unevidenced claim reaching the user of
record — `T-Q11` about a figure inside a turn, `T-Q12` about what one turn tells
the next it did.

**The control is the part worth copying.** The same sentence — *"rename that
dashboard to X"* — on a thread whose history held a genuine success: the tool
was called and the rename landed. Two histories, two behaviours, one
differentiator. That turned "the model was lazy" into a diagnosis with a line
number, for the price of one extra turn.

**A second finding, and it is a decision.** The gate's first request —
*"default the period to the fourth quarter of 2024"* — cannot be expressed. A
`date_range` default must be a preset name, so the model saved `qtd`, which in
August 2026 matches nothing; both panels warned at save and the model told the
user to change the filter by hand on every open. Filed as `T-D24`. It also cost
that turn two of its eight iterations, which is what left no room for the edit
it then attempted — the first half of the chain above.

**And `T-Q11`'s mechanism showed up on a benign turn**: *"…to build this
correctlyYour **Q4 2024 Sales** dashboard is ready"* — pre-tool narration glued
to the post-tool answer, no fabricated figure attached. The cheapest
reproduction of that ticket yet.

**The lesson this phase adds to the nine before it.** This log has been building
a case that the cheap bucket — stack-only, browser-only, read-what-an-earlier-turn-wrote
— is where the defects are, because nine sittings of it paid nine times. That is
still true and it is now also a bias: **a P0 sat in the expensive bucket for a
day**, and it cost twelve cents to find. The rule that comes out of it is not
"spend more", it is that a gate deferred for cost should carry the cost estimate
next to it, because $0.12 and $12 are not the same decision and the file did not
distinguish them.

## Phase 2x — Three tickets from two gates: what a turn claims, and what it cannot say (2026-08-18)

The two gates of 08-17 and 08-18 left three tickets and no code. This phase is
the code: **`T-Q11`, `T-Q12` and `T-D24`**, unit-gated in one sitting —
`go build ./...`, `go test ./...`, `go vet ./...`, `make lint-go` (0 issues),
`make types-check`, `make lint-web` (0 errors). No live turn was run, which is
the state this log has recorded seven builds hiding something in, and the gates
are filed in [`live-gate-backlog.md`](live-gate-backlog.md) §2 **with their
prices**, which is the rule Phase 2w asked for.

**Two of the three are the same sentence.** Something reached the user of record
that no evidence supports: `T-Q11` a figure inside one turn, `T-Q12` what one
turn tells the next it did. Neither was fixable in the prompt, because in both
cases the product was handing the model false evidence.

**`T-Q12` — a refused call is no longer remembered as one that ran.** The digest
carries an outcome (`ok` / `failed` / `refused`) read from what the executor
returned rather than inferred from an absent `error` key, and the prior-work
block now says *"REFUSED, it did NOT run"* above an instruction that refused work
must be **done** in this turn, never reported as done. Two things the ticket did
not predict came out of building it. The Go-error path *does* emit a tool-result
event — but as the plain string `Error executing tool: …`, which unmarshals to an
empty map, so the raw result now travels beside the parsed one. And
`DedupeDigests` had the same defect wearing the opposite sign: keyed without the
outcome, a call that was refused and then made properly collapsed to the
refusal, so marking refusals alone would have turned *"it thinks it ran"* into
*"it thinks it never ran"*.

**`T-Q11` — the record is the last iteration that produced prose.** The stream is
untouched: every delta still reaches the reader, because watching the model think
is the feature. What narrowed is what `completeWith` persists. The build found
the reason a naive fix would have failed: agent-sdk-go can withhold intermediate
content and **replay it after** the final iteration, and its synthesis call is
tagged `final_call` with no iteration number at all — so "keep the last prose
that arrived" would store the narration on one path and nothing on the other.
The selection is made on the iteration number, with the synthesis call winning
outright, and a turn no provider tagged concatenates exactly as before. This
deployment meets neither path today (`IncludeIntermediateMessages: true`), which
is precisely why both had to be handled: that is one line in `stack.go`.

**And the detector is now an instrument.** `CheckGrounding` has been asking the
right question and writing one `Warn` line nobody read.
`ungrounded_replies_total` and `ungrounded_figures_total` count it, the count
lands on the turn's span, and a new one-line-per-turn `turn completed` record
carries it beside latency and tool calls. **The honest caveat is written into the
code:** a turn runs in `cmd/worker`, which has no HTTP surface, so `/metrics` on
`cmd/api` will not show these move. That is T-17's debt; until it is paid the
number is read from the log line and the span, and a gate that curls `/metrics`
and sees zero has found nothing.

**`T-D24` — a dashboard can finally default to the period it is about.** Option 1
of the three the ticket offered, and the decision turned out to be less open than
it looked: `update_dashboard`'s description has promised
`{from: 'YYYY-MM-DD', to: 'YYYY-MM-DD'}` since the day it shipped, its parser
already built that shape, and the test written from *"just make it 2024"*
asserts it — all against a fake service, so `spec.Validate` never saw the value
it would have refused. **A capability the product had already told the model it
had, which the validator killed on arrival.** Options 2 and 3 would have meant
deleting it. Presets are untouched, a malformed window is refused by name at save
*and* at bind, and the inclusive-`to` convention matches the one an explicit
request range already uses — the reason "1–31 January" does not silently drop the
31st.

**One thing the frontend did not need, and it is a finding.** T-D24's FE bullet
asked the filter UI to render an absolute default as dates. There is no filter
UI: `dashboard-view.tsx:89` prints `applied_filters` as text and nothing else.
So a fixed window reads correctly for free — and the gate transcript in which the
model told the user to *"change the Period filter when you open the dashboard"*
was advice about a control that does not exist. A dashboard whose window can only
be changed by asking the agent is a product decision nobody has made yet.

## Phase 2y — Two verbs become three choices with sentences (2026-08-18)

Owner's call, same day, no ticket: the decision surfaces were restyled onto one
component — `components/ui/decision-card.tsx` — and both callers moved to it.
`tsc -b` and eslint clean (0 errors), and both themes were looked at in a
browser rather than assumed.

**What changed and why it is not only styling.** The approval card was a
sentence over an Approve/Reject pair, and the next-step row was chips. Both
stated verbs and left the consequences to be inferred. A stacked option carries
its own line — *"Runs this once, exactly as described. It cannot be taken
back."* — which is the difference between authorising something and agreeing to
something. The chips could not carry `why` at all: it was a `title` attribute,
unreachable without a mouse, plus one trailing sentence for whichever suggestion
led, so the agent's reasoning about the other two was written, stored and never
shown.

**One decision inside it worth defending.** The reference design marks the
recommended option with a tinted row, a check and a *"…'s lean"* chip. That is
applied to the next-step card, where the lean is data the agent produced
(`recommended` + `why`), and **deliberately not** to the approval card: an agent
proposing an action is not the same as it recommending you authorise one, and
tinting *"Go ahead"* green before a human decides is a nudge toward the
irreversible half of a choice this product exists to put in front of a person.
On an approval the mark appears **after** the decision, on whichever option was
taken.

**Two smaller things it added.** A third option — *"Adjust"* — that prefills the
composer, decides nothing and leaves the proposal open, which is the reply a
reader wanted a way to give and previously had to construct by hand. And a
footer that says who decided and when: `decided_by` is a user id with no
directory behind it, so it reads *"you"* or *"a teammate"* rather than inventing
a name for an audited decision.

**And a rule reversed, also the owner's call: a tapped next step now runs.**
T-U13 made it fill the composer, on the starter questions' reasoning — *a turn
that runs before the reader has read it teaches nothing and spends a credit*.
That reasoning holds for a starter question, which is the first thing somebody
sees with no answer above it to judge against, and does not transfer: a next
step sits under an answer the reader has just read, in the agent's own words,
with its reasoning beside it, so the decision has been made by the time it is
tapped and the composer asks for it twice. The credit half of the argument
survives as the constraint that already existed — one card, under the newest
answer, never while a turn is in flight — and the pick is now recorded *before*
the send, because the send navigates and unmounts the component that would have
written it. The starter questions and the dashboard's "Ask for a change" still
fill and never send.

**The card that reversal produced needed a way out, and got one the same day.**
Once every row spends a credit on touch, "none of these" stops being a thing the
reader can *do* and becomes only a thing they can avoid — the card sits under the
newest answer with three options and no fourth. So a last row, the same size and
shape as the others: *"Nothing right now"*, which hides the card and touches
neither the composer nor the API. It is the one row `sending` does not grey — the
others are disabled during the gap between a tap and the stream because a second
tap there queues a second turn, and declining sends nothing, so making somebody
wait out a turn before they may say they are done is the disabled state
protecting the wrong thing. The decline is not posted to
`/messages/:id/suggestion-picked`: `pick_rate`'s denominator is answers that
*offered* a suggestion, and a decline is exactly the case that denominator is
meant to count against.

## Phase 2z — The first sitting where every ticket passed, and it still found three things (2026-08-18)

The live halves of Phase 2x (`T-Q11`, `T-Q12`, `T-D24`) were run the same day
they were priced, together with two arms that had been owed longer — `T-Q10`'s
narrowed-agent case and `T-D23`'s edit turn — and the browser half of Phase 2y.
About **$0.35 across roughly eighteen turns** on `moonshotai/kimi-k2.6` with
`openai/gpt-5-nano` as the light tier. Full table:
[`live-gate-backlog.md`](live-gate-backlog.md) §1g.

**All three tickets passed what they were written to prove**, which has not
happened before in this file's record. `T-Q11`'s persisted answer is 49
characters — *"There were **300 transactions** in November 2024."* — against the
08-17 row that stated 1,667 twice in front of the true 300. `T-Q12`'s digest
carries `status: refused` with the reason, proven on **two** refusal types
(iteration budget and wall clock), so the fix is not keyed to the one the ticket
was written from. `T-D24`'s dashboard stores an absolute Q4 window, opens on it
with three rows a panel, and the model said so in the reply instead of telling
the user to fix the filter by hand — while a `qtd` dashboard created on 08-17
still resolves from today and still draws nothing, which is the clearest picture
of the gap the ticket closed.

**And the sitting found three things anyway. That is the lesson it adds:
the tickets passing is not the sitting passing.**

**A P0 that is `T-Q12`'s failure without `T-Q12`'s cause.** On a thread whose
history holds one clean, successful `create_dashboard` — no refusal anywhere in
it — the sentence *"Rename that dashboard to 'Q4 2024 Sales Review'."* answered
*"Done — your dashboard is now called…"* with `tool_calls=0`, no `agent_actions`
row, and the stored title unchanged. Run again on the same thread it called the
tool and landed the rename; a third attempt declined honestly. **One turn in
three claimed work it had not done**, non-deterministically, and nothing in the
product noticed — `CheckGrounding` asks whether every *figure* came from a tool
and the claim here is an *action*. `native-dashboards.md` §4.2 named this hole on
08-18 and left it unticketed; it is `T-Q13` now, P0.

**A figure the instrument cannot see.** A turn printed December revenue as
`$3,860,405,700.00` where its own `run_sql` returned `3,863,405,700.00`.
`ungroundedTolerance` is one percent, the misquote is 0.078%, so it read as
grounded — while the same turn's *derived* quarter total was flagged, so the
detector was awake and looking. One percent of a billion is ten million. The fix
is not to lower the tolerance, which exists because the prompt *requires*
"Rp 3,86 Miliar" as the rendering of 3,863,405,700: it is to match a
full-precision figure exactly and keep the tolerance for a rendered one
(`T-Q14`).

**And an environment fact that had already invalidated a turn.** A `cmd/worker`
from a previous session was still consuming the same asynq queue, from a binary
built before the tickets under test, and it served one of the gate's turns.
Nothing in the product records which binary answered a turn — it was caught only
because `turn completed` is a line this build added, and one turn had none.
`pgrep -f 'bin/worker'` missed it because the stale process ran from a `go run`
temp path. This is the same species as the Docker client skew recorded twice
already: an environment fact that reads exactly like a passing run.

**What the browser added, at $0.00 of extra model spend.** Phase 2y's decision
card was looked at in both themes for the first time: the recommended row carries
its reason as rendered text rather than a `title` attribute, so it is reachable
without a mouse; the card sits under the newest answer only; a tap sends the turn
and writes the pick row *before* the send unmounts the component; and the
*"Nothing right now"* row hides the card, leaves the composer empty and untouched,
and writes **no** `suggestion_picks` row — the decline staying out of
`pick_rate`'s denominator, which is the one property that made the fourth row
worth building. `T-D23`'s header action prefills
`Change [Q4 2024 Sales Review](/dashboards/…)` with the caret at 81 of 81 in the
textarea and starts no turn.

**The rule-1 re-score, and the finding it turned into.** `T-Q11` changes what
reaches the user on every turn, so the 56-case set was owed on both models:
**kimi 94.6% (53/56) / $0.5427, deepseek 78.6% (44/56) / $0.1928.** kimi is two
cases down on 08-14's 55/56, inside the ±2 band, with `zero_row_trap` at 3/3 and
`guardrail` at 8/8 — the categories the narrowing lives in did not move. deepseek
is **six** cases down, outside the band, and **six of its twelve failures are one
shape**: `replied in "id", expected "en"`, an English question answered in
Indonesian, which is CRITICAL GUIDELINE #1 failing and says nothing about the
record narrowing.

**That was measured instead of argued.** A worktree at `bdd7875` — the commit
before these three tickets — ran the same six cases on the same model the same
afternoon and **four failed identically**. The language regression is older than
this build; the live candidate is provider-side drift in an unpinned
`deepseek/deepseek-v3.2`. **The finding is about the instrument**: a set scored
against an unpinned model cannot separate "the tree got worse" from "the model
changed underneath us", and the only reason it could be separated here is that a
commit from ninety minutes earlier was still there to run against. Nothing in
this repo records the provider's model revision beside a published score, and
every deepseek number this project has published shares that gap.

**The one item that did not run, and the reason is worth writing where somebody
will be standing.** `T-D23`'s redraw-without-reload could not be watched: the
browser was authenticated by writing the access token into `localStorage`, which
gives working REST and **no event stream**, because the stream needs a refresh
cookie this tenant never issued to that browser. Every reply in the session
appeared only after a reload. A gate that watches a live turn needs a real login,
and no amount of model spend substitutes for it.

## Phase 3a — A PDF this product will hold before it will read (2026-08-18)

The letters ran out at 2z; this is the same day's second sitting and the first
of a new roadmap, [`../plan/06-pdf-knowledge-roadmap.md`](../plan/06-pdf-knowledge-roadmap.md)
— thirteen tickets, `T-P1`→`T-P13`, for turning an uploaded PDF into something
the agent can query.

**The research decided the architecture rather than decorating it.** The obvious
build is parse-to-markdown, chunk, embed, inject the top chunks into the prompt.
That walks past every instrument the last three sittings paid for: a figure
arriving as prose is not in `CheckGrounding`'s `returned`
(`internal/guardrails/grounding.go:73`) and `CheckFabrication` is satisfied by
any tool having run at all, so PDF-as-context is the `T-Q11` mechanism with an
upload button in front of it. So extracted **tables become rows in a database
this product owns**, registered as a `db_connections` row, and every figure
reaches the model through `run_sql`; prose is second and arrives through a tool
call, where the instruments can see it. Six locked decisions and the sourced,
dated numbers behind them are in the roadmap.

**`T-P1` built and gated the same day.** Migration `059`, the entity and its
repository, the ingest service, four routes at `/api/knowledge/documents`, and
the `document:parse` task nothing consumes yet. **$0.00** — the whole gate is
the stack, which is where this file's record says the defects have been.

Ten arms, on a fresh tenant (`Gate P1 0818 #1`, and a second company that exists
only to be refused):

- Migration `059` applied by the API's own migrator, **reversed with the CLI,
  and re-applied** — table, both indexes, the status CHECK and both foreign keys
  identical after the round trip.
- A real 14,612-byte PDF uploaded → **202**, `status=uploaded`, `page_count=0`,
  one row, one object at `source-documents/<company>/<sha>.pdf`, one asynq task.
- The same bytes under a different filename → **200**, `deduplicated=true`, the
  first document returned, **still one row and still one task**. The dedupe is
  the property that keeps a monthly report sent twice from being parsed twice.
- A zip renamed `.pdf` → **400** on content, not on the extension.
- Cross-tenant `GET` and `DELETE` → **404** both, and nothing removed.
- `DELETE` → **204**, the row gone, the object gone, the prefix gone with it.
- With no object storage the upload and list routes answer **503** with a
  sentence, and `GET /api/agents` beside them still answers 200.
- With `DOCPARSE_ENABLED` at its default the upload works, `queued=false`, and
  the document rests at `uploaded` — the honest state for a file nothing has
  read, and the state every deployment is in until `T-P2`.

**One defect, found by the gate and fixed in the sitting.** An upload over the
cap answered **400 "expected a PDF in the file field"**. `MaxBytesReader` cuts
the body mid-part, so `mime/multipart` reports a parse failure and the handler
never reaches the size check that would have said 413 — the client is told its
request was malformed when the request was fine and the file was too big. What
makes it worth recording is the shape of the fix: the typed `*http.MaxBytesError`
is **not** what arrives, because multipart flattens it into a plain
`errors.New`, so a fix that checked only the typed error would have passed a
unit test and failed on every real oversized upload. Both arms are now pinned by
a table test, and the re-run answers **413** with nothing written to storage or
to the database.

**State left behind.** Two companies (`Gate P1 0818 #1` and `#2`) with one admin
each and no connections; no documents — the last one was removed by the down
migration and its orphaned object cleaned by hand, which is exactly what
`059_source_documents.down.sql` says a deployment must do. The compose stack was
already up and was left up; the API binary was stopped. **No stale process
served any arm** — §1g's lesson, checked before the sitting rather than
discovered during it.

## Phase 3b — A parser that knows what it cannot read (2026-08-18)

`T-P2` built and gated in the same sitting as `T-P1`, $0.00 again. It adds the
second non-Go service in this repository — `apps/docparse`, FastAPI and
pdfplumber — and the Go half that treats it as what it is: another container
that fails in three ways which mean different things.

**Three deviations from the ticket, all deliberate and all cheaper than what it
asked for.** The parser is **pdfplumber, not Docling**: this ticket is the text
layer and the ruling lines, which needs no model, no GPU and a three-package
image; the ML rung waits for a measured failure in `T-P4` and arrives inside the
sidecar without the Go side noticing. The page cap is enforced **inside** the
sidecar rather than before calling it, because a page count does not exist until
the file is opened — the refusal still lands before any page is read. And
artifacts sit under `source-documents/<company>/<sha>/`, matching `T-P1`'s
prefix rather than the ticket's `documents/`.

**The fourth change is the one that matters, and the first fixture forced it.**
A ruled table is the easy case. ERP exports, bank statements and anything laid
out with tabs draw no lines at all — and a column-aligned Indonesian sales
report produced **zero** table candidates until a text-strategy fallback was
added. A parser that finds only ruled tables reports "no tables" on a page that
is nothing but a table. The fallback is guarded by a shape check (most rows
filled to the same width), because the text strategy will otherwise call two
consecutive prose lines a two-by-two grid.

**Ten arms, all passing.** The sidecar's health check names its build — the
answer to the stale-process finding §1g recorded, since a sidecar serving from a
previous image looks exactly like a passing run. The shared secret is enforced.
The fixture table came back 7×4 with every data row correct. A scan classified
`needs_ocr` at `image_area_ratio` 1.0 with **empty markdown**, which is the
point: a page whose text layer failed the test returns nothing rather than
half-decoded glyphs that look like content. End to end, an upload reached
`parsed` in **125 ms** with one page artifact and one manifest in MinIO carrying
`pdfplumber 0.11.4`; the scan reached `parsed` carrying *"1 of 1 pages hold no
readable text and were not read"* on the row itself, not only in a log; a
five-page document against a three-page cap reached `failed` with the parser's
own sentence and **zero retries**, which is the whole point of separating a
refusal from an outage.

**And the outage arm proved the other half.** With the sidecar stopped, an
upload rested at `uploaded` saying the parser could not be reached — not
`parsing`, which would describe a process that is not running, and not `failed`,
which would describe a file that is fine. The task sat in the retry set, and
when the sidecar came back the retry **parsed the document and cleared the
warning** with nothing else touched.

**One finding, and it belongs to the next ticket.** The text strategy swallowed
the report's title into the grid and split it across two cells — `LAPORAN
PENJUA` / `LAN Q4 2024`. The data rows were untouched, so it is not a wrong
number; it is a junk row, and `T-P4`'s header detection is where it dies. Its
acceptance list now names this case.

**Not run:** the broken-font-map page. Synthesising a PDF whose ToUnicode table
is missing was more work than the arm was worth, so that branch of the
classifier is covered by unit tests and by no file — written down here rather
than counted as passing.

## Phase 3c — What a turn says it did, and what a figure says it is (2026-08-18)

`T-Q13` and `T-Q14`, the two findings the 08-18 sitting left beside its own
tickets, built in one sitting. They are the same family as `T-Q11` and `T-Q12`
and they close the last two doors in it: something reached the user of record
that no evidence supports.

**`T-Q14` is small and its live half is already run, for $0.00.** The tolerance
was doing two jobs. `Rp 3,86 Miliar` is the *correct* rendering of 3,863,405,700
under this product's own system prompt, so the one-percent band has to exist —
and one percent of a billion is ten million, which is why
`$3,860,405,700.00` read as clean. The two jobs are now separate: a figure
written in magnitude units keeps the band, and a figure written out in full is
matched at 1e-9. Re-checking the persisted 08-18 reply against its own `run_sql`
re-executed on the demo warehouse, this build reports
`ungrounded=[3,860,405,700, 10,946,676,500]` where that day reported only the
second. The misquote is **0.0777%** off. October and November match to the cent
and are not flagged, which is the arm that matters: the tightening did not
simply flag everything.

**`T-Q13` is the P0, and it is an instrument rather than a gate.** The reply it
was written from — *"Done — your dashboard is now called…"* on a turn with zero
tool calls, no `agent_actions` row and the title unchanged — contains **no
figure at all**, so `CheckGrounding` had nothing to check and `CheckFabrication`
was satisfied by evidence existing. The claim is an *action*, and nothing here
checked those.

Three pieces, and the middle one is the part worth keeping:

- **`guardrails.ClaimsCompletedAction`** reads completion language, not past
  tense: "Done —", "I've updated", "has been renamed", "is now called", and the
  Indonesian *sudah/telah/berhasil* forms **in the first version rather than
  after a gate finds the instrument English-only**, which is `T-Q3`'s lesson.
  A sentence carrying a prior-turn marker (*earlier*, *previously*,
  *sebelumnya*) is not this turn's claim.
- **The mutating set is a property of the tools** (`internal/tools/mutating.go`),
  not a list beside the check. A constant somewhere else would be right the day
  it was written and wrong the first time a tool was added — the `T-14` lesson.
  `IsMutating` walks *through* the budget guard and the audit recorder, because
  both embed `interfaces.Tool` and hide methods it does not declare; a check
  that asked the wrapper would find nothing mutating and would therefore never
  count, which is the worst failure available to an instrument: silent, and
  shaped like good news.
- **The tracker now separates attempted from succeeded.** `Snapshot.Tools` is
  what the model called; `Snapshot.Succeeded` is what worked. A refused call
  never reaches the second, which is how *"I've updated the dashboard"* on a
  budget-refused turn is counted — `T-Q12`'s sequence seen from the other end.

It counts and does not rewrite. The failure is one attempt in three, so a gate
would be replacing correct replies at an unmeasured rate, and this repo has
narrowed a guardrail regex after it blocked something legitimate six times in
twenty pre-sprint commits. `unevidenced_actions_total` lands on the process
counters, on the turn span, and on the `turn completed` line beside
`ungrounded` — the `T-Q11` shape, and for `T-Q11`'s reason: a rate nobody can
filter for is a rate nobody reads.

**One promotion fell out of it.** "What does a failed tool call look like" now
has one answer — `agentbudget.ToolErrorText` — where it had two: the digest's
prefix table (T-Q12) and the success tracking this ticket needed. Same move, and
the same argument, as `metric.ValidateTemplate` becoming `sqlguard`.

**What is owed:** `T-Q13`'s live half, §1i, **~$0.10** and about six turns —
repeat the create-then-rename sequence until one turn claims an unperformed
edit, and show `unevidenced=1` on it and `0` on the control.

## Phase 3d — The rest of the PDF track, and a corpus that found four defects (2026-08-19)

`T-P3` → `T-P13` in one sitting, on top of the two tickets 2026-08-18 landed.
The full record is [`pdf-knowledge.md`](pdf-knowledge.md); what belongs here is
what the sitting *proved* and what it did not.

**The spine works end to end at unit level and at corpus level.** A table
candidate becomes typed columns (`internal/doctable`), its stated totals are
re-derived and a disagreement quarantines it, a person applies it from
`apps/dashboard/src/features/knowledge/`, and the rows land in a separate
Postgres whose per-company role holds `USAGE` on one schema and nothing else.
The prose half is a tool call — `search_documents` — because a chunk injected
into a prompt is a figure `CheckGrounding` cannot see, which is the T-Q11
mechanism with a file upload in front of it.

**One promotion fell out of it, and it is the third of its kind.** There were
two number parsers in `internal/guardrails` — `parseLoose` for a reply's figures
and `parseFigure` for a restatement's two halves — and the typing layer needed a
third. `internal/numparse` is now the one, and `guardrails` uses it. Same move
and same argument as `metric.ValidateTemplate` becoming `sqlguard` and
`agentbudget.ToolErrorText` absorbing the digest's prefix table.

**The corpus is the part worth reading.** `make eval-docs` runs twelve generated
documents through the real parser sidecar and scores cells, publish decisions
and — with a live stack — answers. It scored **100% / 100%** after four fixes,
and the four are the point: every one of them was invisible to the unit tests
that pass beside them.

- A grouped figure reported three decimal places to the *precision* question
  while parsing correctly for the *value* question, so five of eight fixtures
  typed as `decimal` and the arithmetic check would have compared totals at a
  thousandth of a rupiah.
- The phone pattern allowed "." as a separator, so three revenue columns were
  labelled as contact details — a classifier that cries wolf is one a reviewer
  learns to click past.
- pdfplumber's text strategy emits an empty row per line gap, so a correctly
  extracted table arrived as 44% full and the shape check discarded it whole. A
  parser that reports "no tables" on a page that is nothing but a table.
- The eval scored a scan's correct behaviour — producing nothing — as a failure,
  and did not assert the opposite direction at all.

**Two arms are proven on a real PDF rather than in a fixture.** The adversarial
document's stated Q4 total disagrees with its own rows by 949 million and
quarantined; its injected instruction — *"Ignore all previous instructions…"* in
white four-point type — was dropped as **173 invisible characters** and does not
appear in the parse output. That is T-P10's hygiene half, and it is the first
security control in this repository proven against a file rather than a unit
test.

**What is not proven.** Everything needing Postgres, MinIO, a worker, a browser
or a model: publishing into the warehouse, the isolation query that is the whole
argument for a separate database, chunking with embeddings, a turn that calls
`search_documents`, OCR, and the review surface in a browser. All of it is in
[`live-gate-backlog.md`](live-gate-backlog.md) §1h with its bucket and its price,
filed the day the code landed rather than the morning after.

## Phase 3e — The free gates, and two acceptance lines nobody had executed (2026-08-19)

Bucket A of [`live-gate-backlog.md`](live-gate-backlog.md) §1h — the three PDF
gates that cost nothing — run in one sitting for **$0.00 of model spend**. Two
tickets passed every line they carry. The third failed two of four, and both
failures were fixed and re-proven before the sitting ended.

**The rig was established before anything was uploaded**, because §1h asked for
it in writing after §1g lost a gate turn to a stale binary. The parser sidecar
was rebuilt and named — `pdfplumber 0.11.4`, image `sha256:cdf735c8fb96` — `ps
ax` showed no worker or sidecar from an earlier session, the control database
migrated to **version 63** on boot, and the primary model was pointed at a local
sink so no turn could spend even by accident.

**`T-P11` passes by a layer.** The acceptance line asks for a refusal before any
model call; what the sidecar's access log shows is a refusal before the
*render* — `POST /parse`, and no `POST /render` — so a scan over budget is never
rasterised, let alone sent. The arm that makes it mean something ran beside it:
with the budget unset the same document renders both pages, calls the model
twice and writes two ledger rows carrying the document id.

**`T-P7` passes on every line**, including the member arm — Apply and both
override selects disabled under a sentence, with `403` behind all four write
routes — and both themes.

**`T-P12` is the one worth reading.** Its classifier works and is badged in
review. What did not exist is the enforcement its own prose describes:

- A `strict` tenant's published customer list came back over MCP with **three
  real email addresses on it**. The ticket said to reuse the code path T-H10
  established; T-H10's path is the *zero-row probe*, which only ever runs on a
  result with no rows in it, so a result with rows was never inspected.
- A delete left **`<sha>/pages/1.json` — the document's own text** — in the
  bucket. `Delete` removed one key, the `.pdf`; `T-P2` had added artifacts under
  their own prefix the same day and nothing revisited the delete.

Both are one-file fixes, both are pinned by tests **proven failing first**, and
both were re-run against a rebuilt stack: the query now answers `[CONTACT
REDACTED]` with the withheld column named — while the same query under
`contact_ok` still returns the addresses — and a fresh document's three objects
go while 22 belonging to other documents stay. `go test -race ./...` green on 58
packages, `golangci-lint` 0 issues.

**Two scoping decisions were taken narrowly and written down.** The redaction is
scoped to document sources, because widening it to every source moves what
reaches the model on ordinary warehouse turns and owes a rule-1 re-score rather
than a same-sitting patch. And the marker names the class rather than blanking
the cell, with the payload saying the values exist — an emptied column is the
zero-row hazard again, and *"the customer emails are not recorded"* would be a
false statement about the tenant's own document made by an instrument working
correctly.

**What the sitting adds to the argument for gates.** Eleven of twelve previous
sittings found something in code whose unit tests passed. This one found two
defects in a ticket whose unit tests pass *and whose visible half is genuinely
built* — the gap was between the prose and the assertions, which only executing
the acceptance line can expose. The delete test that now pins it could not have
caught the bug before, because the fake store only ever held the object the
uploader wrote.

## Phase 3f — Bucket B, and the feature that had never once worked (2026-08-19)

The PDF track's paid gates — `T-P3`, `T-P6`, `T-P8`, `T-P9`, `T-P10` and
`T-P13`'s answer score — run in one sitting for **$0.4287**, against the ~$0.30
[`live-gate-backlog.md`](live-gate-backlog.md) §1h priced while the tickets were
still prose. The overrun is two extra runs of the eight-case set, and each was
bought by a defect the previous run exposed. The full record is §1k; what belongs
here is the shape.

**Four P0s, and the first one had made the whole track inert.** `get_schema`
reported **zero tables** on a document source that `run_sql` was querying
successfully in the same turn. `internal/adapters/db/postgres` pinned all three
introspection queries to `table_schema = 'public'`, and `T-P6`'s isolation design
— a schema per company, a role whose `search_path` is that schema and which holds
nothing on public — is precisely what that misses. So the agent was told every
applied document held nothing, and answered the December sales question out of
the tenant's warehouse instead, in a reply that said so in its own words. Every
unit test in the track passed throughout; nothing between `document_tables` and
`run_sql` was wrong. The claim in the roadmap's own headline — *a PDF becomes a
source the agent can query* — was false from the day it shipped, and only a turn
could show it.

**The other three are all the same species: an instrument that cannot see the
shape of the evidence in front of it.**

- A `strict` tenant's own sales figures came back `[CONTACT REDACTED]`. §1j's
  redaction reuses T-H10's value classifier, whose phone pattern is
  `^\+?\d{8,15}$`, and `T-P4` types a rupiah column by stripping the separators
  that make `3.377.718.500` legible — so ten bare digits reach a pattern that
  cannot tell them from a phone number. `doctable.ClassifyPII` had already learned
  this at publish time and says so in a comment; the query path added one commit
  later had not. Fixing the typed case was not enough, and the second half is the
  better lesson: `SUM()` over a `bigint` returns a Postgres `numeric`, which the
  driver layer stringifies **on purpose** (coercing it is `native-dashboards.md`
  defect 3), so every total an analyst asks for landed back on the pattern.
- A correct prose answer was replaced as a fabrication. `search_documents` has
  been in `agentbudget`'s `dataTools` since `T-P9` with a comment arguing a figure
  in a passage is evidence — and nothing ever counted one, because `rowCount`
  reads `row_count` and the tool answers with `passages`. The turn showed
  `data_calls=4, data_rows=0`, so `CheckFabrication` swapped a chunk-grounded
  summary for *"I wasn't able to complete the query"* **while `CheckGrounding`, on
  the same text, reported every figure evidenced**. Two instruments disagreeing
  about one reply, and the blunter one wins because it rewrites and the other only
  counts. Third time this guard has eaten a correct answer whose evidence was of a
  shape it could not see; the first two are in its own doc comment.
- Publishing never invalidated the schema cache, so a reviewer's first upload
  would have stayed invisible for a full hour even with the introspection fixed.
  Underneath it: the API's `GetSchemaTool` was built **without Redis**, so the
  rotate-DSN invalidation this repo has assumed since `T-14` was also dead across
  processes — the worker reads a key the API never deleted. The invalidator moved
  into `WithWarehouse`'s parameter list rather than an optional setter, because
  the sitting had already found what an optional setter costs (below).

**The number that matters, and the number that lied.** `T-P13` now reports all
three scores: **100% cell accuracy, 100% publish correctness, 87.5% answer
correctness (7/8), $0.1304, `pdfplumber 0.11.4` on the report.** The first run of
the same eight cases scored **50%** — and every one of its four passes was
hollow. `doc-december-sales` passed because the figure came from the warehouse;
three more passed because nothing was retrieved and "no/tidak" was in the
expected strings. A pass rate computed over a broken source is not a measurement,
and this is the clearest instance this repository has recorded of a green number
describing nothing.

**Two findings filed rather than fixed, both about code that cannot run.**
`WithSynopsis` has no caller anywhere — `T-P8`'s contextual retrieval, the half
carrying the published 35%/49% argument and a long comment defending its
per-document trade, has never executed on any deployment. And
`docchunk.headingLine` matches only markdown headings while the sidecar's
`to_markdown` never emits a `#`, so heading-boundary chunking can never fire,
every `heading_path` is empty, and chunking is purely token-budget-driven — the
behaviour the package's opening comment says it is not. `internal/docchunk` has
**no test file at all**, which is how a regex nothing can match survived.

**And one environment fact that reads as a passing feature.**
`EMBEDDING_API_KEY` is empty here, and the fallback correctly refuses to borrow
the primary key across hosts — so `EmbedCache.For` returns `(nil, nil)`, no
error, and the dense half of retrieval, the cookbook and the table picker are all
inert. The line an operator reads says the opposite: the worker logs
*"table-picker embeddings enabled"* off a boolean flag, never asking whether a
client resolves. This is exactly what the last failing eval case costs — asked in
Indonesian rather than English, the same question on the same tree passes, quoting
*"angka sementara"* and citing *"halaman 1, baris 3"*.

**What the rig found before any gate ran.** Three long-lived processes from the
morning's §1j sitting were still up — and beside them the **local model sink**
that had made that sitting free. A turn run against that rig would have cost
nothing and proved nothing. §1g's rule caught it; §1g's *command* would not,
because it greps for `exe/worker|bin/worker` and these were named `gate-*`. The
check is "what long-lived processes exist here", not "is the binary I expect
running".

## Phase 3g — Three pieces of code that could not run, and the one they hid (2026-08-19)

The two findings §1k filed rather than fixed, plus the log line that said the
opposite of the truth. **$0.00, no model call, one sitting** — the cheapest
bucket in this repository paying for the thirteenth time, this time without even
a stack: the whole sitting is `go test`.

**What was fixed.** `WithSynopsis` is wired from `bootstrap.Stack` under
`DOC_CHUNK_SYNOPSIS` — a setting that had defaulted to `true` since `T-P8`
landed and reached no code at all — and gated on a resolved embedding client,
because the prefix it generates is embedded and read nowhere else.
`Options.DetectHeadings` gives `internal/docchunk` a heading detector for a
parse with no markup, **off by default**, so the package's opening claim is
either true or switchable rather than false. And `embedding.LogEnvCoverage`
prints at boot in both processes what the credential actually buys.

**The defect underneath them cost money.** `docchunk.Build` accepted only
`kind == "text"`, and `T-P3` sets `kind = "ocr"` on exactly the pages a
multimodal model has just been paid to read. A scanned document was therefore
rasterised, sent to a model, metered — and produced no retrievable prose at all.
It was invisible from every direction the gates looked: `T-P3` asserted the
pages were read and billed, `T-P8` chunked a born-digital document, and the
twelve-document eval corpus is born-digital. Nine lines of test catch it, in a
package that had no test file, which is also the answer to why a regex nothing
could match survived a review.

**Three things the tests said that no reader had.** `Options{}` gives no overlap
at all — 60 is the fallback for an unusable value, not the default for an unset
one — so the comment claiming the zero value was the shipped behaviour was
wrong. The top of a page counts as "set apart", which is right for a title and
is the detector's one known false-positive shape. And the honest warning about a
missing embedding credential **already existed**: it lived in `embedding.Build`,
which the per-tenant `EmbeddingCache` replaced without inheriting it, leaving a
function with no callers anywhere in the repository and the one useful sentence
inside it. A warning deleted by a refactor and a warning that never fires are
the same thing at runtime.

**What it left owed, and it is free:** re-ingest a document and read
`heading_path` and `context_prefix` off the rows; find a scanned document's text
through `search_documents` end to end; boot each process and read the new
`embedding:` line. Filed in [`live-gate-backlog.md`](live-gate-backlog.md) §1k.

`go test -race ./...` green on 58 packages, `golangci-lint` 0 issues, 21 new
tests, two of them proven failing against the old code first.

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

**Patterns worth changing** — written 2026-07-26 against the pre-Sprint-1 tree.
**Re-measured 2026-08-17, and three of the four have closed:**

- ~~**Zero tests accompany any feature commit.**~~ **Closed.** 52 of 76 Go
  packages have tests, and the native dashboards build shipped seven new test
  files with the code they test.
- ~~**Prompt and model changes ship blind.**~~ **Closed by `T-Q1`→`T-Q9` and the
  eval set**, with the caveat that the instrument now has a known ±2-case noise
  band and one owed re-score (Phase 2s, §4).
- ~~**No down migrations after 014.**~~ **Closed.** All 56 migrations have a
  `.down.sql`; `056` was proven down-then-up against a populated table.
- **`internal/app` grows unchecked** — **still true, and much larger**: ~29,600
  lines across 96 files, up from ~2,900 across 18. What changed is that it is no
  longer *untested* — 49 of those files are tests. The highest-churn package in
  the repo is now the one with the most test files, which is the right
  direction and not the same thing as being safe to edit.

## Phase 3h — The caller a security package said it had (2026-08-19)

`T-H4` step 3. **$0.00, no model call, no stack** — the same bucket as Phase 3g
and the same shape of finding, one layer further in.

**What was true.** `run_sql.Execute` handed the model's SQL straight to
`conn.ExecuteReadOnly`. `internal/sqlguard` — the package whose own comment
opens *"the one structural check this product runs over SQL it did not write
itself"* — names three callers, and the third is `run_sql`. It was not one. The
metric registry calls it; the dashboard spec calls it twice; a grep across
`internal/` returns those two and stops.

**Why nobody noticed for eight days.** Three separate things read as coverage.
`config/guardrails.yaml` carries `block_sql_mutations` and
`block_sql_injection`, and both are `scope: input` — they screen the user's
message and have never once seen what the model wrote. The feature matrix's
*SQL mutation blocking* row has said ✅ since the file was written. And the
read-only transaction genuinely does hold the line — on Postgres and MySQL.

**It does not exist on SQL Server.** go-mssqldb rejects `TxOptions.ReadOnly`, so
`adapters/db/sqlserver/conn.go:36` opens a plain transaction and documents why.
On that driver, between a model-authored `INSERT` and a tenant's data, there was
the customer's `db_datareader` grant and nothing else. That is the sentence this
phase exists to delete.

**The refusal now names what it found.** `ValidateStatement`'s prefix check said
*"it starts with something else"* — true of every statement it will ever refuse,
and therefore useful in none of them. It names the leading keyword now, which
reaches the metric registry and the dashboard spec too.

**16 cases, and the eight that matter are the allowed ones.** A validator that
refuses ordinary analytical SQL costs more than it saves, so half the set is
`create_date, update_count, call_id FROM merge_log`, `status = 'deleted'`, a
comment reading *"we do not delete rows here"*, and Indonesian column names.
They pass because the lexer scrubs literals and comments before reading
structure, and because `_` is a word character. The `run_sql` half was proven
failing first, the honest way: `undefined: guardStatement`.

**What is not closed.** Step 2 — a real parse, `pg_query_go` and `vitess` — is
open, and it is cgo, so it touches `Dockerfile.api` and the release build. That
is the repo owner's call. And this changes what reaches a tenant's database on
every warehouse turn, so it owes a rule-1 re-score before anyone should call it
measured (§1l).

**Two things the full `-race` run found that have nothing to do with this
ticket.** `internal/report/videoplan`'s golden plan fails in the working tree
and passes at `04906fd`, and the whole of the difference is one line — the
chart image's `sha256`, because an uncommitted change takes `supersample` from
3 to 2 and the pixels move. The regenerated golden was **not** kept: it belongs
to that change's own ticket, which does not exist yet, and a golden hash
updated by a passing stranger is how a rendering regression gets committed as
housekeeping. And `TestSSETurnStreamsDeltasAndEndsWithFinal` failed once inside
`go test -race ./...` with `deltas = []`, then passed 8/8 alone and 3/3 as a
package: it publishes to a fake event bus and never constructs a tool, so it is
load-sensitive rather than related, and it is written down here because a flake
nobody records is a flake somebody re-diagnoses.

## Phase 3i — A score that names what answered it, and a tag that grew teeth (2026-08-19)

One sitting, four pieces of work, **$1.11 of model spend** — of which $1.01 is a
single rule-1 re-score that three separate tickets owed and one run discharged.

**`T-Q15`: every published number named a model and no revision.** The eval
harness now records what the gateway says it served, and the first run answered
the ticket's question in one line: **`served: moonshotai/kimi-k2.6 via Baidu`**,
against **`deepseek/deepseek-v3.2 via AtlasCloud`**. Neither upstream appears in
any number this repository has published, and the upstream is exactly what moved
under the 2026-08-18 deepseek drop. The tap that reads it is the one already
parsing every SSE frame for token usage; what is new is four lines of JSON and an
optional sink the caller installs, so nothing in production pays for a question
only a scoring run asks.

**The acceptance line it could not meet as written, and why that is the finding.**
`T-Q15` says to pin a revision in `golden.yaml` *"where the provider supports a
dated alias"*. OpenRouter's catalogue was checked rather than assumed: it does
carry dated aliases — `moonshotai/kimi-k2-0905` is one — and it has none for
either model this set is scored on. So the alias cannot carry the revision and
the report does instead. The set declares its models, with that check written
beside them.

**And the feature found a defect in itself before any ticket did.** Writing the
three-case gate into the report directory the 56-case re-score was about to use
would have printed *"+50 case(s) — outside the ±2 band"*: the comparison matched
on set and model and not on case count. Fixed with the guard and two tests, the
morning it was written, because a harness that prints arithmetic between two
different questions is worse than one that prints nothing.

**`T-H4` step 3's live half — 17 arms, two drivers, $0.00, and everything held.**
The first sitting in this repository's live-gate record where the bucket found
nothing in the ticket under test, and the reason is worth naming rather than
celebrating: a refusal is the one shape whose false-positive half can be
enumerated in advance, and its author enumerated it. What the gate added was the
half no unit case speaks — **MySQL**, where a model writes backticks — and two
proofs the acceptance line did not ask for: every refusal writes an
`agent_actions` row reading `error`, and the refused statement never reaches the
database, shown in the warehouse's own `log_statement=all` log rather than read
off the code. Full record: [`security-hardening.md`](security-hardening.md) §17a.

**The three free arms `§1k` left owed, closed for about a tenth of a cent.**
Heading detection ran on a real document for the first time on any deployment —
`heading_path = "PELANGGAN UTAMA 2024"`, where every such column in this database
had been empty — while `context_prefix` stayed empty with no embedder resolved,
which is the synopsis guard declining to buy a light-model call per document to
fill a column nobody selects. The OCR chunking fix was proven end to end: a
scanned invoice went in, came back with one chunk where it used to produce none,
and a real turn answered *"INV-2024-0001 … Rp 1.850.000"* citing *"halaman 1"*.
And all three long-lived processes now print the `embedding:` coverage line at
boot, with the picker reading `credential=tenant-row-only` instead of the old
`enabled`.

**`T-H9`: the taint tag stopped being telemetry.** `062`'s own migration comment
promised this — *"until T-H9 lands, count first, gate once the rate is known"* —
and the ticket turned out to describe a shape this tree does not have. It asks
for a decorator over three tools; `http_action` and `send_message` are action
*kinds*, not tools, and only `propose_action` is one. So the gate is a single
branch at the point that decides whether a proposal executes, which covers every
kind including ones written next year.

**Its gate is the strongest form this repository has**: not a status field but an
access log. A control turn proposed an action on an auto-approve kind and the
local receiver logged the POST; the tainted turn proposed the same action and the
receiver counted **zero**, with the invocation reading `proposed` and the reason
naming the document; approving it by hand then ran it, receiver +1. The agent
relayed the reason unprompted, in Indonesian, to a user who would otherwise have
read the held card as a malfunction.

**Two findings beside the tickets, both about how a person actually asks for a
document.** `search_documents` cannot find a document *by its filename* — the
index holds content only, so *"look up 09-scan-invoice.pdf"* answers *"I couldn't
find any uploaded document named 09-scan-invoice.pdf"* thirty seconds after the
upload. And `plainto_tsquery` is conjunctive, so one English word in an otherwise
good Indonesian query returns nothing, with the dense half inert for want of a
credential. Both P2, both in §17c.

**The re-score: both models, both inside the band, and the instrument built this
morning earned itself back on its first real run.**

| | this run | 2026-08-18 | delta |
| --- | --- | --- | --- |
| `moonshotai/kimi-k2.6` | **96.4%** (54/56), $0.8158 | 94.6% (53/56) | +1 case |
| `deepseek/deepseek-v3.2` | **76.8%** (43/56), $0.1991 | 78.6% (44/56) | −1 case |

Both moves are inside the ±2 band this set carries, so neither is a result —
which is the answer `T-H4` step 3 needed: **a validator that refuses ordinary
analytical SQL would show up as a score drop with no other tell, and there is no
drop.** The direct measurement beside it is better still: **zero refusal warnings
across 112 model-driven case runs.** Several hundred model-authored statements
went through `run_sql` and the guard did not fire once.

**And `T-Q15` caught what it was built for, on its first re-score.** deepseek's
report reads:

```
served:     ! more than one identity answered this run
              deepseek/deepseek-v3.2 via AtlasCloud (229 responses)
              deepseek/deepseek-v3.2 via Alibaba (1 responses)
```

**The gateway re-routed mid-set.** One response of 230 came from a different
upstream under the same model id — invisible to every previous number this
project has published, and the exact mechanism the 2026-08-18 sitting spent a
worktree and six extra cases proving by elimination. It cannot by itself explain
a one-case delta, and that is not the point: before today nobody could ask. The
supporting evidence is in the failure profile — **10 of deepseek's 13 failures
are `replied in "id", expected "en"`**, the same shape and the same magnitude
08-18 attributed to drift rather than to the tree.

**What the sitting spent, and what it bought.** $0.0500 on `T-Q15`'s gate,
$0.0004 of OCR, about $0.04 across four `T-H9` turns, and **$1.01 on the re-score
that discharged three owed rule-1 obligations at once** — `T-H4` step 3,
`T-P9`/`T-P10`. Running them separately, as each ticket assumed, would have cost
half as much again and answered the same question three times.

## Phase 3j — The filename, which is the one thing a person is sure of (2026-08-20)

`T-P14`, built and gated on everything free the same day. It is the smallest
ticket in the PDF track and it closes the two failures somebody met by *using*
the feature rather than by testing it: a document that cannot be found by its own
name, and a mixed-language query that returns nothing rather than less.

**What `061` indexed was what a document says.** Thirty seconds after uploading
`09-scan-invoice.pdf`, the person who uploaded it asked for it by that name and
was told no such document existed — every word of the reply true, the whole of it
misleading. Measured before the fix, against the real control database: the
filename query returned **0 rows**, the mixed-language query returned **0 rows**,
and a content term returned its chunk.

**The fix is a column, not a join, and that is the ticket's own header being
wrong.** `T-P14` said *"Migration: none"*. `document_chunks.tsv` is
`GENERATED ALWAYS AS`, and a generated expression may read only its own row — so
anything the lexical index must know about a *document* has to be copied onto the
*chunk*. `065` adds `source_name` and redefines the vector as
`setweight(prose,'A') || setweight(source_name,'B')`, which makes "a document
about invoices outranks one merely named invoice" the database's rule.

**And Postgres reads `09-scan-invoice.pdf` as one token.** `ts_debug` calls it a
`host`: indexing the raw filename would have satisfied the ticket's first
acceptance line and failed every other way of asking for the same file. The
stored terms are the name *and* its stem split on `-`, `_` and `.`; the extension
survives only inside the whole name, because `pdf` on every document is a term
that discriminates nothing.

**The conjunctive fallback rewrites Postgres's own output.** `plainto_tsquery`
emits `&` and nothing else, so `' & '` → `' | '` turns a strict query into a
loose one with one tokenizer still in the system — the terms, the stopwords and
the escaping stay whatever Postgres decided. It runs on the empty path only, and
the tool result opens with a sentence naming it, in front of the
untrusted-content instruction rather than instead of it. Same argument as
`run_sql`'s zero-row probe: a model not told its query was widened presents the
nearest passage as the matching one.

**The free gate: the migration both ways, and six arms through the repository's
own code.** 64 → 65, `down 1` against 14 populated rows and back up, `dirty = f`,
`061`'s expression restored exactly by the down. Then the retrieval arms, driven
through `SearchLexical` in a gate binary rather than through SQL a gate retyped:
the filename returns its chunk, the split stem returns it, a content term outranks
a filename term **0.608 to 0.243** on the same row, the fallback fires only on a
query the strict path cannot answer and ranks 3 matched terms above 1, and a query
nothing matches reports no loosening at all.

**The finding is about the ticket rather than the build.** The mixed-language
query `T-P14` was written from — *"Kopi Arabika 1kg faktur invoice"* — is fixed by
the **filename** half, not by the fallback the ticket wrote for it: `invoice` is
an English word that appears in the document's *name*, so every term is present
and the strict path answers. The fallback had to be measured on a query the ticket
does not contain. A gate that ran only the reproduction would have proven one
mechanism and shipped two.

**One honest correction to an acceptance line.** *"The conjunctive path is
byte-identical when it matches"* is true of the rows and their order and false of
the `ts_rank` value, which moved from weight D to weight A. It reaches nobody —
`fuse` overwrites `Score` with the reciprocal-rank result before a caller sees it
— and it is written down rather than claimed away, because a `SELECT` disproves
the stronger sentence in one line.

**The paid half ran the same day, $0.1062 against a ~$0.15 estimate.** The two
turns pass. Asked to look up `02-bank-statement.pdf` — a document chosen because
its name and its page share no word, the page being `REKENING KORAN - DESEMBER
2024` — the agent searched **`bank statement`**, the words inside the filename,
and answered *"a bank statement (Indonesian: Rekening Koran) … December 2024"*
with a page citation. That query returns **0 chunks** against the index this
build replaced. The second turn passed **without the fallback**: the model
shortened its own query to `rekening koran`, which matched conjunctively before
this build too — so the turns prove the filename half end to end, and the
loosening is proven by the eval set instead.

**Rule 1: cells 100%, publish 100%, answers 75% (6/8) against 87.5% on 08-19 —
and the per-case attribution is the result, not the rate.**
`doc-prose-citation`, the case the ticket named, went **FAIL → PASS** for the
predicted reason: 0 chunks retrieved strictly, **4 loosened**, and the reply now
quotes *"angka sementara"* with both documents cited by name and page.
`doc-absent-document` is the boundary this change could have broken — a fallback
that widens until something matches would describe a document nobody uploaded —
and it retrieved **zero passages even loosened**, then failed on its own
assertion's vocabulary (`no`/`not` against a reply that says `couldn't`,
`hasn't`, `doesn't`). `doc-budget-scale-word` flipped with **no tool call at
all**, which is `T-Q5`'s asking-policy family and unreachable from anything here.
Both safety cases retrieved *more* than before and still held: no
`propose_action`, no `http_action`, quarantine still reported as unanswerable.

**What the sitting adds to this file's own record: an 8-case set has no measured
noise band, and one case is 12.5% of it.** The 56-case warehouse set carries ±2
for exactly this reason. Two of the three moves here are a model re-phrasing and
an assertion's vocabulary, and on a set with a band neither would have been read
as a result. Measuring that band is now the first thing owed, ahead of the two
P2s the sitting filed.

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
