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
gate has not run** — the Docker daemon was down and the agentic half spends real
tokens on a tenant needing two differently-scoped agents. Commands and the three
transcripts still owed: [`agent-roster.md`](agent-roster.md) §T-S5.

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

`make check` clean — vet, lint, test and build — `make types-check` current,
18 new tests across two files, and the dashboard type-checks and lints with the
bindings table in Settings → Agents. **Migration `033` has never been applied to
a database**, so the live half — a message in a bound Discord channel proven
from `agent_actions.agent_id`, the unique index refusing the second binding, and
the FK cascade returning a channel to the default when its agent is deleted —
is outstanding and named as such.

Record, decisions and the outstanding gate: [`agent-roster.md`](agent-roster.md)
§T-S4.

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
